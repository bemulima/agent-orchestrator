package contextretrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalysisIntentAuthority(t *testing.T) {
	cases := []struct{ task, intent string }{
		{"Trace the completion contract", "READ_ONLY_ANALYSIS"},
		{"Explain the dependency boundary", "EXPLANATION"},
		{"Select focused tests for the DTO", "READ_ONLY_ANALYSIS"},
		{"Change the completion contract", "EXPLICIT_MUTATION"},
		{"If needed, change the completion contract", "CONDITIONAL_MUTATION"},
		{"Completion contract", "AMBIGUOUS_INTENT"},
		{"Trace the API; modify the Student contract", "EXPLICIT_MUTATION"},
		{"Trace the API and modify the Student contract", "EXPLICIT_MUTATION"},
		{"Trace the API and persist the database", "AMBIGUOUS_INTENT"},
		{"Trace the API, optimize the schema", "AMBIGUOUS_INTENT"},
		{"Trace the API and persist database", "AMBIGUOUS_INTENT"},
		{"Trace the API and bypass authentication", "AMBIGUOUS_INTENT"},
		{"Trace the API and process payments", "AMBIGUOUS_INTENT"},
		{"Trace the API and provision database", "AMBIGUOUS_INTENT"},
		{"Trace the API and ping database", "AMBIGUOUS_INTENT"},
		{"Trace the API and re-index database", "AMBIGUOUS_INTENT"},
		{"Trace the API and DROP DATABASE", "AMBIGUOUS_INTENT"},
		{"Trace the API and frobnicate database", "AMBIGUOUS_INTENT"},
		{"Trace the API; readers cannot modify state, modify the API", "EXPLICIT_MUTATION"},
		{"Trace the API; Runtime must not modify state, modify the API", "EXPLICIT_MUTATION"},
		{"Trace the API; check why readers cannot modify state, modify the API", "EXPLICIT_MUTATION"},
		{"Trace the API; identify why readers cannot modify state, modify the API", "EXPLICIT_MUTATION"},
		{"Trace the API; current readers cannot modify state, modify the API", "EXPLICIT_MUTATION"},
		{"Trace why readers cannot modify state, modify the API", "EXPLICIT_MUTATION"},
		{"Explain why readers cannot modify state, modify the API", "EXPLICIT_MUTATION"},
		{"Trace the API or modify the contract", "EXPLICIT_MUTATION"},
		{"Trace the API; do not change the schema but modify the Student contract", "EXPLICIT_MUTATION"},
		{"Trace the API; modify contract so it cannot fail", "EXPLICIT_MUTATION"},
		{"Trace the API; do not change the schema and now modify the Student contract", "EXPLICIT_MUTATION"},
		{"Trace the API; apply the patch", "EXPLICIT_MUTATION"},
		{"Trace the API then patch the Student contract", "EXPLICIT_MUTATION"},
		{"Trace the API; write the new Completion contract", "EXPLICIT_MUTATION"},
		{"Trace the API; extend the schema", "EXPLICIT_MUTATION"},
		{"Trace the API; frobnicate the database", "AMBIGUOUS_INTENT"},
		{"Inspect to modify the Student contract", "EXPLICIT_MUTATION"},
		{"Trace the API & modify the Student contract", "EXPLICIT_MUTATION"},
		{"Trace the API\nmodify the Student contract", "EXPLICIT_MUTATION"},
		{"Trace the API; you need to modify the Student contract", "EXPLICIT_MUTATION"},
		{"Trace the API; also please modify the Student contract", "EXPLICIT_MUTATION"},
		{"Trace the API; now modify the Student contract", "EXPLICIT_MUTATION"},
		{"Trace the API; please modify the domain model", "EXPLICIT_MUTATION"},
		{"Trace the API; we need to change the contract", "EXPLICIT_MUTATION"},
		{"Trace the API; if necessary, update the Student contract", "CONDITIONAL_MUTATION"},
		{"Trace the API; do not modify the Student contract", "READ_ONLY_ANALYSIS"},
		{"Explain why manifest text cannot change prompt authority", "EXPLANATION"},
	}
	for _, c := range cases {
		t.Run(c.task, func(t *testing.T) {
			if got := AnalysisIntent(c.task); got != c.intent {
				t.Fatalf("intent=%s want=%s", got, c.intent)
			}
		})
	}
}

// A generic fixture deliberately avoids service names and conventional DTO/API
// names. It uses an interface-mediated call and an explicit imported result.
func analysisFixture(t *testing.T) (core.RetrievalRequest, domain.RoutingResult, domain.ContractPlan, agentcontrol.Catalog, map[string]string) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                           "module example.test/local\n\ngo 1.23\n",
		"internal/domain/view.go":          "package domain\ntype PublicEnvelope struct { Instruction string }\n",
		"internal/usecase/query.go":        "package usecase\nimport (\"context\"; dto \"example.test/local/internal/domain\")\ntype Reader struct{}\nfunc (*Reader) Fetch(ctx context.Context, id string) (dto.PublicEnvelope,error) { return dto.PublicEnvelope{},nil }\n",
		"internal/transport/http/query.go": "package http\nimport (\"context\"; model \"example.test/local/internal/domain\")\ntype ReadPort interface { Fetch(context.Context,string) (model.PublicEnvelope,error) }\ntype Handler struct { Queries ReadPort }\nfunc (h *Handler) Serve(ctx context.Context) { h.Queries.Fetch(ctx,\"x\") }\n",
	}
	for p, s := range files {
		dest := filepath.Join(root, p)
		if e := os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(dest, []byte(s), 0600); e != nil {
			t.Fatal(e)
		}
	}
	catalog, e := agentcontrol.LoadCatalog(os.DirFS("../../.."))
	if e != nil {
		t.Fatal(e)
	}
	source := core.SourceAdmission{Identity: "local:analysis-fixture", RouteIdentity: "local-project", Root: root, ReadPaths: []string{"go.mod", "internal"}}
	request := core.RetrievalRequest{SchemaVersion: core.SchemaVersion, Task: "Trace the source API", Purpose: "analysis", Sources: []core.SourceAdmission{source}}
	for _, a := range []struct{ path, symbol string }{{"internal/domain/view.go", "PublicEnvelope"}, {"internal/usecase/query.go", "Fetch"}, {"internal/transport/http/query.go", "Serve"}} {
		request.RequiredFacets = append(request.RequiredFacets, core.Facet{ID: a.symbol, Kind: "symbol", QueryKind: core.QueryDefinition, SourceIdentity: source.Identity, Path: a.path, Symbol: a.symbol, ExpectedHash: hashBytes([]byte(files[a.path])), ClaimType: core.ImplementationBehavior, Required: true})
	}
	route := domain.RoutingResult{Status: domain.RoutingStatusUnresolved, CatalogDigest: catalog.Digest, ScopeMode: "analysis_only", OwnerReviewRequired: true, Profiles: []domain.ArchitectureProfileResolution{{ProjectID: source.RouteIdentity, Status: domain.ProfileResolutionResolved, ProfileID: "go.canonical"}}}
	contract := domain.ContractPlan{}
	return request, route, contract, catalog, files
}

func TestAnalysisAdmissionAndCompatibility(t *testing.T) {
	request, route, contract, catalog, _ := analysisFixture(t)
	originalRoute, _ := json.Marshal(route)
	originalContract, _ := json.Marshal(contract)
	adapted, scope, e := PrepareAnalysisRequest(context.Background(), request, route, &contract, []core.CoverageResult{}, catalog)
	if e != nil {
		t.Fatal(e)
	}
	if len(scope.WritableOwners) != 0 || adapted.Route.OwnerReviewRequired {
		t.Fatal("read scope inherited writable owner/freeze gate")
	}
	pack, _, e := NewAnalysisEngine().Prepare(context.Background(), adapted)
	if e != nil || pack.Status != core.Complete {
		t.Fatalf("pack %s: %v %+v", pack.Status, e, pack.UnresolvedQuestions)
	}
	if len(pack.ForbiddenScope.Write) != 1 {
		t.Fatal("write prohibition missing")
	}
	afterRoute, _ := json.Marshal(route)
	afterContract, _ := json.Marshal(contract)
	if string(originalRoute) != string(afterRoute) || string(originalContract) != string(afterContract) {
		t.Fatal("legacy objects mutated")
	}
	if _, exists := NewEngine().Resolvers["analysis-boundary"]; exists {
		t.Fatal("legacy resolver/digest set changed")
	}
	t.Run("neighbor", func(t *testing.T) {
		r := request
		r.Sources = append([]core.SourceAdmission{}, request.Sources...)
		r.Sources[0].Neighbor = true
		_, s, e := PrepareAnalysisRequest(context.Background(), r, route, &contract, nil, catalog)
		if e != nil {
			t.Fatal(e)
		}
		if len(s.WritableOwners) != 0 {
			t.Fatal("neighbor writable")
		}
	})
	t.Run("wrong-hash", func(t *testing.T) {
		r := request
		r.RequiredFacets = append([]core.Facet{}, request.RequiredFacets...)
		r.RequiredFacets[0].ExpectedHash = hashBytes([]byte("different"))
		r, _, e := PrepareAnalysisRequest(context.Background(), r, route, &contract, nil, catalog)
		if e != nil {
			t.Fatal(e)
		}
		p, _, e := NewAnalysisEngine().Prepare(context.Background(), r)
		if e != nil {
			t.Fatal(e)
		}
		if p.Status == core.Complete {
			t.Fatal("hash mismatch complete")
		}
	})
	t.Run("stale-snapshot", func(t *testing.T) {
		r := request
		r.Sources = append([]core.SourceAdmission{}, request.Sources...)
		r.Sources[0].ExpectedSnapshot = hashBytes([]byte("stale"))
		if _, _, e := PrepareAnalysisRequest(context.Background(), r, route, &contract, nil, catalog); e == nil {
			t.Fatal("stale analysis accepted")
		}
	})
	t.Run("catalog", func(t *testing.T) {
		r := route
		r.CatalogDigest = "different"
		if _, _, e := PrepareAnalysisRequest(context.Background(), request, r, &contract, nil, catalog); e == nil {
			t.Fatal("catalog mismatch accepted")
		}
	})
	t.Run("unanchored-business", func(t *testing.T) {
		r := request
		r.RequiredFacets = nil
		r.Task = "Trace business ownership of Completion"
		r, _, e := PrepareAnalysisRequest(context.Background(), r, route, &contract, nil, catalog)
		if e != nil {
			t.Fatal(e)
		}
		p, _, e := NewAnalysisEngine().Prepare(context.Background(), r)
		if e != nil {
			t.Fatal(e)
		}
		if p.Status == core.Complete {
			t.Fatal("unanchored business inference complete")
		}
	})
}

func TestAnalysisTypedBoundaryProof(t *testing.T) {
	request, route, _, catalog, files := analysisFixture(t)
	makeContract := func() domain.ContractPlan {
		return domain.ContractPlan{State: domain.ContractPlanState("FREEZE_REQUIRED"), Required: true, FreezeRequired: true, Boundaries: []domain.PlannedContractBoundary{
			{Kind: "domain-model", Owner: domain.RouteReference{ProjectID: "local-project", RouteID: "backend.domain"}, TargetPath: "internal/domain/domain_model.go"},
			{Kind: "application-command-result", Owner: domain.RouteReference{ProjectID: "local-project", RouteID: "backend.usecase"}, TargetPath: "internal/usecase/application_command_result.go"},
		}}
	}
	contract := makeContract()
	r, scope, e := PrepareAnalysisRequest(context.Background(), request, route, &contract, nil, catalog)
	if e != nil {
		t.Fatal(e)
	}
	if len(scope.Bindings) != 2 {
		t.Fatalf("typed bindings %+v", scope.Bindings)
	}
	p, _, e := NewAnalysisEngine().Prepare(context.Background(), r)
	if e != nil || p.Status != core.Complete {
		t.Fatalf("typed pack %s %v %+v", p.Status, e, p.UnresolvedQuestions)
	}
	for _, b := range scope.Bindings {
		found := false
		for _, v := range p.Evidence {
			if v.RelativePath == b.OriginalPath {
				t.Fatal("fake proposed file")
			}
			if containsAnalysis(v.FacetIDs, b.FacetID) {
				found = true
			}
		}
		if !found {
			t.Fatal("conceptual obligation missing")
		}
	}
	if contract.State != domain.ContractPlanState("FREEZE_REQUIRED") || !contract.FreezeRequired {
		t.Fatal("freeze semantics changed")
	}
	tests := []struct{ name, path, body string }{
		{"foreign-result", "internal/usecase/query.go", "package usecase\nimport (\"context\"; dto \"foreign.test/other/internal/domain\")\ntype Reader struct{}\nfunc (*Reader) Fetch(context.Context,string) (dto.PublicEnvelope,error) { return dto.PublicEnvelope{},nil }\n"},
		{"comment-only-consumer", "internal/transport/http/query.go", "package http\nimport (\"context\"; model \"example.test/local/internal/domain\")\ntype ReadPort interface { Fetch(context.Context,string) (model.PublicEnvelope,error) }\ntype Handler struct { Queries ReadPort }\nfunc (h *Handler) Serve(ctx context.Context) { /* h.Queries.Fetch(ctx,\"x\") */ }\n"},
		{"counterfeit-signature", "internal/transport/http/query.go", "package http\nimport \"context\"\ntype ReadPort interface { Fetch(context.Context,string) (string,error) }\ntype Handler struct { Queries ReadPort }\nfunc (h *Handler) Serve(ctx context.Context) { h.Queries.Fetch(ctx,\"x\") }\n"},
		{"shadowed-consumer-receiver", "internal/transport/http/query.go", "package http\nimport (\"context\"; model \"example.test/local/internal/domain\")\ntype ReadPort interface { Fetch(context.Context,string) (model.PublicEnvelope,error) }\ntype WrongPort interface { Fetch(context.Context,string) (string,error) }\ntype Handler struct { Queries ReadPort }\ntype OtherHandler struct { Queries WrongPort }\nfunc (h *Handler) Serve(ctx context.Context) { { h := &OtherHandler{}; h.Queries.Fetch(ctx,\"x\") } }\n"},
		{"duplicate-receiver-field", "internal/transport/http/query.go", "package http\nimport (\"context\"; model \"example.test/local/internal/domain\")\ntype ReadPort interface { Fetch(context.Context,string) (model.PublicEnvelope,error) }\ntype Handler struct { Queries ReadPort; Queries string }\nfunc (h *Handler) Serve(ctx context.Context) { h.Queries.Fetch(ctx,\"x\") }\n"},
		{"ambiguous-interface", "internal/transport/http/query.go", files["internal/transport/http/query.go"] + "type ReadPort interface { Fetch(context.Context,string) (string,error) }\n"},
		{"duplicate-interface-method", "internal/transport/http/query.go", "package http\nimport (\"context\"; model \"example.test/local/internal/domain\")\ntype ReadPort interface { Fetch(context.Context,string) (model.PublicEnvelope,error); Fetch(context.Context,string) (string,error) }\ntype Handler struct { Queries ReadPort }\nfunc (h *Handler) Serve(ctx context.Context) { h.Queries.Fetch(ctx,\"x\") }\n"},
		{"ambiguous-owner", "internal/usecase/query.go", files["internal/usecase/query.go"] + "func (*Reader) Fetch(context.Context,string) (dto.PublicEnvelope,error) { return dto.PublicEnvelope{},nil }\n"},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			req, rt, _, cat, _ := analysisFixture(t)
			if e := os.WriteFile(filepath.Join(req.Sources[0].Root, c.path), []byte(c.body), 0600); e != nil {
				t.Fatal(e)
			}
			for i := range req.RequiredFacets {
				if req.RequiredFacets[i].Path == c.path {
					req.RequiredFacets[i].ExpectedHash = hashBytes([]byte(c.body))
				}
			}
			cp := makeContract()
			req, _, e := PrepareAnalysisRequest(context.Background(), req, rt, &cp, nil, cat)
			if e != nil {
				t.Fatal(e)
			}
			pack, _, e := NewAnalysisEngine().Prepare(context.Background(), req)
			if e != nil {
				t.Fatal(e)
			}
			if pack.Status == core.Complete {
				t.Fatal("counterfeit typed binding COMPLETE")
			}
		})
	}
	for _, extra := range []struct{ name, path, body string }{
		{"cross-file-owner", "internal/usecase/duplicate.go", files["internal/usecase/query.go"]},
		{"cross-file-domain", "internal/domain/duplicate.go", files["internal/domain/view.go"]},
		{"cross-file-consumer", "internal/transport/http/duplicate.go", files["internal/transport/http/query.go"]},
	} {
		t.Run(extra.name, func(t *testing.T) {
			req, rt, _, cat, _ := analysisFixture(t)
			if e := os.WriteFile(filepath.Join(req.Sources[0].Root, extra.path), []byte(extra.body), 0600); e != nil {
				t.Fatal(e)
			}
			cp := makeContract()
			req, _, e := PrepareAnalysisRequest(context.Background(), req, rt, &cp, nil, cat)
			if e != nil {
				t.Fatal(e)
			}
			pack, _, e := NewAnalysisEngine().Prepare(context.Background(), req)
			if e != nil {
				t.Fatal(e)
			}
			if pack.Status == core.Complete {
				t.Fatal("cross-file ambiguity complete")
			}
		})
	}
	t.Run("tampered-proof", func(t *testing.T) {
		req := r
		req.Route.SeedFacets = append([]core.Facet{}, r.Route.SeedFacets...)
		for i := range req.Route.SeedFacets {
			if req.Route.SeedFacets[i].Resolver == "analysis-boundary" {
				var b AnalysisBinding
				json.Unmarshal([]byte(req.Route.SeedFacets[i].Text), &b)
				b.Domain.Hash = hashBytes([]byte("tamper"))
				req.Route.SeedFacets[i].Text = analysisBindingJSON(b)
			}
		}
		pack, _, e := NewAnalysisEngine().Prepare(context.Background(), req)
		if e != nil {
			t.Fatal(e)
		}
		if pack.Status == core.Complete {
			t.Fatal("tampered proof COMPLETE")
		}
	})
	t.Run("stale-expand", func(t *testing.T) {
		if e := os.WriteFile(filepath.Join(r.Sources[0].Root, "internal/domain/view.go"), []byte("package domain\ntype Different struct{}\n"), 0600); e != nil {
			t.Fatal(e)
		}
		if _, _, e := NewAnalysisEngine().Expand(context.Background(), p, r, core.ExpandRequest{Facet: r.RequiredFacets[0], Reason: "replay current source"}); e == nil {
			t.Fatal("stale Expand accepted")
		}
	})
}
func containsAnalysis(v []string, s string) bool {
	for _, a := range v {
		if a == s {
			return true
		}
	}
	return false
}

func TestAnalysisLegacyContractPrerequisite(t *testing.T) {
	request, route, _, catalog, _ := analysisFixture(t)
	contract := domain.ContractPlan{State: domain.ContractPlanPlanned, Required: true}
	original, _ := json.Marshal(contract)
	r, scope, err := PrepareAnalysisRequest(context.Background(), request, route, &contract, nil, catalog)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range scope.LegacyDiagnostics {
		found = found || d.Code == "CONTRACT_REQUIREMENTS_UNRESOLVED"
	}
	if !found {
		t.Fatal("legacy writer prerequisite silently discarded")
	}
	p, _, err := NewAnalysisEngine().Prepare(context.Background(), r)
	if err != nil || p.Status != core.Complete {
		t.Fatalf("writer prerequisite blocks exact reads: %s %v %+v", p.Status, err, p.UnresolvedQuestions)
	}
	after, _ := json.Marshal(contract)
	if string(after) != string(original) || len(scope.WritableOwners) != 0 {
		t.Fatal("contract or authority changed")
	}
	t.Run("actual-required-boundary", func(t *testing.T) {
		cp := contract
		cp.Boundaries = []domain.PlannedContractBoundary{{Kind: "external-read-contract", Owner: domain.RouteReference{ProjectID: "local-project", RouteID: "backend.usecase"}, TargetPath: "contracts/absent.md", Existing: true}}
		r, _, err := PrepareAnalysisRequest(context.Background(), request, route, &cp, nil, catalog)
		if err != nil {
			t.Fatal(err)
		}
		p, _, err := NewAnalysisEngine().Prepare(context.Background(), r)
		if err != nil || p.Status == core.Complete {
			t.Fatalf("required missing contract hidden: %s %v", p.Status, err)
		}
	})
	t.Run("unresolved-freeze-authority", func(t *testing.T) {
		cp := contract
		cp.State = domain.ContractPlanFreezeNeeded
		cp.FreezeRequired = true
		r, _, err := PrepareAnalysisRequest(context.Background(), request, route, &cp, nil, catalog)
		if err != nil {
			t.Fatal(err)
		}
		p, _, err := NewAnalysisEngine().Prepare(context.Background(), r)
		if err != nil || p.Status == core.Complete {
			t.Fatal("unresolved explicit freeze authority hidden")
		}
	})
	t.Run("resolved-route-missing-contract-authority", func(t *testing.T) {
		rt := route
		rt.Status = domain.RoutingStatusResolved
		rt.OwnerReviewRequired = false
		r, _, err := PrepareAnalysisRequest(context.Background(), request, rt, &contract, nil, catalog)
		if err != nil {
			t.Fatal(err)
		}
		p, _, err := NewAnalysisEngine().Prepare(context.Background(), r)
		if err != nil || p.Status == core.Complete {
			t.Fatal("resolved route missing contract authority hidden")
		}
	})
}

func TestAnalysisContextTraversalBounds(t *testing.T) {
	t.Run("context-cannot-substitute-typed-anchor", func(t *testing.T) {
		r, route, _, catalog, files := analysisFixture(t)
		body := files["internal/usecase/query.go"] + "func Probe() {}\n"
		if err := os.WriteFile(filepath.Join(r.Sources[0].Root, "internal/usecase/query.go"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		r.RequiredFacets = []core.Facet{{ID: "probe", Kind: "symbol", QueryKind: core.QueryDefinition, SourceIdentity: r.Sources[0].Identity, Path: "internal/usecase/query.go", Symbol: "Probe", ExpectedHash: hashBytes([]byte(body)), Resolver: "go", ClaimType: core.ImplementationBehavior, Required: true}}
		cp := domain.ContractPlan{State: domain.ContractPlanFreezeNeeded, Required: true, FreezeRequired: true, Boundaries: []domain.PlannedContractBoundary{{Kind: "domain-model", Owner: domain.RouteReference{ProjectID: "local-project", RouteID: "backend.domain"}, TargetPath: "internal/domain/domain_model.go"}, {Kind: "application-command-result", Owner: domain.RouteReference{ProjectID: "local-project", RouteID: "backend.usecase"}, TargetPath: "internal/usecase/application_command_result.go"}}}
		r, scope, err := PrepareAnalysisRequest(context.Background(), r, route, &cp, nil, catalog)
		if err != nil {
			t.Fatal(err)
		}
		if len(scope.Bindings) != 0 {
			t.Fatal("same-file declarations became original typed anchors")
		}
		contextRead := false
		for _, l := range scope.Layers {
			if l.Layer == "backend.domain" {
				for _, a := range l.Chain {
					contextRead = contextRead || a.Symbol == "Reader.Fetch"
				}
			}
		}
		if !contextRead {
			t.Fatal("bounded contextual source relationship was not acquired")
		}
		p, _, err := NewAnalysisEngine().Prepare(context.Background(), r)
		if err != nil || p.Status == core.Complete {
			t.Fatal("unanchored typed obligations silently satisfied")
		}
	})
	t.Run("discovered-files-not-context-expanded", func(t *testing.T) {
		r, route, cp, catalog, _ := analysisFixture(t)
		body := "package usecase\nimport edge \"example.test/local/internal/infrastructure/client\"\nfunc Probe() { edge.Entry() }\n"
		files := map[string]string{"internal/usecase/query.go": body, "internal/infrastructure/client/query.go": "package client\nfunc Entry() {}\nfunc Unrelated() { Boot() }\n", "cmd/example/main.go": "package main\nfunc Boot() {}\n"}
		for p, b := range files {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(r.Sources[0].Root, p)), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(r.Sources[0].Root, p), []byte(b), 0600); err != nil {
				t.Fatal(err)
			}
		}
		r.Sources[0].ReadPaths = append(r.Sources[0].ReadPaths, "cmd")
		r.RequiredFacets = []core.Facet{{ID: "probe", Kind: "symbol", QueryKind: core.QueryDefinition, SourceIdentity: r.Sources[0].Identity, Path: "internal/usecase/query.go", Symbol: "Probe", ExpectedHash: hashBytes([]byte(body)), Resolver: "go", ClaimType: core.ImplementationBehavior, Required: true}}
		_, scope, err := PrepareAnalysisRequest(context.Background(), r, route, &cp, nil, catalog)
		if err != nil {
			t.Fatal(err)
		}
		client := false
		for _, l := range scope.Layers {
			if l.Layer == "backend.composition" {
				t.Fatal("discovered file recursively expanded unrelated declarations")
			}
			client = client || l.Layer == "backend.infrastructure.client"
		}
		if !client {
			t.Fatal("actual syntax-linked client declaration missing")
		}
	})
	t.Run("context-cap-propagates-partial", func(t *testing.T) {
		r, route, cp, catalog, _ := analysisFixture(t)
		var b strings.Builder
		b.WriteString("package usecase\nfunc Probe() {}\n")
		for i := 0; i < 1002; i++ {
			fmt.Fprintf(&b, "func Context%04d() {}\n", i)
		}
		body := b.String()
		if err := os.WriteFile(filepath.Join(r.Sources[0].Root, "internal/usecase/query.go"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		r.RequiredFacets = []core.Facet{{ID: "probe", Kind: "symbol", QueryKind: core.QueryDefinition, SourceIdentity: r.Sources[0].Identity, Path: "internal/usecase/query.go", Symbol: "Probe", ExpectedHash: hashBytes([]byte(body)), Resolver: "go", ClaimType: core.ImplementationBehavior, Required: true}}
		r, _, err := PrepareAnalysisRequest(context.Background(), r, route, &cp, nil, catalog)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, d := range r.Route.Diagnostics {
			found = found || d.Code == "ANALYSIS_CONTEXT_LIMIT" && d.Status == core.Partial
		}
		if !found {
			t.Fatal("context omission not diagnosed")
		}
		p, _, err := NewAnalysisEngine().Prepare(context.Background(), r)
		if err != nil || p.Status == core.Complete {
			t.Fatal("context limit did not propagate PARTIAL")
		}
	})
}
