package architecturecatalog

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestBuilderBuildsDeterministicCurrentHierarchyAndCompleteness(t *testing.T) {
	covered := catalogSource("teacher", "teacher", serviceManifest("teacher-service", []string{
		"operations/http.yaml", "operations/request.yaml", "operations/subscriber.yaml", "operations/worker.yaml", "operations/scheduled.yaml", "operations/missing-a.yaml", "operations/missing-b.yaml",
	}, []domain.ArchitectureEndpointGroup{{
		ID: "student-api", Name: "Student API", Description: statement("HTTP operations"), Operations: []string{"http", "request", "absent"},
	}}), []domain.ArchitectureOperationManifest{
		operation("http", "teacher-service", domain.ArchitectureOperationHTTP),
		operation("request", "teacher-service", domain.ArchitectureOperationNATSRequestReply),
		operation("subscriber", "teacher-service", domain.ArchitectureOperationNATSEventSubscriber),
		operation("worker", "teacher-service", domain.ArchitectureOperationWorker),
		operation("scheduled", "teacher-service", domain.ArchitectureOperationScheduled),
	})
	// Verify that a nested manifest schema is copied and cannot mutate the
	// returned immutable projection after Build returns.
	covered.Operations[0].Input.Body = &domain.ArchitectureSchemaRef{Schema: map[string]any{"type": "object"}}
	covered.DiscoveredOperations = []domain.DiscoveredOperation{
		{Type: domain.ArchitectureOperationHTTP, Protocol: "http", Method: "get", Path: "/http"},
		{Type: domain.ArchitectureOperationHTTP, Protocol: "http", Method: "GET", Path: "/not-in-manifest"},
		{Type: domain.ArchitectureOperationNATSRequestReply, Protocol: "nats", Subject: "request.v1"},
		{Type: domain.ArchitectureOperationNATSEventSubscriber, Protocol: "nats", Subject: "subscriber.v1"},
		{Type: domain.ArchitectureOperationWorker, Protocol: "worker", Name: "worker"},
		{Type: domain.ArchitectureOperationScheduled, Protocol: "scheduled", Name: "scheduled", Schedule: "@every 1m"},
	}
	uncovered := catalogSource("content", "content", domain.ArchitectureServiceManifest{}, nil)

	builder := Builder{}
	got, err := builder.Build(context.Background(), []domain.ArchitectureCatalogSource{uncovered, covered})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if got.Mode != domain.ArchitectureCatalogModeCurrent || got.Fingerprint == "" {
		t.Fatalf("catalog CURRENT identity = %#v", got)
	}
	if names := []string{got.Platform.Services[0].Source.ProjectID, got.Platform.Services[1].Source.ProjectID}; !reflect.DeepEqual(names, []string{"content", "teacher"}) {
		t.Fatalf("service order = %v, want source-name order", names)
	}
	if got.Platform.Services[0].Covered || got.Platform.Services[0].Completeness.UncoveredServiceCount != 1 {
		t.Fatalf("uncovered source was omitted or marked covered: %#v", got.Platform.Services[0])
	}
	service := got.Platform.Services[1]
	if !service.Covered || !service.Source.SourceCurrent {
		t.Fatalf("covered CURRENT service = %#v", service)
	}
	if len(service.Groups) != 1 || len(service.Groups[0].Operations) != 2 || !reflect.DeepEqual(service.Groups[0].MissingOperationIDs, []string{"absent"}) {
		t.Fatalf("group hierarchy = %#v", service.Groups)
	}
	if got := operationIDs(service.Ungrouped); !reflect.DeepEqual(got, []string{"subscriber", "scheduled", "worker"}) {
		t.Fatalf("ungrouped operations = %v", got)
	}
	if service.Completeness.DeclaredOperationManifestCount != 7 || service.Completeness.ParsedOperationManifestCount != 5 ||
		service.Completeness.MissingDeclaredOperationManifestCount != 2 || service.Completeness.MissingGroupOperationCount != 1 {
		t.Fatalf("service completeness = %#v", service.Completeness)
	}
	if service.Completeness.TotalDiscoveredOperations != 6 || service.Completeness.HTTPOperations != 2 ||
		service.Completeness.NATSRequestReplyOperations != 1 || service.Completeness.EventOperations != 1 || service.Completeness.WorkerOperations != 1 ||
		service.Completeness.ScheduledOperations != 1 || service.Completeness.OperationsWithManifests != 5 ||
		service.Completeness.OperationsMissingManifests != 1 || service.Completeness.OperationsBlocked != 0 ||
		service.Completeness.ManifestOperationsWithoutDiscovery != 0 {
		t.Fatalf("discovered-to-manifest completeness = %#v", service.Completeness)
	}
	if got := kindMap(service.Completeness); !reflect.DeepEqual(got, map[domain.ArchitectureOperationType]int{
		domain.ArchitectureOperationHTTP: 2, domain.ArchitectureOperationNATSRequestReply: 1,
		domain.ArchitectureOperationNATSEventSubscriber: 1, domain.ArchitectureOperationWorker: 1,
		domain.ArchitectureOperationScheduled: 1,
	}) {
		t.Fatalf("operation kind coverage = %#v", got)
	}
	if got.Platform.Completeness.SourceCount != 2 || got.Platform.Completeness.CoveredServiceCount != 1 || got.Platform.Completeness.UncoveredServiceCount != 1 {
		t.Fatalf("platform coverage = %#v", got.Platform.Completeness)
	}

	reversed, err := builder.Build(context.Background(), []domain.ArchitectureCatalogSource{covered, uncovered})
	if err != nil {
		t.Fatalf("Build(reversed) error = %v", err)
	}
	if got.Fingerprint != reversed.Fingerprint {
		t.Fatalf("fingerprint varies with source order: %s != %s", got.Fingerprint, reversed.Fingerprint)
	}
	covered.Operations[0].Input.Body.Schema["type"] = "changed"
	if actual := service.Groups[0].Operations[0].Manifest.Input.Body.Schema["type"]; actual != "object" {
		t.Fatalf("catalog shares input manifest map: got %v", actual)
	}
}

func TestBuilderMatchesUniqueScheduledManifestByEvidenceSource(t *testing.T) {
	scheduled := operation("business-cleanup", "service", domain.ArchitectureOperationScheduled)
	scheduled.Evidence = []domain.ArchitectureEvidence{{SourcePath: "internal/runtime/cleanup.go"}}
	source := catalogSource("service", "service", serviceManifest("service", []string{"operations/cleanup.yaml"}, nil), []domain.ArchitectureOperationManifest{scheduled})
	source.DiscoveredOperations = []domain.DiscoveredOperation{{
		Type: domain.ArchitectureOperationScheduled, Protocol: "scheduled",
		Name: "internal/runtime/cleanup.run", Schedule: "configured interval", SourcePath: "internal/runtime/cleanup.go",
	}}

	catalog, err := (Builder{}).Build(context.Background(), []domain.ArchitectureCatalogSource{source})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	coverage := catalog.Platform.Services[0].Completeness
	if coverage.OperationsWithManifests != 1 || coverage.OperationsMissingManifests != 0 || coverage.OperationsBlocked != 0 || coverage.ManifestOperationsWithoutDiscovery != 0 {
		t.Fatalf("source-evidenced scheduled completeness = %#v", coverage)
	}
}

func TestBuilderTreatsStableDirtySnapshotAsCurrent(t *testing.T) {
	source := catalogSource("service", "service", serviceManifest("service", nil, nil), nil)
	source.Topology.Project.IsDirty = true
	source.Topology.Snapshot.IsDirty = true
	source.Topology.Report.IsDirty = true

	catalog, err := (Builder{}).Build(context.Background(), []domain.ArchitectureCatalogSource{source})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	status := catalog.Platform.Services[0].Source
	if !status.SourceCurrent || !status.IsDirty {
		t.Fatalf("stable dirty source status = %#v", status)
	}
}

func TestBuilderProjectsOnlyEvidenceBackedManifestRelations(t *testing.T) {
	providerManifest := serviceManifest("provider-service", nil, nil)
	providerManifest.ProducedContracts = []domain.ArchitectureContractReference{{
		Transport: "http", Code: "course.contract", Direction: "produced", Description: statementWithEvidence("provider contract", "provider.go"),
	}}
	providerManifest.PublishedEvents = []domain.ArchitectureContractReference{{
		Transport: "nats", Code: "course.published", Direction: "produced", Description: statementWithEvidence("provider event", "provider.go"),
	}}
	operationEvent := operation("emit-course-event", "provider-service", domain.ArchitectureOperationScheduled)
	operationEvent.Output.EmittedEvents = []domain.ArchitectureContractReference{{
		Transport: "nats", Code: "operation.only", Direction: "produced", Description: statementWithEvidence("scheduled emitter", "provider.go"),
	}}
	provider := catalogSource("provider", "provider", providerManifest, []domain.ArchitectureOperationManifest{operationEvent})

	consumerManifest := serviceManifest("consumer-service", nil, nil)
	consumerManifest.ConsumedContracts = []domain.ArchitectureContractReference{{
		Transport: "http", Code: "course.contract", Direction: "consumed", Description: statementWithEvidence("consumer contract", "consumer.go"),
	}}
	consumerManifest.SubscribedEvents = []domain.ArchitectureContractReference{{
		Transport: "nats", Code: "course.published", Direction: "consumed", Description: statementWithEvidence("consumer event", "consumer.go"),
	}}
	consumerManifest.OutboundDependencies = []domain.ArchitectureExternalInteraction{{
		ID: "provider", Transport: "http", Target: "provider-service", Direction: "outbound", Description: statementWithEvidence("call provider", "consumer.go"),
	}}
	call := operation("call-unresolved", "consumer-service", domain.ArchitectureOperationHTTP)
	call.ExternalInteractions = []domain.ArchitectureExternalInteraction{{
		ID: "unknown", Transport: "http", Target: "provider-service-v2", Direction: "outbound", Description: statementWithEvidence("unresolved target", "consumer.go"),
	}}
	consumer := catalogSource("consumer", "consumer", consumerManifest, []domain.ArchitectureOperationManifest{call})

	got, err := (Builder{}).Build(context.Background(), []domain.ArchitectureCatalogSource{consumer, provider})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(got.Platform.Relations) != 5 {
		t.Fatalf("relation count = %d, want 5: %#v", len(got.Platform.Relations), got.Platform.Relations)
	}
	byType := make(map[domain.ArchitectureCatalogRelationType][]domain.ArchitectureCatalogRelation)
	for _, relation := range got.Platform.Relations {
		if relation.ID == "" || len(relation.Evidence) == 0 {
			t.Fatalf("relation must have deterministic identity and evidence: %#v", relation)
		}
		byType[relation.Type] = append(byType[relation.Type], relation)
	}
	if values := byType[domain.ArchitectureCatalogRelationOutboundDependency]; len(values) != 1 || values[0].SourceProjectID != "consumer" || values[0].TargetProjectID != "provider" || values[0].Direction != "outbound" {
		t.Fatalf("outbound dependency = %#v", values)
	}
	if values := byType[domain.ArchitectureCatalogRelationOperationInteraction]; len(values) != 1 || values[0].OperationID != "call-unresolved" || values[0].TargetProjectID != "" || values[0].ExternalTarget != "provider-service-v2" {
		t.Fatalf("operation interaction must retain unresolved literal target: %#v", values)
	}
	if values := byType[domain.ArchitectureCatalogRelationContract]; len(values) != 1 || values[0].SourceProjectID != "consumer" || values[0].TargetProjectID != "provider" || values[0].Direction != "inbound" || values[0].Contract != "course.contract" {
		t.Fatalf("contract provider-consumer relation = %#v", values)
	}
	if values := byType[domain.ArchitectureCatalogRelationEvent]; len(values) != 2 {
		t.Fatalf("event relations = %#v", values)
	} else {
		var paired, unresolved bool
		for _, relation := range values {
			if relation.Contract == "course.published" && relation.SourceProjectID == "consumer" && relation.TargetProjectID == "provider" && relation.Direction == "inbound" {
				paired = true
			}
			if relation.Contract == "operation.only" && relation.OperationID == "emit-course-event" && relation.ExternalTarget == "unknown subscriber for contract operation.only" {
				unresolved = true
			}
		}
		if !paired || !unresolved {
			t.Fatalf("event pairing/unknown evidence = %#v", values)
		}
	}

	reversed, err := (Builder{}).Build(context.Background(), []domain.ArchitectureCatalogSource{provider, consumer})
	if err != nil || got.Fingerprint != reversed.Fingerprint || !reflect.DeepEqual(got.Platform.Relations, reversed.Platform.Relations) {
		t.Fatalf("relations must be deterministic: fingerprint %q/%q, error %v", got.Fingerprint, reversed.Fingerprint, err)
	}
}

func TestGraphIDsDoNotDependOnTransientProjectOrSnapshotIDs(t *testing.T) {
	providerManifest := serviceManifest("provider-service", nil, nil)
	consumerManifest := serviceManifest("consumer-service", nil, nil)
	consumerManifest.OutboundDependencies = []domain.ArchitectureExternalInteraction{{
		ID: "course-call", Transport: "http", Target: "provider-service", Direction: "outbound",
		Description: statementWithEvidence("calls the provider", "internal/client.go"),
	}}
	provider := catalogSource("provider-db-id-a", "provider", providerManifest, nil)
	consumer := catalogSource("consumer-db-id-a", "consumer", consumerManifest, nil)
	provider.Topology.Project.SourceIdentity = "git:github.com/example/provider"
	consumer.Topology.Project.SourceIdentity = "git:github.com/example/consumer"

	first, err := (Builder{}).Build(context.Background(), []domain.ArchitectureCatalogSource{provider, consumer})
	if err != nil {
		t.Fatalf("Build(first) error = %v", err)
	}
	if len(first.Platform.Relations) != 1 {
		t.Fatalf("first relation count = %d, want 1", len(first.Platform.Relations))
	}
	firstReferences := make(map[string]string, len(first.Platform.Services))
	for _, service := range first.Platform.Services {
		firstReferences[service.Source.ProjectName] = service.Source.ReferenceID
	}
	firstEdge := first.Platform.Relations[0]
	if firstEdge.EdgeID == "" || firstEdge.SourceReferenceID != firstReferences["consumer"] || firstEdge.TargetReferenceID != firstReferences["provider"] {
		t.Fatalf("first stable edge identity = %#v", firstEdge)
	}

	provider.Topology.Project.ID = "provider-db-id-b"
	provider.Topology.Snapshot.ID = "snapshot-provider-db-id-b"
	provider.Topology.Snapshot.ProjectID = provider.Topology.Project.ID
	provider.Topology.Report.ProjectID = provider.Topology.Project.ID
	consumer.Topology.Project.ID = "consumer-db-id-b"
	consumer.Topology.Snapshot.ID = "snapshot-consumer-db-id-b"
	consumer.Topology.Snapshot.ProjectID = consumer.Topology.Project.ID
	consumer.Topology.Report.ProjectID = consumer.Topology.Project.ID
	second, err := (Builder{}).Build(context.Background(), []domain.ArchitectureCatalogSource{consumer, provider})
	if err != nil {
		t.Fatalf("Build(second) error = %v", err)
	}
	secondReferences := make(map[string]string, len(second.Platform.Services))
	for _, service := range second.Platform.Services {
		secondReferences[service.Source.ProjectName] = service.Source.ReferenceID
	}
	secondEdge := second.Platform.Relations[0]
	if !reflect.DeepEqual(firstReferences, secondReferences) || firstEdge.EdgeID != secondEdge.EdgeID {
		t.Fatalf("graph IDs changed with database identities: refs %v != %v, edges %q != %q", firstReferences, secondReferences, firstEdge.EdgeID, secondEdge.EdgeID)
	}
	if err := ValidateStableGraphIDs(second); err != nil {
		t.Fatalf("ValidateStableGraphIDs() error = %v", err)
	}
	second.Platform.Relations[0].EdgeID += "-tampered"
	if err := ValidateStableGraphIDs(second); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("ValidateStableGraphIDs(tampered) error = %v, want ErrValidation", err)
	}
}

func TestBuilderRejectsInconsistentOrUnsupportedSources(t *testing.T) {
	validManifest := serviceManifest("teacher-service", nil, nil)
	for name, source := range map[string]domain.ArchitectureCatalogSource{
		"topology mismatch": func() domain.ArchitectureCatalogSource {
			source := catalogSource("teacher", "teacher", validManifest, nil)
			source.Topology.Report.CommitSHA = "other"
			return source
		}(),
		"operation belongs elsewhere": catalogSource("teacher", "teacher", validManifest, []domain.ArchitectureOperationManifest{operation("http", "other-service", domain.ArchitectureOperationHTTP)}),
		"unsupported operation type":  catalogSource("teacher", "teacher", validManifest, []domain.ArchitectureOperationManifest{operation("http", "teacher-service", "unknown")}),
		"uncovered with operation":    catalogSource("teacher", "teacher", domain.ArchitectureServiceManifest{}, []domain.ArchitectureOperationManifest{operation("http", "teacher-service", domain.ArchitectureOperationHTTP)}),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := (Builder{}).Build(context.Background(), []domain.ArchitectureCatalogSource{source})
			if !errors.Is(err, domain.ErrConflict) && !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("Build() error = %v, want validation or conflict", err)
			}
		})
	}
}

func TestBuilderCompletenessRetainsUncoveredAndAmbiguousDiscovery(t *testing.T) {
	uncovered := catalogSource("uncovered", "uncovered", domain.ArchitectureServiceManifest{}, nil)
	uncovered.DiscoveredOperations = []domain.DiscoveredOperation{{
		Type: domain.ArchitectureOperationHTTP, Protocol: "http", Method: "GET", Path: "/uncovered",
	}}
	uncoveredCatalog, err := (Builder{}).Build(context.Background(), []domain.ArchitectureCatalogSource{uncovered})
	if err != nil {
		t.Fatalf("Build(uncovered) error = %v", err)
	}
	coverage := uncoveredCatalog.Platform.Completeness
	if coverage.UncoveredServiceCount != 1 || coverage.TotalDiscoveredOperations != 1 ||
		coverage.OperationsMissingManifests != 1 || coverage.OperationsBlocked != 0 {
		t.Fatalf("uncovered discovery completeness = %#v", coverage)
	}

	first := operation("first", "teacher-service", domain.ArchitectureOperationHTTP)
	duplicate := operation("duplicate", "teacher-service", domain.ArchitectureOperationHTTP)
	duplicate.Identity.HTTP = &domain.ArchitectureHTTPIdentity{Method: "GET", Path: "/first"}
	ambiguous := catalogSource("teacher", "teacher", serviceManifest("teacher-service", nil, nil), []domain.ArchitectureOperationManifest{first, duplicate})
	ambiguous.DiscoveredOperations = []domain.DiscoveredOperation{{
		Type: domain.ArchitectureOperationHTTP, Protocol: "http", Method: "GET", Path: "/first",
	}}
	ambiguousCatalog, err := (Builder{}).Build(context.Background(), []domain.ArchitectureCatalogSource{ambiguous})
	if err != nil {
		t.Fatalf("Build(ambiguous) error = %v", err)
	}
	coverage = ambiguousCatalog.Platform.Completeness
	if coverage.OperationsBlocked != 1 || coverage.OperationsWithManifests != 0 ||
		coverage.OperationsMissingManifests != 0 || coverage.ManifestOperationsWithoutDiscovery != 0 {
		t.Fatalf("ambiguous discovery completeness = %#v", coverage)
	}
}

func catalogSource(id, name string, manifest domain.ArchitectureServiceManifest, operations []domain.ArchitectureOperationManifest) domain.ArchitectureCatalogSource {
	return domain.ArchitectureCatalogSource{
		Topology: domain.TopologySource{
			Project:  domain.Project{ID: id, Name: name, SourceIdentity: "git:github.com/example/" + id, Status: domain.ProjectStatusAnalyzed, RepositoryRole: domain.RepositoryRoleService, HeadCommit: "commit-" + id},
			Snapshot: domain.ServiceSnapshot{ID: "snapshot-" + id, ProjectID: id, Status: "complete", CommitSHA: "commit-" + id, Branch: "main", ContentChecksum: "checksum-" + id},
			Report:   domain.DiscoveryReport{SchemaVersion: 3, ProjectID: id, CommitSHA: "commit-" + id, ContentChecksum: "checksum-" + id},
		},
		ServiceManifest: manifest,
		Operations:      operations,
	}
}

func serviceManifest(id string, paths []string, groups []domain.ArchitectureEndpointGroup) domain.ArchitectureServiceManifest {
	return domain.ArchitectureServiceManifest{
		Schema: domain.ArchitectureManifestSchemaV1, Kind: "service", ID: id, ManifestRevision: 1,
		Identity:           domain.ArchitectureServiceIdentity{Name: id, Kind: "backend_service"},
		OperationManifests: paths, EndpointGroups: groups,
	}
}

func operation(id, serviceID string, kind domain.ArchitectureOperationType) domain.ArchitectureOperationManifest {
	operation := domain.ArchitectureOperationManifest{
		Schema: domain.ArchitectureManifestSchemaV1, Kind: "operation", ID: id, ManifestRevision: 1,
		ServiceID: serviceID, Type: kind,
	}
	switch kind {
	case domain.ArchitectureOperationHTTP:
		operation.Identity = domain.ArchitectureOperationIdentity{Transport: "http", HTTP: &domain.ArchitectureHTTPIdentity{Method: "GET", Path: "/" + id}}
	case domain.ArchitectureOperationNATSRequestReply, domain.ArchitectureOperationNATSEventSubscriber:
		operation.Identity = domain.ArchitectureOperationIdentity{Transport: "nats", NATS: &domain.ArchitectureNATSIdentity{Subject: id + ".v1", Role: "handler"}}
	case domain.ArchitectureOperationWorker:
		operation.Identity = domain.ArchitectureOperationIdentity{Transport: "worker", Worker: &domain.ArchitectureWorkerIdentity{Name: id}}
	case domain.ArchitectureOperationScheduled:
		operation.Identity = domain.ArchitectureOperationIdentity{Transport: "scheduled", Scheduled: &domain.ArchitectureScheduledIdentity{Name: id, Schedule: "@every 1m"}}
	}
	return operation
}

func statement(value string) domain.ArchitectureStatement {
	return domain.ArchitectureStatement{Value: value}
}

func statementWithEvidence(value, path string) domain.ArchitectureStatement {
	return domain.ArchitectureStatement{Value: value, Confidence: 1, Evidence: []domain.ArchitectureEvidence{{SourcePath: path}}}
}

func operationIDs(values []domain.ArchitectureCatalogOperation) []string {
	ids := make([]string, 0, len(values))
	for _, value := range values {
		ids = append(ids, value.Manifest.ID)
	}
	return ids
}

func kindMap(completeness domain.ArchitectureCatalogCompleteness) map[domain.ArchitectureOperationType]int {
	result := make(map[domain.ArchitectureOperationType]int, len(completeness.OperationKinds))
	for _, count := range completeness.OperationKinds {
		result[count.Type] = count.Count
	}
	return result
}
