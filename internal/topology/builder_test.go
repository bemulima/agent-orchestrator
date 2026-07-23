package topology

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestBuilderBuildsDeterministicServiceTopologyAndDrift(t *testing.T) {
	sources := []domain.TopologySource{
		topologySource("orders-id", "orders", domain.RepositoryRoleService, domain.ServiceKindBackendService,
			fact("capability", "http_route", "GET /api/v1/orders", "internal/routes.go"),
			fact("contract", "http_produce", "GET /api/v1/orders", "internal/routes.go"),
			fact("contract", "event_publish", "orders.created.v1", "internal/events.go"),
			fact("ownership", "database_table", "orders", "db/001.sql")),
		topologySource("admin-id", "admin-nextjs", domain.RepositoryRoleFrontend, domain.ServiceKindFrontendApplication,
			fact("contract", "http_consume", "GET /api/v2/orders", "src/client.ts"),
			fact("contract", "event_subscribe", "orders.created.v2", "src/events.ts")),
		topologySource("gateway-id", "gateway", domain.RepositoryRoleService, domain.ServiceKindGateway,
			fact("relation", "gateway_routes_to", "http://orders:8080", "nginx.conf")),
		topologySource("infra-id", "infrastructure", domain.RepositoryRoleInfrastructure, domain.ServiceKindInfrastructure,
			fact("relation", "depends_on", "orders", "docker-compose.yml")),
		topologySource("policy-id", "prompts", domain.RepositoryRolePolicy, domain.ServiceKindUnknown,
			fact("capability", "http_route", "GET /api/v1/should-not-exist", "README.md")),
	}

	catalog, err := (Builder{}).Build(context.Background(), sources)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if catalog.Revision.ProjectCount != 5 || catalog.Revision.ServiceCount != 4 {
		t.Fatalf("revision counts = %#v", catalog.Revision)
	}
	if len(catalog.Relations) != 4 {
		t.Fatalf("relations = %#v, want HTTP, event, gateway, and infrastructure relations", catalog.Relations)
	}
	if len(catalog.Drifts) != 2 {
		t.Fatalf("drifts = %#v, want HTTP and event version drift", catalog.Drifts)
	}
	for _, drift := range catalog.Drifts {
		if drift.Severity != domain.DriftSeverityError || drift.ProducerVersion != "v1" || drift.ConsumerVersion != "v2" {
			t.Fatalf("drift = %#v", drift)
		}
	}
	for _, service := range catalog.Services {
		if service.ProjectID == "policy-id" {
			t.Fatal("policy repository became a topology service")
		}
	}
	reversed := append([]domain.TopologySource(nil), sources...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	rebuilt, err := (Builder{}).Build(context.Background(), reversed)
	if err != nil {
		t.Fatalf("reordered Build() error = %v", err)
	}
	if rebuilt.Revision.Fingerprint != catalog.Revision.Fingerprint {
		t.Fatalf("fingerprints differ: %q != %q", rebuilt.Revision.Fingerprint, catalog.Revision.Fingerprint)
	}
}

func TestBuilderReportsMissingProducerAndRejectsMismatchedSource(t *testing.T) {
	source := topologySource("frontend-id", "nextjs", domain.RepositoryRoleFrontend, domain.ServiceKindFrontendApplication,
		fact("contract", "http_consume", "GET /api/v1/courses", "src/client.ts"))
	catalog, err := (Builder{}).Build(context.Background(), []domain.TopologySource{source})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(catalog.Drifts) != 1 || catalog.Drifts[0].Severity != domain.DriftSeverityCritical || catalog.Drifts[0].ProducerProjectID != nil {
		t.Fatalf("missing producer drift = %#v", catalog.Drifts)
	}

	source.Report.CommitSHA = "other"
	if _, err := (Builder{}).Build(context.Background(), []domain.TopologySource{source}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("mismatched source error = %v, want conflict", err)
	}
}

func TestBuilderIncludesApprovedSemanticCapabilitiesAndRelations(t *testing.T) {
	sources := []domain.TopologySource{
		topologySource("lessons-id", "ms-go-course", domain.RepositoryRoleService, domain.ServiceKindBackendService,
			fact("business_rule", "publish_reviewed_only", "Only reviewed lessons can be published", ".ai/discovery/semantic-report.json"),
			fact("relation", "authenticates_through", "ms-go-auth", ".ai/discovery/semantic-report.json")),
		topologySource("auth-id", "ms-go-auth", domain.RepositoryRoleService, domain.ServiceKindBackendService),
	}
	catalog, err := (Builder{}).Build(context.Background(), sources)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(catalog.Capabilities) != 1 || catalog.Capabilities[0].Name != "Only reviewed lessons can be published" {
		t.Fatalf("semantic capabilities = %#v", catalog.Capabilities)
	}
	if len(catalog.Relations) != 1 || catalog.Relations[0].RelationType != domain.RelationAuthenticatesThrough ||
		catalog.Relations[0].TargetProjectID != "auth-id" {
		t.Fatalf("semantic relations = %#v", catalog.Relations)
	}
}

func TestSortCatalogUsesTotalOrdering(t *testing.T) {
	firstContractCode := "http:get:/items/{version}"
	secondContractCode := "http:get:/status/{version}"
	first := domain.TopologyCatalog{
		Ownership: []domain.ServiceOwnership{
			{ProjectID: "service-id", ResourceType: "table", ResourceName: "items", Source: "b.sql"},
			{ProjectID: "service-id", ResourceType: "table", ResourceName: "items", Source: "a.sql"},
		},
		Contracts: []domain.Contract{
			{ProjectID: "service-id", Code: firstContractCode, Type: domain.ContractTypeHTTP, Version: "v2", Direction: domain.ContractDirectionProvides},
			{ProjectID: "service-id", Code: firstContractCode, Type: domain.ContractTypeHTTP, Version: "v1", Direction: domain.ContractDirectionProvides},
		},
		Relations: []domain.ServiceRelation{
			{SourceProjectID: "consumer-id", TargetProjectID: "service-id", RelationType: domain.RelationConsumes, ContractCode: &secondContractCode, Source: "b.ts"},
			{SourceProjectID: "consumer-id", TargetProjectID: "service-id", RelationType: domain.RelationConsumes, ContractCode: &firstContractCode, Source: "a.ts"},
		},
	}
	second := domain.TopologyCatalog{
		Ownership: append([]domain.ServiceOwnership(nil), first.Ownership[1], first.Ownership[0]),
		Contracts: append([]domain.Contract(nil), first.Contracts[1], first.Contracts[0]),
		Relations: append([]domain.ServiceRelation(nil), first.Relations[1], first.Relations[0]),
	}

	sortCatalog(&first)
	sortCatalog(&second)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("equivalent catalogs sorted differently:\nfirst:  %#v\nsecond: %#v", first, second)
	}
}

func TestBuilderRejectsDescriptiveContractValues(t *testing.T) {
	source := topologySource("service-id", "service", domain.RepositoryRoleService, domain.ServiceKindBackendService,
		fact("contract", "http_produce", "ANY /health", "router.go"),
		fact("contract", "http_produce", "GET /health returns status", ".ai/discovery/semantic-report.json"),
		fact("contract", "event_publish", "orders.created.v1", "events.go"),
		fact("contract", "event_publish", "created event: id, timestamp", ".ai/discovery/semantic-report.json"))

	catalog, err := (Builder{}).Build(context.Background(), []domain.TopologySource{source})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(catalog.Contracts) != 2 {
		t.Fatalf("contracts = %#v, want only the shaped HTTP and event contracts", catalog.Contracts)
	}
	codes := map[string]bool{}
	for _, contract := range catalog.Contracts {
		codes[contract.Code] = true
	}
	if !codes["http:any:/health"] || !codes["event:orders.created.{version}"] {
		t.Fatalf("contract codes = %#v", codes)
	}
}

func topologySource(id, name string, role domain.RepositoryRole, kind domain.ServiceKind, facts ...domain.Evidence) domain.TopologySource {
	return domain.TopologySource{
		Project: domain.Project{ID: id, Name: name, RepositoryRole: role},
		Snapshot: domain.ServiceSnapshot{ID: id + "-snapshot", ProjectID: id, CommitSHA: "commit", ServiceKind: kind,
			Purpose: name + " purpose"},
		Report: domain.DiscoveryReport{ProjectID: id, ProjectName: name, RepositoryRole: role, CommitSHA: "commit", Facts: facts},
	}
}

func fact(category, name, value, path string) domain.Evidence {
	return domain.Evidence{Category: category, Name: name, Value: value, SourcePath: path, Confidence: .9, Explanation: "fixture"}
}
