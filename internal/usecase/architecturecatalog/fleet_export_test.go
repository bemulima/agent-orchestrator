package architecturecatalog

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	projection "github.com/bemulima/agent-orchestrator/internal/architecturecatalog"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"sort"
	"strings"
	"testing"
)

type fleetTestReader struct {
	bytes map[string][]byte
	drift bool
}

func (r fleetTestReader) Read(_ context.Context, root, identity, commit, file string) (domain.ArchitectureGraphPin, []byte, error) {
	raw := r.bytes[identity]
	if declaration, ok := r.bytes[identity+"\x00"+file]; ok {
		raw = declaration
	}
	sum := sha256.Sum256(raw)
	oid := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(raw))), raw...))
	pin := domain.ArchitectureGraphPin{SourceIdentity: identity, CommitSHA: commit, Path: file, BlobOID: hex.EncodeToString(oid[:]), ContentSHA256: hex.EncodeToString(sum[:])}
	if r.drift {
		pin.BlobOID = strings.Repeat("f", 40)
	}
	return pin, raw, nil
}
func TestFleetExportPinnedDeterministicAndMismatch(t *testing.T) {
	fleet := domain.ArchitectureFleetInputs{SchemaVersion: domain.ArchitectureFleetInputsSchemaV1}
	reader := fleetTestReader{bytes: map[string][]byte{}}
	roots := map[string]string{}
	for i := 0; i < 42; i++ {
		id := fmt.Sprintf("service-%02d", i)
		repo := "example/" + id
		identity := "git:github.com/" + repo
		reader.bytes[identity] = []byte(fmt.Sprintf("schema: architecture/v1\nkind: service\nid: %s\nmanifest_revision: 1\nidentity: {name: %s, kind: backend_service}\npurpose: {value: Test service, confidence: 0.9, evidence: [{source_path: README.md}]}\nevidence: [{source_path: README.md}]\nconfidence: 0.9\n", id, id))
		roots[identity] = "ignored-fixture"
		pin, _, _ := reader.Read(context.Background(), "", identity, strings.Repeat("a", 40), ".ai/architecture/service.yaml")
		fleet.Repositories = append(fleet.Repositories, domain.ArchitectureFleetRepository{RepositoryID: repo, SourceIdentity: identity, RemoteURL: "https://github.com/" + repo + ".git", CommitSHA: pin.CommitSHA, Profile: "GO_SERVICE", ServiceID: id, RepositoryRole: domain.RepositoryRoleService, Declarations: []domain.ArchitectureFleetDeclaration{{Path: pin.Path, BlobOID: pin.BlobOID, ContentSHA256: pin.ContentSHA256}}})
	}
	raw, _ := json.Marshal(fleet)
	uc := FleetExport{Inputs: raw, Roots: roots, Resolver: reader, Producer: domain.ArchitectureGraphProducer{RepositoryID: "example/producer", CommitSHA: strings.Repeat("b", 40)}}
	graph, err := uc.Handle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.References) != 42 || len(graph.Diagnostics) != 0 || graph.FleetInputs == nil {
		t.Fatalf("incomplete locked graph: %#v", graph.Diagnostics)
	}
	repeat, err := uc.Handle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := projection.CanonicalGraphJSON(graph)
	b, _ := projection.CanonicalGraphJSON(repeat)
	if string(a) != string(b) {
		t.Fatal("nondeterministic fleet graph")
	}
	identities := []string{}
	for _, repo := range fleet.Repositories {
		identities = append(identities, repo.SourceIdentity)
	}
	inventoryRaw, _ := json.Marshal(map[string]any{"schema_version": "architecture-graph-inventory.v1", "source_identities": identities})
	reader.bytes["git:github.com/example/producer"] = inventoryRaw
	uc.Inventory = &InventoryRequest{Root: "ignored-fixture", SourceIdentity: "git:github.com/example/producer", CommitSHA: uc.Producer.CommitSHA, Path: ".ai/architecture/fleet-inventory.v1.json"}
	inventoryGraph, err := uc.Handle(context.Background())
	if err != nil || inventoryGraph.Inventory == nil || len(inventoryGraph.Diagnostics) != 0 {
		t.Fatalf("committed inventory: %v", err)
	}
	reader.bytes["git:github.com/example/producer"] = []byte(`{"schema_version":"architecture-graph-inventory.v1","source_identities":["git:github.com/example/wrong"]}`)
	mismatch, err := uc.Handle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range mismatch.Diagnostics {
		if d.Code == "FLEET_INVENTORY_SCOPE_MISMATCH" {
			found = true
		}
	}
	if !found {
		t.Fatal("mismatched committed inventory not blocked")
	}
	uc.Inventory = nil
	reader.drift = true
	uc.Resolver = reader
	if _, err = uc.Handle(context.Background()); err == nil {
		t.Fatal("mismatched blob accepted")
	}
}

func operationFleetFixture(t *testing.T, relative string) (domain.ArchitectureFleetInputs, fleetTestReader, map[string]string) {
	t.Helper()
	fleet := domain.ArchitectureFleetInputs{SchemaVersion: domain.ArchitectureFleetInputsSchemaV1}
	reader := fleetTestReader{bytes: map[string][]byte{}}
	roots := map[string]string{}
	evidence := []domain.ArchitectureEvidence{{SourcePath: "README.md"}}
	unknown := domain.ArchitectureStatement{Value: "unknown", Confidence: 0, Evidence: evidence}
	for i := 0; i < 42; i++ {
		id := fmt.Sprintf("service-%02d", i)
		repo := "example/" + id
		identity := "git:github.com/" + repo
		roots[identity] = "fixture"
		service := domain.ArchitectureServiceManifest{Schema: domain.ArchitectureManifestSchemaV1, Kind: "service", ID: id, ManifestRevision: 1, Identity: domain.ArchitectureServiceIdentity{Name: id, Kind: "backend_service"}, Purpose: unknown, Evidence: evidence, Confidence: 0.9, OperationManifests: []string{relative}}
		operation := domain.ArchitectureOperationManifest{Schema: domain.ArchitectureManifestSchemaV1, Kind: "operation", ID: "health", ServiceID: id, ManifestRevision: 1, Type: domain.ArchitectureOperationHTTP, Identity: domain.ArchitectureOperationIdentity{Transport: "http", HTTP: &domain.ArchitectureHTTPIdentity{Method: "GET", Path: "/health"}}, Access: domain.ArchitectureOperationAccess{Audience: unknown, Authentication: unknown, Authorization: unknown, Idempotency: unknown}, Trigger: domain.ArchitectureOperationTrigger{Description: unknown}, BusinessTask: unknown, Evidence: evidence, Confidence: 0.9}
		raw, _ := json.Marshal(service)
		reader.bytes[identity] = raw
		raw, _ = json.Marshal(operation)
		reader.bytes[identity+"\x00.ai/architecture/"+relative] = raw
		entry := domain.ArchitectureFleetRepository{RepositoryID: repo, SourceIdentity: identity, RemoteURL: "https://github.com/" + repo + ".git", CommitSHA: strings.Repeat("a", 40), Profile: "GO_SERVICE", ServiceID: id, RepositoryRole: domain.RepositoryRoleService}
		for _, file := range []string{".ai/architecture/service.yaml", ".ai/architecture/" + relative} {
			pin, _, _ := reader.Read(context.Background(), "fixture", identity, entry.CommitSHA, file)
			entry.Declarations = append(entry.Declarations, domain.ArchitectureFleetDeclaration{Path: file, BlobOID: pin.BlobOID, ContentSHA256: pin.ContentSHA256})
		}
		sort.Slice(entry.Declarations, func(i, j int) bool { return entry.Declarations[i].Path < entry.Declarations[j].Path })
		fleet.Repositories = append(fleet.Repositories, entry)
	}
	return fleet, reader, roots
}
func exportOperationFleet(fleet domain.ArchitectureFleetInputs, reader fleetTestReader, roots map[string]string) (domain.ArchitectureGraph, error) {
	raw, _ := json.Marshal(fleet)
	return (FleetExport{Inputs: raw, Roots: roots, Resolver: reader, Producer: domain.ArchitectureGraphProducer{RepositoryID: "example/producer", CommitSHA: strings.Repeat("b", 40)}}).Handle(context.Background())
}
func TestFleetExportResolvesDeclaredOperationPaths(t *testing.T) {
	for _, relative := range []string{"endpoints/health.yaml", "operations/health.yml"} {
		t.Run(relative, func(t *testing.T) {
			fleet, reader, roots := operationFleetFixture(t, relative)
			graph, err := exportOperationFleet(fleet, reader, roots)
			if err != nil {
				t.Fatal(err)
			}
			if len(graph.Diagnostics) != 0 || len(graph.References) != 42 || graph.Completeness.ParsedOperationManifestCount != 42 {
				t.Fatalf("incomplete operation graph: %#v", graph.Completeness)
			}
			for _, ref := range graph.References {
				if len(ref.DeclarationPins) != 2 {
					t.Fatal("service/operation bundle missing")
				}
			}
			repeat, err := exportOperationFleet(fleet, reader, roots)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := projection.CanonicalGraphJSON(graph)
			b, _ := projection.CanonicalGraphJSON(repeat)
			if string(a) != string(b) {
				t.Fatal("operation graph nondeterministic")
			}
		})
	}
}
func TestFleetExportRejectsMissingUndeclaredAndEscapedOperations(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		fleet, reader, roots := operationFleetFixture(t, "endpoints/health.yaml")
		fleet.Repositories[0].Declarations = fleet.Repositories[0].Declarations[1:]
		if _, err := exportOperationFleet(fleet, reader, roots); err == nil {
			t.Fatal("missing operation accepted")
		}
	})
	t.Run("undeclared", func(t *testing.T) {
		fleet, reader, roots := operationFleetFixture(t, "endpoints/health.yaml")
		repo := &fleet.Repositories[0]
		file := ".ai/architecture/endpoints/orphan.yaml"
		reader.bytes[repo.SourceIdentity+"\x00"+file] = reader.bytes[repo.SourceIdentity+"\x00.ai/architecture/endpoints/health.yaml"]
		pin, _, _ := reader.Read(context.Background(), "fixture", repo.SourceIdentity, repo.CommitSHA, file)
		repo.Declarations = append(repo.Declarations, domain.ArchitectureFleetDeclaration{Path: file, BlobOID: pin.BlobOID, ContentSHA256: pin.ContentSHA256})
		sort.Slice(repo.Declarations, func(i, j int) bool { return repo.Declarations[i].Path < repo.Declarations[j].Path })
		if _, err := exportOperationFleet(fleet, reader, roots); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("undeclared operation not rejected: %v", err)
		}
	})
	for _, relative := range []string{"/endpoints/health.yaml", "../endpoints/health.yaml", "endpoints/../operations/health.yaml", ".ai/architecture/endpoints/health.yaml", "endpoints/nested/health.yaml"} {
		t.Run(relative, func(t *testing.T) {
			fleet, reader, roots := operationFleetFixture(t, relative)
			if _, err := exportOperationFleet(fleet, reader, roots); err == nil {
				t.Fatal("escaped operation accepted")
			}
		})
	}
}
