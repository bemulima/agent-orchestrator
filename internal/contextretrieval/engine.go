package contextretrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Engine has only read-only ports. No transport, database or model belongs here.
type Engine struct {
	Loader    SourceLoader
	Resolvers map[string]Resolver
}

func (e *Engine) Prepare(ctx context.Context, request RetrievalRequest) (ContextPack, RetrievalTrace, error) {
	started := time.Now()
	plan, err := BuildPlan(request)
	if err != nil {
		return ContextPack{}, RetrievalTrace{}, err
	}
	e.fillVersions(&plan)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(plan.Limits.MaxDurationMillis)*time.Millisecond)
	defer cancel()
	snapshot, err := e.load(ctx, plan)
	if err != nil {
		return ContextPack{}, RetrievalTrace{}, err
	}
	result := e.retrieve(ctx, plan, snapshot, append(append([]Facet{}, plan.RequiredFacets...), plan.OptionalFacets...))
	result.Candidates, result.Diagnostics = verifyCandidates(plan, snapshot, result.Candidates, result.Diagnostics)
	// A second bounded load detects mutation during resolution. Partial inventory
	// stays partial; equality never proves an unscanned remainder was read.
	fresh, err := e.load(ctx, plan)
	if err != nil {
		return ContextPack{}, RetrievalTrace{}, err
	}
	if !sameSnapshots(snapshot, fresh) {
		return ContextPack{}, RetrievalTrace{}, ErrStaleBase
	}
	quality := AnalyzeQualityWithDocuments(plan, snapshot.Sources, result.Candidates, snapshot.Documents)
	coverage := append(append(append([]CoverageResult{}, plan.Route.Coverage...), snapshot.Coverage...), result.Coverage...)
	diagnostics := append(append([]RetrievalDiagnostic{}, snapshot.Diagnostics...), result.Diagnostics...)
	pack, err := BuildPack(plan, snapshot.Sources, quality, coverage, diagnostics, BudgetUsed{})
	return pack, traceFor(pack, result, len(quality.Candidates), started), err
}

func (e *Engine) load(ctx context.Context, plan RetrievalPlan) (SnapshotResult, error) {
	if e == nil || e.Loader == nil {
		return SnapshotResult{}, fmt.Errorf("%w: source reader unavailable", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return SnapshotResult{}, err
	}
	snapshot, err := e.Loader.Load(ctx, plan.Sources, plan.Limits)
	if err != nil {
		return SnapshotResult{}, fmt.Errorf("source snapshot unavailable: %w", ctxOrInvalid(ctx))
	}
	if err := ctx.Err(); err != nil {
		return SnapshotResult{}, err
	}
	return snapshot, nil
}
func ctxOrInvalid(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrInvalidRequest
}

func (e *Engine) retrieve(ctx context.Context, plan RetrievalPlan, snapshot SnapshotResult, facets []Facet) RetrievalResult {
	result := RetrievalResult{Candidates: []EvidenceCandidate{}, Coverage: []CoverageResult{}, Diagnostics: []RetrievalDiagnostic{}}
	for _, facet := range facets {
		source, ok := sourceAdmission(plan, facet.SourceIdentity)
		if !ok || (facet.Path != "" && !PathAdmitted(source, facet.Path)) {
			result.Coverage = append(result.Coverage, facetCoverage(facet, Blocked, ExcludedByPolicy, false, "outside explicit read admission"))
			continue
		}
		if err := ctx.Err(); err != nil {
			result.Coverage = append(result.Coverage, facetCoverage(facet, Partial, NotVerified, false, "retrieval cancelled or deadline exceeded"))
			result.Diagnostics = append(result.Diagnostics, RetrievalDiagnostic{Code: "RETRIEVAL_DEADLINE", Status: Partial, FacetID: facet.ID, Message: "Bounded retrieval stopped before this facet."})
			continue
		}
		resolver, ok := e.Resolvers[facet.Resolver]
		if !ok || resolver == nil {
			result.Coverage = append(result.Coverage, facetCoverage(facet, Unsupported, RequirementUnsupported, false, "resolver capability unavailable"))
			result.Diagnostics = append(result.Diagnostics, RetrievalDiagnostic{Code: "QUERY_UNSUPPORTED", Status: Unsupported, FacetID: facet.ID, Message: "Requested resolver is not available."})
			continue
		}
		part, err := resolver.Resolve(ctx, plan, facet, snapshot)
		if err != nil {
			result.Coverage = append(result.Coverage, facetCoverage(facet, Partial, NotVerified, false, "resolver failed; absence not verified"))
			result.Diagnostics = append(result.Diagnostics, RetrievalDiagnostic{Code: "RESOLVER_ERROR", Status: Partial, FacetID: facet.ID, Message: "Resolver could not verify requested evidence."})
			continue
		}
		if len(part.Coverage) == 0 {
			part.Coverage = append(part.Coverage, facetCoverage(facet, Partial, NotVerified, false, "resolver supplied no coverage proof"))
		}
		for i := range part.Coverage {
			part.Coverage[i].FacetID = facet.ID
			part.Coverage[i].SourceIdentity = facet.SourceIdentity
		}
		remaining := plan.Limits.MaxResults - len(result.Candidates)
		if remaining < 0 {
			remaining = 0
		}
		if len(part.Candidates) > remaining {
			omitted := len(part.Candidates) - remaining
			part.Candidates = part.Candidates[:remaining]
			part.Coverage = append(part.Coverage, CoverageResult{SourceIdentity: facet.SourceIdentity, FacetID: facet.ID, Stage: "result_projection", Status: Partial, RequirementState: OmittedByLimit, Complete: false, Omitted: omitted, TerminatedByLimit: true, Reasons: []string{"MAX_RESULTS"}})
			part.Diagnostics = append(part.Diagnostics, RetrievalDiagnostic{Code: "RESULT_LIMIT", Status: Partial, FacetID: facet.ID, Message: "Acquired candidates exceeded the shared result limit."})
		}
		for i := range part.Candidates {
			candidate := &part.Candidates[i]
			candidate.FacetIDs = sortedUnique(append(candidate.FacetIDs, facet.ID))
			candidate.Required = facet.Required
			candidate.Resolver = resolver.ID()
			candidate.ResolverVersion = resolver.Version()
			candidate.Query = facet.QueryKind
		}
		result.Candidates = append(result.Candidates, part.Candidates...)
		result.Coverage = append(result.Coverage, part.Coverage...)
		result.Diagnostics = append(result.Diagnostics, part.Diagnostics...)
	}
	return result
}
func sourceAdmission(plan RetrievalPlan, id string) (SourceAdmission, bool) {
	for _, source := range plan.Sources {
		if source.Identity == id {
			return source, true
		}
	}
	return SourceAdmission{}, false
}
func facetCoverage(f Facet, s Status, r RequirementState, complete bool, reason string) CoverageResult {
	return CoverageResult{SourceIdentity: f.SourceIdentity, Stage: "resolver", FacetID: f.ID, Status: s, RequirementState: r, Complete: complete, Reasons: []string{reason}}
}
func sourceSetDigest(sources []EvidenceSource) string {
	clone := append([]EvidenceSource{}, sources...)
	sort.Slice(clone, func(i, j int) bool { return clone[i].Identity < clone[j].Identity })
	return hashJSON(clone)
}
func sameSnapshots(a, b SnapshotResult) bool {
	return sourceSetDigest(a.Sources) == sourceSetDigest(b.Sources) && hashJSON(a.Coverage) == hashJSON(b.Coverage)
}

// Resolver ports cannot forge file bytes, pin identities, external provenance or
// registered instruction trust. Candidate content must be an exact source span.
func verifyCandidates(plan RetrievalPlan, snapshot SnapshotResult, candidates []EvidenceCandidate, diagnostics []RetrievalDiagnostic) ([]EvidenceCandidate, []RetrievalDiagnostic) {
	docs := map[string]Document{}
	for _, doc := range snapshot.Documents {
		docs[doc.Source.Identity+"\x00"+doc.RelativePath] = doc
	}
	accepted := []EvidenceCandidate{}
	for _, candidate := range candidates {
		doc, ok := docs[candidate.SourceIdentity+"\x00"+candidate.RelativePath]
		valid := ok && candidate.ContentHash == doc.ContentHash && candidate.SourceRevision == doc.Source.Revision && candidate.SourceSnapshot == doc.Source.Snapshot
		start, end := candidate.Span.StartByte, candidate.Span.EndByte
		valid = valid && start >= 0 && end >= start && end <= len(doc.Content)
		if valid {
			valid = doc.Content[start:end] == candidate.Content
		}
		if !valid {
			diagnostics = append(diagnostics, RetrievalDiagnostic{Code: "SOURCE_SPAN_UNVERIFIED", Status: Partial, SourceIdentity: candidate.SourceIdentity, RelativePath: candidate.RelativePath, FacetID: firstFacet(candidate), Message: "Resolver evidence does not match the admitted source snapshot."})
			continue
		}
		if doc.External {
			candidate.Provenance = ExternalContent
		}
		for _, link := range candidate.Links {
			if link.Kind != "declared_source" && link.Kind != "source_pin" && link.Kind != "input" {
				continue
			}
			identity := link.SourceIdentity
			if identity == "" {
				identity = candidate.SourceIdentity
			}
			cited, exists := docs[identity+"\x00"+link.RelativePath]
			code := ""
			if !exists {
				code = "CITATION_NOT_VERIFIED"
			} else if link.ExpectedHash != "" && !EqualContentHash(link.ExpectedHash, cited.ContentHash) {
				code = "CITATION_STALE"
			} else if link.Symbol != "" && !strings.Contains(cited.Content, link.Symbol) {
				code = "CITATION_SYMBOL_NOT_VERIFIED"
			}
			if code != "" {
				candidate.Limitations = append(candidate.Limitations, code)
				diagnostics = append(diagnostics, RetrievalDiagnostic{Code: code, Status: Partial, SourceIdentity: identity, RelativePath: link.RelativePath, FacetID: firstFacet(candidate), Message: "Declared source citation could not be verified in the admitted snapshot."})
			}
		}
		// A registered policy is intentionally separate from incidental AGENTS text.
		for _, policy := range plan.TrustedPolicies {
			if policy.SourceIdentity == candidate.SourceIdentity && policy.RelativePath == candidate.RelativePath && EqualContentHash(policy.ContentHash, candidate.ContentHash) && PolicyApplies(plan, policy) {
				candidate.Provenance = TrustedPolicy
			}
		}
		accepted = append(accepted, candidate)
	}
	return accepted, diagnostics
}
func firstFacet(c EvidenceCandidate) string {
	if len(c.FacetIDs) > 0 {
		return c.FacetIDs[0]
	}
	return ""
}

// Expand re-admits scope from current caller input, then verifies both digest and
// live snapshot. The base package is disposable evidence, never a trust root.
func (e *Engine) Expand(ctx context.Context, base ContextPack, current RetrievalRequest, request ExpandRequest) (ContextDelta, RetrievalTrace, error) {
	return e.expand(ctx, base, current, request, true)
}
func (e *Engine) expand(ctx context.Context, base ContextPack, current RetrievalRequest, request ExpandRequest, replay bool) (ContextDelta, RetrievalTrace, error) {
	started := time.Now()
	if err := VerifyDigest(base); err != nil {
		return ContextDelta{}, RetrievalTrace{}, fmt.Errorf("%w: base digest", ErrInvalidRequest)
	}
	plan, err := BuildPlan(current)
	if err != nil {
		return ContextDelta{}, RetrievalTrace{}, err
	}
	e.fillVersions(&plan)
	if admissionIdentity(plan) != admissionIdentity(base.RetrievalPlan) {
		return ContextDelta{}, RetrievalTrace{}, ErrScopeChange
	}
	if base.SchemaVersion != SchemaVersion || base.Engine.Version != EngineVersion {
		return ContextDelta{}, RetrievalTrace{}, fmt.Errorf("%w: base ABI", ErrInvalidRequest)
	}
	facet := request.Facet
	if facet.ID == "" || !knownQuery(facet.QueryKind) || !knownClaim(facet.ClaimType) || len(facet.Text) > 4096 || len(facet.Symbol) > 512 || facet.Path != "" && !SafeRelativePath(facet.Path) {
		return ContextDelta{}, RetrievalTrace{}, fmt.Errorf("%w: expand selector", ErrInvalidRequest)
	}
	if request.Reason == "" || len(request.Reason) > 4096 {
		return ContextDelta{}, RetrievalTrace{}, fmt.Errorf("%w: expand reason required", ErrInvalidRequest)
	}
	source, ok := sourceAdmission(plan, facet.SourceIdentity)
	if !ok || facet.Path != "" && !PathAdmitted(source, facet.Path) {
		return ContextDelta{}, RetrievalTrace{}, ErrScopeChange
	}
	if facet.Resolver == "" {
		facet.Resolver = resolverFor(facet.QueryKind)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(plan.Limits.MaxDurationMillis)*time.Millisecond)
	defer cancel()
	snapshot, err := e.load(ctx, plan)
	if err != nil {
		return ContextDelta{}, RetrievalTrace{}, err
	}
	if sourceSetDigest(snapshot.Sources) != sourceSetDigest(base.Sources) {
		return ContextDelta{}, RetrievalTrace{}, ErrStaleBase
	}
	// Recompute evidence validity and baseline pack from current caller selectors.
	// Merely editing a disposable pack and recomputing its digest cannot confer trust.
	if replay {
		replayRequest := current
		replayRequest.RequestID = base.RequestID
		original, _, prepareErr := e.Prepare(ctx, replayRequest)
		if prepareErr != nil {
			return ContextDelta{}, RetrievalTrace{}, prepareErr
		}
		if len(base.RetrievalPlan.ExpansionHistory) > plan.Limits.MaxExpandDepth {
			return ContextDelta{}, RetrievalTrace{}, fmt.Errorf("%w: expansion history", ErrInvalidRequest)
		}
		for _, step := range base.RetrievalPlan.ExpansionHistory {
			delta, _, replayErr := e.expand(ctx, original, replayRequest, step, false)
			if replayErr != nil {
				return ContextDelta{}, RetrievalTrace{}, replayErr
			}
			original = delta.Pack
		}
		if original.ContentDigest != base.ContentDigest || original.BudgetUsed != base.BudgetUsed {
			return ContextDelta{}, RetrievalTrace{}, fmt.Errorf("%w: base does not match deterministic trusted replay", ErrInvalidRequest)
		}
	}
	// Every return carrying base evidence follows live verification and trusted
	// replay; budget/depth diagnostics are not a bypass for a forged cache.
	if base.BudgetUsed.ExpandCount >= plan.Limits.MaxExpandDepth {
		return failedDelta(base, "EXPAND_DEPTH_LIMIT", Blocked), RetrievalTrace{}, nil
	}
	if err := checkRemainingBudget(plan.Budget, base.BudgetUsed, request.RemainingBudget); err != nil {
		return failedDelta(base, "BUDGET_UNSATISFIED", Blocked), RetrievalTrace{}, nil
	}
	for _, old := range append(append([]Facet{}, base.RetrievalPlan.RequiredFacets...), base.RetrievalPlan.OptionalFacets...) {
		if old.ID == facet.ID && !sameFacet(old, facet) {
			return ContextDelta{}, RetrievalTrace{}, fmt.Errorf("%w: expand facet identity reused", ErrInvalidRequest)
		}
	}
	priorCandidates, priorDiags := verifyCandidates(plan, snapshot, base.Evidence, nil)
	if len(priorCandidates) != len(base.Evidence) {
		return ContextDelta{}, RetrievalTrace{}, ErrStaleBase
	}
	for i := range priorCandidates {
		priorCandidates[i].Required = true
	}
	if facet.Required {
		plan.RequiredFacets = append(plan.RequiredFacets, facet)
	} else {
		plan.OptionalFacets = append(plan.OptionalFacets, facet)
	}
	// Keep prior expanded requirements and replayable history in the semantic
	// package. The current caller remains the only source of initial admission.
	if len(base.RetrievalPlan.ExpansionHistory) > 0 {
		initial := map[string]bool{}
		for _, f := range append(append([]Facet{}, plan.RequiredFacets...), plan.OptionalFacets...) {
			initial[f.ID] = true
		}
		for _, f := range append(append([]Facet{}, base.RetrievalPlan.RequiredFacets...), base.RetrievalPlan.OptionalFacets...) {
			if !initial[f.ID] {
				if f.Required {
					plan.RequiredFacets = append(plan.RequiredFacets, f)
				} else {
					plan.OptionalFacets = append(plan.OptionalFacets, f)
				}
			}
		}
	}
	plan.ExpansionHistory = append(append([]ExpandRequest{}, base.RetrievalPlan.ExpansionHistory...), request)
	result := e.retrieve(ctx, plan, snapshot, []Facet{facet})
	result.Candidates, result.Diagnostics = verifyCandidates(plan, snapshot, result.Candidates, result.Diagnostics)
	combined := append(append([]EvidenceCandidate{}, priorCandidates...), result.Candidates...)
	quality := AnalyzeQualityWithDocuments(plan, snapshot.Sources, combined, snapshot.Documents)
	coverage := append(append([]CoverageResult{}, base.Coverage...), result.Coverage...)
	diagnostics := append(append(append([]RetrievalDiagnostic{}, base.UnresolvedQuestions...), priorDiags...), result.Diagnostics...)
	fresh, err := e.load(ctx, plan)
	if err != nil {
		return ContextDelta{}, RetrievalTrace{}, err
	}
	if !sameSnapshots(snapshot, fresh) {
		return ContextDelta{}, RetrievalTrace{}, ErrStaleBase
	}
	previous := base.BudgetUsed
	previous.ExpandCount++
	pack, err := BuildPack(plan, snapshot.Sources, quality, coverage, diagnostics, previous)
	if err != nil {
		return ContextDelta{}, RetrievalTrace{}, err
	}
	budgetFailed := false
	for _, diagnostic := range pack.UnresolvedQuestions {
		if diagnostic.Code == "BUDGET_UNSATISFIED" {
			budgetFailed = true
		}
	}
	if budgetFailed {
		failed := failedDelta(base, "BUDGET_UNSATISFIED", Blocked)
		failed.Diagnostics = append(failed.Diagnostics, pack.UnresolvedQuestions...)
		return failed, traceFor(pack, result, len(quality.Candidates), started), nil
	}
	if (request.RemainingBudget.MaxSourceBytes > 0 && pack.BudgetUsed.CumulativeSourceBytes-base.BudgetUsed.CumulativeSourceBytes > request.RemainingBudget.MaxSourceBytes) || (request.RemainingBudget.MaxContextTokens > 0 && pack.BudgetUsed.CumulativeContextTokens-base.BudgetUsed.CumulativeContextTokens > request.RemainingBudget.MaxContextTokens) {
		return failedDelta(base, "BUDGET_UNSATISFIED", Blocked), RetrievalTrace{}, nil
	}
	if additionalBudgetExceeded(base, pack, request) {
		return failedDelta(base, "BUDGET_UNSATISFIED", Blocked), RetrievalTrace{}, nil
	}
	oldIDs := map[string]bool{}
	for _, item := range base.Evidence {
		oldIDs[item.EvidenceID] = true
	}
	added := []string{}
	for _, item := range pack.Evidence {
		if !oldIDs[item.EvidenceID] {
			added = append(added, item.EvidenceID)
		}
	}
	delta := ContextDelta{SchemaVersion: SchemaVersion, Status: pack.Status, BaseDigest: base.ContentDigest, ContentDigest: pack.ContentDigest, AddedEvidenceIDs: added, Pack: pack, Diagnostics: result.Diagnostics}
	return delta, traceFor(pack, result, len(quality.Candidates), started), nil
}
func admissionIdentity(plan RetrievalPlan) string {
	// Expanded facets differ, but roots, policy, route and all initial caller
	// requirements must remain bound. Existing expanded facets are checked above.
	normalized := normalizePlan(plan)
	return hashJSON(struct {
		Sources       []SourceAdmission
		Route         RouteContext
		Policies      []PolicyRegistration
		Budget        Budget
		Limits        Limits
		Versions      map[string]string
		Task, Purpose string
	}{normalized.Sources, normalized.Route, normalized.TrustedPolicies, normalized.Budget, normalized.Limits, normalized.AdapterVersions, normalized.Task, normalized.Purpose})
}
func checkRemainingBudget(b Budget, used BudgetUsed, remaining Budget) error {
	if used.CumulativeSourceBytes > b.MaxSourceBytes || used.CumulativeContextTokens+used.ReservedPromptTokens > b.MaxContextTokens {
		return ErrInvalidRequest
	}
	if remaining.MaxSourceBytes < 0 || remaining.MaxContextTokens < 0 || remaining.ReservedPromptTokens < 0 || remaining.PerFacetTokens < 0 || remaining.PerSourceBytes < 0 {
		return ErrInvalidRequest
	}
	if remaining.MaxSourceBytes > 0 && remaining.MaxSourceBytes > b.MaxSourceBytes-used.CumulativeSourceBytes {
		return ErrInvalidRequest
	}
	if remaining.MaxContextTokens > 0 && remaining.MaxContextTokens > b.MaxContextTokens-used.CumulativeContextTokens-used.ReservedPromptTokens {
		return ErrInvalidRequest
	}
	return nil
}

func additionalBudgetExceeded(base, pack ContextPack, request ExpandRequest) bool {
	b := request.RemainingBudget
	if b.ReservedPromptTokens > 0 && pack.BudgetUsed.CumulativeContextTokens+b.ReservedPromptTokens > pack.RetrievalPlan.Budget.MaxContextTokens {
		return true
	}
	old := map[string]bool{}
	for _, candidate := range base.Evidence {
		old[candidate.EvidenceID] = true
	}
	bytesBySource := map[string]int{}
	facetTokens := 0
	for _, candidate := range pack.Evidence {
		if !old[candidate.EvidenceID] {
			bytesBySource[candidate.SourceIdentity] += len(candidate.Content)
		}
		for _, id := range candidate.FacetIDs {
			if id == request.Facet.ID {
				raw, _ := json.Marshal(candidate)
				facetTokens += (len(raw) + 3) / 4
				break
			}
		}
	}
	if b.PerFacetTokens > 0 && facetTokens > b.PerFacetTokens {
		return true
	}
	if b.PerSourceBytes > 0 {
		for _, bytes := range bytesBySource {
			if bytes > b.PerSourceBytes {
				return true
			}
		}
	}
	return false
}
func failedDelta(base ContextPack, code string, status Status) ContextDelta {
	return ContextDelta{SchemaVersion: SchemaVersion, Status: status, BaseDigest: base.ContentDigest, ContentDigest: base.ContentDigest, AddedEvidenceIDs: []string{}, Pack: base, Diagnostics: []RetrievalDiagnostic{{Code: code, Status: status, Message: "Bounded expansion cannot proceed."}}}
}
func (e *Engine) fillVersions(plan *RetrievalPlan) {
	plan.AdapterVersions = map[string]string{}
	if e != nil {
		for id, r := range e.Resolvers {
			if r != nil {
				plan.AdapterVersions[id] = r.Version()
			}
		}
	}
}
func traceFor(pack ContextPack, result RetrievalResult, validated int, started time.Time) RetrievalTrace {
	trace := RetrievalTrace{RequestID: pack.RequestID, PackDigest: pack.ContentDigest, ResolverVersions: pack.Engine.AdapterVersions, CandidateCount: len(result.Candidates), SelectedCount: len(pack.Evidence), CoverageStatus: pack.Status, ConflictCount: len(pack.AuthorityConflicts), Bytes: pack.BudgetUsed.SourceBytes, TokenEstimate: pack.BudgetUsed.ContextTokens, LatencyMillis: time.Since(started).Milliseconds(), ExpandCount: pack.BudgetUsed.ExpandCount}
	for _, source := range pack.Sources {
		trace.SnapshotIDs = append(trace.SnapshotIDs, source.Snapshot)
	}
	for _, candidate := range result.Candidates {
		trace.QueryKinds = append(trace.QueryKinds, candidate.Query)
	}
	sort.Slice(trace.QueryKinds, func(i, j int) bool { return trace.QueryKinds[i] < trace.QueryKinds[j] })
	for _, omission := range pack.Omissions {
		trace.OmissionReasons = append(trace.OmissionReasons, omission.Reason)
	}
	trace.OmissionReasons = sortedUnique(trace.OmissionReasons)
	_ = validated
	return trace
}

// UnmarshalPack deliberately has no side effects or hidden source admission.
func UnmarshalPack(data []byte) (ContextPack, error) {
	var pack ContextPack
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&pack)
	if err != nil {
		return pack, err
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return pack, ErrInvalidRequest
	}
	return pack, VerifyDigest(pack)
}
