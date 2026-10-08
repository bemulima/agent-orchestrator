package capability

import (
	"fmt"
	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"strings"
)

// CapabilityAcceptanceSchema is independent of retrieval-gold/v1 and the pack ABI.
// Callers author classifications from capability contracts and independent audits,
// never from which evidence happened to be selected.
const CapabilityAcceptanceSchema = "retrieval-capability-acceptance/v2"

type CapabilityClass string

const (
	SupportedRequired       CapabilityClass = "SUPPORTED_REQUIRED"
	SupportedOptional       CapabilityClass = "SUPPORTED_OPTIONAL"
	UnsupportedSemantic     CapabilityClass = "UNSUPPORTED_SEMANTIC"
	SecurityExcluded        CapabilityClass = "SECURITY_EXCLUDED"
	MissingOwnerContract    CapabilityClass = "MISSING_OWNER_CONTRACT"
	MissingBusinessContract CapabilityClass = "MISSING_BUSINESS_CONTRACT"
	NotApplicable           CapabilityClass = "NOT_APPLICABLE"
)

type Selector struct {
	Source string `json:"source,omitempty"`
	Path   string `json:"path,omitempty"`
	Symbol string `json:"symbol,omitempty"`
	Facet  string `json:"facet,omitempty"`
}

type CapabilityRequirement struct {
	Diagnostics  []string            `json:"diagnostics,omitempty"`
	Selector     Selector            `json:"selector"`
	Class        CapabilityClass     `json:"class"`
	Diagnostic   string              `json:"diagnostic,omitempty"`
	Basis        string              `json:"basis"`
	AbsenceProof *ScopedAbsenceProof `json:"absence_proof,omitempty"`
}

// ScopedAbsenceProof is produced by a separate complete, bounded resolver search.
// It does not alter the original pack or claim its wider acquisition was complete.
type ScopedAbsenceProof struct {
	Snapshot string              `json:"snapshot"`
	Scope    string              `json:"scope"`
	Coverage core.CoverageResult `json:"coverage"`
}
type CapabilityCounts struct {
	Expected int `json:"expected"`
	Found    int `json:"found"`
}
type CapabilityReport struct {
	Schema     string                               `json:"schema"`
	Total      int                                  `json:"total"`
	Categories map[CapabilityClass]CapabilityCounts `json:"categories"`
	Failures   []string                             `json:"failures"`
	Passed     bool                                 `json:"passed"`
}

// EvaluateCapabilities retains every obligation in its original denominator.
// Non-supported classes pass only with explicit scoped diagnostics and honest
// coverage; lexical bytes cannot certify an unsupported semantic obligation.
func EvaluateCapabilities(pack core.ContextPack, required []CapabilityRequirement) CapabilityReport {
	r := CapabilityReport{Schema: CapabilityAcceptanceSchema, Total: len(required), Categories: map[CapabilityClass]CapabilityCounts{}, Failures: []string{}}
	for _, q := range required {
		counts := r.Categories[q.Class]
		counts.Expected++
		found := false
		for _, e := range pack.Evidence {
			if capabilityMatches(q.Selector, e) {
				found = true
			}
		}
		valid := q.Basis != ""
		var state core.RequirementState
		complete := false
		coverageSeen := false
		for _, c := range pack.Coverage {
			if q.Selector.Facet != "" && c.FacetID == q.Selector.Facet {
				state = c.RequirementState
				complete = c.Complete
				coverageSeen = true
			}
		}
		diagnosed := false
		for _, d := range pack.UnresolvedQuestions {
			scoped := q.Selector.Facet != "" && d.FacetID == q.Selector.Facet
			if q.Selector.Facet == "" {
				scoped = d.SourceIdentity == q.Selector.Source && d.RelativePath == q.Selector.Path
			}
			codeMatch := d.Code == q.Diagnostic
			for _, code := range q.Diagnostics {
				codeMatch = codeMatch || d.Code == code
			}
			if scoped && codeMatch {
				diagnosed = true
			}
		}
		switch q.Class {
		case SupportedRequired:
			valid = valid && found
			if coverageSeen && state == core.RequirementUnsupported {
				valid = false
			}
			if found {
				counts.Found++
			}
		case SupportedOptional:
			if found {
				counts.Found++
			}
		case UnsupportedSemantic:
			valid = valid && diagnosed && coverageSeen && state == core.RequirementUnsupported && !complete && pack.Status != core.Complete
			if found {
				valid = false
			} // facet-bound selection cannot pretend semantic proof
		case SecurityExcluded:
			valid = valid && diagnosed && coverageSeen && state == core.ExcludedByPolicy && !complete && !found && pack.Status != core.Complete
		case MissingBusinessContract, MissingOwnerContract:
			absence := coverageSeen && state == core.NotFound && complete
			if proof := q.AbsenceProof; proof != nil {
				row := proof.Coverage
				absence = absence || (len(proof.Snapshot) >= 64 && proof.Scope == q.Selector.Path && row.SourceIdentity == q.Selector.Source && row.FacetID == q.Selector.Facet && row.RequirementState == core.NotFound && row.Complete && !row.TerminatedByLimit && row.Errors == 0 && row.Unreadable == 0)
			}
			valid = valid && diagnosed && absence && !found && pack.Status != core.Complete
		case NotApplicable:
		default:
			valid = false
		}
		if !valid {
			r.Failures = append(r.Failures, fmt.Sprintf("%s:%s:%s (%s) obligation not satisfied", q.Selector.Source, q.Selector.Path, q.Selector.Facet, q.Class))
		}
		r.Categories[q.Class] = counts
	}
	r.Passed = len(r.Failures) == 0
	return r
}

// V2 preserves the Go resolver's unqualified syntax-selector convention without
// changing the exact symbol matching semantics of the original Gold v1.
func capabilityMatches(s Selector, e core.EvidenceCandidate) bool {
	wanted := s.Symbol
	s.Symbol = ""
	facetMatch := s.Facet == ""
	for _, id := range e.FacetIDs {
		facetMatch = facetMatch || id == s.Facet
	}
	return (s.Source == "" || s.Source == e.SourceIdentity) && (s.Path == "" || s.Path == e.RelativePath) && facetMatch && (wanted == "" || e.Symbol == wanted || strings.HasSuffix(e.Symbol, "."+wanted))
}
