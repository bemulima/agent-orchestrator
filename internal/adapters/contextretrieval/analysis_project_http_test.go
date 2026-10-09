package contextretrieval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func projectHTTPFixture(t *testing.T, metadata string, files map[string]string, callerPath, callerSymbol string) core.RetrievalRequest {
	t.Helper()
	r, _ := projectAreaFixture(t, metadata)
	for p, body := range files {
		if e := os.MkdirAll(filepath.Dir(filepath.Join(r.Sources[0].Root, p)), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(r.Sources[0].Root, p), []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	r.RequiredFacets = []core.Facet{{ID: "actual-http-caller", SourceIdentity: r.Sources[0].Identity, Path: callerPath, Symbol: callerSymbol, ExpectedHash: hashBytes([]byte(files[callerPath])), QueryKind: core.QueryDefinition, Kind: "symbol", ClaimType: core.ImplementationBehavior, Required: true}}
	return r
}

func projectHTTPPrepare(t *testing.T, r core.RetrievalRequest) ProjectAnalysisScope {
	t.Helper()
	_, route, contract, catalog, _ := projectFixture(t)
	_, _, scope, e := PrepareProjectContext(context.Background(), r, route, &contract, nil, catalog)
	if e != nil {
		t.Fatal(e)
	}
	return scope
}

func projectHasLayer(scope ProjectAnalysisScope, name string) bool {
	for _, l := range scope.Layers {
		if l.Layer == name {
			return true
		}
	}
	return false
}

func TestProjectAnalysisNamedEntrypointsPreserveAuthoredLayers(t *testing.T) {
	for _, meta := range []string{
		"entrypoints: [{name: service, source_path: cmd/service/main.go, responsibilities: [inert prose]}]\nlayers: {websocket_transport: internal/customarea}\n",
		"entrypoints: {application: internal/customarea, executable: cmd/service}\nlayers: {websocket_transport: internal/customarea}\n",
	} {
		r, _ := projectAreaFixture(t, meta)
		s := projectHTTPPrepare(t, r)
		if !projectHasLayer(s, "project.websocket_transport") || len(s.ReadAreaGaps) != 0 {
			t.Fatalf("valid existing entrypoint shape discarded authored layers: %+v", s.ReadAreaGaps)
		}
	}
}

func TestProjectAnalysisInfrastructureDirectHTTPSyntax(t *testing.T) {
	p := "internal/customarea/reader.go"
	r := projectHTTPFixture(t, "layers: {infrastructure: internal/customarea}\n", map[string]string{p: "package customarea\nimport \"net/http\"\nfunc Fetch() { _,_ = http.NewRequest(\"GET\", \"https://example.invalid\", nil) }\n"}, p, "Fetch")
	s := projectHTTPPrepare(t, r)
	if !projectHasLayer(s, "project.infrastructure") || !projectHasLayer(s, "backend.infrastructure.client") {
		t.Fatal("actual admitted outgoing HTTP syntax in authored infrastructure lost client attribution")
	}
}

func projectConcreteHTTPFiles() map[string]string {
	return map[string]string{
		"internal/customarea/reader.go": "package customarea\ntype Reader struct { wire Wire }\nfunc normalize() int { return 1 }\n",
		"internal/customarea/wire.go":   "package customarea\nimport h \"net/http\"\ntype Wire struct { client *h.Client }\n",
		"internal/assembly/build.go":    "package assembly\nimport wire \"example.test/local/internal/customarea\"\nfunc Build(client wire.Wire) {}\n",
	}
}

func TestProjectAnalysisConcreteHTTPTypeReference(t *testing.T) {
	for _, caller := range []struct{ path, symbol, context string }{
		{"internal/customarea/reader.go", "normalize", "SAME_FILE_CONTEXT"},
		{"internal/assembly/build.go", "Build", "concrete"},
	} {
		t.Run(caller.symbol, func(t *testing.T) {
			r := projectHTTPFixture(t, "layers: {infrastructure: internal/customarea, composition: internal/assembly}\n", projectConcreteHTTPFiles(), caller.path, caller.symbol)
			s := projectHTTPPrepare(t, r)
			found := false
			for _, l := range s.Layers {
				if l.Layer != "backend.infrastructure.client" {
					continue
				}
				found = l.Witness.Path == "internal/customarea/wire.go" && l.Witness.Symbol == "customarea.Wire" && len(l.Chain) >= 3 && strings.Contains(l.Relation, caller.context)
			}
			if !found || len(s.WritableOwners) != 0 {
				t.Fatal("bounded current concrete type must be actual terminal witness, with origin/context chain and no write authority")
			}
		})
	}
}

func TestProjectAnalysisHTTPPathDoesNotProveIncomingDTO(t *testing.T) {
	for _, inbound := range []bool{false, true} {
		files := map[string]string{"internal/customarea/data.go": "package customarea\ntype Envelope struct{ Value string }\n"}
		if inbound {
			files["internal/transport/http/ordinary.go"] = "package handler\nimport (\"net/http\"; dto \"example.test/local/internal/customarea\")\nfunc Serve(w http.ResponseWriter, r *http.Request) { _ = dto.Envelope{} }\n"
		} else {
			files["internal/transport/http/ordinary.go"] = "package handler\nimport dto \"example.test/local/internal/customarea\"\nfunc Ordinary() { _ = dto.Envelope{} }\n"
		}
		r := projectHTTPFixture(t, "layers: {transport: internal/customarea}\n", files, "internal/customarea/data.go", "Envelope")
		sym := "Ordinary"
		if inbound {
			sym = "Serve"
		}
		p := "internal/transport/http/ordinary.go"
		r.RequiredFacets = append(r.RequiredFacets, core.Facet{ID: "dto-caller", SourceIdentity: r.Sources[0].Identity, Path: p, Symbol: sym, ExpectedHash: hashBytes([]byte(files[p])), QueryKind: core.QueryDefinition, Kind: "symbol", ClaimType: core.ImplementationBehavior, Required: true})
		s := projectHTTPPrepare(t, r)
		dtoIncoming := false
		for _, l := range s.Layers {
			if l.Layer == "backend.transport.http" && l.Witness.Path == "internal/customarea/data.go" {
				dtoIncoming = true
			}
		}
		if dtoIncoming != inbound {
			t.Fatalf("HTTP path gave incoming role without actual inbound AST: inbound=%t", inbound)
		}
	}
}

func TestProjectAnalysisConcreteHTTPTypeControls(t *testing.T) {
	for _, name := range []string{"plain-area", "interface-field", "alias", "generic", "embedded", "duplicate", "wrong-import", "dot-import", "shadow", "malformed", "mixed-package", "excluded", "wrong-module", "nested-module", "wrong-caller-hash", "no-caller-symbol", "unrequested-context", "imported-foreign-module", "imported-shadow"} {
		t.Run(name, func(t *testing.T) {
			files := projectConcreteHTTPFiles()
			callerPath, callerSymbol := "internal/customarea/reader.go", "normalize"
			meta := "layers: {infrastructure: internal/customarea, composition: internal/assembly}\n"
			switch name {
			case "plain-area":
				files[callerPath] = "package customarea\nfunc normalize() int { return 1 }\n"
			case "interface-field":
				files[callerPath] = strings.Replace(files[callerPath], "wire Wire", "wire interface{}", 1)
			case "alias":
				files["internal/customarea/wire.go"] = strings.Replace(files["internal/customarea/wire.go"], "type Wire struct", "type Wire = struct", 1)
			case "generic":
				files["internal/customarea/wire.go"] = strings.Replace(files["internal/customarea/wire.go"], "type Wire struct", "type Wire[T any] struct", 1)
			case "embedded":
				files["internal/customarea/wire.go"] = strings.Replace(files["internal/customarea/wire.go"], "client *h.Client", "*h.Client", 1)
			case "duplicate":
				files["internal/customarea/other.go"] = files["internal/customarea/wire.go"]
			case "wrong-import":
				files["internal/customarea/wire.go"] = strings.Replace(files["internal/customarea/wire.go"], "net/http", "net/url", 1)
			case "dot-import":
				files["internal/customarea/wire.go"] = strings.Replace(files["internal/customarea/wire.go"], "import h", "import .", 1)
			case "shadow":
				files["internal/customarea/wire.go"] = strings.Replace(files["internal/customarea/wire.go"], "type Wire", "type h struct{ Client int }\ntype Wire", 1)
			case "malformed":
				files["internal/customarea/other.go"] = "package customarea\nfunc broken(\n"
			case "mixed-package":
				files["internal/customarea/other.go"] = "package unrelated\n"
			case "excluded":
				files["internal/customarea/other.go"] = "package customarea\ntype Unseen struct{}\n"
			case "wrong-module":
				files["go.mod"] = "module example.test/foreign\ngo 1.23\n"
				callerPath, callerSymbol = "internal/assembly/build.go", "Build"
			case "nested-module":
				files["internal/customarea/go.mod"] = "module example.test/nested\ngo 1.23\n"
			case "unrequested-context":
				callerPath, callerSymbol = "internal/transport/http/query.go", "Handler.Serve"
				files[callerPath] = "package http\nfunc (Handler) Serve() {}\ntype Handler struct{}\n"
			case "imported-foreign-module":
				callerPath, callerSymbol = "internal/assembly/build.go", "Build"
				files[callerPath] = strings.Replace(files[callerPath], "example.test/local", "example.test/foreign", 1)
			case "imported-shadow":
				callerPath, callerSymbol = "internal/assembly/build.go", "Build"
				files[callerPath] = strings.Replace(files[callerPath], "func Build", "type wire struct{}\nfunc Build", 1)
			}
			r := projectHTTPFixture(t, meta, files, callerPath, callerSymbol)
			if name == "excluded" {
				r.Sources[0].ExcludePaths = []string{"internal/customarea/other.go"}
			}
			if name == "wrong-caller-hash" {
				r.RequiredFacets[0].ExpectedHash = strings.Repeat("0", 64)
			}
			if name == "no-caller-symbol" {
				r.RequiredFacets[0].Symbol = ""
			}
			s := projectHTTPPrepare(t, r)
			if projectHasLayer(s, "backend.infrastructure.client") {
				t.Fatal("unverified type/context promoted descriptive outgoing client role")
			}
		})
	}
}

func TestProjectAnalysisEntrypointControls(t *testing.T) {
	for _, entry := range []string{"{application: {source_path: internal/customarea}}", "[{name: service}]", "[{name: service, source_path: ../escape}]", "[{name: service, source_path: internal/*}]", "[{name: service, source_path: [internal/customarea]}]", "{application: internal/customarea, application: internal/customarea}", "[&entry {name: service, source_path: internal/customarea}, *entry]", "[{name: service, source_path: internal/customarea}]\n---\nentrypoints: []", "[" + strings.Repeat("internal/customarea,", 65) + "]"} {
		t.Run(hashBytes([]byte(entry))[:8], func(t *testing.T) {
			r, _ := projectAreaFixture(t, "layers: {websocket_transport: internal/customarea}\nentrypoints: "+entry+"\n")
			s := projectHTTPPrepare(t, r)
			if projectHasLayer(s, "project.websocket_transport") || len(s.ReadAreaGaps) != 1 || s.ReadAreaGaps[0].Coverage.Complete {
				t.Fatal("invalid entrypoint accepted partial declarations or complete absence")
			}
		})
	}
}

func TestProjectAnalysisConcreteHTTPTypeJointBudget(t *testing.T) {
	r := projectHTTPFixture(t, "layers: {infrastructure: internal/customarea}\n", projectConcreteHTTPFiles(), "internal/customarea/reader.go", "normalize")
	_, route, contract, catalog, _ := projectFixture(t)
	_, _, s, e := PrepareProjectContext(context.Background(), r, route, &contract, nil, catalog)
	if e != nil || !projectHasLayer(s, "backend.infrastructure.client") {
		t.Fatal("full current certificate missing")
	}
	r.Budget = core.Budget{MaxSourceBytes: 1 << 20, MaxContextTokens: 1025, ReservedPromptTokens: 1024, PerFacetTokens: 16384, PerSourceBytes: 1 << 20}
	_, _, small, e := PrepareProjectContext(context.Background(), r, route, &contract, nil, catalog)
	if !errors.Is(e, ErrProjectAnalysisBudget) || len(small.Layers) != 0 {
		t.Fatal("tiny budget leaked terminal proof or silently omitted required certificate")
	}
}

func TestProjectAnalysisReviewedHTTPGuards(t *testing.T) {
	for _, name := range []string{"blank-field", "selected-generic", "selected-alias", "selected-embedded", "selected-wrapped", "duplicate-origin", "duplicate-import", "blank-import", "context-limit", "hidden-module", "hidden-imported-module", "bare-import-collision", "implicit-package-mismatch", "multiple-terminals"} {
		t.Run(name, func(t *testing.T) {
			files := projectConcreteHTTPFiles()
			p, s := "internal/customarea/reader.go", "normalize"
			switch name {
			case "blank-field":
				files["internal/customarea/wire.go"] = strings.Replace(files["internal/customarea/wire.go"], "client *h.Client", "_ *h.Client", 1)
			case "selected-generic":
				p, s = "internal/customarea/wire.go", "Wire"
				files[p] = strings.Replace(files[p], "type Wire struct", "type Wire[T any] struct", 1)
			case "selected-alias":
				p, s = "internal/customarea/wire.go", "Wire"
				files[p] = strings.Replace(files[p], "type Wire struct", "type Wire = struct", 1)
			case "selected-embedded":
				p, s = "internal/customarea/wire.go", "Wire"
				files[p] = strings.Replace(files[p], "client *h.Client", "*h.Client", 1)
			case "selected-wrapped":
				p, s = "internal/customarea/wire.go", "Wire"
				files[p] = strings.Replace(files[p], "client *h.Client", "clients []*h.Client", 1)
			case "duplicate-origin":
				s = "Reader"
				files[p] += "type Holder struct{wire Wire}\n"
				files["internal/customarea/other.go"] = "package customarea\ntype Reader struct{}\n"
			case "duplicate-import":
				files[p] = "package customarea\nimport(h \"net/http\";h \"net/url\")\nfunc normalize(){_,_=h.NewRequest(\"GET\",\"https://example.invalid\",nil)}\n"
			case "blank-import":
				files[p] = "package customarea\nimport _ \"net/http\"\nfunc normalize(){_,_=_.NewRequest(\"GET\",\"https://example.invalid\",nil)}\n"
			case "context-limit":
				files[p] = "package customarea\nfunc normalize(){}\n"
				for i := 0; i < 1001; i++ {
					files[p] += fmt.Sprintf("type A%04d struct{value int}\n", i)
				}
				files[p] += "type ZHolder struct{wire Wire}\n"
			case "hidden-module", "hidden-imported-module":
				files["internal/go.mod"] = "module example.test/hidden\ngo 1.23\n"
				if name == "hidden-imported-module" {
					p, s = "internal/assembly/build.go", "Build"
				}
			case "bare-import-collision":
				files[p] = "package customarea\nimport Wire \"net/url\"\ntype Reader struct{wire Wire}\nfunc normalize(){}\n"
			case "implicit-package-mismatch":
				p, s = "internal/assembly/build.go", "Build"
				files[p] = "package assembly\nimport \"example.test/local/internal/customarea\"\nfunc Build(w customarea.Wire){}\n"
				for _, f := range []string{"internal/customarea/reader.go", "internal/customarea/wire.go"} {
					files[f] = strings.Replace(files[f], "package customarea", "package renamed", 1)
				}
			case "multiple-terminals":
				files[p] += "type OtherHolder struct{wire OtherWire}\n"
				files["internal/customarea/other.go"] = "package customarea\nimport \"net/http\"\ntype OtherWire struct{ client *http.Client }\n"
			}
			r := projectHTTPFixture(t, "layers: {infrastructure: internal/customarea, composition: internal/assembly}\n", files, p, s)
			if strings.HasPrefix(name, "hidden-") {
				r.Sources[0].ExcludePaths = []string{"internal/go.mod"}
			}
			if name == "context-limit" {
				r.Budget = core.Budget{MaxSourceBytes: 1 << 22, MaxContextTokens: 262144, ReservedPromptTokens: 1024, PerFacetTokens: 262144, PerSourceBytes: 1 << 22}
			}
			scope := projectHTTPPrepare(t, r)
			if projectHasLayer(scope, "backend.infrastructure.client") {
				t.Fatal("reviewed unverified/ambiguous/context-limited syntax promoted client role")
			}
		})
	}
}

func TestProjectAnalysisEntrypointRecursiveGlobRejected(t *testing.T) {
	for _, entry := range []string{"internal/customarea/**", "{application: internal/customarea/**}", "[{name: service, source_path: internal/customarea/**}]"} {
		t.Run(hashBytes([]byte(entry))[:8], func(t *testing.T) {
			r, _ := projectAreaFixture(t, "layers: {websocket_transport: internal/customarea}\nentrypoints: "+entry+"\n")
			s := projectHTTPPrepare(t, r)
			if projectHasLayer(s, "project.websocket_transport") || len(s.ReadAreaGaps) != 1 || s.ReadAreaGaps[0].Coverage.Complete {
				t.Fatal("raw entrypoint recursive glob accepted through layer normalization")
			}
		})
	}
}

func TestProjectAnalysisConcreteBoundaryHiddenAncestorModule(t *testing.T) {
	r, route, contract, catalog, _ := projectConcreteFixture(t)
	p := "internal/usecase/go.mod"
	if e := os.WriteFile(filepath.Join(r.Sources[0].Root, p), []byte("module example.test/hidden\ngo 1.23\n"), 0600); e != nil {
		t.Fatal(e)
	}
	r.Sources[0].ExcludePaths = []string{p}
	_, _, s, e := PrepareProjectContext(context.Background(), r, route, &contract, nil, catalog)
	if e != nil {
		t.Fatal(e)
	}
	if len(s.BoundaryProofs) > 0 {
		t.Fatal("unread excluded ancestor module certified concrete local boundary")
	}
}

func TestProjectAnalysisExplicitRenamedConcreteImport(t *testing.T) {
	files := projectConcreteHTTPFiles()
	for _, p := range []string{"internal/customarea/reader.go", "internal/customarea/wire.go"} {
		files[p] = strings.Replace(files[p], "package customarea", "package renamed", 1)
	}
	r := projectHTTPFixture(t, "layers: {infrastructure: internal/customarea, composition: internal/assembly}\n", files, "internal/assembly/build.go", "Build")
	s := projectHTTPPrepare(t, r)
	if !projectHasLayer(s, "backend.infrastructure.client") {
		t.Fatal("explicit imported qualifier lost current declared-package terminal")
	}
}

func TestProjectAnalysisHTTPContextRequiresOrdinaryReceiver(t *testing.T) {
	for _, receiver := range []string{"type Reader[T any] struct{}\nfunc (*Reader[T]) Helper(w Wire){}", "type Reader struct{}\nfunc (a,b Reader) Helper(w Wire){}", "type Reader = Actual\ntype Actual struct{}\nfunc (Reader) Helper(w Wire){}", "func (Unknown) Helper(w Wire){}"} {
		t.Run(hashBytes([]byte(receiver))[:8], func(t *testing.T) {
			files := projectConcreteHTTPFiles()
			files["internal/customarea/reader.go"] = "package customarea\nfunc normalize(){}\n" + receiver + "\n"
			r := projectHTTPFixture(t, "layers: {infrastructure: internal/customarea}\n", files, "internal/customarea/reader.go", "normalize")
			if projectHasLayer(projectHTTPPrepare(t, r), "backend.infrastructure.client") {
				t.Fatal("unsupported/aliased/generic context receiver supplied ordinary parameter proof")
			}
		})
	}
}

func TestProjectAnalysisHTTPConstructorRequiresOrdinaryReceiver(t *testing.T) {
	for _, receiver := range []string{"type Reader = Actual\ntype Actual struct{}\nfunc (Reader) Fetch()", "func (Unknown) Fetch()"} {
		t.Run(hashBytes([]byte(receiver))[:8], func(t *testing.T) {
			p := "internal/customarea/reader.go"
			body := "package customarea\nimport \"net/http\"\n" + receiver + " { _,_=http.NewRequest(\"GET\",\"https://example.invalid\",nil) }\n"
			r := projectHTTPFixture(t, "layers: {infrastructure: internal/customarea}\n", map[string]string{p: body}, p, "Fetch")
			if projectHasLayer(projectHTTPPrepare(t, r), "backend.infrastructure.client") {
				t.Fatal("unverified constructor receiver bypassed ordinary declaration requirement")
			}
		})
	}
}

func TestProjectAnalysisHTTPConstructorNamespaceCollision(t *testing.T) {
	p := "internal/customarea/reader.go"
	files := map[string]string{p: "package customarea\nimport h \"net/http\"\nfunc Fetch(){_,_=h.NewRequest(\"GET\",\"https://example.invalid\",nil)}\n", "internal/customarea/other.go": "package customarea\ntype h struct{}\n"}
	r := projectHTTPFixture(t, "layers: {infrastructure: internal/customarea}\n", files, p, "Fetch")
	if projectHasLayer(projectHTTPPrepare(t, r), "backend.infrastructure.client") {
		t.Fatal("package/file import namespace collision supplied outgoing constructor proof")
	}
}

func TestProjectAnalysisGeneratedSeedCannotOriginateHTTPType(t *testing.T) {
	r := projectHTTPFixture(t, "layers: {infrastructure: internal/customarea}\n", projectConcreteHTTPFiles(), "internal/customarea/reader.go", "normalize")
	caller := r.RequiredFacets[0]
	r.RequiredFacets = []core.Facet{{ID: "directory-caller", SourceIdentity: caller.SourceIdentity, Path: "internal/customarea", QueryKind: core.QueryExact, Kind: "source", ClaimType: core.ImplementationBehavior, Required: true}}
	_, route, contract, catalog, _ := projectFixture(t)
	route.EvidenceIDs = []string{"generated-http-source"}
	route.EvidenceIndex = []domain.RoutingEvidence{{ID: "generated-http-source", ProjectID: r.Sources[0].RouteIdentity, Path: caller.Path, Symbol: caller.Symbol, Checksum: caller.ExpectedHash, Kind: "code"}}
	pack, _, s, e := PrepareProjectContext(context.Background(), r, route, &contract, nil, catalog)
	if e != nil {
		t.Fatal(e)
	}
	retained := false
	for _, f := range pack.RetrievalPlan.RequiredFacets {
		if f.Path == caller.Path && f.Symbol == caller.Symbol && f.ExpectedHash == caller.ExpectedHash {
			retained = true
		}
	}
	if !retained || projectHasLayer(s, "backend.infrastructure.client") {
		t.Fatal("actual generated source selector was missing or originated concrete HTTP proof")
	}
	_, _, expanded, e := ExpandProjectContext(context.Background(), pack, r, route, &contract, nil, catalog, core.ExpandRequest{Facet: caller, Reason: "inspect explicit current concrete source"})
	if e != nil || !projectHasLayer(expanded, "backend.infrastructure.client") {
		t.Fatal("successful explicit retained Expand lost concrete terminal")
	}
}

func TestProjectAnalysisConcreteHTTPStaleTerminalRejectsPortableBase(t *testing.T) {
	r := projectHTTPFixture(t, "layers: {infrastructure: internal/customarea}\n", projectConcreteHTTPFiles(), "internal/customarea/reader.go", "normalize")
	_, route, contract, catalog, _ := projectFixture(t)
	pack, _, s, e := PrepareProjectContext(context.Background(), r, route, &contract, nil, catalog)
	if e != nil || !projectHasLayer(s, "backend.infrastructure.client") {
		t.Fatal("initial concrete proof missing")
	}
	b, _ := json.Marshal(pack)
	var portable core.ContextPack
	if e = json.Unmarshal(b, &portable); e != nil {
		t.Fatal(e)
	}
	p := "internal/customarea/wire.go"
	if e = os.WriteFile(filepath.Join(r.Sources[0].Root, p), []byte(projectConcreteHTTPFiles()[p]+"\n// changed current terminal\n"), 0600); e != nil {
		t.Fatal(e)
	}
	_, _, bad, e := ExpandProjectContext(context.Background(), portable, r, route, &contract, nil, catalog, core.ExpandRequest{Facet: r.RequiredFacets[0], Reason: "inspect current source"})
	if !errors.Is(e, core.ErrStaleBase) || len(bad.Layers) > 0 {
		t.Fatal("portable stale terminal leaked concrete proof")
	}
}

func TestProjectAnalysisWholeFileSeedPreservesHTTPContext(t *testing.T) {
	for _, symbol := range []string{"normalize", "Reader.Name"} {
		t.Run(symbol, func(t *testing.T) {
			files := projectConcreteHTTPFiles()
			p := "internal/customarea/reader.go"
			files[p] += "func (Reader) Name() string { return \"reader\" }\n"
			r := projectHTTPFixture(t, "layers: {infrastructure: internal/customarea}\n", files, p, symbol)
			caller := r.RequiredFacets[0]
			_, route, contract, catalog, _ := projectFixture(t)
			route.EvidenceIDs = []string{"whole-file-context"}
			route.EvidenceIndex = []domain.RoutingEvidence{{ID: "whole-file-context", ProjectID: r.Sources[0].RouteIdentity, Path: p, Checksum: caller.ExpectedHash, Kind: "code"}}
			pack, _, scope, e := PrepareProjectContext(context.Background(), r, route, &contract, nil, catalog)
			if e != nil {
				t.Fatal(e)
			}
			seed := false
			for _, f := range pack.RetrievalPlan.RequiredFacets {
				seed = seed || f.Path == p && f.Symbol == "" && f.ExpectedHash == caller.ExpectedHash
			}
			found := false
			for _, l := range scope.Layers {
				if l.Layer != "backend.infrastructure.client" || l.Witness.Path != "internal/customarea/wire.go" || l.Witness.Symbol != "customarea.Wire" || !strings.Contains(l.Relation, "SAME_FILE_CONTEXT") {
					continue
				}
				for _, a := range l.Chain {
					found = found || a.Source == caller.SourceIdentity && a.Path == p && a.Hash == caller.ExpectedHash && symbolMatches(a.Symbol, symbol)
				}
			}
			if !seed || scope.JointBudgetStatus != core.Complete {
				t.Fatal("control requires an actual generated whole-file seed and an unblocked emitted-bundle budget")
			}
			if !found || len(scope.WritableOwners) != 0 {
				t.Fatal("generated whole-file acquisition erased independently pinned caller adjacency or its bounded terminal chain")
			}
		})
	}
}

func TestProjectAnalysisWholeFileSeedAloneCannotOriginateHTTPContext(t *testing.T) {
	r := projectHTTPFixture(t, "layers: {infrastructure: internal/customarea}\n", projectConcreteHTTPFiles(), "internal/customarea/reader.go", "normalize")
	caller := r.RequiredFacets[0]
	r.RequiredFacets = []core.Facet{{ID: "directory-caller", SourceIdentity: caller.SourceIdentity, Path: "internal/customarea", QueryKind: core.QueryExact, Kind: "source", ClaimType: core.ImplementationBehavior, Required: true}}
	_, route, contract, catalog, _ := projectFixture(t)
	route.EvidenceIDs = []string{"whole-file-context"}
	route.EvidenceIndex = []domain.RoutingEvidence{{ID: "whole-file-context", ProjectID: r.Sources[0].RouteIdentity, Path: caller.Path, Checksum: caller.ExpectedHash, Kind: "code"}}
	pack, _, scope, e := PrepareProjectContext(context.Background(), r, route, &contract, nil, catalog)
	if e != nil || projectHasLayer(scope, "backend.infrastructure.client") {
		t.Fatal("generated whole-file seed originated an HTTP proof without an explicit pinned caller")
	}
	_, _, expanded, e := ExpandProjectContext(context.Background(), pack, r, route, &contract, nil, catalog, core.ExpandRequest{Facet: caller, Reason: "inspect explicit current source"})
	if e != nil || !projectHasLayer(expanded, "backend.infrastructure.client") {
		t.Fatal("successful explicit Expand did not restore independently bounded caller adjacency")
	}
}

func TestProjectAnalysisWholeFileHTTPContextGlobalLimit(t *testing.T) {
	for _, scenario := range []struct {
		count, repeated int
		allowed         bool
	}{{999, 0, true}, {1000, 0, false}, {1000, 1, false}} {
		t.Run(fmt.Sprintf("%d/repeated%d", scenario.count, scenario.repeated), func(t *testing.T) {
			files := projectConcreteHTTPFiles()
			delete(files, "internal/customarea/reader.go")
			p, q := "internal/customarea/a.go", "internal/customarea/b.go"
			files[p] = "package customarea\nfunc first(){}\n"
			for i := 0; i < scenario.count; i++ {
				files[p] += fmt.Sprintf("type A%04d struct{ value int }\n", i)
			}
			files[q] = "package customarea\nfunc second(){}\ntype Holder struct{wire Wire}\n"
			r := projectHTTPFixture(t, "layers: {infrastructure: internal/customarea}\n", files, p, "first")
			second := r.RequiredFacets[0]
			second.ID, second.Path, second.Symbol, second.ExpectedHash = "second-current-caller", q, "second", hashBytes([]byte(files[q]))
			r.RequiredFacets = append(r.RequiredFacets, second)
			if scenario.repeated != 0 {
				again := r.RequiredFacets[0]
				again.ID = "repeated-current-caller"
				r.RequiredFacets = append(r.RequiredFacets, again)
			}
			r.Budget = core.Budget{MaxSourceBytes: 1 << 22, MaxContextTokens: 262144, ReservedPromptTokens: 1024, PerFacetTokens: 262144, PerSourceBytes: 1 << 22}
			_, route, contract, catalog, _ := projectFixture(t)
			route.EvidenceIDs = []string{"whole-file-a", "whole-file-b"}
			route.EvidenceIndex = []domain.RoutingEvidence{
				{ID: "whole-file-a", ProjectID: r.Sources[0].RouteIdentity, Path: p, Checksum: hashBytes([]byte(files[p])), Kind: "code"},
				{ID: "whole-file-b", ProjectID: r.Sources[0].RouteIdentity, Path: q, Checksum: second.ExpectedHash, Kind: "code"},
			}
			pack, _, companion, e := PrepareProjectContext(context.Background(), r, route, &contract, nil, catalog)
			if e != nil {
				t.Fatal(e)
			}
			seeded := map[string]bool{}
			current := r
			current.RequiredFacets = nil
			for _, f := range pack.RetrievalPlan.RequiredFacets {
				if f.Path != "" && f.ExpectedHash != "" {
					current.RequiredFacets = append(current.RequiredFacets, f)
				}
				if f.Symbol == "" && (f.Path == p || f.Path == q) {
					seeded[f.Path] = true
				}
			}
			if !seeded[p] || !seeded[q] {
				t.Fatal("control requires both actual generated whole-file seeds")
			}
			// Inspect trusted retained-plan acquisition before bundle clipping. The
			// initial request scope does not yet include generated core seeds.
			prepared, raw, e := prepareAnalysisRequest(context.Background(), current, route, &contract, nil, catalog, true, &r.RequiredFacets)
			if e != nil {
				t.Fatal(e)
			}
			limited := false
			for _, d := range prepared.Route.Diagnostics {
				limited = limited || d.Code == "ANALYSIS_HTTP_CLIENT_CONTEXT_LIMIT" && d.Status == core.Partial
			}
			if limited == scenario.allowed || projectHasLayer(ProjectAnalysisScope{AnalysisScope: raw}, "backend.infrastructure.client") != scenario.allowed {
				t.Fatalf("global1000 adjacency limit: diagnostic=%v, client=%v, diagnostics=%+v", limited, projectHasLayer(ProjectAnalysisScope{AnalysisScope: raw}, "backend.infrastructure.client"), prepared.Route.Diagnostics)
			}
			assertDiagnostic := func(s ProjectAnalysisScope) {
				t.Helper()
				for _, d := range s.MetadataDiagnostics {
					if d.Code == "ANALYSIS_HTTP_CLIENT_CONTEXT_LIMIT" && d.Status == core.Partial {
						if !scenario.allowed {
							return
						}
						t.Fatal("context at exactly1000 was incorrectly diagnosed as omitted")
					}
				}
				if !scenario.allowed {
					t.Fatal("public emitted companion omitted the PARTIAL context-limit diagnostic")
				}
			}
			assertDiagnostic(companion)
			_, _, expanded, e := ExpandProjectContext(context.Background(), pack, r, route, &contract, nil, catalog, core.ExpandRequest{Facet: second, Reason: "inspect retained current declaration context"})
			if e != nil {
				t.Fatal(e)
			}
			assertDiagnostic(expanded)
		})
	}
}
