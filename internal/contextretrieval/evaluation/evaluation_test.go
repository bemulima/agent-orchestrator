package evaluation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
)

func TestEvaluationIndependentLabelsAndHardGateFaults(t *testing.T) {
	base := Fixture{Schema: FixtureSchema, ID: "independent", Scenario: "Independent ground truth", Mode: "prepare", Expected: Expected{Status: core.Complete, MustFind: []Selector{{Path: "required.go"}}, Relevant: []Selector{{Path: "required.go"}}}}
	good := core.EvidenceCandidate{SourceIdentity: "fixture", SourceRevision: "revision", RelativePath: "required.go", ContentHash: strings.Repeat("a", 64), EvidenceKind: "source", Freshness: core.Current, ClaimType: core.ImplementationBehavior, Span: core.EvidenceSpan{StartLine: 1, EndLine: 2}}
	if result := Evaluate(base, Observation{Status: core.Complete, Evidence: []core.EvidenceCandidate{good}}); !result.Passed {
		t.Fatal(result.Failures)
	}
	for _, tc := range []struct {
		name        string
		fixture     func(*Fixture)
		observation Observation
		failure     string
	}{
		{"recall", nil, Observation{Status: core.Complete}, "must-find recall below 1.00"},
		{"precision", nil, Observation{Status: core.Complete, Evidence: []core.EvidenceCandidate{good, {RelativePath: "irrelevant.go"}}}, "precision below 0.80"},
		{"scope", func(f *Fixture) { f.Expected.MustNotFind = []Selector{{Path: "required.go"}} }, Observation{Status: core.Complete, Evidence: []core.EvidenceCandidate{good}}, "forbidden-scope violation"},
		{"write owner", func(f *Fixture) { f.Expected.MustNotRouteWrites = []string{"neighbor"} }, Observation{Status: core.Complete, Evidence: []core.EvidenceCandidate{good}, WriteOwners: []string{"neighbor"}}, "forbidden-scope violation"},
		{"missing contract", func(f *Fixture) {
			f.Expected.RequiredContracts = []Selector{{Path: "contract.yaml", Facet: "contract"}}
		}, Observation{Status: core.Complete, Evidence: []core.EvidenceCandidate{good}}, "silent missing required contract"},
		{"conflict suppression", func(f *Fixture) { f.Expected.Conflicts = []string{"independent-claim"} }, Observation{Status: core.Complete, Evidence: []core.EvidenceCandidate{good}}, "silent authority conflict"},
		{"false complete", func(f *Fixture) { f.Expected.IncompleteMandatory = true }, Observation{Status: core.Complete, Evidence: []core.EvidenceCandidate{good}}, "false COMPLETE after incomplete mandatory search"},
		{"stale leak", nil, Observation{Status: core.Complete, Evidence: []core.EvidenceCandidate{func() core.EvidenceCandidate { e := good; e.Freshness = core.StaleEvidence; return e }()}}, "stale runtime evidence leak"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := base
			if tc.fixture != nil {
				tc.fixture(&f)
			}
			r := Evaluate(f, tc.observation)
			if r.Passed || !contains(r.Failures, tc.failure) {
				t.Fatalf("fault was not detected: %#v", r)
			}
		})
	}
}

func TestEvaluationMissingContractExplanationMustMatchRequirement(t *testing.T) {
	f := Fixture{ID: "contract", Mode: "prepare", Expected: Expected{Status: core.Blocked, RequiredContracts: []Selector{{Path: "contract.yaml", Facet: "contract"}}}}
	o := Observation{Status: core.Blocked, Diagnostics: []core.RetrievalDiagnostic{{Code: "irrelevant", RelativePath: "elsewhere.yaml"}}}
	if Evaluate(f, o).Metrics.SilentMissingContracts != 1 {
		t.Fatal("unrelated error concealed missing contract")
	}
	o.Coverage = []core.CoverageResult{{FacetID: "contract", RequirementState: core.NotVerified}}
	if Evaluate(f, o).Metrics.SilentMissingContracts != 0 {
		t.Fatal("explicit matching unknown requirement was treated as silent")
	}
}

func TestEvaluationRevisionsAndConflictClaimsAreNotDuplicates(t *testing.T) {
	a := core.EvidenceCandidate{SourceIdentity: "source", SourceRevision: "one", ContentHash: "hash", RelativePath: "file.go", EvidenceKind: "source", Span: core.EvidenceSpan{StartLine: 1, EndLine: 2}}
	b := a
	b.SourceRevision = "two"
	if duplicate(a, b) {
		t.Fatal("different revisions considered duplicates")
	}
	b = a
	b.ClaimKey = "claim"
	b.ClaimValue = "other"
	if duplicate(a, b) {
		t.Fatal("conflicting claims considered duplicates")
	}
	b = a
	b.Span.StartLine = 2
	b.Span.EndLine = 3
	if !duplicate(a, b) {
		t.Fatal("overlap not counted")
	}
}

func TestFixtureReaderRejectsUnsafeSourcesAndUnknownSchema(t *testing.T) {
	dir := t.TempDir()
	good := `{"schema":"retrieval-gold/v1","id":"case","scenario":"safe synthetic","mode":"prepare","request":{},"expected":{"status":"BLOCKED"}}`
	for _, tc := range []struct{ name, content string }{{"unknown.json", strings.Replace(good, `"request":{}`, `"request":{},"unknown":true`, 1)}, {"trailing.json", good + good}, {"version.json", strings.Replace(good, "retrieval-gold/v1", "retrieval-gold/v999", 1)}} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := readFixture(path); err == nil {
				t.Fatal("invalid fixture accepted")
			}
		})
	}
	for _, name := range []string{"credentials.json", "private.key.json", ".env.json"} {
		if _, err := readFixture(filepath.Join(dir, name)); err == nil || !strings.Contains(err.Error(), "excluded by policy") {
			t.Fatalf("secret path %s not rejected before open: %v", name, err)
		}
	}
	target := filepath.Join(dir, "safe.json")
	if err := os.WriteFile(target, []byte(good), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readFixture(link); err == nil {
		t.Fatal("symlink accepted")
	}
	ancestor := filepath.Join(dir, "alias")
	if err := os.Symlink(dir, ancestor); err != nil {
		t.Fatal(err)
	}
	if _, err := readFixture(filepath.Join(ancestor, "safe.json")); err == nil {
		t.Fatal("symlink ancestor accepted")
	}
	hardlink := filepath.Join(dir, "hardlink.json")
	if err := os.Link(target, hardlink); err != nil {
		t.Fatal(err)
	}
	if _, err := readFixture(hardlink); err == nil {
		t.Fatal("hardlink accepted")
	}
	if _, err := EvaluateDirectory(context.Background(), filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing gold suite passed")
	}
}

func TestFixtureLabelsRequireIndependentSelectors(t *testing.T) {
	f := Fixture{Schema: FixtureSchema, ID: "bad", Scenario: "bad selector", Mode: "prepare", Expected: Expected{Status: core.Complete, Relevant: []Selector{{Source: "fixture"}}}}
	if ValidateFixture(f) == nil {
		t.Fatal("unbounded all-source relevance selector accepted")
	}
}

func TestFixtureExpansionDepthValidation(t *testing.T) {
	f := Fixture{Schema: FixtureSchema, ID: "expand-depth", Scenario: "bounded expansion", Mode: "expand", Expand: &core.ExpandRequest{}, Expected: Expected{Status: core.Partial}}
	for _, count := range []int{-1, 33} {
		f.ExpandTimes = count
		if ValidateFixture(f) == nil {
			t.Fatalf("expand_times=%d accepted", count)
		}
	}
	for _, count := range []int{0, 1, 32} {
		f.ExpandTimes = count
		if err := ValidateFixture(f); err != nil {
			t.Fatalf("expand_times=%d: %v", count, err)
		}
	}
}
