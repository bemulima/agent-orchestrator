package contextretrieval

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
)

// LookupDocument searches the admitted immutable snapshot, never the filesystem.
func LookupDocument(snapshot core.SnapshotResult, identity, relative string) (core.Document, bool) {
	for _, doc := range snapshot.Documents {
		if doc.Source.Identity == identity && doc.RelativePath == relative {
			return doc, true
		}
	}
	return core.Document{}, false
}

// CandidateFromDocument constructs inert evidence; instruction authority must
// be separately registered by the trusted caller and validated by the core.
func CandidateFromDocument(doc core.Document, facet core.Facet, resolverID, version string, span core.EvidenceSpan, kind string, provenance core.Provenance) core.EvidenceCandidate {
	if span.EndByte == 0 {
		span = wholeSpan(doc.Content)
	}
	content := ""
	if span.StartByte >= 0 && span.EndByte >= span.StartByte && span.EndByte <= len(doc.Content) {
		content = doc.Content[span.StartByte:span.EndByte]
	}
	freshness := core.Current
	if doc.Source.Dirty {
		freshness = core.DirtySnapshot
	}
	if doc.External {
		provenance = core.ExternalContent
	}
	candidate := core.EvidenceCandidate{SourceIdentity: doc.Source.Identity, SourceRevision: doc.Source.Revision, SourceSnapshot: doc.Source.Snapshot, RelativePath: doc.RelativePath, ContentHash: doc.ContentHash, ExpectedHash: strings.TrimPrefix(facet.ExpectedHash, "sha256:"), Symbol: facet.Symbol, Span: span, EvidenceKind: kind, Resolver: resolverID, ResolverVersion: version, Query: facet.QueryKind, Provenance: provenance, ClaimType: facet.ClaimType, ClaimKey: facet.ClaimKey, ClaimValue: facet.ClaimValue, Freshness: freshness, Content: content, Size: len(content), TokenEstimate: (len(content) + 3) / 4, FacetIDs: []string{facet.ID}, Required: facet.Required, ExactMatch: true, Limitations: []string{}, Links: []core.EvidenceLink{}}
	candidate.EvidenceID = "evidence:" + hashJSON(struct {
		Identity, Revision, Snapshot, Path, Hash, Symbol, Kind string
		Span                                                   core.EvidenceSpan
		ClaimType                                              core.ClaimType
		ClaimKey, ClaimValue                                   string
	}{candidate.SourceIdentity, candidate.SourceRevision, candidate.SourceSnapshot, candidate.RelativePath, candidate.ContentHash, candidate.Symbol, kind, span, facet.ClaimType, facet.ClaimKey, facet.ClaimValue})
	return candidate
}
func wholeSpan(content string) core.EvidenceSpan {
	end := strings.Count(content, "\n") + 1
	if strings.HasSuffix(content, "\n") && end > 1 {
		end--
	}
	return core.EvidenceSpan{StartLine: 1, EndLine: end, StartByte: 0, EndByte: len(content)}
}

// CoverageForFacet prevents an absence claim when admitted acquisition was incomplete.
func CoverageForFacet(snapshot core.SnapshotResult, facet core.Facet, count int) core.CoverageResult {
	coverage := core.CoverageResult{SourceIdentity: facet.SourceIdentity, FacetID: facet.ID, Stage: "resolver", Status: core.Complete, Complete: true, CandidateCount: count, SelectedCount: count, RequirementState: core.NotFound}
	sourceSeen := false
	for _, source := range snapshot.Sources {
		if source.Identity == facet.SourceIdentity || facet.SourceIdentity == "" {
			sourceSeen = true
		}
	}
	if !sourceSeen {
		coverage.Complete = false
		coverage.Status = core.Partial
		coverage.RequirementState = core.NotVerified
		coverage.Reasons = append(coverage.Reasons, "source_not_admitted")
	}
	coverageSeen := false
	for _, item := range snapshot.Coverage {
		if facet.SourceIdentity == "" || item.SourceIdentity == facet.SourceIdentity {
			coverageSeen = true
			if !item.Complete {
				coverage.Complete = false
				coverage.Status = core.Partial
				coverage.RequirementState = core.NotVerified
				coverage.Reasons = append(coverage.Reasons, item.Reasons...)
			}
		}
	}
	if !coverageSeen {
		coverage.Complete = false
		coverage.Status = core.Partial
		coverage.RequirementState = core.NotVerified
		coverage.Reasons = append(coverage.Reasons, "inventory_coverage_unknown")
	}
	if count > 0 {
		coverage.RequirementState = core.Found
		return coverage
	}
	for _, diagnostic := range snapshot.Diagnostics {
		if diagnostic.SourceIdentity != facet.SourceIdentity || facet.Path == "" || !(diagnostic.RelativePath == facet.Path || (diagnostic.RelativePath != "" && strings.HasPrefix(facet.Path, diagnostic.RelativePath+"/"))) {
			continue
		}
		switch diagnostic.Code {
		case "SOURCE_TOO_LARGE", "BYTE_LIMIT", "FILE_LIMIT", "DEPTH_LIMIT", "GLOBAL_BYTE_LIMIT":
			coverage.RequirementState = core.OmittedByLimit
		case "UNREADABLE_SOURCE":
			coverage.RequirementState = core.Unreadable
		case "EXCLUDED_BY_POLICY", "SECRET_CONTENT_EXCLUDED", "FILE_TYPE_EXCLUDED":
			coverage.RequirementState = core.ExcludedByPolicy
		}
		coverage.Complete = false
		coverage.Status = core.Partial
		coverage.Reasons = append(coverage.Reasons, diagnostic.Code)
	}
	sort.Strings(coverage.Reasons)
	return coverage
}
func unsupportedResult(facet core.Facet, resolver string) core.RetrievalResult {
	return core.RetrievalResult{Coverage: []core.CoverageResult{{SourceIdentity: facet.SourceIdentity, FacetID: facet.ID, Stage: "resolver", Status: core.Unsupported, Complete: false, RequirementState: core.RequirementUnsupported}}, Diagnostics: []core.RetrievalDiagnostic{{Code: "UNSUPPORTED_QUERY", Status: core.Unsupported, SourceIdentity: facet.SourceIdentity, FacetID: facet.ID, Message: fmt.Sprintf("%s does not support %s", resolver, facet.QueryKind)}}}
}

// ExactResolver performs deterministic exact path/text matching on snapshots.
type ExactResolver struct{}

func (ExactResolver) ID() string      { return "exact" }
func (ExactResolver) Version() string { return "exact.v1" }
func (resolver ExactResolver) Resolve(ctx context.Context, plan core.RetrievalPlan, facet core.Facet, snapshot core.SnapshotResult) (core.RetrievalResult, error) {
	if facet.QueryKind != core.QueryExact && facet.QueryKind != core.QueryDocs {
		return unsupportedResult(facet, resolver.ID()), nil
	}
	result := core.RetrievalResult{}
	if facet.Path != "" && !safeRelativePath(facet.Path) {
		return core.RetrievalResult{}, fmt.Errorf("unsafe selector path")
	}
	for _, doc := range snapshot.Documents {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if facet.SourceIdentity != "" && doc.Source.Identity != facet.SourceIdentity {
			continue
		}
		if facet.Path != "" && doc.RelativePath != facet.Path && !strings.HasPrefix(doc.RelativePath, strings.TrimSuffix(facet.Path, "/")+"/") {
			continue
		}
		if facet.QueryKind == core.QueryDocs && !isDocPath(doc.RelativePath) {
			continue
		}
		if facet.Text != "" && !strings.Contains(doc.Content, facet.Text) {
			continue
		}
		provenance := documentProvenance(doc.RelativePath)
		kind := facet.Kind
		if kind == "" {
			kind = "source"
		}
		// Exact content is kept as a coherent complete unit. Budgeting never clips it.
		candidate := CandidateFromDocument(doc, facet, resolver.ID(), resolver.Version(), wholeSpan(doc.Content), kind, provenance)
		candidate.Limitations = append(candidate.Limitations, "exact text is lexical evidence; no execution or type resolution")
		result.Candidates = append(result.Candidates, candidate)
	}
	result.Coverage = []core.CoverageResult{CoverageForFacet(snapshot, facet, len(result.Candidates))}
	capResult(&result, plan.Limits.MaxResults)
	return result, nil
}
func isDocPath(value string) bool {
	ext := strings.ToLower(path.Ext(value))
	return ext == ".md" || ext == ".txt" || ext == ".rst"
}
func documentProvenance(value string) core.Provenance {
	lower := strings.ToLower(value)
	if strings.HasSuffix(lower, "_test.go") || strings.Contains(lower, "/testdata/") || strings.HasPrefix(lower, "test/") {
		return core.ProjectTest
	}
	if strings.HasSuffix(lower, ".mmd") {
		return core.GeneratedContent
	}
	if isDocPath(lower) {
		return core.ProjectDoc
	}
	return core.ProjectSource
}
func capResult(result *core.RetrievalResult, limit int) {
	sort.Slice(result.Candidates, func(i, j int) bool {
		left, right := result.Candidates[i], result.Candidates[j]
		if left.SourceIdentity != right.SourceIdentity {
			return left.SourceIdentity < right.SourceIdentity
		}
		if left.RelativePath != right.RelativePath {
			return left.RelativePath < right.RelativePath
		}
		if left.Span.StartByte != right.Span.StartByte {
			return left.Span.StartByte < right.Span.StartByte
		}
		return left.EvidenceID < right.EvidenceID
	})
	if limit <= 0 {
		limit = 100
	}
	if len(result.Candidates) <= limit {
		return
	}
	omitted := len(result.Candidates) - limit
	result.Candidates = result.Candidates[:limit]
	for i := range result.Coverage {
		result.Coverage[i].Complete = false
		result.Coverage[i].Status = core.Partial
		result.Coverage[i].SelectedCount = limit
		result.Coverage[i].Omitted += omitted
		result.Coverage[i].TerminatedByLimit = true
		result.Coverage[i].Reasons = append(result.Coverage[i].Reasons, "result_limit")
	}
	result.Diagnostics = append(result.Diagnostics, core.RetrievalDiagnostic{Code: "RESULT_LIMIT", Status: core.Partial, Message: "found evidence omitted by result limit"})
}

var _ core.Resolver = ExactResolver{}
