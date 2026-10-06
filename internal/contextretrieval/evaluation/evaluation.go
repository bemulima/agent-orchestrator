// Package evaluation executes independently labelled, local retrieval gold cases.
// Expected labels are fixture inputs; they are never inferred from engine output.
package evaluation

import (
	"fmt"
	"sort"
	"strings"
	"time"

	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
)

const FixtureSchema = "retrieval-gold/v1"
const ReportSchema = "retrieval-evaluation.v1"

type Selector struct {
	Source   string `json:"source,omitempty"`
	Path     string `json:"path,omitempty"`
	Symbol   string `json:"symbol,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Revision string `json:"revision,omitempty"`
	Facet    string `json:"facet,omitempty"`
}

type Expected struct {
	Status              core.Status                      `json:"status"`
	MustFind            []Selector                       `json:"must_find,omitempty"`
	ShouldFind          []Selector                       `json:"should_find,omitempty"`
	Relevant            []Selector                       `json:"relevant,omitempty"`
	MustNotFind         []Selector                       `json:"must_not_find,omitempty"`
	MustNotRouteWrites  []string                         `json:"must_not_route_writes,omitempty"`
	Diagnostics         []string                         `json:"diagnostics,omitempty"`
	Conflicts           []string                         `json:"authority_conflicts,omitempty"`
	RequiredContracts   []Selector                       `json:"required_contracts,omitempty"`
	IncompleteMandatory bool                             `json:"incomplete_mandatory,omitempty"`
	EvidenceCount       *int                             `json:"evidence_count,omitempty"`
	Coverage            map[string]core.RequirementState `json:"coverage,omitempty"`
	Counters            map[string]int                   `json:"counters,omitempty"`
	MinimumCounters     map[string]int                   `json:"minimum_counters,omitempty"`
	Provenance          map[string]core.Provenance       `json:"provenance,omitempty"`
	Freshness           map[string]core.Freshness        `json:"freshness,omitempty"`
	ErrorContains       string                           `json:"error_contains,omitempty"`
}

// FileSpec describes synthetic bytes, never real user repositories or secrets.
type FileSpec struct {
	Source  string `json:"source,omitempty"`
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	Repeat  int    `json:"repeat,omitempty"`
	Count   int    `json:"count,omitempty"`
	Kind    string `json:"kind,omitempty"`
}

type Fixture struct {
	Schema      string                   `json:"schema"`
	ID          string                   `json:"id"`
	Scenario    string                   `json:"scenario"`
	Adversarial bool                     `json:"adversarial,omitempty"`
	Mode        string                   `json:"mode"`
	Files       []FileSpec               `json:"files,omitempty"`
	Request     core.RetrievalRequest    `json:"request"`
	Candidates  []core.EvidenceCandidate `json:"candidates,omitempty"`
	Expand      *core.ExpandRequest      `json:"expand,omitempty"`
	ExpandTimes int                      `json:"expand_times,omitempty"`
	Mutations   []FileSpec               `json:"mutations,omitempty"`
	R1Setup     string                   `json:"r1_setup,omitempty"`
	Expected    Expected                 `json:"expected"`
}

type Observation struct {
	Status      core.Status
	Evidence    []core.EvidenceCandidate
	Coverage    []core.CoverageResult
	Diagnostics []core.RetrievalDiagnostic
	Conflicts   []core.AuthorityConflict
	Omissions   []core.Omission
	WriteOwners []string
	Tokens      int
	Latency     time.Duration
	Counters    map[string]int
	Error       string
}

// Counts are retained with every ratio so the report cannot hide empty denominators.
type Metrics struct {
	MustFindExpected         int            `json:"must_find_expected"`
	MustFindFound            int            `json:"must_find_found"`
	MustFindRecall           float64        `json:"must_find_recall"`
	RecallExpected           int            `json:"recall_expected"`
	RecallFound              int            `json:"recall_found"`
	Recall                   float64        `json:"recall"`
	Selected                 int            `json:"selected_evidence"`
	RelevantSelected         int            `json:"relevant_selected"`
	Precision                float64        `json:"precision"`
	ForbiddenScopeViolations int            `json:"forbidden_scope_violations"`
	ContractsExpected        int            `json:"contracts_expected"`
	ContractsMissing         int            `json:"contracts_missing"`
	MissingContractRate      float64        `json:"missing_contract_rate"`
	SilentMissingContracts   int            `json:"silent_missing_required_contracts"`
	ContextTokens            int            `json:"context_token_count"`
	Duplicates               int            `json:"duplicate_evidence"`
	DuplicateRatio           float64        `json:"duplicate_ratio"`
	Sources                  int            `json:"selected_source_revisions"`
	StaleSources             int            `json:"stale_source_revisions"`
	StaleSourceRate          float64        `json:"stale_source_rate"`
	StaleEvidenceLeaks       int            `json:"stale_evidence_leaks"`
	ExpectedConflicts        int            `json:"expected_authority_conflicts"`
	ConflictsSurfaced        int            `json:"authority_conflicts_surfaced"`
	SilentAuthorityConflicts int            `json:"silent_authority_conflicts"`
	FalseCompleteCases       int            `json:"false_complete_cases"`
	RetrievalLatencyMillis   float64        `json:"retrieval_latency_millis"`
	CoverageStatuses         map[string]int `json:"coverage_truncation_status"`
}

type CaseResult struct {
	ID          string         `json:"id"`
	Scenario    string         `json:"scenario"`
	Adversarial bool           `json:"adversarial"`
	Status      core.Status    `json:"status"`
	Metrics     Metrics        `json:"metrics"`
	Counters    map[string]int `json:"counters"`
	Failures    []string       `json:"failures"`
	Passed      bool           `json:"passed"`
}

type Report struct {
	SchemaVersion    string       `json:"schema_version"`
	FixtureCount     int          `json:"fixture_count"`
	ScenarioCount    int          `json:"scenario_count"`
	AdversarialCount int          `json:"adversarial_count"`
	Cases            []CaseResult `json:"cases"`
	Metrics          Metrics      `json:"metrics"`
	GatesPassed      bool         `json:"gates_passed"`
	Failures         []string     `json:"failures"`
}

func ratio(n, d int) float64 {
	if d == 0 {
		return 1
	}
	return float64(n) / float64(d)
}

func (m *Metrics) ratios() {
	m.MustFindRecall = ratio(m.MustFindFound, m.MustFindExpected)
	m.Recall = ratio(m.RecallFound, m.RecallExpected)
	m.Precision = ratio(m.RelevantSelected, m.Selected)
	m.MissingContractRate = 0
	if m.ContractsExpected > 0 {
		m.MissingContractRate = float64(m.ContractsMissing) / float64(m.ContractsExpected)
	}
	m.DuplicateRatio = 0
	if m.Selected > 0 {
		m.DuplicateRatio = float64(m.Duplicates) / float64(m.Selected)
	}
	m.StaleSourceRate = 0
	if m.Sources > 0 {
		m.StaleSourceRate = float64(m.StaleSources) / float64(m.Sources)
	}
}

func matches(s Selector, e core.EvidenceCandidate) bool {
	return (s.Source == "" || s.Source == e.SourceIdentity) && (s.Path == "" || s.Path == e.RelativePath) && (s.Symbol == "" || s.Symbol == e.Symbol) && (s.Kind == "" || s.Kind == e.EvidenceKind) && (s.Revision == "" || s.Revision == e.SourceRevision) && (s.Facet == "" || contains(e.FacetIDs, s.Facet))
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
func found(s Selector, xs []core.EvidenceCandidate) bool {
	for _, e := range xs {
		if matches(s, e) {
			return true
		}
	}
	return false
}
func relevant(e core.EvidenceCandidate, selectors []Selector) bool {
	for _, s := range selectors {
		if matches(s, e) {
			return true
		}
	}
	return false
}

func ValidateFixture(f Fixture) error {
	if f.Schema != FixtureSchema || f.ID == "" || f.Scenario == "" {
		return fmt.Errorf("invalid fixture identity/schema")
	}
	switch f.Mode {
	case "prepare", "expand", "quality", "routing_coverage":
	default:
		return fmt.Errorf("unknown mode %q", f.Mode)
	}
	if f.Expected.Status == "" {
		return fmt.Errorf("expected status must be independently declared")
	}
	switch f.Expected.Status {
	case core.Complete, core.Partial, core.Blocked, core.Unsupported, core.Stale, core.Invalid, core.Unknown:
	default:
		return fmt.Errorf("unknown expected status")
	}
	for _, xs := range [][]Selector{f.Expected.MustFind, f.Expected.ShouldFind, f.Expected.Relevant, f.Expected.MustNotFind, f.Expected.RequiredContracts} {
		for _, s := range xs {
			if s.Path == "" && s.Symbol == "" && s.Facet == "" {
				return fmt.Errorf("selector needs explicit path, symbol, or facet")
			}
		}
	}
	if f.Mode == "expand" && f.Expand == nil {
		return fmt.Errorf("expand request required")
	}
	if f.ExpandTimes < 0 || f.ExpandTimes > 32 {
		return fmt.Errorf("expand_times must be within [0,32]")
	}
	return nil
}

func Evaluate(f Fixture, o Observation) CaseResult {
	r := CaseResult{ID: f.ID, Scenario: f.Scenario, Adversarial: f.Adversarial, Status: o.Status, Counters: o.Counters, Failures: []string{}}
	m := Metrics{Selected: len(o.Evidence), ContextTokens: o.Tokens, RetrievalLatencyMillis: float64(o.Latency) / float64(time.Millisecond), CoverageStatuses: map[string]int{}}
	if o.Status != f.Expected.Status {
		r.Failures = append(r.Failures, fmt.Sprintf("status %s, expected %s", o.Status, f.Expected.Status))
	}
	if f.Expected.ErrorContains != "" && !strings.Contains(o.Error, f.Expected.ErrorContains) {
		r.Failures = append(r.Failures, "missing expected error: "+f.Expected.ErrorContains)
	}
	if f.Expected.ErrorContains == "" && o.Error != "" {
		r.Failures = append(r.Failures, "unexpected engine error: "+o.Error)
	}
	labels := append(append([]Selector{}, f.Expected.MustFind...), f.Expected.ShouldFind...)
	m.MustFindExpected = len(f.Expected.MustFind)
	m.RecallExpected = len(labels)
	for _, s := range f.Expected.MustFind {
		if found(s, o.Evidence) {
			m.MustFindFound++
		}
	}
	for _, s := range labels {
		if found(s, o.Evidence) {
			m.RecallFound++
		}
	}
	relevance := append(append([]Selector{}, f.Expected.Relevant...), labels...)
	relevance = append(relevance, f.Expected.RequiredContracts...)
	sources, stale := map[string]bool{}, map[string]bool{}
	for i, e := range o.Evidence {
		if relevant(e, relevance) {
			m.RelevantSelected++
		}
		for _, s := range f.Expected.MustNotFind {
			if matches(s, e) {
				m.ForbiddenScopeViolations++
				break
			}
		}
		key := e.SourceIdentity + "\x00" + e.SourceRevision
		sources[key] = true
		if e.Freshness != core.Current && e.Freshness != core.DirtySnapshot {
			stale[key] = true
			if e.ClaimType == core.ImplementationBehavior || e.ClaimType == core.DatabaseSchema || e.ClaimType == core.DataSemantics {
				m.StaleEvidenceLeaks++
			}
		}
		for j := 0; j < i; j++ {
			if duplicate(o.Evidence[j], e) {
				m.Duplicates++
				break
			}
		}
	}
	m.Sources = len(sources)
	m.StaleSources = len(stale)
	for _, source := range f.Expected.MustNotRouteWrites {
		if contains(o.WriteOwners, source) {
			m.ForbiddenScopeViolations++
		}
	}
	m.ContractsExpected = len(f.Expected.RequiredContracts)
	for _, s := range f.Expected.RequiredContracts {
		if !found(s, o.Evidence) {
			m.ContractsMissing++
			if !explained(s, o) {
				m.SilentMissingContracts++
			}
		}
	}
	for _, key := range f.Expected.Conflicts {
		m.ExpectedConflicts++
		surfaced := false
		for _, c := range o.Conflicts {
			if c.ClaimKey == key && len(c.EvidenceIDs) >= 2 && len(c.Values) >= 2 {
				surfaced = true
				break
			}
		}
		if surfaced {
			m.ConflictsSurfaced++
		} else {
			m.SilentAuthorityConflicts++
		}
	}
	codes := map[string]bool{}
	for _, d := range o.Diagnostics {
		codes[d.Code] = true
	}
	for _, v := range o.Omissions {
		codes[v.Reason] = true
	}
	incomplete := f.Expected.IncompleteMandatory
	for _, c := range o.Coverage {
		m.CoverageStatuses[string(c.Status)]++
		for _, reason := range c.Reasons {
			codes[reason] = true
		}
		for _, facet := range f.Request.RequiredFacets {
			if c.FacetID == facet.ID && c.RequirementState != core.Found && c.RequirementState != core.NotFound {
				incomplete = true
			}
		}
	}
	for _, code := range f.Expected.Diagnostics {
		if !codes[code] {
			r.Failures = append(r.Failures, "missing diagnostic: "+code)
		}
	}
	if incomplete && o.Status == core.Complete {
		m.FalseCompleteCases++
	}
	for facet, state := range f.Expected.Coverage {
		ok := false
		for _, c := range o.Coverage {
			if c.FacetID == facet && c.RequirementState == state {
				ok = true
			}
		}
		if !ok {
			r.Failures = append(r.Failures, "missing coverage "+facet+"="+string(state))
		}
	}
	for counter, expected := range f.Expected.Counters {
		actual, ok := o.Counters[counter]
		if !ok || actual != expected {
			r.Failures = append(r.Failures, fmt.Sprintf("counter %s=%d, expected %d (present=%t)", counter, actual, expected, ok))
		}
	}
	for counter, minimum := range f.Expected.MinimumCounters {
		actual, ok := o.Counters[counter]
		if !ok || actual < minimum {
			r.Failures = append(r.Failures, fmt.Sprintf("counter %s=%d, expected >=%d (present=%t)", counter, actual, minimum, ok))
		}
	}
	for path, expected := range f.Expected.Provenance {
		seen := false
		for _, e := range o.Evidence {
			if e.RelativePath == path {
				seen = true
				if e.Provenance != expected {
					r.Failures = append(r.Failures, fmt.Sprintf("provenance %s=%s, expected %s", path, e.Provenance, expected))
				}
			}
		}
		if !seen {
			r.Failures = append(r.Failures, "missing provenance subject: "+path)
		}
	}
	for path, expected := range f.Expected.Freshness {
		seen := false
		for _, e := range o.Evidence {
			if e.RelativePath == path {
				seen = true
				if e.Freshness != expected {
					r.Failures = append(r.Failures, fmt.Sprintf("freshness %s=%s, expected %s", path, e.Freshness, expected))
				}
			}
		}
		if !seen {
			r.Failures = append(r.Failures, "missing freshness subject: "+path)
		}
	}
	if f.Expected.EvidenceCount != nil && len(o.Evidence) != *f.Expected.EvidenceCount {
		r.Failures = append(r.Failures, fmt.Sprintf("selected evidence count %d, expected %d", len(o.Evidence), *f.Expected.EvidenceCount))
	}
	m.ratios()
	r.Metrics = m
	r.Failures = append(r.Failures, gateFailures(m)...)
	sort.Strings(r.Failures)
	r.Passed = len(r.Failures) == 0
	return r
}

func explained(s Selector, o Observation) bool {
	for _, c := range o.Coverage {
		if s.Facet != "" && c.FacetID == s.Facet && c.RequirementState != "" && c.RequirementState != core.Found {
			return true
		}
	}
	for _, d := range o.Diagnostics {
		if (s.Facet != "" && d.FacetID == s.Facet) || (s.Path != "" && d.RelativePath == s.Path) {
			return true
		}
	}
	for _, v := range o.Omissions {
		if (s.Facet != "" && v.FacetID == s.Facet) || (s.Path != "" && v.RelativePath == s.Path) {
			return true
		}
	}
	return false
}

func duplicate(a, b core.EvidenceCandidate) bool {
	if a.SourceIdentity != b.SourceIdentity || a.SourceRevision != b.SourceRevision || a.ContentHash != b.ContentHash || a.RelativePath != b.RelativePath || a.Symbol != b.Symbol || a.EvidenceKind != b.EvidenceKind || a.ClaimKey != b.ClaimKey || a.ClaimValue != b.ClaimValue {
		return false
	}
	return a.Span.StartLine <= b.Span.EndLine && b.Span.StartLine <= a.Span.EndLine
}

func gateFailures(m Metrics) []string {
	xs := []string{}
	if m.MustFindFound != m.MustFindExpected {
		xs = append(xs, "must-find recall below 1.00")
	}
	if m.ForbiddenScopeViolations > 0 {
		xs = append(xs, "forbidden-scope violation")
	}
	if m.SilentMissingContracts > 0 {
		xs = append(xs, "silent missing required contract")
	}
	if m.StaleEvidenceLeaks > 0 {
		xs = append(xs, "stale runtime evidence leak")
	}
	if m.SilentAuthorityConflicts > 0 {
		xs = append(xs, "silent authority conflict")
	}
	if m.FalseCompleteCases > 0 {
		xs = append(xs, "false COMPLETE after incomplete mandatory search")
	}
	if m.Precision < .8 {
		xs = append(xs, "precision below 0.80")
	}
	return xs
}

func aggregate(rs []CaseResult) Metrics {
	m := Metrics{CoverageStatuses: map[string]int{}}
	for _, r := range rs {
		v := r.Metrics
		m.MustFindExpected += v.MustFindExpected
		m.MustFindFound += v.MustFindFound
		m.RecallExpected += v.RecallExpected
		m.RecallFound += v.RecallFound
		m.Selected += v.Selected
		m.RelevantSelected += v.RelevantSelected
		m.ForbiddenScopeViolations += v.ForbiddenScopeViolations
		m.ContractsExpected += v.ContractsExpected
		m.ContractsMissing += v.ContractsMissing
		m.SilentMissingContracts += v.SilentMissingContracts
		m.ContextTokens += v.ContextTokens
		m.Duplicates += v.Duplicates
		m.Sources += v.Sources
		m.StaleSources += v.StaleSources
		m.StaleEvidenceLeaks += v.StaleEvidenceLeaks
		m.ExpectedConflicts += v.ExpectedConflicts
		m.ConflictsSurfaced += v.ConflictsSurfaced
		m.SilentAuthorityConflicts += v.SilentAuthorityConflicts
		m.FalseCompleteCases += v.FalseCompleteCases
		m.RetrievalLatencyMillis += v.RetrievalLatencyMillis
		for k, n := range v.CoverageStatuses {
			m.CoverageStatuses[k] += n
		}
	}
	m.ratios()
	return m
}
