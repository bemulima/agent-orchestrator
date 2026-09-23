package architecturepresentation

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestPlatformUsesOnlyExplicitAndDeterministicClassification(t *testing.T) {
	catalog := presentationCatalog()
	rules := DefaultClassificationRules()
	rules.Explicit["course"] = ExplicitClassification{
		Kind: ServiceClassificationBusiness, DomainID: "learning", DomainName: "Learning",
		Reason: statement("Owner classified this product service", 0.95, []domain.ArchitectureEvidence{{SourcePath: ".ai/architecture/service.yaml", Symbol: "classification"}}),
	}
	projector := NewProjector(rules)

	business, err := projector.Platform(context.Background(), catalog, PlatformViewBusiness)
	if err != nil {
		t.Fatalf("Platform(business) error = %v", err)
	}
	if names := groupServiceNames(business.Domains); !reflect.DeepEqual(names, []string{"course", "unknown"}) {
		t.Fatalf("business services = %v, want business and unclassified only", names)
	}
	if hasNodeType(business.Graph, NodeTypeStartEnd) || hasOperationNode(business.Graph) {
		t.Fatalf("platform graph must never emit operation nodes: %#v", business.Graph.Nodes)
	}
	if !hasNode(business.Graph, "external:hidden:validator") {
		t.Fatalf("filtered technical counterpart must remain explicit: %#v", business.Graph.Nodes)
	}

	technical, err := projector.Platform(context.Background(), catalog, PlatformViewTechnical)
	if err != nil {
		t.Fatalf("Platform(technical) error = %v", err)
	}
	if names := groupServiceNames(technical.Domains); !reflect.DeepEqual(names, []string{"validator", "unknown"}) {
		t.Fatalf("technical services = %v, want technical and unclassified only", names)
	}
	unknown := findSummary(technical.Domains, "unknown")
	if unknown.Classification.Kind != ServiceClassificationUnclassified || unknown.Classification.Source != ClassificationSourceUnknown || unknown.Classification.Reason.Value != "unknown" {
		t.Fatalf("unproven service must remain unclassified: %#v", unknown.Classification)
	}

	_, err = projector.Platform(context.Background(), catalog, "semantic")
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("invalid mode error = %v, want validation", err)
	}
}

func TestClassifierDoesNotInferBusinessFromPurposeOrCapabilities(t *testing.T) {
	service := presentationCatalog().Platform.Services[0]
	service.Manifest.Purpose = statement("Owns student learning journey", 1, []domain.ArchitectureEvidence{{SourcePath: "purpose.go"}})
	service.Manifest.Capabilities = []domain.ArchitectureStatement{statement("Course delivery", 1, []domain.ArchitectureEvidence{{SourcePath: "course.go"}})}
	classification := NewClassifier(DefaultClassificationRules()).Classify(service)
	if classification.Kind != ServiceClassificationUnclassified || classification.Source != ClassificationSourceUnknown {
		t.Fatalf("purpose/capability must not classify business: %#v", classification)
	}
}

func TestServicePresentsCapabilityAndTransportGroupsWithoutOperationNodes(t *testing.T) {
	projector := NewProjector(DefaultClassificationRules())
	value, err := projector.Service(context.Background(), presentationCatalog(), "course")
	if err != nil {
		t.Fatalf("Service() error = %v", err)
	}
	if groups := groupNames(value.CapabilityGroups); !reflect.DeepEqual(groups, []string{"Ungrouped", "Course API"}) {
		t.Fatalf("capability groups = %v", groups)
	}
	if groups := groupNames(value.TransportGroups); !reflect.DeepEqual(groups, []string{"Event subscriber", "HTTP", "NATS request/reply", "Scheduled", "Worker"}) {
		t.Fatalf("transport groups = %v", groups)
	}
	for _, group := range value.TransportGroups {
		if len(group.Operations) == 0 {
			t.Fatalf("empty transport group: %#v", group)
		}
	}
	if hasOperationNode(value.Graph) {
		t.Fatalf("service graph has an operation node: %#v", value.Graph.Nodes)
	}
	if !hasNodeType(value.Graph, NodeTypeOwnedResource) || !hasNodeType(value.Graph, NodeTypeExternalService) || !hasNodeType(value.Graph, NodeTypeCapabilityGroup) {
		t.Fatalf("service semantic graph is incomplete: %#v", value.Graph.Nodes)
	}

	_, err = projector.Service(context.Background(), presentationCatalog(), "missing")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing service error = %v", err)
	}
}

func TestOperationKeepsUnknownAndBuildsSemanticViews(t *testing.T) {
	projector := NewProjector(DefaultClassificationRules())
	value, err := projector.Operation(context.Background(), presentationCatalog(), "course", "create-course")
	if err != nil {
		t.Fatalf("Operation() error = %v", err)
	}
	if value.BusinessExplanation.Validation.Value != "unknown" || value.BusinessExplanation.ImportantDecisions[0].Value != "unknown" {
		t.Fatalf("v1 manifest has no typed decisions and must preserve unknown: %#v", value.BusinessExplanation)
	}
	if value.BusinessExplanation.Result.Value != "Course created" || len(value.BusinessExplanation.MainRules) != 1 || len(value.BusinessExplanation.FailureMeaning) != 1 {
		t.Fatalf("business explanation = %#v", value.BusinessExplanation)
	}
	for _, needed := range []NodeType{NodeTypeStartEnd, NodeTypeInputOutput, NodeTypeProcess, NodeTypeDecision, NodeTypeDatastore, NodeTypeExternalService, NodeTypeEventQueue} {
		if !hasNodeType(value.Flow, needed) {
			t.Fatalf("flow misses semantic node %q: %#v", needed, value.Flow.Nodes)
		}
	}
	if !hasEdgeType(value.Flow, EdgeTypeDataWrite) || !hasEdgeType(value.Flow, EdgeTypeEventPublish) || !hasEdgeType(value.Flow, EdgeTypeFailure) {
		t.Fatalf("flow misses semantic edges: %#v", value.Flow.Edges)
	}
	if !hasNodeType(value.Contracts, NodeTypeContract) || len(value.Sequence.Nodes) < 2 || len(value.Evidence) != 1 {
		t.Fatalf("operation views incomplete: contracts=%#v sequence=%#v evidence=%#v", value.Contracts, value.Sequence, value.Evidence)
	}

	value.Operation.BusinessRules[0].Value = "mutated"
	again, err := projector.Operation(context.Background(), presentationCatalog(), "course", "create-course")
	if err != nil || again.Operation.BusinessRules[0].Value != "course owner may create it" {
		t.Fatalf("operation projection must not share common mutable fields: %#v, %v", again, err)
	}

	_, err = projector.Operation(context.Background(), presentationCatalog(), "course", "missing")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing operation error = %v", err)
	}
}

func TestProjectorRequiresCurrentCatalogAndHonorsCancellation(t *testing.T) {
	projector := NewProjector(DefaultClassificationRules())
	catalog := presentationCatalog()
	catalog.Mode = "TARGET"
	_, err := projector.Platform(context.Background(), catalog, PlatformViewBusiness)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("non-current platform error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = projector.Platform(ctx, presentationCatalog(), PlatformViewBusiness)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled platform error = %v", err)
	}
}

func presentationCatalog() domain.ArchitectureCatalog {
	evidence := []domain.ArchitectureEvidence{{SourcePath: "internal/course/create.go", Symbol: "CreateCourse", StartLine: 10, EndLine: 20}}
	create := domain.ArchitectureOperationManifest{
		Schema: domain.ArchitectureManifestSchemaV1, Kind: "operation", ID: "create-course", ServiceID: "course-service", Type: domain.ArchitectureOperationHTTP,
		Identity:             domain.ArchitectureOperationIdentity{Transport: "http", HTTP: &domain.ArchitectureHTTPIdentity{Method: "POST", Path: "/courses"}},
		Trigger:              domain.ArchitectureOperationTrigger{Description: statement("Invoked when a teacher creates a course", 0.9, evidence)},
		Input:                domain.ArchitectureOperationInput{Body: &domain.ArchitectureSchemaRef{Name: "CreateCourse", Description: statement("Course creation payload", 0.9, evidence)}},
		Access:               domain.ArchitectureOperationAccess{Authentication: statement("Authenticated teacher", 0.9, evidence), Authorization: statement("Teacher owns the course", 0.9, evidence)},
		BusinessTask:         statement("Create a course", 0.9, evidence),
		BusinessProcess:      []domain.ArchitectureOperationStep{{ID: "create", Description: statement("Create course aggregate", 0.9, evidence)}},
		BusinessRules:        []domain.ArchitectureStatement{statement("course owner may create it", 0.9, evidence)},
		Implementation:       domain.ArchitectureImplementationFlow{Handler: evidence, UseCases: evidence, Repositories: evidence},
		DataAccess:           []domain.ArchitectureDataAccess{{Resource: "courses", Access: "write", Description: statement("Persist course", 0.9, evidence)}},
		ExternalInteractions: []domain.ArchitectureExternalInteraction{{ID: "image", Transport: "http", Target: "image-service", Contract: "image.create.v1", Direction: "outbound", Description: statement("Request image processing", 0.9, evidence)}},
		SideEffects:          []domain.ArchitectureSideEffect{{Type: "audit", Description: statement("Record audit entry", 0.9, evidence)}},
		Output:               domain.ArchitectureOperationOutput{Result: &domain.ArchitectureSchemaRef{Name: "Course", Description: statement("Course created", 0.9, evidence)}, EmittedEvents: []domain.ArchitectureContractReference{{Transport: "nats", Code: "course.created.v1", Description: statement("Publish course created", 0.9, evidence)}}},
		Errors:               []domain.ArchitectureOperationError{{Code: "forbidden", StatusCode: 403, Description: statement("Teacher is not allowed to create course", 0.9, evidence)}},
		Evidence:             evidence, Confidence: 0.9,
	}
	operations := []domain.ArchitectureCatalogOperation{{Manifest: create}, {Manifest: operation("nats-request", "course-service", domain.ArchitectureOperationNATSRequestReply)}, {Manifest: operation("event-consume", "course-service", domain.ArchitectureOperationNATSEventSubscriber)}, {Manifest: operation("worker", "course-service", domain.ArchitectureOperationWorker)}, {Manifest: operation("scheduled", "course-service", domain.ArchitectureOperationScheduled)}}
	courseManifest := &domain.ArchitectureServiceManifest{Schema: domain.ArchitectureManifestSchemaV1, Kind: "service", ID: "course-service", Identity: domain.ArchitectureServiceIdentity{Name: "course", Kind: "backend_service"}, Purpose: statement("Own course lifecycle", 0.9, evidence), Responsibilities: []domain.ArchitectureStatement{statement("Own courses", 0.9, evidence)}, Capabilities: []domain.ArchitectureStatement{statement("Course management", 0.9, evidence)}, OwnedResources: []domain.ArchitectureOwnedResource{{Type: "postgres", Name: "courses", Description: statement("Course table", 0.9, evidence)}}, OutboundDependencies: []domain.ArchitectureExternalInteraction{{ID: "validator", Transport: "http", Target: "validator", Direction: "outbound", Description: statement("Validate source", 0.9, evidence)}}, EndpointGroups: []domain.ArchitectureEndpointGroup{{ID: "course-api", Name: "Course API", Description: statement("Course-facing HTTP operations", 0.9, evidence), Operations: []string{"create-course"}}}, Evidence: evidence, Confidence: 0.9}
	validatorManifest := &domain.ArchitectureServiceManifest{Schema: domain.ArchitectureManifestSchemaV1, Kind: "service", ID: "validator-service", Identity: domain.ArchitectureServiceIdentity{Name: "validator", Kind: "validator"}, Purpose: statement("Validate source", 0.9, evidence), Evidence: evidence, Confidence: 0.9}
	unknownManifest := &domain.ArchitectureServiceManifest{Schema: domain.ArchitectureManifestSchemaV1, Kind: "service", ID: "unknown-service", Identity: domain.ArchitectureServiceIdentity{Name: "unknown", Kind: "backend_service"}, Evidence: evidence, Confidence: 0.9}
	return domain.ArchitectureCatalog{Mode: domain.ArchitectureCatalogModeCurrent, Fingerprint: "current-fingerprint", Platform: domain.ArchitectureCatalogPlatform{Services: []domain.ArchitectureCatalogService{
		{Source: domain.ArchitectureCatalogSourceStatus{ProjectID: "course", ProjectName: "course"}, Covered: true, Manifest: courseManifest, Groups: []domain.ArchitectureCatalogEndpointGroup{{ID: "course-api", Name: "Course API", Description: statement("Course-facing HTTP operations", 0.9, evidence), Operations: operations[:1]}}, Ungrouped: operations[1:]},
		{Source: domain.ArchitectureCatalogSourceStatus{ProjectID: "validator", ProjectName: "validator"}, Covered: true, Manifest: validatorManifest},
		{Source: domain.ArchitectureCatalogSourceStatus{ProjectID: "unknown", ProjectName: "unknown"}, Covered: true, Manifest: unknownManifest},
	}, Relations: []domain.ArchitectureCatalogRelation{{ID: "course-validator", Type: domain.ArchitectureCatalogRelationOutboundDependency, SourceProjectID: "course", TargetProjectID: "validator", Transport: "http", Direction: "outbound", Description: statement("Validate source", 0.9, evidence), Evidence: evidence, Confidence: 0.9}}}}
}

func operation(id, serviceID string, kind domain.ArchitectureOperationType) domain.ArchitectureOperationManifest {
	value := domain.ArchitectureOperationManifest{Schema: domain.ArchitectureManifestSchemaV1, Kind: "operation", ID: id, ServiceID: serviceID, Type: kind, BusinessTask: unknownStatement("not material")}
	switch kind {
	case domain.ArchitectureOperationNATSRequestReply:
		value.Identity = domain.ArchitectureOperationIdentity{Transport: "nats", NATS: &domain.ArchitectureNATSIdentity{Subject: id + ".v1", Role: "handler"}}
	case domain.ArchitectureOperationNATSEventSubscriber:
		value.Identity = domain.ArchitectureOperationIdentity{Transport: "nats", NATS: &domain.ArchitectureNATSIdentity{Subject: id + ".v1", Role: "subscriber"}}
	case domain.ArchitectureOperationWorker:
		value.Identity = domain.ArchitectureOperationIdentity{Transport: "worker", Worker: &domain.ArchitectureWorkerIdentity{Name: id}}
	case domain.ArchitectureOperationScheduled:
		value.Identity = domain.ArchitectureOperationIdentity{Transport: "scheduled", Scheduled: &domain.ArchitectureScheduledIdentity{Name: id, Schedule: "@hourly"}}
	}
	return value
}

func groupServiceNames(groups []PlatformDomainGroup) []string {
	var result []string
	for _, group := range groups {
		for _, service := range group.Services {
			result = append(result, service.ProjectID)
		}
	}
	return result
}
func findSummary(groups []PlatformDomainGroup, id string) ServiceSummary {
	for _, group := range groups {
		for _, service := range group.Services {
			if service.ProjectID == id {
				return service
			}
		}
	}
	return ServiceSummary{}
}
func groupNames(groups []OperationGroup) []string {
	result := make([]string, len(groups))
	for index, group := range groups {
		result[index] = group.Name
	}
	return result
}
func hasNode(graph Diagram, id string) bool {
	for _, node := range graph.Nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}
func hasNodeType(graph Diagram, kind NodeType) bool {
	for _, node := range graph.Nodes {
		if node.Type == kind {
			return true
		}
	}
	return false
}
func hasEdgeType(graph Diagram, kind EdgeType) bool {
	for _, edge := range graph.Edges {
		if edge.Type == kind {
			return true
		}
	}
	return false
}
func hasOperationNode(graph Diagram) bool {
	for _, node := range graph.Nodes {
		if node.OperationID != "" {
			return true
		}
	}
	return false
}
