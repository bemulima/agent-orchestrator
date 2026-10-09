package contextretrieval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func projectConcreteFixture(t *testing.T) (core.RetrievalRequest, domain.RoutingResult, domain.ContractPlan, agentcontrol.Catalog, map[string]string) {
	request, route, _, catalog, _ := projectFixture(t)
	files := map[string]string{
		"internal/usecase/pipeline/processor.go": "package pipeline\nimport \"context\"\ntype Processor struct{}\ntype Receipt struct { Count int; Label string }\nfunc (p Processor) Fetch(ctx context.Context) (Receipt,error) { return Receipt{Count:1,Label:\"sample\"},nil }\n",
		"internal/transport/http/endpoint.go":    "package incoming\nimport (\"net/http\"; workflow \"example.test/local/internal/usecase/pipeline\")\ntype Endpoint struct { Workflow *workflow.Processor }\nfunc (e Endpoint) Serve(w http.ResponseWriter,r *http.Request) { receipt,err:=e.Workflow.Fetch(r.Context());if err!=nil{return};_,_=w.Write([]byte(receipt.Label)) }\n",
	}
	request.RequiredFacets = nil
	for p, b := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(request.Sources[0].Root, p)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(request.Sources[0].Root, p), []byte(b), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []struct{ p, s string }{{"internal/usecase/pipeline/processor.go", "Processor.Fetch"}, {"internal/transport/http/endpoint.go", "Endpoint.Serve"}} {
		request.RequiredFacets = append(request.RequiredFacets, core.Facet{ID: a.s, SourceIdentity: request.Sources[0].Identity, Path: a.p, Symbol: a.s, ExpectedHash: hashBytes([]byte(files[a.p])), Kind: "symbol", QueryKind: core.QueryDefinition, ClaimType: core.ImplementationBehavior, Required: true})
	}
	request.Budget = core.Budget{MaxSourceBytes: 1 << 20, MaxContextTokens: 65536, ReservedPromptTokens: 1024, PerFacetTokens: 16384, PerSourceBytes: 1 << 20}
	contract := domain.ContractPlan{State: domain.ContractPlanFreezeNeeded, Required: true, FreezeRequired: true, Boundaries: []domain.PlannedContractBoundary{{Kind: "application-command-result", Owner: domain.RouteReference{ProjectID: "local-project", RouteID: "backend.usecase"}, TargetPath: "internal/usecase/application_command_result.go"}}}
	return request, route, contract, catalog, files
}

func TestProjectAnalysisConcreteProofRejectsAmbiguityAndForeignSyntax(t *testing.T) {
	for _, name := range []string{"malformed-sibling", "excluded-sibling", "multiple-receiver-names", "embedded-field-collision", "duplicate-module", "malformed-module", "foreign-module", "foreign-result", "result-alias", "owner-alias", "receiver-shadow", "wrong-arity", "wrong-field", "duplicate-method", "duplicate-result", "error-shadow", "cross-file-error-shadow", "generic-owner", "nested-module", "missing-owner-selector", "second-consumer", "excluded-target", "omitted-target"} {
		t.Run(name, func(t *testing.T) {
			request, route, contract, catalog, files := projectConcreteFixture(t)
			ownerPath := "internal/usecase/pipeline/processor.go"
			consumerPath := "internal/transport/http/endpoint.go"
			o, c := files[ownerPath], files[consumerPath]
			extra := map[string]string{}
			switch name {
			case "malformed-sibling":
				extra["internal/usecase/pipeline/bad.go"] = "package pipeline\ntype Receipt struct{}\nfunc broken(\n"
			case "excluded-sibling":
				extra["internal/usecase/pipeline/hidden.go"] = "package pipeline\ntype Receipt struct{}\n"
				request.Sources[0].ExcludePaths = []string{"internal/usecase/pipeline/hidden.go"}
			case "multiple-receiver-names":
				o = strings.ReplaceAll(o, "(p Processor)", "(p, q Processor)")
			case "embedded-field-collision":
				c = strings.ReplaceAll(c, "type Endpoint struct { Workflow", "type Endpoint struct { other.Workflow; Workflow")
				c = strings.ReplaceAll(c, "import (", "import (other \"foreign.test/other\";")
			case "duplicate-module":
				extra["go.mod"] = "module example.test/local\nmodule conflicting.test/other\n"
			case "malformed-module":
				extra["go.mod"] = "module example.test/local extra\n"
			case "foreign-module":
				c = strings.ReplaceAll(c, "example.test/local/internal/usecase/pipeline", "foreign.test/other/internal/usecase/pipeline")
			case "foreign-result":
				o = strings.ReplaceAll(o, "import \"context\"", "import (\"context\"; other \"foreign.test/other\")")
				o = strings.ReplaceAll(o, "(Receipt,error)", "(other.Receipt,error)")
			case "result-alias":
				o = strings.ReplaceAll(o, "type Receipt struct", "type Receipt = Other\ntype Other struct")
			case "owner-alias":
				o = strings.ReplaceAll(o, "type Processor struct{}", "type Processor = Actual\ntype Actual struct{}")
			case "receiver-shadow":
				c = strings.ReplaceAll(c, "receipt,err:=e.Workflow.Fetch(r.Context())", "{ e:=Endpoint{}; receipt,err:=e.Workflow.Fetch(r.Context())")
				c = strings.ReplaceAll(c, "[]byte(receipt.Label)) }", "[]byte(receipt.Label)) } }")
			case "wrong-arity":
				c = strings.ReplaceAll(c, "Fetch(r.Context())", "Fetch()")
			case "wrong-field":
				c = strings.ReplaceAll(c, "e.Workflow.Fetch", "e.Other.Fetch")
			case "duplicate-method":
				o += "func (p Processor) Fetch(ctx context.Context) (Receipt,error) { return Receipt{},nil }\n"
			case "duplicate-result":
				extra["internal/usecase/pipeline/duplicate.go"] = "package pipeline\ntype Receipt struct {Other int}\n"
			case "error-shadow":
				o += "type error struct{}\n"
			case "cross-file-error-shadow":
				extra["internal/usecase/pipeline/shadow.go"] = "package pipeline\nvar error string\n"
			case "generic-owner":
				o = strings.ReplaceAll(o, "(p Processor)", "(p Processor[int])")
			case "nested-module":
				extra["internal/usecase/go.mod"] = "module foreign.test/nested\n"
			case "missing-owner-selector":
				request.RequiredFacets = request.RequiredFacets[1:]
			case "second-consumer":
				c += "func (e Endpoint) Second(w http.ResponseWriter,r *http.Request) { e.Workflow.Fetch(r.Context()) }\n"
				f := request.RequiredFacets[1]
				f.ID = "other-consumer"
				f.Symbol = "Endpoint.Second"
				request.RequiredFacets = append(request.RequiredFacets, f)
			case "excluded-target":
				request.Sources[0].ExcludePaths = []string{contract.Boundaries[0].TargetPath}
			case "omitted-target":
				extra[contract.Boundaries[0].TargetPath] = "package usecase\n// " + strings.Repeat("x", 4096) + "\n"
				request.Limits.MaxFileBytes = 1024
			}
			files[ownerPath] = o
			files[consumerPath] = c
			for p, b := range extra {
				files[p] = b
			}
			for p, b := range files {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(request.Sources[0].Root, p)), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(request.Sources[0].Root, p), []byte(b), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for i := range request.RequiredFacets {
				request.RequiredFacets[i].ExpectedHash = hashBytes([]byte(files[request.RequiredFacets[i].Path]))
			}
			pack, _, scope, err := PrepareProjectContext(context.Background(), request, route, &contract, nil, catalog)
			if err != nil {
				t.Fatal(err)
			}
			if projectResultFound(pack) || len(scope.BoundaryProofs) != 0 || pack.Status == core.Complete {
				t.Fatal("unverified application equivalence certified")
			}
			retained := false
			for _, f := range pack.RetrievalPlan.RequiredFacets {
				retained = retained || f.ClaimKey == "application-command-result" && f.Path == contract.Boundaries[0].TargetPath
			}
			if !retained || !contract.FreezeRequired {
				t.Fatal("negative removed original requirement/freeze")
			}
		})
	}
}

func TestProjectAnalysisProofOriginalSeedAndStrictJSON(t *testing.T) {
	request, route, contract, catalog, _ := projectConcreteFixture(t)
	prepared, _, err := PrepareProjectAnalysisRequest(context.Background(), request, route, &contract, nil, catalog)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := core.BuildPlan(prepared)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := NewEngine().Loader.Load(context.Background(), plan.Sources, plan.Limits)
	if err != nil {
		t.Fatal(err)
	}
	var original core.Facet
	for _, f := range plan.RequiredFacets {
		if f.Resolver == "analysis-project-boundary" {
			original = f
		}
	}
	if original.ID == "" {
		t.Fatal("positive proof missing")
	}
	for _, name := range []string{"unicode-fold-duplicate", "missing-seed", "changed-expand-identity", "changed-required", "missing-caller", "unknown-key", "duplicate-key", "casefold-duplicate-key", "trailing-json", "forged-result", "stale-module"} {
		t.Run(name, func(t *testing.T) {
			p := plan
			f := original
			s := snapshot
			p.Route.SeedFacets = append([]core.Facet{}, plan.Route.SeedFacets...)
			switch name {
			case "unicode-fold-duplicate":
				f.Text = strings.Replace(f.Text, "{", "{\"verſion\":\"forged\",", 1)
			case "missing-seed":
				p.Route.SeedFacets = nil
			case "changed-expand-identity":
				f.ID = "forged-obligation"
			case "changed-required":
				f.Required = false
			case "missing-caller":
				p.RequiredFacets = nil
				p.OptionalFacets = nil
			case "unknown-key":
				f.Text = strings.TrimSuffix(f.Text, "}") + ",\"unknown\":1}"
			case "duplicate-key":
				f.Text = strings.TrimSuffix(f.Text, "}") + ",\"version\":\"" + ProjectAnalysisVersion + "\"}"
			case "casefold-duplicate-key":
				f.Text = strings.TrimSuffix(f.Text, "}") + ",\"VERSION\":\"" + ProjectAnalysisVersion + "\"}"
			case "trailing-json":
				f.Text += " {}"
			case "forged-result":
				var b ProjectBoundaryProof
				if err := json.Unmarshal([]byte(f.Text), &b); err != nil {
					t.Fatal(err)
				}
				b.Result.Symbol = "pipeline.Processor"
				f.Text = projectProofJSON(b)
			case "stale-module":
				s.Documents = append([]core.Document{}, snapshot.Documents...)
				for i := range s.Documents {
					if s.Documents[i].RelativePath == "go.mod" {
						s.Documents[i].Content += "\n// changed\n"
						s.Documents[i].ContentHash = hashBytes([]byte(s.Documents[i].Content))
					}
				}
			}
			// Parser controls still run with an exact trusted seed match, so they
			// cannot pass merely because the membership guard rejected the payload.
			if name == "unicode-fold-duplicate" || name == "unknown-key" || name == "duplicate-key" || name == "casefold-duplicate-key" || name == "trailing-json" || name == "forged-result" {
				for i := range p.Route.SeedFacets {
					if p.Route.SeedFacets[i].ID == original.ID {
						p.Route.SeedFacets[i] = f
					}
				}
			}
			r, err := (ProjectBoundaryResolver{}).Resolve(context.Background(), p, f, s)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Candidates) != 0 || len(r.Diagnostics) == 0 {
				t.Fatal("forged or ungrounded proof returned evidence")
			}
		})
	}
}

func projectResultFound(pack core.ContextPack) bool {
	for _, f := range pack.RetrievalPlan.RequiredFacets {
		if f.ClaimKey == "application-command-result" {
			for _, e := range pack.Evidence {
				if containsAnalysis(e.FacetIDs, f.ID) {
					return true
				}
			}
		}
	}
	return false
}

func TestProjectAnalysisConcreteLocalResult(t *testing.T) {
	request, route, contract, catalog, _ := projectConcreteFixture(t)
	pack, _, scope, err := PrepareProjectContext(context.Background(), request, route, &contract, nil, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if !projectResultFound(pack) {
		t.Fatalf("current local result not retrieved: %+v", pack.UnresolvedQuestions)
	}
	if pack.Status != core.Complete || len(scope.WritableOwners) != 0 || !contract.FreezeRequired {
		t.Fatalf("source equivalence changed read/freeze behavior: %s %+v", pack.Status, pack.UnresolvedQuestions)
	}
	for _, e := range pack.Evidence {
		if e.Resolver == "analysis-project-boundary" {
			if !reflect.DeepEqual(e.Authority, core.ClaimAuthority(e)) || len(e.Limitations) == 0 {
				t.Fatal("canonical factual rank/equivalence disclosure missing")
			}
		}
		if e.RelativePath == "internal/usecase/application_command_result.go" {
			t.Fatal("fabricated original contract file")
		}
	}
	legacy, _, err := PrepareAnalysisRequest(context.Background(), request, route, &contract, nil, catalog)
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := NewAnalysisEngine().Prepare(context.Background(), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if projectResultFound(old) {
		t.Fatal("v1.1 gained unversioned local-result support")
	}
}

func TestProjectAnalysisExistingInterfaceProofVersion(t *testing.T) {
	request, route, _, catalog, _ := projectFixture(t)
	contract := domain.ContractPlan{State: domain.ContractPlanFreezeNeeded, Required: true, FreezeRequired: true, Boundaries: []domain.PlannedContractBoundary{{Kind: "domain-model", Owner: domain.RouteReference{ProjectID: "local-project", RouteID: "backend.domain"}, TargetPath: "internal/domain/domain_model.go"}, {Kind: "application-command-result", Owner: domain.RouteReference{ProjectID: "local-project", RouteID: "backend.usecase"}, TargetPath: "internal/usecase/application_command_result.go"}}}
	request.Budget = core.Budget{MaxSourceBytes: 1 << 20, MaxContextTokens: 65536, ReservedPromptTokens: 1024, PerFacetTokens: 16384, PerSourceBytes: 1 << 20}
	pack, _, _, err := PrepareProjectContext(context.Background(), request, route, &contract, nil, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Status != core.Complete {
		t.Fatalf("v1.2 consistent declaration replay missing: %+v", pack.UnresolvedQuestions)
	}
}

func TestProjectAnalysisPortableBoundaryProofs(t *testing.T) {
	for _, name := range []string{"concrete", "interface"} {
		t.Run(name, func(t *testing.T) {
			request, route, contract, catalog, _ := projectConcreteFixture(t)
			if name == "interface" {
				request, route, _, catalog, _ = projectFixture(t)
				contract = domain.ContractPlan{State: domain.ContractPlanFreezeNeeded, Required: true, FreezeRequired: true, Boundaries: []domain.PlannedContractBoundary{{Kind: "application-command-result", Owner: domain.RouteReference{ProjectID: "local-project", RouteID: "backend.usecase"}, TargetPath: "internal/usecase/application_command_result.go"}}}
			}
			pack, _, _, e := PrepareProjectContext(context.Background(), request, route, &contract, nil, catalog)
			if e != nil || !projectResultFound(pack) {
				t.Fatalf("initialproof:%v", e)
			}
			body, _ := json.Marshal(pack)
			var portable core.ContextPack
			if e := json.Unmarshal(body, &portable); e != nil {
				t.Fatal(e)
			}
			facet := core.Facet{ID: "module-current", SourceIdentity: request.Sources[0].Identity, Path: "go.mod", ExpectedHash: hashBytes([]byte("module example.test/local\n\ngo 1.23\n")), QueryKind: core.QueryExact, Kind: "source", ClaimType: core.ImplementationBehavior, Required: true}
			delta, _, scope, e := ExpandProjectContext(context.Background(), portable, request, route, &contract, nil, catalog, core.ExpandRequest{Facet: facet, Reason: "inspect current module proof"})
			if e != nil {
				t.Fatal(e)
			}
			again, _, scopeAgain, e := ExpandProjectContext(context.Background(), portable, request, route, &contract, nil, catalog, core.ExpandRequest{Facet: facet, Reason: "inspect current module proof"})
			if e != nil {
				t.Fatal(e)
			}
			if !projectResultFound(delta.Pack) || delta.Pack.ContentDigest != again.Pack.ContentDigest || scope.BindingDigest != scopeAgain.BindingDigest {
				t.Fatal("portable proof/digest failure")
			}
			portable.ForbiddenScope.Write = nil
			portable.ContentDigest, _ = core.SemanticDigest(portable)
			failed, _, bad, e := ExpandProjectContext(context.Background(), portable, request, route, &contract, nil, catalog, core.ExpandRequest{Facet: facet, Reason: "inspect current module proof"})
			if e == nil || len(failed.Pack.Evidence) > 0 || len(bad.Layers) > 0 {
				t.Fatal("forged proof pack leaked evidence")
			}
		})
	}
}
