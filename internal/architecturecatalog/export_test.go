package architecturecatalog

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func graphFixtureSources() ([]domain.ArchitectureCatalogSource, []domain.ArchitectureGraphPin) {
	consumer := catalogSource("consumer", "consumer", serviceManifest("consumer", nil, nil), nil)
	provider := catalogSource("provider", "provider", serviceManifest("provider", nil, nil), nil)
	for _, tuple := range []struct{ contract, target string }{{"alpha", "provider"}, {"beta", "provider"}, {"gamma", "external-api"}} {
		consumer.ServiceManifest.OutboundDependencies = append(consumer.ServiceManifest.OutboundDependencies, domain.ArchitectureExternalInteraction{Transport: "http", Target: tuple.target, Contract: tuple.contract, Direction: "outbound"})
	}
	sources := []domain.ArchitectureCatalogSource{consumer, provider}
	pins := []domain.ArchitectureGraphPin{}
	for i := range sources {
		source := &sources[i]
		commit := strings.Repeat(string(rune('b'+i)), 40)
		checksum := strings.Repeat(string(rune('d'+i)), 64)
		blob := strings.Repeat("f", 40)
		if i == 1 {
			blob = strings.Repeat("0", 40)
		}
		source.Topology.Project.HeadCommit = commit
		source.Topology.Snapshot.CommitSHA = commit
		source.Topology.Report.CommitSHA = commit
		source.Topology.Report.ArchitectureManifests = []domain.ArchitectureManifestMetadata{{Path: ".ai/architecture/service.yaml", Checksum: checksum, Kind: "service", ID: source.ServiceManifest.ID}}
		pins = append(pins, domain.ArchitectureGraphPin{SourceIdentity: source.Topology.Project.SourceIdentity, CommitSHA: commit, Path: ".ai/architecture/service.yaml", BlobOID: blob, ContentSHA256: checksum})
	}
	return sources, pins
}
func graphFixtureProducer() domain.ArchitectureGraphProducer {
	return domain.ArchitectureGraphProducer{RepositoryID: "bemulima/agent-orchestrator", CommitSHA: strings.Repeat("a", 40)}
}
func buildGraphFixture(t *testing.T, sources []domain.ArchitectureCatalogSource, pins []domain.ArchitectureGraphPin, inventories ...VerifiedInventory) domain.ArchitectureGraph {
	t.Helper()
	catalog, err := (Builder{}).Build(context.Background(), sources)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := Export(catalog, sources, graphFixtureProducer(), pins, nil, inventories...)
	if err != nil {
		t.Fatal(err)
	}
	return graph
}
func TestExportGoldenDeterministicParallelAndUnresolved(t *testing.T) {
	sources, pins := graphFixtureSources()
	graph := buildGraphFixture(t, sources, pins)
	bytes, err := CanonicalGraphJSON(graph)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("testdata/architecture-graph.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes)+"\n" != string(expected) {
		t.Fatalf("portable fixture differs:\n%s", bytes)
	}
	if len(graph.Edges) != 3 {
		t.Fatalf("parallel/unresolved edges dropped: %#v", graph.Edges)
	}
	unresolved := 0
	for _, edge := range graph.Edges {
		if edge.TargetReferenceID == "" {
			unresolved++
			if edge.ExternalTarget != "external-api" {
				t.Fatal("literal unresolved target lost")
			}
		}
	}
	if unresolved != 1 {
		t.Fatal("unexpected unresolved targets")
	}
	// Shuffle input and change every transient database identifier and name.
	for i := range sources {
		source := &sources[i]
		old := source.Topology.Project.ID
		source.Topology.Project.ID = "changed-" + old
		source.Topology.Project.Name = "renamed-" + old
		source.Topology.Snapshot.ID = "new-snapshot-" + old
		source.Topology.Snapshot.ProjectID = source.Topology.Project.ID
		source.Topology.Report.ProjectID = source.Topology.Project.ID
	}
	sources[0], sources[1] = sources[1], sources[0]
	pins[0], pins[1] = pins[1], pins[0]
	again := buildGraphFixture(t, sources, pins)
	againBytes, _ := CanonicalGraphJSON(again)
	if string(bytes) != string(againBytes) {
		t.Fatal("portable bytes changed with input order/database identities")
	}
	semantic := graph
	semantic.ContentSHA256 = ""
	payload, _ := CanonicalGraphJSON(semantic)
	sum := sha256.Sum256(payload)
	if hex.EncodeToString(sum[:]) != graph.ContentSHA256 {
		t.Fatal("semantic digest mismatch")
	}
}
func TestExportDiagnosesMissingAndDriftedPins(t *testing.T) {
	sources, pins := graphFixtureSources()
	sources[0].Topology.Report.IsDirty = true
	pins[1].ContentSHA256 = strings.Repeat("9", 64)
	graph := buildGraphFixture(t, sources, pins[1:])
	codes := map[string]bool{}
	for _, diagnostic := range graph.Diagnostics {
		codes[diagnostic.Code] = true
	}
	for _, code := range []string{"SOURCE_DIRTY", "DECLARATION_PIN_MISSING", "DECLARATION_CONTENT_MISMATCH", "FLEET_SCOPE_UNPROVEN"} {
		if !codes[code] {
			t.Fatalf("missing diagnostic %s", code)
		}
	}
	for _, ref := range graph.References {
		if len(ref.DeclarationPins) != 0 {
			t.Fatal("unproven pin promoted into export")
		}
	}
}
func fixtureInventory(t *testing.T, identities []string) VerifiedInventory {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"schema_version": "architecture-graph-inventory.v1", "source_identities": identities})
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	blob := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(raw))), raw...))
	producer := graphFixtureProducer()
	pin := domain.ArchitectureGraphPin{SourceIdentity: "git:github.com/" + producer.RepositoryID, CommitSHA: producer.CommitSHA, Path: ".ai/architecture/platform-inventory.v1.json", BlobOID: hex.EncodeToString(blob[:]), ContentSHA256: hex.EncodeToString(hash[:])}
	verified, err := VerifyInventory(pin, raw, producer)
	if err != nil {
		t.Fatal(err)
	}
	bad := pin
	bad.CommitSHA = strings.Repeat("8", 40)
	if _, err := VerifyInventory(bad, raw, producer); err == nil {
		t.Fatal("wrong inventory commit accepted")
	}
	if _, err := VerifyInventory(pin, append(raw, ' '), producer); err == nil {
		t.Fatal("changed inventory bytes accepted")
	}
	return verified
}
func TestExportScopeRequiresVerifiedMatchingInventory(t *testing.T) {
	sources, pins := graphFixtureSources()
	inventory := fixtureInventory(t, []string{sources[0].Topology.Project.SourceIdentity, sources[1].Topology.Project.SourceIdentity})
	graph := buildGraphFixture(t, sources, pins, inventory)
	if graph.Inventory == nil {
		t.Fatal("verified matching inventory omitted")
	}
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "FLEET_SCOPE_UNPROVEN" {
			t.Fatal("scope proof ignored")
		}
	}
	mismatching := fixtureInventory(t, []string{sources[0].Topology.Project.SourceIdentity})
	graph = buildGraphFixture(t, sources, pins, mismatching)
	if graph.Inventory != nil {
		t.Fatal("mismatching inventory promoted")
	}
	found := false
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "FLEET_INVENTORY_SCOPE_MISMATCH" {
			found = true
		}
	}
	if !found {
		t.Fatal("inventory mismatch diagnosis absent")
	}
}
func TestCanonicalGraphJSONStrings(t *testing.T) {
	got, err := CanonicalGraphJSON(map[string]any{"z": "<>&é\u2028\u2029\n", "a": `literal \u2028`})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"a\":\"literal \\\\u2028\",\"z\":\"<>&é\u2028\u2029\\n\"}"
	if string(got) != want {
		t.Fatalf("UTF8 canonical strings differ: %q != %q", got, want)
	}
}
func TestExportSchemaRejectsFabricatedFields(t *testing.T) {
	schemaBytes, err := os.ReadFile("../../docs/schemas/architecture-graph.v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("architecture-graph.v1.schema.json", value); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("architecture-graph.v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/architecture-graph.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(document); err != nil {
		t.Fatal(err)
	}
	document["required"] = false
	if err := schema.Validate(document); err == nil {
		t.Fatal("fabricated required flag accepted")
	}
	delete(document, "required")
	sources, pins := graphFixtureSources()
	graph := buildGraphFixture(t, sources, pins, fixtureInventory(t, []string{sources[0].Topology.Project.SourceIdentity, sources[1].Topology.Project.SourceIdentity}))
	raw, _ = CanonicalGraphJSON(graph)
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(document); err != nil {
		t.Fatal(err)
	}
}

func TestExportRejectsInventedConsistentRelation(t *testing.T) {
	sources, pins := graphFixtureSources()
	catalog, err := (Builder{}).Build(context.Background(), sources)
	if err != nil {
		t.Fatal(err)
	}
	relation := &catalog.Platform.Relations[0]
	relation.Contract = "invented-contract"
	relation.EdgeID = StableEdgeID(relation.SourceReferenceID, string(relation.Type), relation.TargetReferenceID, relation.ExternalTarget, relation.OperationID, relation.Transport, relation.Contract, relation.Direction)
	if _, err := Export(catalog, sources, graphFixtureProducer(), pins, nil); err == nil {
		t.Fatal("internally consistent invented relation accepted")
	}
}

func TestVerifyInventoryRejectsDuplicateOrNonexactKeys(t *testing.T) {
	for _, raw := range []string{
		`{"schema_version":"architecture-graph-inventory.v1","schema_version":"architecture-graph-inventory.v1","source_identities":["git:github.com/example/owner"]}`,
		`{"schema_version":"architecture-graph-inventory.v1","source_identities":["git:github.com/example/earlier"],"source_identities":["git:github.com/example/owner"]}`,
		`{"schema_version":"architecture-graph-inventory.v1","schema_\u0076ersion":"architecture-graph-inventory.v1","source_identities":["git:github.com/example/owner"]}`,
		`{"SCHEMA_VERSION":"architecture-graph-inventory.v1","source_identities":["git:github.com/example/owner"]}`,
	} {
		bytes := []byte(raw)
		hash := sha256.Sum256(bytes)
		blob := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(bytes))), bytes...))
		producer := graphFixtureProducer()
		pin := domain.ArchitectureGraphPin{SourceIdentity: "git:github.com/" + producer.RepositoryID, CommitSHA: producer.CommitSHA, Path: "inventory.json", BlobOID: hex.EncodeToString(blob[:]), ContentSHA256: hex.EncodeToString(hash[:])}
		if _, err := VerifyInventory(pin, bytes, producer); err == nil {
			t.Fatalf("ambiguous owner inventory accepted: %s", raw)
		}
	}
}
