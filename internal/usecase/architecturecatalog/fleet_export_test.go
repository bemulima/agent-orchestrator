package architecturecatalog

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	projection "github.com/bemulima/agent-orchestrator/internal/architecturecatalog"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"strings"
	"testing"
)

type fleetTestReader struct {
	bytes map[string][]byte
	drift bool
}

func (r fleetTestReader) Read(_ context.Context, root, identity, commit, file string) (domain.ArchitectureGraphPin, []byte, error) {
	raw := r.bytes[identity]
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
