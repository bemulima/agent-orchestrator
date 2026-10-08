package wave2_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	adapter "github.com/bemulima/agent-orchestrator/internal/adapters/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/planning"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type label struct{ Source, Path, Symbol, Hash string }
type scenario struct {
	ID, Target, Task, Owner string
	Layers, Relevant        []string
	Prepare, Expand, Tests  []label
	Request                 core.RetrievalRequest
	Expands                 []core.Facet
}
type document struct{ Path, Blob, SHA256 string }

func readJSON(t *testing.T, name string, out any) {
	t.Helper()
	b, e := os.ReadFile(filepath.Join("testdata", name))
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, out); e != nil {
		t.Fatal(e)
	}
}
func TestWave2FrozenObligations(t *testing.T) {
	var cases []scenario
	readJSON(t, "cases.json", &cases)
	if len(cases) != 27 {
		t.Fatal("frozen case denominator changed")
	}
	var manifests map[string][]document
	readJSON(t, "sources.json", &manifests)
	roots := map[string]string{}
	for identity, docs := range manifests {
		root := t.TempDir()
		roots[identity] = root
		for _, d := range docs {
			content, e := os.ReadFile(filepath.Join("testdata", d.Blob))
			if e != nil {
				t.Fatal(e)
			}
			h := sha256.Sum256(content)
			if hex.EncodeToString(h[:]) != d.SHA256 {
				t.Fatalf("source hash mismatch %s", d.Path)
			}
			clean := filepath.Clean(d.Path)
			if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
				t.Fatal("invalid admitted path")
			}
			dest := filepath.Join(root, clean)
			if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(dest, content, 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	catalog, e := agentcontrol.LoadCatalog(os.DirFS("../../../.."))
	if e != nil {
		t.Fatal(e)
	}
	prepareTotal, expandTotal, testTotal := 0, 0, 0
	for _, c := range cases {
		prepareTotal += len(c.Prepare)
		expandTotal += len(c.Expand)
		testTotal += len(c.Tests)
	}
	if prepareTotal != 62 || expandTotal != 49 || testTotal != 56 {
		t.Fatalf("frozen denominators %d/%d/%d", prepareTotal, expandTotal, testTotal)
	}
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			request := c.Request
			request.Sources = append([]core.SourceAdmission(nil), request.Sources...)
			request.RequestID = c.ID
			request.Task = c.Task
			request.Purpose = "analysis"
			projects := []domain.Project{}
			admitted := map[string]core.SourceAdmission{}
			for i := range request.Sources {
				s := &request.Sources[i]
				root, ok := roots[s.Identity]
				if !ok {
					t.Fatal("unknown frozen source")
				}
				s.Root = root
				admitted[s.RouteIdentity] = *s
				projects = append(projects, domain.Project{ID: s.RouteIdentity, SourceIdentity: s.Identity, HeadCommit: s.Revision, LocalPath: &root})
			}
			route, contract, coverage, e := planning.BuildRoutingMetadataWithCoverage(c.Task, domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: c.Target}}}, projects, catalog)
			if e != nil {
				t.Fatal(e)
			}
			companion, e := adapter.AdaptRoutingCoverage(coverage, route, &contract, admitted)
			if e != nil {
				t.Fatal(e)
			}
			request.Route, e = adapter.AdaptTaskRoute(route, admitted, &contract, companion, append(append([]core.Facet(nil), request.RequiredFacets...), request.OptionalFacets...))
			if e != nil {
				t.Fatal(e)
			}
			for _, r := range route.Routes {
				if r.ProjectID != c.Target || !contains(c.Layers, r.RouteID) {
					t.Fatalf("unjustified owner/layer %+v", r)
				}
			}
			engine := adapter.NewEngine()
			pack, _, e := engine.Prepare(context.Background(), request)
			if e != nil {
				t.Fatal(e)
			}
			assertEvidence(t, pack, c.Prepare, c.Relevant)
			repeated, _, e := engine.Prepare(context.Background(), request)
			if e != nil || repeated.ContentDigest != pack.ContentDigest {
				t.Fatal("non-deterministic Prepare")
			}
			expanded := pack
			for _, facet := range c.Expands {
				delta, _, e := engine.Expand(context.Background(), expanded, request, core.ExpandRequest{Facet: facet, Reason: "Independent frozen obligation"})
				if e != nil {
					t.Fatal(e)
				}
				for _, d := range delta.Diagnostics {
					if d.Code == "BUDGET_UNSATISFIED" {
						t.Fatalf("required attempted expansion blocked: %s", facet.ID)
					}
				}
				expanded = delta.Pack
			}
			assertEvidence(t, expanded, append(append([]label{}, c.Prepare...), c.Expand...), c.Relevant)
			assertEvidence(t, expanded, c.Tests, c.Relevant)
			if c.ID == "T08" {
				for _, path := range []string{"internal/usecase/application_command_result.go", "internal/domain/domain_model.go"} {
					facetID := ""
					for _, facet := range expanded.RetrievalPlan.RequiredFacets {
						if facet.Path == path {
							facetID = facet.ID
						}
					}
					if facetID == "" {
						t.Fatalf("retained contract obligation deleted: %s", path)
					}
					notImplemented, missing := false, false
					for _, q := range expanded.UnresolvedQuestions {
						if q.FacetID == facetID {
							notImplemented = notImplemented || q.Code == "CONTRACT_NOT_IMPLEMENTED"
							missing = missing || q.Code == "MISSING_REQUIRED_CONTRACT"
						}
					}
					if !notImplemented || !missing {
						t.Fatalf("contract adapter gap silently concealed: %s", path)
					}
					for _, e := range expanded.Evidence {
						if e.RelativePath == path {
							t.Fatalf("invented contract presented as existing: %s", path)
						}
					}
				}
			}

			for _, owner := range expanded.RouteRef.Owners {
				if owner != c.Owner {
					t.Fatalf("foreign owner %s", owner)
				}
			}
			if expanded.Status == core.Complete {
				t.Fatal("frozen owner/contract/acquisition gaps cannot be COMPLETE")
			}
			if route.Status == domain.RoutingStatusUnresolved && expanded.Status != core.Blocked {
				t.Fatal("unresolved owner must remain explicitly blocked")
			}
		})
	}
}
func contains(v []string, w string) bool {
	for _, s := range v {
		if s == w {
			return true
		}
	}
	return false
}
func assertEvidence(t *testing.T, p core.ContextPack, labels []label, relevant []string) {
	t.Helper()
	for _, l := range labels {
		found := false
		for _, e := range p.Evidence {
			if e.SourceIdentity == l.Source && e.RelativePath == l.Path && (l.Symbol == "" || e.Symbol == l.Symbol || strings.HasSuffix(e.Symbol, "."+l.Symbol)) && (l.Hash == "" || e.ContentHash == l.Hash) {
				found = true
			}
		}
		if !found {
			t.Fatalf("required source/test anchor missing %+v", l)
		}
	}
	count := 0
	for _, e := range p.Evidence {
		if contains(relevant, e.RelativePath) {
			count++
		}
		dirtyAdmitted := false
		for _, source := range p.Sources {
			if source.Identity == e.SourceIdentity && source.Dirty && source.Snapshot == e.SourceSnapshot {
				dirtyAdmitted = true
			}
		}
		if e.Freshness != core.Current && !(e.Freshness == core.DirtySnapshot && dirtyAdmitted) {
			t.Fatalf("stale evidence %s", e.RelativePath)
		}
		if core.SecretPathComponent(e.RelativePath) {
			t.Fatalf("secret evidence %s", e.RelativePath)
		}
	}
	if len(p.Evidence) > 0 && float64(count)/float64(len(p.Evidence)) < 0.8 {
		t.Fatal("precision below frozen acceptance")
	}
}
