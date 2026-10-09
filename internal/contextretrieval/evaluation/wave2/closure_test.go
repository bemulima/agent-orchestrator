package wave2_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	adapter "github.com/bemulima/agent-orchestrator/internal/adapters/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/planning"
	"os"
	"path/filepath"
	"testing"
)

func closureCorpus(t *testing.T) ([]scenario, map[string]string) {
	t.Helper()
	var cases []scenario
	readJSON(t, "cases.json", &cases)
	var manifests map[string][]document
	readJSON(t, "sources.json", &manifests)
	roots := map[string]string{}
	for id, docs := range manifests {
		root := t.TempDir()
		roots[id] = root
		for _, d := range docs {
			b, e := os.ReadFile(filepath.Join("testdata", d.Blob))
			if e != nil {
				t.Fatal(e)
			}
			h := sha256.Sum256(b)
			if hex.EncodeToString(h[:]) != d.SHA256 {
				t.Fatal("hash mismatch")
			}
			dest := filepath.Join(root, d.Path)
			if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(dest, b, 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	return cases, roots
}

// The unchanged historical suite exercises the default contract. This suite
// freezes readiness separately, with every original label and obligation kept.
func TestWave2AnalysisClosure(t *testing.T) {
	cases, roots := closureCorpus(t)
	catalog, e := agentcontrol.LoadCatalog(os.DirFS("../../../.."))
	if e != nil {
		t.Fatal(e)
	}
	var historical map[string][]core.Facet
	readJSON(t, "closure-obligations.json", &historical)
	layers, requirements, found := 0, 0, 0
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
				root := roots[s.Identity]
				s.Root = root
				admitted[s.RouteIdentity] = *s
				projects = append(projects, domain.Project{ID: s.RouteIdentity, SourceIdentity: s.Identity, HeadCommit: s.Revision, LocalPath: &root})
			}
			route, contract, report, e := planning.BuildRoutingMetadataWithCoverage(c.Task, domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: c.Target}}}, projects, catalog)
			if e != nil {
				t.Fatal(e)
			}
			coverage, e := adapter.AdaptRoutingCoverage(report, route, &contract, admitted)
			if e != nil {
				t.Fatal(e)
			}
			request.Route, e = adapter.AdaptTaskRoute(route, admitted, &contract, coverage, request.RequiredFacets)
			if e != nil {
				t.Fatal(e)
			}
			request, scope, e := adapter.PrepareAnalysisRequest(context.Background(), request, route, &contract, coverage, catalog)
			if e != nil {
				t.Fatal(e)
			}
			selected := []string{}
			for _, r := range scope.Layers {
				selected = append(selected, r.Layer)
			}
			for _, l := range c.Layers {
				layers++
				if !contains(selected, l) {
					t.Errorf("SUPPORTED_MISSING read layer %s", l)
				}
			}
			engine := adapter.NewAnalysisEngine()
			pack, _, e := engine.Prepare(context.Background(), request)
			if e != nil {
				t.Fatal(e)
			}
			assertEvidence(t, pack, c.Prepare, c.Relevant)
			repeat, _, e := engine.Prepare(context.Background(), request)
			if e != nil || repeat.ContentDigest != pack.ContentDigest {
				t.Fatal("Prepare digest")
			}
			for _, f := range c.Expands {
				d, _, e := engine.Expand(context.Background(), pack, request, core.ExpandRequest{Facet: f, Reason: "Frozen independent obligation"})
				if e != nil {
					t.Fatal(e)
				}
				for _, q := range d.Diagnostics {
					if q.Code == "BUDGET_UNSATISFIED" {
						t.Fatal("failed attempted Expand")
					}
				}
				pack = d.Pack
			}
			assertEvidence(t, pack, append(append([]label{}, c.Prepare...), c.Expand...), c.Relevant)
			assertEvidence(t, pack, c.Tests, c.Relevant)
			if len(pack.RetrievalPlan.RequiredFacets) != len(historical[c.ID]) {
				t.Fatal("historical requirements changed")
			}
			for _, original := range historical[c.ID] {
				retained := false
				for _, f := range pack.RetrievalPlan.RequiredFacets {
					if f.ID == original.ID && f.Path == original.Path && f.SourceIdentity == original.SourceIdentity && f.Kind == original.Kind && f.QueryKind == original.QueryKind && f.ClaimType == original.ClaimType && f.ClaimKey == original.ClaimKey {
						retained = true
					}
				}
				if !retained {
					t.Fatalf("historical requirement relabeled %+v", original)
				}
			}
			for _, layer := range scope.Layers {
				ev := layer.Evidence
				var bytes []byte
				for _, d := range manifestsForClosure(t, layer.Source) {
					if d.Path == ev.RelativePath {
						bytes, e = os.ReadFile(filepath.Join("testdata", d.Blob))
						if e != nil {
							t.Fatal(e)
						}
					}
				}
				if ev.ContentHash != layer.Witness.Hash || ev.SourceSnapshot == "" || ev.SourceIdentity != layer.Source || ev.Span.StartByte < 0 || ev.Span.EndByte > len(bytes) || string(bytes[ev.Span.StartByte:ev.Span.EndByte]) != ev.Content {
					t.Fatalf("layer witness is not admitted source bytes %+v", layer.Witness)
				}
			}
			for _, f := range pack.RetrievalPlan.RequiredFacets {
				requirements++
				yes := false
				for _, v := range pack.Evidence {
					if contains(v.FacetIDs, f.ID) {
						yes = true
					}
				}
				if yes {
					found++
				} else {
					t.Errorf("SUPPORTED_MISSING plan obligation %s %s", f.ID, f.Path)
				}
			}
			for _, q := range pack.UnresolvedQuestions {
				if q.Status != core.Complete && q.Code != "REVISION_PIN_UNSUPPORTED" {
					t.Errorf("supported analysis gap: %+v", q)
				}
			}
			if pack.Status != core.Complete && pack.Status != core.Partial {
				t.Errorf("analysis acquisition status %s", pack.Status)
			}
			if len(scope.WritableOwners) != 0 {
				t.Fatal("read acquisition grants writes")
			}
			for _, s := range request.Sources {
				if !contains(pack.ForbiddenScope.Write, s.Identity) {
					t.Fatal("source missing write prohibition")
				}
			}
		})
	}
	if layers != 82 || requirements != 198 || found != 198 {
		t.Errorf("frozen closure layers=%d plan=%d found=%d", layers, requirements, found)
	}
}

func manifestsForClosure(t *testing.T, source string) []document {
	t.Helper()
	var x map[string][]document
	readJSON(t, "sources.json", &x)
	return x[source]
}
