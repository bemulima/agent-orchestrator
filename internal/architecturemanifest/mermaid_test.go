package architecturemanifest

import (
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestServiceMermaidIncludesManifestRelationsAndIsDeterministic(t *testing.T) {
	m := domain.ArchitectureServiceManifest{
		ID: "orders", Identity: domain.ArchitectureServiceIdentity{Name: "orders-api"},
		Purpose:              domain.ArchitectureStatement{Value: "Accept orders"},
		Responsibilities:     []domain.ArchitectureStatement{{Value: "Order lifecycle"}},
		Capabilities:         []domain.ArchitectureStatement{{Value: "Checkout"}},
		OwnedResources:       []domain.ArchitectureOwnedResource{{Type: "table", Name: "orders"}},
		InboundInterfaces:    []domain.ArchitectureInterface{{Transport: "http", Name: "public API"}},
		OutboundDependencies: []domain.ArchitectureExternalInteraction{{Transport: "nats", Target: "payments", Direction: "request"}},
		EndpointGroups:       []domain.ArchitectureEndpointGroup{{ID: "orders", Name: "Order endpoints", Operations: []string{"orders.create"}}},
		ProducedContracts:    []domain.ArchitectureContractReference{{Transport: "http", Code: "OrderCreated.v1"}},
		ConsumedContracts:    []domain.ArchitectureContractReference{{Transport: "nats", Code: "PaymentApproved.v1"}},
	}
	ops := []domain.ArchitectureOperationManifest{{ID: "orders.create", Type: domain.ArchitectureOperationHTTP}}
	first, second := ServiceMermaid(m, ops), ServiceMermaid(m, ops)
	if first != second {
		t.Fatal("ServiceMermaid is not deterministic")
	}
	for _, want := range []string{"GENERATED", "READ-ONLY", "orders-api", "purpose: Accept orders", "responsibilities", "capabilities", "owned resource", "inbound http", "outbound nats", "endpoint group", "produced contract", "consumed contract", "orders.create"} {
		if !strings.Contains(first, want) {
			t.Errorf("ServiceMermaid missing %q:\n%s", want, first)
		}
	}
}

func TestOperationMermaidIncludesAllEvidenceBackedSectionsAndUnknown(t *testing.T) {
	m := domain.ArchitectureOperationManifest{
		ID: "orders.create", Type: domain.ArchitectureOperationHTTP,
		Identity:             domain.ArchitectureOperationIdentity{Transport: "http", HTTP: &domain.ArchitectureHTTPIdentity{Method: "POST", Path: "/orders"}},
		Access:               domain.ArchitectureOperationAccess{Audience: domain.ArchitectureStatement{Value: "internal"}},
		Input:                domain.ArchitectureOperationInput{Body: &domain.ArchitectureSchemaRef{Name: "CreateOrder"}},
		BusinessTask:         domain.ArchitectureStatement{Value: "Create an order"},
		BusinessProcess:      []domain.ArchitectureOperationStep{{ID: "reserve", Description: domain.ArchitectureStatement{Value: "Reserve stock"}}},
		BusinessRules:        []domain.ArchitectureStatement{{Value: "Stock must be available"}},
		DataAccess:           []domain.ArchitectureDataAccess{{Resource: "orders", Access: "write"}},
		ExternalInteractions: []domain.ArchitectureExternalInteraction{{Transport: "nats", Target: "payments"}},
		SideEffects:          []domain.ArchitectureSideEffect{{Type: "event", Description: domain.ArchitectureStatement{Value: "Publish created"}}},
		Output:               domain.ArchitectureOperationOutput{Responses: []domain.ArchitectureResponse{{StatusCode: 201}}},
		Errors:               []domain.ArchitectureOperationError{{Code: "invalid", StatusCode: 400}},
	}
	out := OperationMermaid(m)
	for _, want := range []string{"READ-ONLY", "orders.create", "POST", "/orders", "identity", "input", "CreateOrder", "access", "unknown", "business task", "business process", "business rule", "implementation", "data access", "external interactions", "side effects", "output", "errors", "evidence", "confidence"} {
		if !strings.Contains(out, want) {
			t.Errorf("OperationMermaid missing %q:\n%s", want, out)
		}
	}
}
