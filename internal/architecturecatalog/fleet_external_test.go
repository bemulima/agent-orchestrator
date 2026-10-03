package architecturecatalog

import (
	"context"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"strings"
	"testing"
)

func TestPinnedRelationsDoNotInventConsumersAndRetainPartialUnknown(t *testing.T) {
	source := catalogSource("owner", "owner", serviceManifest("owner", nil, nil), nil)
	source.PinnedDeclarations = true
	source.ServiceManifest.ProducedContracts = []domain.ArchitectureContractReference{{Code: "owner.api", Transport: "http", Description: statementWithEvidence("Provider", "README.md")}}
	source.ServiceManifest.ConsumedContracts = []domain.ArchitectureContractReference{{Code: "opaque-unknown-provider", Transport: "http", Description: statementWithEvidence("Interface descriptor", "README.md")}}
	source.ServiceManifest.SubscribedEvents = []domain.ArchitectureContractReference{{Code: "opaque-unknown-publisher", Transport: "nats", Description: statementWithEvidence("Interface descriptor", "README.md")}}
	if CountUnmatchedInterfaceMetadata([]domain.ArchitectureCatalogSource{source}) != 3 {
		t.Fatal("missing opaque metadata count")
	}
	source.ServiceManifest.OutboundDependencies = []domain.ArchitectureExternalInteraction{{ID: "real", Transport: "http", Target: "unknown", Direction: "outbound", Description: statementWithEvidence("Real unresolved dependency", "README.md")}}
	catalog, err := (Builder{}).Build(context.Background(), []domain.ArchitectureCatalogSource{source})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Platform.Relations) != 1 || catalog.Platform.Relations[0].ExternalTarget != "unknown" {
		t.Fatalf("invented or erased relation: %#v", catalog.Platform.Relations)
	}
	marker := domain.ArchitectureExternalInteraction{ID: "unknown", Transport: "unknown", Target: "unknown", Direction: "unknown", Description: domain.ArchitectureStatement{Value: "unknown"}}
	if !IsUnassertedInteraction(marker) {
		t.Fatal("marker not recognized")
	}
	marker.Target = "real"
	if IsUnassertedInteraction(marker) {
		t.Fatal("named unresolved dependency erased")
	}
}
func TestExternalResolutionAllowsReviewedLiteralOnly(t *testing.T) {
	relation := domain.ArchitectureCatalogRelation{Type: domain.ArchitectureCatalogRelationOutboundDependency, SourceReferenceID: "source", ExternalTarget: "PostgreSQL", Transport: "postgres", Direction: "outbound", Confidence: 0.9, Evidence: []domain.ArchitectureEvidence{{SourcePath: "main.go"}}}
	relation.EdgeID = StableEdgeID("source", string(relation.Type), "", relation.ExternalTarget, "", relation.Transport, "", relation.Direction)
	pin := domain.ArchitectureGraphPin{SourceIdentity: "git:github.com/example/owner", CommitSHA: strings.Repeat("a", 40), Path: "service.yaml", BlobOID: strings.Repeat("b", 40), ContentSHA256: strings.Repeat("c", 64)}
	makeGraph := func() domain.ArchitectureGraph {
		return domain.ArchitectureGraph{Edges: []domain.ArchitectureGraphEdge{{EdgeID: relation.EdgeID, Relation: relation.Type, SourceReferenceID: "source", ExternalTarget: relation.ExternalTarget, Transport: relation.Transport, Direction: relation.Direction, DeclarationPins: []domain.ArchitectureGraphPin{pin}}}, Diagnostics: []domain.ArchitectureGraphDiagnostic{{Severity: "BLOCKED", Code: "EDGE_TARGET_UNRESOLVED", EdgeID: relation.EdgeID}}}
	}
	graph := makeGraph()
	ResolveFleetExternals(&graph, domain.ArchitectureCatalog{Platform: domain.ArchitectureCatalogPlatform{Relations: []domain.ArchitectureCatalogRelation{relation}}})
	if len(graph.References) != 1 || len(graph.Diagnostics) != 0 || graph.Edges[0].TargetReferenceID == "" {
		t.Fatal("known resource not resolved")
	}
	relation.ExternalTarget = "PostgreSQL-typo"
	graph = makeGraph()
	ResolveFleetExternals(&graph, domain.ArchitectureCatalog{Platform: domain.ArchitectureCatalogPlatform{Relations: []domain.ArchitectureCatalogRelation{relation}}})
	if len(graph.References) != 0 || len(graph.Diagnostics) != 1 {
		t.Fatal("unknown target promoted")
	}
}
