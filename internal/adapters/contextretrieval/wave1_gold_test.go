package contextretrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/planning"
)

func wave1Files(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
func wave1Route(t *testing.T, root, text string) (domain.RoutingResult, domain.ContractPlan, []core.CoverageResult, core.RouteContext) {
	t.Helper()
	catalog, err := agentcontrol.LoadCatalog(os.DirFS("../../.."))
	if err != nil {
		t.Fatal(err)
	}
	route, contract, report, err := planning.BuildRoutingMetadataWithCoverage(text, domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "fixture"}}}, []domain.Project{{ID: "fixture", SourceIdentity: "local:fixture", LocalPath: &root}}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]core.SourceAdmission{"fixture": {Identity: "local:fixture", Root: root, ReadPaths: []string{"."}}}
	coverage, err := AdaptRoutingCoverage(report, route, &contract, sources)
	if err != nil {
		t.Fatal(err)
	}
	adapted, err := AdaptRoute(route, sources, &contract, coverage)
	if err != nil {
		t.Fatal(err)
	}
	return route, contract, coverage, adapted
}
func wave1Shape() map[string]string {
	return map[string]string{"go.mod": "module fixture\n", "internal/domain/model.go": "package domain\ntype Model struct{}\n", "internal/usecase/change.go": "package usecase\nfunc Change(){}\n", "internal/transport/handler.go": "package transport\nfunc Handle(){}\n", "internal/infrastructure/store.go": "package infrastructure\nfunc Store(){}\n"}
}

// These offline integration Gold cases exercise the real routing/R1 adapter,
// which serialized request.Route fixtures cannot regress.
func TestWave1GoldAggregateSelectors(t *testing.T) {
	for _, long := range []bool{false, true} {
		t.Run(fmt.Sprint(long), func(t *testing.T) {
			files := wave1Shape()
			a, b := "Reader", "Result"
			if long {
				a += strings.Repeat("A", 270)
				b += strings.Repeat("B", 270)
			}
			files["internal/domain/ports.go"] = "package domain\ntype " + a + " interface { Read() }\ntype " + b + " struct{}\n"
			root := wave1Files(t, files)
			_, _, _, route := wave1Route(t, root, "change repository interface method")
			seen := false
			for _, f := range route.SeedFacets {
				if f.Path == "internal/domain/ports.go" && f.QueryKind == core.QueryContract {
					seen = true
					if f.Symbol != "" {
						t.Fatalf("aggregate used as scalar symbol (%d bytes)", len(f.Symbol))
					}
				}
			}
			if !seen {
				t.Fatal("real routing contract seed absent")
			}
			req := core.RetrievalRequest{RequestID: "aggregate", Task: "inspect existing port", Purpose: "analysis", Route: route, Sources: []core.SourceAdmission{{Identity: "local:fixture", Root: root, ReadPaths: []string{"."}}}, Budget: core.Budget{MaxContextTokens: 32000, MaxSourceBytes: 1 << 20}}
			pack, _, err := NewEngine().Prepare(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, e := range pack.Evidence {
				found = found || e.RelativePath == "internal/domain/ports.go"
			}
			if !found {
				t.Fatal("existing contract missing")
			}
			for _, d := range pack.UnresolvedQuestions {
				if d.Code == "MISSING_REQUIRED_CONTRACT" && d.RelativePath == "internal/domain/ports.go" {
					t.Fatal("false missing contract")
				}
			}
		})
	}
}
func TestWave1GoldCoverageEnvelope(t *testing.T) {
	files := wave1Shape()
	for i := 0; i < 490; i++ {
		files[fmt.Sprintf(".ai/contracts/f%04d.yaml", i)] = "contract: fixture\n"
	}
	root := wave1Files(t, files)
	_, _, coverage, route := wave1Route(t, root, "change business process")
	raw, _ := json.Marshal(coverage)
	if len(coverage) > 40 || len(raw) > 20000 {
		t.Fatalf("unbounded R1 envelope: rows=%d bytes=%d", len(coverage), len(raw))
	}
	request := core.RetrievalRequest{RequestID: "envelope", Task: "inspect existing process", Purpose: "analysis", Route: route, Sources: []core.SourceAdmission{{Identity: "local:fixture", Root: root, ReadPaths: []string{"."}}}, Budget: core.Budget{MaxContextTokens: 32000, ReservedPromptTokens: 1024, MaxSourceBytes: 1 << 20}}
	pack, _, err := NewEngine().Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Evidence) == 0 {
		t.Fatalf("envelope consumed entire budget: %s", pack.Status)
	}
	canonical, err := core.CanonicalJSON(pack)
	if err != nil {
		t.Fatal(err)
	}
	if pack.BudgetUsed.ContextTokens != (len(canonical)+3)/4 {
		t.Fatal("canonical accounting mismatch")
	}
	if err := core.VerifyDigest(pack); err != nil {
		t.Fatal(err)
	}
}
func TestWave1GoldExactTestFile(t *testing.T) {
	snapshot := projectSnapshot("chosen_test.go", "package fixture\nimport \"testing\"\nfunc TestChosen(t *testing.T){}\n")
	sibling := projectSnapshot("sibling_test.go", "package fixture\nimport \"testing\"\nfunc TestSibling(t *testing.T){}\n")
	snapshot.Documents = append(snapshot.Documents, sibling.Documents...)
	for _, name := range []string{"chosen_test.go", "missing_test.go"} {
		f := core.Facet{ID: "test", SourceIdentity: "local:fixture", Path: name, QueryKind: core.QueryTests}
		result, err := (TestResolver{}).Resolve(context.Background(), core.RetrievalPlan{}, f, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range result.Candidates {
			if e.RelativePath != name {
				t.Fatalf("exact %s leaked sibling %s", name, e.RelativePath)
			}
		}
		if name == "chosen_test.go" && len(result.Candidates) != 1 {
			t.Fatal("mandatory assertion lost")
		}
	}
}
