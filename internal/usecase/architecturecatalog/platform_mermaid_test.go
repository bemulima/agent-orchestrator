package architecturecatalog

import (
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestRenderPlatformMermaidIsDeterministicAndShowsUnknownCounterparts(t *testing.T) {
	catalog := domain.ArchitectureCatalog{Platform: domain.ArchitectureCatalogPlatform{
		Services: []domain.ArchitectureCatalogService{
			{Source: domain.ArchitectureCatalogSourceStatus{ProjectID: "consumer", ProjectName: "consumer"}, Covered: true, Manifest: &domain.ArchitectureServiceManifest{Identity: domain.ArchitectureServiceIdentity{Name: "consumer"}}},
			{Source: domain.ArchitectureCatalogSourceStatus{ProjectID: "provider", ProjectName: "provider"}, Covered: true, Manifest: &domain.ArchitectureServiceManifest{Identity: domain.ArchitectureServiceIdentity{Name: "provider"}}},
		},
		Relations: []domain.ArchitectureCatalogRelation{
			{ID: "z", Type: domain.ArchitectureCatalogRelationOperationInteraction, SourceProjectID: "consumer", ExternalTarget: "unknown provider \"v2\"", Transport: "http", Direction: "outbound"},
			{ID: "a", Type: domain.ArchitectureCatalogRelationContract, SourceProjectID: "consumer", TargetProjectID: "provider", Transport: "http", Contract: "course.contract", Direction: "inbound"},
		},
	}}
	first := RenderPlatformMermaid(catalog)
	catalog.Platform.Services[0], catalog.Platform.Services[1] = catalog.Platform.Services[1], catalog.Platform.Services[0]
	catalog.Platform.Relations[0], catalog.Platform.Relations[1] = catalog.Platform.Relations[1], catalog.Platform.Relations[0]
	second := RenderPlatformMermaid(catalog)
	if first != second {
		t.Fatalf("renderer varies with input ordering:\n%s\n---\n%s", first, second)
	}
	for _, want := range []string{
		"%% GENERATED — READ-ONLY CURRENT PRESENTATION",
		"service: consumer",
		"service: provider",
		"external: unknown provider &quot;v2&quot;",
		"contract · http · course.contract",
	} {
		if !strings.Contains(first, want) {
			t.Fatalf("Mermaid missing %q:\n%s", want, first)
		}
	}
	// A consumed contract is rendered provider -> consumer, rather than losing
	// the manifest's inbound direction in the global graph.
	if !strings.Contains(first, "service_1 -->|contract · http · course.contract| service_0") {
		t.Fatalf("inbound edge direction was not preserved:\n%s", first)
	}
}
