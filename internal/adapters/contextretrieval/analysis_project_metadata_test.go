package contextretrieval

import (
	"context"
	"encoding/json"
	"errors"
	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func projectAreaFixture(t *testing.T, metadata string) (core.RetrievalRequest, string) {
	t.Helper()
	request, _, _, _, _ := projectFixture(t)
	p := "internal/customarea/reader.go"
	body := "package customarea\ntype Reader struct{}\nfunc (Reader) Fetch() string { return helper() }\nfunc helper() string { return \"inert evidence\" }\n"
	if e := os.MkdirAll(filepath.Dir(filepath.Join(request.Sources[0].Root, p)), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(request.Sources[0].Root, p), []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	request.Sources[0].ReadPaths = append(request.Sources[0].ReadPaths, ".ai")
	if metadata != "" {
		if e := os.MkdirAll(filepath.Join(request.Sources[0].Root, ".ai"), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(request.Sources[0].Root, ".ai/architecture.yaml"), []byte(metadata), 0600); e != nil {
			t.Fatal(e)
		}
	}
	request.RequiredFacets = []core.Facet{{ID: "explicit-reader", SourceIdentity: request.Sources[0].Identity, Path: p, Symbol: "Reader.Fetch", ExpectedHash: hashBytes([]byte(body)), QueryKind: core.QueryDefinition, Kind: "symbol", ClaimType: core.ImplementationBehavior, Required: true}}
	return request, body
}

func TestProjectAnalysisMissingAuthoredAreaDiagnostic(t *testing.T) {
	_, route, contract, catalog, _ := projectFixture(t)
	request, _ := projectAreaFixture(t, "layers: {domain: internal/domain}\n")
	pack, _, scope, err := PrepareProjectContext(context.Background(), request, route, &contract, nil, catalog)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(scope)
	var wire map[string]json.RawMessage
	_ = json.Unmarshal(raw, &wire)
	var gaps []map[string]any
	_ = json.Unmarshal(wire["read_area_gaps"], &gaps)
	if len(gaps) != 1 {
		t.Fatalf("missing current explicit source area must have exactly one gap, got %s", wire["read_area_gaps"])
	}
	if len(scope.WritableOwners) != 0 {
		t.Fatal("missing author attribution promoted authority/status")
	}
	found := false
	for _, d := range scope.MetadataDiagnostics {
		if d.Code == "ANALYSIS_AUTHORED_READ_AREA_MISSING" && d.FacetID == "explicit-reader" && d.EvidenceID != "" {
			found = true
		}
	}
	if !found || len(pack.Evidence) == 0 {
		t.Fatal("missing author binding must not discard current source evidence")
	}
}

func projectOperationFixture(t *testing.T) (core.RetrievalRequest, string, string) {
	request, body := projectAreaFixture(t, "")
	p := ".ai/architecture/endpoints/post-read.yaml"
	meta := `schema: architecture/v1
kind: operation
id: fixture.read
manifest_revision: 1
service_id: fixture
type: http
identity: {transport: http, http: {method: POST, path: /api/v1/read}}
access:
  audience: {value: unknown, confidence: 0, evidence: [{source_path: internal/customarea/reader.go}]}
  authentication: {value: unknown, confidence: 0, evidence: [{source_path: internal/customarea/reader.go}]}
  authorization: {value: unknown, confidence: 0, evidence: [{source_path: internal/customarea/reader.go}]}
  idempotency: {value: unknown, confidence: 0, evidence: [{source_path: internal/customarea/reader.go}]}
trigger: {description: {value: unknown, confidence: 0, evidence: [{source_path: internal/customarea/reader.go}]}}
business_task: {value: unknown, confidence: 0, evidence: [{source_path: internal/customarea/reader.go}]}
implementation:
  use_cases: [{source_path: internal/customarea/reader.go, symbol: Reader.Fetch, checksum: ` + hashBytes([]byte(body)) + `}]
evidence: [{source_path: internal/customarea/reader.go}]
confidence: 0
`
	if e := os.MkdirAll(filepath.Dir(filepath.Join(request.Sources[0].Root, p)), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(request.Sources[0].Root, p), []byte(meta), 0600); e != nil {
		t.Fatal(e)
	}
	return request, p, meta
}

func TestProjectAnalysisStrictUseCaseAlias(t *testing.T) {
	request, _, _ := projectOperationFixture(t)
	_, route, contract, catalog, _ := projectFixture(t)
	_, _, scope, err := PrepareProjectContext(context.Background(), request, route, &contract, nil, catalog)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range scope.Layers {
		if l.Layer == "use_cases:internal/customarea" && l.Witness.Symbol == "Reader.Fetch" {
			found = true
		}
	}
	if !found {
		t.Fatal("current strict use-case source must expose generic directory alias")
	}
}

func TestProjectAnalysisAuthoredAreaControls(t *testing.T) {
	for _, tc := range []struct {
		name, metadata string
		gap, complete  bool
	}{
		{"literal", "layers: {read: internal/customarea}\n", false, true},
		{"component-boundary", "layers: {read: internal/custom}\n", true, true},
		{"unsafe", "layers: {read: ../internal/customarea}\n", true, false},
		{"duplicate", "layers: {read: internal/customarea, read: internal/domain}\n", true, false},
		{"aliased", "layers: {read: &area internal/customarea, other: *area}\n", true, false},
		{"partial-malformed", "layers: {read: [internal/customarea, {bad: value}]}\n", true, false},
		{"wrong-layers-shape", "layers: internal/customarea\n", true, false},
		{"new-schema-not-authorized", "read_areas: [{source_path: internal/customarea, role: desired}]\n", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, route, contract, catalog, _ := projectFixture(t)
			request, _ := projectAreaFixture(t, tc.metadata)
			_, _, scope, err := PrepareProjectContext(context.Background(), request, route, &contract, nil, catalog)
			if err != nil {
				t.Fatal(err)
			}
			if (len(scope.ReadAreaGaps) > 0) != tc.gap {
				t.Fatalf("area membership: %+v", scope.ReadAreaGaps)
			}
			if tc.gap && scope.ReadAreaGaps[0].Coverage.Complete != tc.complete {
				t.Fatal("invalid metadata certified complete absence")
			}
			if tc.gap {
				g := scope.ReadAreaGaps[0]
				id := g.DiagnosticID
				g.DiagnosticID = ""
				if id != "analysis-read-area-gap:"+hashJSON(g) || g.Anchor.Hash != request.RequiredFacets[0].ExpectedHash || g.RequirementState != core.NotVerified {
					t.Fatal("gap provenance/hash binding")
				}
			}
		})
	}
}

func TestProjectAnalysisMetadataDirectoryOmission(t *testing.T) {
	for _, excluded := range []string{".ai/architecture", ".ai/architecture/endpoints"} {
		t.Run(excluded, func(t *testing.T) {
			request, _ := projectAreaFixture(t, "layers: {domain: internal/domain}\n")
			request.Sources[0].ExcludePaths = []string{excluded}
			p := filepath.Join(request.Sources[0].Root, ".ai/architecture/endpoints/hidden.yaml")
			if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(p, []byte("hidden metadata is not read\n"), 0600); e != nil {
				t.Fatal(e)
			}
			_, route, contract, catalog, _ := projectFixture(t)
			_, _, scope, e := PrepareProjectContext(context.Background(), request, route, &contract, nil, catalog)
			if e != nil {
				t.Fatal(e)
			}
			if len(scope.ReadAreaGaps) != 1 || scope.ReadAreaGaps[0].Coverage.Complete {
				t.Fatal("excluded metadata subtree certified complete author absence")
			}
		})
	}
}

func TestProjectAnalysisProfileFingerprintAttribution(t *testing.T) {
	for _, fingerprint := range []string{"", "sha256:contradictory"} {
		t.Run(fingerprint, func(t *testing.T) {
			request, route, contract, catalog, _ := projectFixture(t)
			route.Profiles[0].ProfileFingerprint = fingerprint
			request.RequiredFacets = request.RequiredFacets[:1]
			_, _, scope, e := PrepareProjectContext(context.Background(), request, route, &contract, nil, catalog)
			if e != nil {
				t.Fatal(e)
			}
			if len(scope.ReadAreaGaps) != 1 || scope.ReadAreaGaps[0].Coverage.Complete {
				t.Fatal("unverified profile certified canonical membership")
			}
		})
	}
}

func TestProjectAnalysisCurrentOperationReferenceControls(t *testing.T) {
	for _, name := range []string{"unpinned", "same-file-helper", "sibling", "stale-checksum", "no-symbol", "unknown-field", "duplicate-field", "ambiguous-symbol", "malformed-package", "no-caller", "wrong-caller-hash", "neighbor", "tiny-budget"} {
		t.Run(name, func(t *testing.T) {
			request, p, meta := projectOperationFixture(t)
			_, route, contract, catalog, _ := projectFixture(t)
			meta = strings.Replace(meta, ", checksum: "+request.RequiredFacets[0].ExpectedHash, "", 1)
			positive := name == "unpinned" || name == "same-file-helper" || name == "neighbor"
			switch name {
			case "same-file-helper":
				request.RequiredFacets[0].Symbol = "customarea.helper"
			case "sibling":
				body := "package customarea\nfunc Other() string {return \"other\"}\n"
				target := "internal/customarea/other.go"
				if e := os.WriteFile(filepath.Join(request.Sources[0].Root, target), []byte(body), 0600); e != nil {
					t.Fatal(e)
				}
				request.RequiredFacets[0].Path = target
				request.RequiredFacets[0].Symbol = "customarea.Other"
				request.RequiredFacets[0].ExpectedHash = hashBytes([]byte(body))
			case "stale-checksum":
				meta = strings.Replace(meta, "symbol: Reader.Fetch}", "symbol: Reader.Fetch, checksum: "+strings.Repeat("0", 64)+"}", 1)
			case "no-symbol":
				meta = strings.Replace(meta, ", symbol: Reader.Fetch", "", 1)
			case "unknown-field":
				meta += "unknown_field: value\n"
			case "duplicate-field":
				meta += "implementation: {}\n"
			case "ambiguous-symbol":
				body := "package customarea\ntype Other struct{}\nfunc (Other) Fetch() string {return \"other\"}\n"
				if e := os.WriteFile(filepath.Join(request.Sources[0].Root, "internal/customarea/other.go"), []byte(body), 0600); e != nil {
					t.Fatal(e)
				}
				meta = strings.Replace(meta, "symbol: Reader.Fetch", "symbol: Fetch", 1)
			case "malformed-package":
				if e := os.WriteFile(filepath.Join(request.Sources[0].Root, "internal/customarea/other.go"), []byte("package customarea\nfunc broken(\n"), 0600); e != nil {
					t.Fatal(e)
				}
			case "no-caller":
				request.RequiredFacets = nil
			case "wrong-caller-hash":
				request.RequiredFacets[0].ExpectedHash = strings.Repeat("0", 64)
			case "neighbor":
				request.Sources[0].Neighbor = true
			case "tiny-budget":
				request.Budget = core.Budget{MaxSourceBytes: 1 << 20, MaxContextTokens: 1025, ReservedPromptTokens: 1024, PerFacetTokens: 16384, PerSourceBytes: 1 << 20}
			}
			if e := os.WriteFile(filepath.Join(request.Sources[0].Root, p), []byte(meta), 0600); e != nil {
				t.Fatal(e)
			}
			_, _, scope, e := PrepareProjectContext(context.Background(), request, route, &contract, nil, catalog)
			if name == "tiny-budget" {
				if !errors.Is(e, ErrProjectAnalysisBudget) || len(scope.Layers) > 0 {
					t.Fatal("missing minimal certificate budget error")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			found, unpinned := false, false
			for _, l := range scope.Layers {
				if l.Layer == "use_cases:internal/customarea" {
					found = true
					if l.Witness.Symbol != "Reader.Fetch" || len(l.Evidence.Limitations) == 0 {
						t.Fatal("current ref witness/pin disclosure")
					}
				}
			}
			for _, d := range scope.MetadataDiagnostics {
				if d.Code == "ANALYSIS_OPERATION_REFERENCE_UNPINNED" {
					unpinned = true
					if d.EvidenceID == "" || d.Status != core.Partial {
						t.Fatal("unbound current-only diagnostic")
					}
				}
			}
			if found != positive || unpinned != positive {
				t.Fatalf("positive=%v aliases=%v unpinned=%v", positive, found, unpinned)
			}
			if positive && (len(scope.ReadAreaGaps) != 0 || len(scope.WritableOwners) != 0) {
				t.Fatal("positive current reference changed authority/area")
			}
		})
	}
}

func TestProjectAnalysisGeneratedSeedCannotOriginateAuthorProof(t *testing.T) {
	request, p, meta := projectOperationFixture(t)
	caller := request.RequiredFacets[0]
	meta = strings.Replace(meta, ", checksum: "+caller.ExpectedHash, "", 1)
	if e := os.WriteFile(filepath.Join(request.Sources[0].Root, p), []byte(meta), 0600); e != nil {
		t.Fatal(e)
	}
	request.RequiredFacets = []core.Facet{{ID: "metadata-caller", SourceIdentity: caller.SourceIdentity, Path: p, ExpectedHash: hashBytes([]byte(meta)), QueryKind: core.QueryMetadata, Kind: "metadata", ClaimType: core.ArchitectureRule, Required: true}, {ID: "directory-caller", SourceIdentity: caller.SourceIdentity, Path: "internal/customarea", QueryKind: core.QueryExact, Kind: "source", ClaimType: core.ImplementationBehavior, Required: true}}
	_, route, contract, catalog, _ := projectFixture(t)
	route.EvidenceIDs = []string{"generated-source"}
	route.EvidenceIndex = []domain.RoutingEvidence{{ID: "generated-source", ProjectID: request.Sources[0].RouteIdentity, Path: caller.Path, Symbol: caller.Symbol, Checksum: caller.ExpectedHash, Kind: "code"}}
	pack, _, scope, e := PrepareProjectContext(context.Background(), request, route, &contract, nil, catalog)
	if e != nil {
		t.Fatal(e)
	}
	seedRetained := false
	for _, f := range pack.RetrievalPlan.RequiredFacets {
		if f.Path == caller.Path && f.Symbol == caller.Symbol && f.ExpectedHash == caller.ExpectedHash {
			seedRetained = true
		}
	}
	if !seedRetained {
		t.Fatal("negative did not retain the generated source selector")
	}
	for _, l := range scope.Layers {
		if l.Layer == "use_cases:internal/customarea" {
			t.Fatal("generated selector originated unpinned caller proof")
		}
	}
	if len(scope.ReadAreaGaps) != 0 {
		t.Fatal("generated source created explicit caller gap")
	}
	delta, _, expanded, e := ExpandProjectContext(context.Background(), pack, request, route, &contract, nil, catalog, core.ExpandRequest{Facet: caller, Reason: "inspect explicit current source"})
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, l := range expanded.Layers {
		if l.Layer == "use_cases:internal/customarea" {
			found = true
		}
	}
	if !found || len(delta.Pack.RetrievalPlan.ExpansionHistory) != 1 {
		t.Fatal("successful explicit caller lost author proof")
	}
}

func TestProjectAnalysisScopedGapRetainsGeneralCitationDiagnostics(t *testing.T) {
	request, _ := projectAreaFixture(t, "layers: {domain: internal/domain}\n")
	p := filepath.Join(request.Sources[0].Root, ".ai/architecture/service.yaml")
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte("schema: architecture/v1\nkind: service\nid: fixture\nmanifest_revision: 1\n"), 0600); e != nil {
		t.Fatal(e)
	}
	_, route, contract, catalog, _ := projectFixture(t)
	_, _, scope, e := PrepareProjectContext(context.Background(), request, route, &contract, nil, catalog)
	if e != nil {
		t.Fatal(e)
	}
	retained := false
	for _, d := range scope.MetadataDiagnostics {
		if d.RelativePath == ".ai/architecture/service.yaml" {
			retained = true
		}
	}
	if !retained || len(scope.ReadAreaGaps) != 1 || !scope.ReadAreaGaps[0].Coverage.Complete {
		t.Fatal("unrelated general metadata diagnostic lost or polluted scoped area search")
	}
}

func TestProjectAnalysisGapPackageUncertainty(t *testing.T) {
	for _, name := range []string{"malformed", "excluded"} {
		t.Run(name, func(t *testing.T) {
			request, _ := projectAreaFixture(t, "layers: {domain: internal/domain}\n")
			p := "internal/customarea/other.go"
			body := "package customarea\nfunc broken(\n"
			if name == "excluded" {
				body = "package customarea\ntype Other struct{}\n"
				request.Sources[0].ExcludePaths = []string{p}
			}
			if e := os.WriteFile(filepath.Join(request.Sources[0].Root, p), []byte(body), 0600); e != nil {
				t.Fatal(e)
			}
			_, route, contract, catalog, _ := projectFixture(t)
			_, _, scope, e := PrepareProjectContext(context.Background(), request, route, &contract, nil, catalog)
			if e != nil {
				t.Fatal(e)
			}
			if len(scope.ReadAreaGaps) != 1 || scope.ReadAreaGaps[0].Coverage.Complete {
				t.Fatal("uncertain anchor package certified complete attribution search")
			}
		})
	}
}

func TestProjectAnalysisHTTPAliasShadowIsNotProtocolProof(t *testing.T) {
	request, _ := projectAreaFixture(t, "layers: {outbound: internal/customarea}\n")
	body := "package customarea\nimport \"net/http\"\ntype Reader struct{}\nfunc (Reader) Fetch() int {http:=struct{Client int}{Client:1};return http.Client}\n"
	if e := os.WriteFile(filepath.Join(request.Sources[0].Root, request.RequiredFacets[0].Path), []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	request.RequiredFacets[0].ExpectedHash = hashBytes([]byte(body))
	_, route, contract, catalog, _ := projectFixture(t)
	_, _, scope, e := PrepareProjectContext(context.Background(), request, route, &contract, nil, catalog)
	if e != nil {
		t.Fatal(e)
	}
	for _, l := range scope.Layers {
		if l.Layer == "backend.infrastructure.client" {
			t.Fatal("local shadow promoted outgoing HTTP role")
		}
	}
}

func TestProjectAnalysisUnsupportedRevisionIsSeparateFromAreaCoverage(t *testing.T) {
	request, _ := projectAreaFixture(t, "layers: {domain: internal/domain}\n")
	request.Sources[0].Revision = strings.Repeat("a", 40)
	_, route, contract, catalog, _ := projectFixture(t)
	_, _, scope, e := PrepareProjectContext(context.Background(), request, route, &contract, nil, catalog)
	if e != nil {
		t.Fatal(e)
	}
	if len(scope.ReadAreaGaps) != 1 || !scope.ReadAreaGaps[0].Coverage.Complete {
		t.Fatal("unsupported Git pin falsely negated current metadata acquisition")
	}
}
