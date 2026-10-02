package architecturecatalog

import (
	"context"
	"errors"
	"strings"
	"testing"

	catalogprojection "github.com/bemulima/agent-orchestrator/internal/architecturecatalog"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type topologyRepositoryFake struct {
	catalog domain.TopologyCatalog
	err     error
}

func (f topologyRepositoryFake) Get(context.Context) (domain.TopologyCatalog, error) {
	return f.catalog, f.err
}
func (topologyRepositoryFake) Replace(context.Context, domain.TopologyCatalog) (domain.TopologyCatalog, error) {
	return domain.TopologyCatalog{}, nil
}

type catalogProjectReaderFake struct {
	projects  map[string]domain.Project
	snapshots map[string]domain.ServiceSnapshot
	reports   map[string]domain.DiscoveryReport
	err       error
}

func (f catalogProjectReaderFake) Get(_ context.Context, id string) (domain.Project, error) {
	if f.err != nil {
		return domain.Project{}, f.err
	}
	project, ok := f.projects[id]
	if !ok {
		return domain.Project{}, domain.ErrNotFound
	}
	return project, nil
}
func (f catalogProjectReaderFake) GetLatestDiscovery(_ context.Context, id string) (domain.ServiceSnapshot, domain.DiscoveryReport, error) {
	if f.err != nil {
		return domain.ServiceSnapshot{}, domain.DiscoveryReport{}, f.err
	}
	snapshot, ok := f.snapshots[id]
	if !ok {
		return domain.ServiceSnapshot{}, domain.DiscoveryReport{}, domain.ErrNotFound
	}
	return snapshot, f.reports[id], nil
}

func TestCurrentBuildsCurrentCatalogFromMatchingTopologySnapshots(t *testing.T) {
	project, snapshot, report := catalogFixture("teacher", "teacher-snapshot")
	current := Current{
		Topology: topologyRepositoryFake{catalog: domain.TopologyCatalog{Services: []domain.TopologyService{{ProjectID: project.ID, SnapshotID: snapshot.ID}}}},
		Projects: catalogProjectReaderFake{projects: map[string]domain.Project{project.ID: project}, snapshots: map[string]domain.ServiceSnapshot{project.ID: snapshot}, reports: map[string]domain.DiscoveryReport{project.ID: report}},
		Builder:  catalogprojection.Builder{},
	}

	catalog, err := current.Handle(context.Background())
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if catalog.Mode != domain.ArchitectureCatalogModeCurrent || len(catalog.Platform.Services) != 1 {
		t.Fatalf("catalog = %#v", catalog)
	}
	service := catalog.Platform.Services[0]
	if !service.Covered || service.Source.SnapshotID != snapshot.ID || len(service.Ungrouped) != 1 || service.Completeness.OperationsWithManifests != 1 {
		t.Fatalf("service does not retain immutable manifest source: %#v", service)
	}
	if got, err := (Service{Current: current}).Handle(context.Background(), project.ID); err != nil || got.Source.ProjectID != project.ID {
		t.Fatalf("Service.Handle() = %#v, %v", got, err)
	}
	operation, err := (Operation{Service: Service{Current: current}}).Handle(context.Background(), project.ID, "health")
	if err != nil || operation.Manifest.ID != "health" {
		t.Fatalf("Operation.Handle() = %#v, %v", operation, err)
	}
	serviceMermaid, err := (ServiceMermaid{Service: Service{Current: current}}).Handle(context.Background(), project.ID)
	if err != nil || !strings.HasPrefix(serviceMermaid, "%% GENERATED") {
		t.Fatalf("ServiceMermaid.Handle() = %q, %v", serviceMermaid, err)
	}
	operationMermaid, err := (OperationMermaid{Operation: Operation{Service: Service{Current: current}}}).Handle(context.Background(), project.ID, "health")
	if err != nil || !strings.Contains(operationMermaid, "health") {
		t.Fatalf("OperationMermaid.Handle() = %q, %v", operationMermaid, err)
	}
}

func TestCurrentRejectsTopologyThatIsStaleAgainstLatestSnapshot(t *testing.T) {
	project, snapshot, report := catalogFixture("teacher", "latest")
	current := Current{
		Topology: topologyRepositoryFake{catalog: domain.TopologyCatalog{Services: []domain.TopologyService{{ProjectID: project.ID, SnapshotID: "older"}}}},
		Projects: catalogProjectReaderFake{projects: map[string]domain.Project{project.ID: project}, snapshots: map[string]domain.ServiceSnapshot{project.ID: snapshot}, reports: map[string]domain.DiscoveryReport{project.ID: report}},
		Builder:  catalogprojection.Builder{},
	}
	_, err := current.Handle(context.Background())
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Handle() error = %v, want ErrConflict", err)
	}
	if !strings.Contains(err.Error(), "rebuild topology is required") {
		t.Fatalf("stale error must explain remediation: %v", err)
	}
}

func TestServiceAndOperationReturnNotFoundForMissingCanonicalIDs(t *testing.T) {
	current := Current{Topology: topologyRepositoryFake{catalog: domain.TopologyCatalog{}}, Projects: catalogProjectReaderFake{}, Builder: catalogprojection.Builder{}}
	if _, err := (Service{Current: current}).Handle(context.Background(), "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Service.Handle() error = %v, want ErrNotFound", err)
	}
	if _, err := (Operation{Service: Service{Current: current}}).Handle(context.Background(), "missing", "operation"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Operation.Handle() error = %v, want ErrNotFound", err)
	}
}

func catalogFixture(projectID, snapshotID string) (domain.Project, domain.ServiceSnapshot, domain.DiscoveryReport) {
	project := domain.Project{ID: projectID, Name: projectID, SourceIdentity: "git:github.com/example/" + projectID, Status: domain.ProjectStatusAnalyzed, RepositoryRole: domain.RepositoryRoleService, HeadCommit: "commit"}
	snapshot := domain.ServiceSnapshot{ID: snapshotID, ProjectID: projectID, Status: "complete", CommitSHA: "commit", Branch: "main", ContentChecksum: "checksum"}
	service := &domain.ArchitectureServiceManifest{
		Schema: domain.ArchitectureManifestSchemaV1, Kind: "service", ID: "teacher-service", ManifestRevision: 1,
		Identity: domain.ArchitectureServiceIdentity{Name: "teacher", Kind: "backend_service"}, OperationManifests: []string{"endpoints/health.yaml"},
	}
	operation := domain.ArchitectureOperationManifest{
		Schema: domain.ArchitectureManifestSchemaV1, Kind: "operation", ID: "health", ManifestRevision: 1, ServiceID: service.ID, Type: domain.ArchitectureOperationHTTP,
		Identity: domain.ArchitectureOperationIdentity{Transport: "http", HTTP: &domain.ArchitectureHTTPIdentity{Method: "GET", Path: "/health"}},
	}
	report := domain.DiscoveryReport{
		SchemaVersion: 18, ProjectID: project.ID, ProjectName: project.Name, RepositoryRole: project.RepositoryRole, CommitSHA: snapshot.CommitSHA, ContentChecksum: snapshot.ContentChecksum,
		ArchitectureService: service, ArchitectureOperations: []domain.ArchitectureOperationManifest{operation},
		Operations: []domain.DiscoveredOperation{{
			Type: domain.ArchitectureOperationHTTP, Protocol: "http", Method: "GET", Path: "/health", SourcePath: "internal/http/router.go", Confidence: 1,
		}},
	}
	return project, snapshot, report
}
