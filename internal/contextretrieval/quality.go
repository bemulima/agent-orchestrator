package contextretrieval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"
)

// QualityResult keeps rejected sources visible through diagnostics and omissions.
// Candidates never carry instruction power unless their exact bytes were registered.
type QualityResult struct {
	Candidates  []EvidenceCandidate
	Diagnostics []RetrievalDiagnostic
	Conflicts   []AuthorityConflict
	Omissions   []Omission
}

var qualitySecretPattern = regexp.MustCompile(`(?i)(-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----|\bAKIA[0-9A-Z]{16}\b|\b(?:api[_-]?key|api[_-]?token|auth[_-]?token|access[_-]?token|refresh[_-]?token|client[_-]?secret|password|secret)["']?\s*[:=]\s*["'][^"']{8,}["'])`)
var qualityHashPattern = regexp.MustCompile(`^(?:sha256:)?[a-f0-9]{64}$`)

// EqualContentHash accepts the two supported spellings of a SHA-256 pin.
func EqualContentHash(a, b string) bool {
	return qualityHashPattern.MatchString(a) && qualityHashPattern.MatchString(b) && strings.TrimPrefix(a, "sha256:") == strings.TrimPrefix(b, "sha256:")
}

// ContainsSecretLikeContent is a defensive content gate, never an authorization source.
func ContainsSecretLikeContent(content string) bool { return qualitySecretPattern.MatchString(content) }

// SafeRelativePath checks ingestion paths before any adapter opens a file.
func SafeRelativePath(value string) bool {
	if value == "" || path.IsAbs(value) || path.Clean(value) != value || strings.Contains(value, "\\") {
		return false
	}
	for _, char := range value {
		if char < 32 || char == 127 {
			return false
		}
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
		if SecretPathComponent(component) {
			return false
		}
	}
	return true
}

// SecretPathComponent is shared by scope validation, readers and CLI inputs.
// Literal credential stems are excluded without rejecting tokenization.go.
func SecretPathComponent(component string) bool {
	lower := strings.ToLower(path.Base(component))
	if lower == ".git" || lower == ".ssh" || lower == ".aws" || lower == ".gnupg" || lower == ".kube" || lower == ".gcloud" || lower == ".docker" || lower == ".netrc" || lower == ".npmrc" || lower == ".pypirc" || lower == "auth.json" || lower == ".codex" || lower == ".agents" || strings.Contains(lower, ".env") {
		return true
	}
	for _, token := range []string{"credential", "secret", "private_key", "private-key", "id_rsa", "id_ed25519", "id_ecdsa", "id_dsa", ".pem", ".key", ".p12", ".pfx"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	stem := strings.SplitN(lower, ".", 2)[0]
	for _, name := range []string{"token", "tokens", "access_token", "access-token", "api_token", "api-token", "api_key", "api-key", "auth_token", "auth-token", "refresh_token", "refresh-token"} {
		if stem == name {
			return true
		}
	}
	return false
}

func qualityPathCovered(scope, relative string) bool {
	if scope == "." || scope == "" {
		return true
	}
	scope = strings.TrimSuffix(scope, "/")
	if strings.HasSuffix(scope, "/**") {
		scope = strings.TrimSuffix(scope, "/**")
	}
	return relative == scope || strings.HasPrefix(relative, scope+"/")
}

func qualityPathAdmitted(plan RetrievalPlan, candidate EvidenceCandidate) bool {
	for _, admission := range plan.Sources {
		if admission.Identity != candidate.SourceIdentity {
			continue
		}
		return PathAdmitted(admission, candidate.RelativePath)
	}
	return false
}

func qualityPolicyRegistered(plan RetrievalPlan, candidate EvidenceCandidate) bool {
	for _, policy := range plan.TrustedPolicies {
		if policy.SourceIdentity == candidate.SourceIdentity && policy.RelativePath == candidate.RelativePath && EqualContentHash(policy.ContentHash, candidate.ContentHash) {
			return PolicyApplies(plan, policy)
		}
	}
	return false
}

// PolicyApplies binds registered procedures to selected task facets. Policy
// retrieval itself cannot manufacture applicability to an unrelated subtree.
func PolicyApplies(plan RetrievalPlan, policy PolicyRegistration) bool {
	if policy.Scope == "." {
		return true
	}
	for _, facet := range append(append([]Facet{}, plan.RequiredFacets...), plan.OptionalFacets...) {
		if facet.SourceIdentity != policy.SourceIdentity || facet.Path == policy.RelativePath || facet.Kind == "policy" {
			continue
		}
		if facet.Path != "" && qualityPathCovered(policy.Scope, facet.Path) || facet.Path == "" && policy.Scope == "." {
			return true
		}
	}
	return false
}

func qualityValidProvenance(value Provenance) bool {
	switch value {
	case TrustedPolicy, TrustedProjectMetadata, ProjectSource, ProjectTest, ProjectDoc, ExternalContent, GeneratedContent:
		return true
	}
	return false
}
func qualityValidFreshness(value Freshness) bool {
	switch value {
	case Current, StaleEvidence, DirtySnapshot, Unverified, Missing, Invalidated:
		return true
	}
	return false
}
func qualityValidClaim(value ClaimType) bool {
	switch value {
	case BusinessOwnership, PublicAPI, ImplementationBehavior, DatabaseSchema, DataSemantics, TestingPolicy, ArchitectureRule, HistoricalDecision:
		return true
	}
	return false
}

// ClaimAuthority derives factual authority separately for each kind of question.
// PRIMARY/SUPPORTING/CONTEXT never authorize instruction execution or foreign writes.
func ClaimAuthority(candidate EvidenceCandidate) Authority {
	authority := Authority{ClaimType: candidate.ClaimType, Role: "CONTEXT", Basis: "inert contextual evidence"}
	contract := strings.Contains(candidate.EvidenceKind, "contract") || candidate.Query == QueryContract
	metadata := candidate.Provenance == TrustedProjectMetadata
	source := candidate.Provenance == ProjectSource
	policy := candidate.Provenance == TrustedPolicy
	test := candidate.Provenance == ProjectTest
	primary, supporting := false, false
	switch candidate.ClaimType {
	case BusinessOwnership:
		primary, supporting = metadata || policy || contract, source
	case PublicAPI:
		primary, supporting = contract && (source || metadata || policy), source || test
	case ImplementationBehavior:
		primary, supporting = source, test
	case DatabaseSchema:
		primary, supporting = source && (candidate.Query == QuerySchema || strings.Contains(candidate.EvidenceKind, "schema") || strings.Contains(candidate.EvidenceKind, "query")), source || test || metadata
	case DataSemantics:
		primary, supporting = contract && (source || metadata || policy), source || test
	case TestingPolicy:
		primary, supporting = policy, metadata || test
	case ArchitectureRule:
		primary, supporting = policy, metadata || source || contract
	case HistoricalDecision:
		primary, supporting = candidate.Provenance == ProjectDoc && (candidate.Query == QueryHistory || strings.Contains(candidate.EvidenceKind, "decision")), metadata || source
	}
	if primary {
		authority.Role = "PRIMARY"
		authority.Basis = "claim-specific canonical " + string(candidate.Provenance)
	} else if supporting {
		authority.Role = "SUPPORTING"
		authority.Basis = "claim-specific supporting " + string(candidate.Provenance)
	}
	if candidate.Provenance == GeneratedContent || candidate.Provenance == ExternalContent {
		authority.Role = "CONTEXT"
		authority.Basis = "generated or external factual context"
	}
	return authority
}

func qualityDiagnostic(candidate EvidenceCandidate, code string, status Status, message string) RetrievalDiagnostic {
	return RetrievalDiagnostic{Code: code, Status: status, SourceIdentity: candidate.SourceIdentity, RelativePath: candidate.RelativePath, EvidenceID: candidate.EvidenceID, Message: message}
}

// AnalyzeQuality validates pins and scope, surfaces conflicting claims, then ranks
// and deduplicates evidence. Stale claim participants remain visible as conflict IDs.
func AnalyzeQuality(plan RetrievalPlan, sources []EvidenceSource, candidates []EvidenceCandidate) QualityResult {
	return analyzeQuality(plan, sources, candidates, nil)
}

// AnalyzeQualityWithDocuments additionally verifies generated input pins against
// the complete admitted snapshot. Documents are read-only source-loader output;
// presence here never grants instruction trust or expands the admitted scope.
func AnalyzeQualityWithDocuments(plan RetrievalPlan, sources []EvidenceSource, candidates []EvidenceCandidate, documents []Document) QualityResult {
	return analyzeQuality(plan, sources, candidates, documents)
}

func analyzeQuality(plan RetrievalPlan, sources []EvidenceSource, candidates []EvidenceCandidate, documents []Document) QualityResult {
	result := QualityResult{Candidates: []EvidenceCandidate{}, Diagnostics: []RetrievalDiagnostic{}, Conflicts: []AuthorityConflict{}, Omissions: []Omission{}}
	bySource := map[string]EvidenceSource{}
	for _, source := range sources {
		bySource[source.Identity] = source
	}
	input := append([]EvidenceCandidate(nil), candidates...)
	sort.Slice(input, func(i, j int) bool { return qualityCandidateKey(input[i]) < qualityCandidateKey(input[j]) })
	claims := []EvidenceCandidate{}
	for _, candidate := range input {
		candidate.ContentHash = strings.TrimPrefix(candidate.ContentHash, "sha256:")
		candidate.ExpectedHash = strings.TrimPrefix(candidate.ExpectedHash, "sha256:")
		candidate.Links = append([]EvidenceLink{}, candidate.Links...)
		for index := range candidate.Links {
			candidate.Links[index].ExpectedHash = strings.TrimPrefix(candidate.Links[index].ExpectedHash, "sha256:")
		}
		candidate.FacetIDs = qualityStrings(candidate.FacetIDs)
		for _, facet := range plan.RequiredFacets {
			for _, id := range candidate.FacetIDs {
				if facet.ID == id {
					candidate.Required = true
				}
			}
		}
		candidate.Limitations = qualityStrings(candidate.Limitations)
		candidate.Links = qualityLinks(candidate.Links)
		code, message := "", ""
		source, exists := bySource[candidate.SourceIdentity]
		switch {
		case !exists || !SafeRelativePath(candidate.RelativePath) || !qualityPathAdmitted(plan, candidate):
			code, message = "EVIDENCE_SCOPE_REJECTED", "evidence path is outside admitted read scope"
		case candidate.EvidenceID == "" || candidate.SourceRevision == "" || candidate.SourceSnapshot == "" || candidate.Resolver == "" || candidate.ResolverVersion == "" || !knownQuery(candidate.Query) || !qualityHashPattern.MatchString(candidate.ContentHash):
			code, message = "EVIDENCE_PROVENANCE_INVALID", "evidence requires an identity, resolver version and verified content/snapshot pins"
		case candidate.Span.StartLine < 1 || candidate.Span.EndLine < candidate.Span.StartLine || candidate.Span.StartByte < 0 || candidate.Span.EndByte < candidate.Span.StartByte:
			code, message = "EVIDENCE_SPAN_INVALID", "evidence span is not a coherent source unit"
		case !qualityValidProvenance(candidate.Provenance) || !qualityValidFreshness(candidate.Freshness) || !qualityValidClaim(candidate.ClaimType):
			code, message = "EVIDENCE_CLASSIFICATION_INVALID", "unsupported provenance, freshness or claim classification"
		case ContainsSecretLikeContent(candidate.Content) || ContainsSecretLikeContent(candidate.ClaimValue):
			code, message = "SECRET_CONTENT_EXCLUDED", "secret-like content was excluded"
		}
		if code != "" {
			result.Diagnostics = append(result.Diagnostics, qualityDiagnostic(candidate, code, Partial, message))
			result.Omissions = append(result.Omissions, Omission{EvidenceID: candidate.EvidenceID, SourceIdentity: candidate.SourceIdentity, RelativePath: candidate.RelativePath, Reason: code, Required: candidate.Required})
			continue
		}
		if candidate.Provenance == TrustedPolicy && !qualityPolicyRegistered(plan, candidate) {
			candidate.Provenance = ProjectDoc
			candidate.Limitations = qualityStrings(append(candidate.Limitations, "retrieved policy-shaped text is unregistered inert evidence"))
			result.Diagnostics = append(result.Diagnostics, qualityDiagnostic(candidate, "UNREGISTERED_POLICY_AS_DATA", Partial, "policy trust requires exact caller registration"))
		}
		if qualityPolicyRegistered(plan, candidate) {
			candidate.Provenance = TrustedPolicy
		}
		if candidate.SourceSnapshot != source.Snapshot || candidate.SourceRevision != source.Revision {
			candidate.Freshness = Invalidated
		}
		if candidate.ExpectedHash != "" && !EqualContentHash(candidate.ExpectedHash, candidate.ContentHash) {
			candidate.Freshness = StaleEvidence
		}
		if candidate.Freshness == Current && source.Dirty {
			candidate.Freshness = DirtySnapshot
		}
		if candidate.Provenance == GeneratedContent {
			pins, valid, staleInput := 0, true, false
			for _, link := range candidate.Links {
				if link.Kind != "input" && link.Kind != "generated_input" && link.Kind != "source_pin" && link.Kind != "captured_declaration" {
					continue
				}
				pins++
				matched := false
				identity := link.SourceIdentity
				if identity == "" {
					identity = candidate.SourceIdentity
				}
				for _, other := range input {
					otherSource, exists := bySource[other.SourceIdentity]
					if exists && other.SourceIdentity == identity && other.RelativePath == link.RelativePath && EqualContentHash(other.ContentHash, link.ExpectedHash) && other.SourceSnapshot == otherSource.Snapshot && other.SourceRevision == otherSource.Revision && (other.Freshness == Current || other.Freshness == DirtySnapshot) && link.ExpectedHash != "" && SafeRelativePath(other.RelativePath) && qualityPathAdmitted(plan, other) {
						matched = true
					}
				}
				for _, document := range documents {
					inputSource, exists := bySource[identity]
					probe := EvidenceCandidate{SourceIdentity: identity, RelativePath: link.RelativePath}
					if !exists || document.Source.Identity != identity || document.RelativePath != link.RelativePath || document.Source.Snapshot != inputSource.Snapshot || document.Source.Revision != inputSource.Revision || !SafeRelativePath(link.RelativePath) || !qualityPathAdmitted(plan, probe) || ContainsSecretLikeContent(document.Content) {
						continue
					}
					if link.ExpectedHash != "" && EqualContentHash(document.ContentHash, link.ExpectedHash) {
						matched = true
					} else if link.ExpectedHash != "" {
						staleInput = true
					}
				}
				valid = valid && matched
			}
			if pins == 0 || !valid {
				candidate.Freshness = Unverified
				code := "GENERATED_INPUT_UNVERIFIED"
				if staleInput {
					candidate.Freshness = StaleEvidence
					code = "GENERATED_INPUT_STALE"
				}
				result.Diagnostics = append(result.Diagnostics, qualityDiagnostic(candidate, code, Partial, "generated evidence requires current validated input pins"))
			} else {
				candidate.Limitations = qualityStrings(append(candidate.Limitations, "generated input content verified in admitted snapshots; captured Git revisions are not certified by this check"))
			}
		}
		candidate.Authority = ClaimAuthority(candidate)
		candidate.Size = len(candidate.Content)
		candidate.TokenEstimate = (candidate.Size + 3) / 4
		claims = append(claims, candidate)
		if candidate.Freshness != Current && candidate.Freshness != DirtySnapshot {
			result.Diagnostics = append(result.Diagnostics, qualityDiagnostic(candidate, "EVIDENCE_FRESHNESS_"+string(candidate.Freshness), Partial, "noncurrent evidence was withheld; claim conflict remains observable"))
			result.Omissions = append(result.Omissions, Omission{EvidenceID: candidate.EvidenceID, SourceIdentity: candidate.SourceIdentity, RelativePath: candidate.RelativePath, Reason: "FRESHNESS_" + string(candidate.Freshness), Required: candidate.Required})
			continue
		}
		result.Candidates = append(result.Candidates, candidate)
	}
	result.Conflicts = qualityConflicts(claims)
	result.Candidates = qualityDeduplicate(result.Candidates)
	sort.SliceStable(result.Candidates, func(i, j int) bool { return qualityRankLess(result.Candidates[i], result.Candidates[j]) })
	result.Diagnostics = qualityDiagnostics(result.Diagnostics)
	result.Omissions = qualityOmissions(result.Omissions)
	return result
}

func qualityCandidateKey(c EvidenceCandidate) string {
	encoded, _ := json.Marshal([]any{c.SourceIdentity, c.SourceRevision, c.ContentHash, c.RelativePath, c.Symbol, c.Span.StartLine, c.Span.EndLine, c.Span.StartByte, c.Span.EndByte, c.EvidenceKind, c.ClaimType, c.ClaimKey, c.ClaimValue, c.EvidenceID, c.Resolver})
	return string(encoded)
}
func qualityHash(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
func qualityStrings(values []string) []string {
	result := append([]string{}, values...)
	sort.Strings(result)
	output := result[:0]
	for _, value := range result {
		if len(output) == 0 || output[len(output)-1] != value {
			output = append(output, value)
		}
	}
	return output
}
func qualityLinks(values []EvidenceLink) []EvidenceLink {
	result := append([]EvidenceLink{}, values...)
	sort.Slice(result, func(i, j int) bool {
		a, _ := json.Marshal(result[i])
		b, _ := json.Marshal(result[j])
		return string(a) < string(b)
	})
	output := result[:0]
	for _, value := range result {
		if len(output) == 0 || output[len(output)-1] != value {
			output = append(output, value)
		}
	}
	return output
}
func qualityDiagnostics(values []RetrievalDiagnostic) []RetrievalDiagnostic {
	result := append([]RetrievalDiagnostic{}, values...)
	sort.Slice(result, func(i, j int) bool {
		a, _ := json.Marshal(result[i])
		b, _ := json.Marshal(result[j])
		return string(a) < string(b)
	})
	output := result[:0]
	for _, value := range result {
		if len(output) == 0 || output[len(output)-1] != value {
			output = append(output, value)
		}
	}
	return output
}
func qualityOmissions(values []Omission) []Omission {
	result := append([]Omission{}, values...)
	sort.Slice(result, func(i, j int) bool {
		a, _ := json.Marshal(result[i])
		b, _ := json.Marshal(result[j])
		return string(a) < string(b)
	})
	output := result[:0]
	for _, value := range result {
		if len(output) == 0 || output[len(output)-1] != value {
			output = append(output, value)
		}
	}
	return output
}

func qualityConflicts(candidates []EvidenceCandidate) []AuthorityConflict {
	groups := map[string][]EvidenceCandidate{}
	for _, candidate := range candidates {
		if candidate.ClaimKey == "" || candidate.ClaimValue == "" {
			continue
		}
		key := string(candidate.ClaimType) + "\x00" + candidate.ClaimKey
		// Intended API and actual behavior can explicitly share a contract claim key.
		if candidate.ClaimType == PublicAPI || candidate.ClaimType == ImplementationBehavior {
			key = "behavior\x00" + candidate.ClaimKey
		}
		groups[key] = append(groups[key], candidate)
	}
	result := []AuthorityConflict{}
	keys := []string{}
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		ids, values := []string{}, []string{}
		group := groups[key]
		for _, candidate := range group {
			ids = append(ids, candidate.EvidenceID)
			values = append(values, strings.TrimSpace(candidate.ClaimValue))
		}
		values = qualityStrings(values)
		if len(values) < 2 {
			continue
		}
		ids = qualityStrings(ids)
		result = append(result, AuthorityConflict{ID: "conflict:" + qualityHash([]any{key, ids, values}), ClaimType: group[0].ClaimType, ClaimKey: group[0].ClaimKey, EvidenceIDs: ids, Values: values, Resolution: "UNRESOLVED; preserve all participants or report budget/freshness omission"})
	}
	return result
}

func qualityDedupKey(c EvidenceCandidate) string {
	return qualityHash([]any{c.SourceIdentity, c.SourceRevision, c.SourceSnapshot, c.ContentHash, c.RelativePath, c.Symbol, c.EvidenceKind, c.ClaimType, c.ClaimKey, c.ClaimValue, c.Provenance, c.Freshness})
}

func qualityMergeSpans(a, b EvidenceCandidate) (EvidenceCandidate, bool) {
	if a.Span.StartLine > b.Span.EndLine || b.Span.StartLine > a.Span.EndLine {
		return a, false
	}
	if a.Span == b.Span && a.Content == b.Content {
		return a, true
	}
	aLines := strings.Split(strings.TrimSuffix(a.Content, "\n"), "\n")
	bLines := strings.Split(strings.TrimSuffix(b.Content, "\n"), "\n")
	if len(aLines) != a.Span.EndLine-a.Span.StartLine+1 || len(bLines) != b.Span.EndLine-b.Span.StartLine+1 {
		return a, false
	}
	start, end := a.Span.StartLine, a.Span.EndLine
	if b.Span.StartLine < start {
		start = b.Span.StartLine
	}
	if b.Span.EndLine > end {
		end = b.Span.EndLine
	}
	lines := make([]string, end-start+1)
	for line := start; line <= end; line++ {
		inA := line >= a.Span.StartLine && line <= a.Span.EndLine
		inB := line >= b.Span.StartLine && line <= b.Span.EndLine
		if inA && inB && aLines[line-a.Span.StartLine] != bLines[line-b.Span.StartLine] {
			return a, false
		}
		if inA {
			lines[line-start] = aLines[line-a.Span.StartLine]
		} else {
			lines[line-start] = bLines[line-b.Span.StartLine]
		}
	}
	a.Span.StartLine = start
	a.Span.EndLine = end
	if b.Span.StartByte < a.Span.StartByte {
		a.Span.StartByte = b.Span.StartByte
	}
	if b.Span.EndByte > a.Span.EndByte {
		a.Span.EndByte = b.Span.EndByte
	}
	trailingNewline := strings.HasSuffix(a.Content, "\n") || strings.HasSuffix(b.Content, "\n")
	a.Content = strings.Join(lines, "\n")
	if trailingNewline {
		a.Content += "\n"
	}
	return a, true
}

func qualityDeduplicate(candidates []EvidenceCandidate) []EvidenceCandidate {
	result := []EvidenceCandidate{}
	for _, candidate := range candidates {
		merged := false
		for index, existing := range result {
			if qualityDedupKey(existing) != qualityDedupKey(candidate) {
				continue
			}
			combined, ok := qualityMergeSpans(existing, candidate)
			if !ok {
				continue
			}
			combined.FacetIDs = qualityStrings(append(existing.FacetIDs, candidate.FacetIDs...))
			combined.Limitations = qualityStrings(append(existing.Limitations, candidate.Limitations...))
			combined.Links = append(append([]EvidenceLink{}, existing.Links...), candidate.Links...)
			if combined.Span != existing.Span {
				combined.EvidenceID = "evidence:" + qualityHash(struct {
					Identity, Revision, Snapshot, Path, Hash, Symbol, Kind string
					Span                                                   EvidenceSpan
					ClaimType                                              ClaimType
					ClaimKey, ClaimValue                                   string
				}{combined.SourceIdentity, combined.SourceRevision, combined.SourceSnapshot, combined.RelativePath, combined.ContentHash, combined.Symbol, combined.EvidenceKind, combined.Span, combined.ClaimType, combined.ClaimKey, combined.ClaimValue})
			}
			for _, id := range []string{existing.EvidenceID, candidate.EvidenceID} {
				if id != combined.EvidenceID {
					combined.Links = append(combined.Links, EvidenceLink{Kind: "deduplicated_evidence", EvidenceID: id})
				}
			}
			combined.Links = qualityLinks(combined.Links)
			combined.Required = existing.Required || candidate.Required
			combined.ExactMatch = existing.ExactMatch || candidate.ExactMatch
			if candidate.ArchitecturalDistance < combined.ArchitecturalDistance {
				combined.ArchitecturalDistance = candidate.ArchitecturalDistance
			}
			combined.Size = len(combined.Content)
			combined.TokenEstimate = (combined.Size + 3) / 4
			result[index] = combined
			merged = true
			break
		}
		if !merged {
			result = append(result, candidate)
		}
	}
	return result
}
func qualityRankLess(a, b EvidenceCandidate) bool {
	if a.Required != b.Required {
		return a.Required
	}
	role := func(value string) int {
		switch value {
		case "PRIMARY":
			return 0
		case "SUPPORTING":
			return 1
		}
		return 2
	}
	if role(a.Authority.Role) != role(b.Authority.Role) {
		return role(a.Authority.Role) < role(b.Authority.Role)
	}
	if a.Freshness != b.Freshness {
		return a.Freshness == Current
	}
	if a.ExactMatch != b.ExactMatch {
		return a.ExactMatch
	}
	if a.ArchitecturalDistance != b.ArchitecturalDistance {
		return a.ArchitecturalDistance < b.ArchitecturalDistance
	}
	if (a.Provenance == ProjectTest) != (b.Provenance == ProjectTest) {
		return a.Provenance == ProjectTest
	}
	return qualityCandidateKey(a) < qualityCandidateKey(b)
}
