package contextretrieval

import (
	"context"
	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"testing"
)

func TestClosureSecurityExclusionDiagnostic(t *testing.T) {
	snapshot := projectSnapshot("safe.go", "package fixture\n")
	f := core.Facet{ID: "assertion", Required: true, SourceIdentity: "local:fixture", Path: "assertion_test.go", QueryKind: core.QueryTests, Resolver: "tests"}
	snapshot.Diagnostics = append(snapshot.Diagnostics, core.RetrievalDiagnostic{Code: "SECRET_CONTENT_EXCLUDED", Status: core.Complete, SourceIdentity: f.SourceIdentity, RelativePath: f.Path})
	result, err := (TestResolver{}).Resolve(context.Background(), core.RetrievalPlan{}, f, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	plan := core.RetrievalPlan{RequiredFacets: []core.Facet{f}, Budget: core.Budget{MaxContextTokens: 32000}}
	pack, err := core.BuildPack(plan, nil, core.QualityResult{Candidates: result.Candidates}, result.Coverage, append(snapshot.Diagnostics, result.Diagnostics...), core.BudgetUsed{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range pack.UnresolvedQuestions {
		found = found || (d.Code == "REQUIRED_EVIDENCE_EXCLUDED_BY_SECURITY" && d.FacetID == f.ID)
	}
	if !found {
		t.Fatalf("security exclusion was not explicitly classified: %+v", pack.UnresolvedQuestions)
	}
	if len(pack.Evidence) != 0 || pack.Status == core.Complete {
		t.Fatal("excluded bytes fabricated or false complete")
	}
}

func TestClosureLexicalSQLCapability(t *testing.T) {
	snapshot := projectSnapshot("migrations/0001.sql", "CREATE TABLE stat_events (lesson_id UUID, event_type String);\nSELECT event_type FROM stat_events;\n")
	reference := projectSnapshot("internal/repository/store.go", "package repository\nconst schemaFile = \"migrations/0001.sql\"\n")
	snapshot.Documents = append(snapshot.Documents, reference.Documents...)
	for _, probe := range []struct{ path, text string }{
		{"migrations/0001.sql", ""}, {"migrations/0001.sql", "stat_events"}, {"migrations/0001.sql", "lesson_id"}, {"migrations/0001.sql", "CREATE TABLE"}, {"migrations/0001.sql", "SELECT event_type"}, {"internal/repository/store.go", "migrations/0001.sql"},
	} {
		facet := core.Facet{ID: "lexical", SourceIdentity: "local:fixture", Path: probe.path, Text: probe.text, QueryKind: core.QueryExact}
		result, err := (ExactResolver{}).Resolve(context.Background(), core.RetrievalPlan{}, facet, snapshot)
		if err != nil || len(result.Candidates) != 1 {
			t.Fatalf("literal probe %+v: %v %+v", probe, err, result)
		}
		if len(result.Candidates[0].Limitations) == 0 {
			t.Fatal("lexical limitation absent")
		}
		facet.QueryKind = core.QuerySchema
		semantic, err := (ExactResolver{}).Resolve(context.Background(), core.RetrievalPlan{}, facet, snapshot)
		if err != nil || len(semantic.Candidates) != 0 || len(semantic.Coverage) != 1 || semantic.Coverage[0].RequirementState != core.RequirementUnsupported {
			t.Fatal("semantic SQL impersonation")
		}
	}
}
