package topology

import (
	"context"
	"errors"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestProjectQueryReturnsDependenciesConsumersAndTransitiveImpact(t *testing.T) {
	projects := topologyProjectRepoFake{projects: []domain.Project{
		{ID: "orders", Name: "orders"}, {ID: "admin", Name: "admin"},
		{ID: "gateway", Name: "gateway"}, {ID: "portal", Name: "portal"},
	}}
	catalog := topologyCatalogRepoFake{catalog: domain.TopologyCatalog{
		Services: []domain.TopologyService{
			{ProjectID: "orders", Name: "orders"}, {ProjectID: "admin", Name: "admin"},
			{ProjectID: "gateway", Name: "gateway"}, {ProjectID: "portal", Name: "portal"},
		},
		Contracts: []domain.Contract{{ProjectID: "orders", Code: "http:get:/orders"}},
		Relations: []domain.ServiceRelation{
			{SourceProjectID: "admin", TargetProjectID: "orders", RelationType: domain.RelationConsumes},
			{SourceProjectID: "gateway", TargetProjectID: "orders", RelationType: domain.RelationRoutesTo},
			{SourceProjectID: "portal", TargetProjectID: "admin", RelationType: domain.RelationDependsOn},
		},
	}}
	view, err := (ProjectQuery{Projects: projects, Catalog: &catalog}).Handle(context.Background(), "orders")
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(view.Dependencies) != 0 || len(view.Consumers) != 2 || len(view.Impact) != 3 || len(view.Contracts) != 1 {
		t.Fatalf("project topology = %#v", view)
	}
	if view.Impact[0].Name != "admin" || view.Impact[1].Name != "gateway" || view.Impact[2].Name != "portal" {
		t.Fatalf("sorted impact = %#v", view.Impact)
	}
}

func TestRebuildSkipsProjectsWithoutDiscovery(t *testing.T) {
	project := domain.Project{ID: "orders", Name: "orders", RepositoryRole: domain.RepositoryRoleService}
	repository := &topologyProjectRepoFake{projects: []domain.Project{project, {ID: "unscanned", Name: "unscanned"}}, reports: map[string]domain.TopologySource{
		project.ID: {
			Project:  project,
			Snapshot: domain.ServiceSnapshot{ID: "snapshot", ProjectID: project.ID, CommitSHA: "commit", ServiceKind: domain.ServiceKindBackendService},
			Report:   domain.DiscoveryReport{ProjectID: project.ID, CommitSHA: "commit"},
		},
	}}
	catalog := &topologyCatalogRepoFake{}
	result, err := (Rebuild{Projects: repository, Catalog: catalog, Builder: topologyBuilderFake{}}).Handle(context.Background())
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if result.Revision.ProjectCount != 1 || len(catalog.catalog.Services) != 1 {
		t.Fatalf("rebuilt catalog = %#v", result)
	}
}

type topologyProjectRepoFake struct {
	projects []domain.Project
	reports  map[string]domain.TopologySource
}

func (f topologyProjectRepoFake) Upsert(context.Context, domain.Project) (domain.Project, error) {
	panic("unexpected call")
}
func (f topologyProjectRepoFake) Get(_ context.Context, id string) (domain.Project, error) {
	for _, project := range f.projects {
		if project.ID == id {
			return project, nil
		}
	}
	return domain.Project{}, domain.ErrNotFound
}
func (f topologyProjectRepoFake) GetByName(_ context.Context, name string) (domain.Project, error) {
	for _, project := range f.projects {
		if project.Name == name {
			return project, nil
		}
	}
	return domain.Project{}, domain.ErrNotFound
}
func (f topologyProjectRepoFake) List(context.Context) ([]domain.Project, error) {
	return f.projects, nil
}
func (f topologyProjectRepoFake) UpdateSourceState(context.Context, string, domain.ProjectStatus, domain.RepositorySource) (domain.Project, error) {
	panic("unexpected call")
}
func (f topologyProjectRepoFake) UpdateStatus(context.Context, string, domain.ProjectStatus) error {
	panic("unexpected call")
}
func (f topologyProjectRepoFake) SaveDiscovery(context.Context, domain.Project, domain.ServiceSnapshot, domain.DiscoveryReport) (domain.ServiceSnapshot, error) {
	panic("unexpected call")
}
func (f topologyProjectRepoFake) GetLatestDiscovery(_ context.Context, id string) (domain.ServiceSnapshot, domain.DiscoveryReport, error) {
	source, exists := f.reports[id]
	if !exists {
		return domain.ServiceSnapshot{}, domain.DiscoveryReport{}, domain.ErrNotFound
	}
	return source.Snapshot, source.Report, nil
}

type topologyCatalogRepoFake struct {
	catalog domain.TopologyCatalog
}

func (f *topologyCatalogRepoFake) Replace(_ context.Context, catalog domain.TopologyCatalog) (domain.TopologyCatalog, error) {
	f.catalog = catalog
	return catalog, nil
}
func (f *topologyCatalogRepoFake) Get(context.Context) (domain.TopologyCatalog, error) {
	if f.catalog.Revision.Fingerprint == "missing" {
		return domain.TopologyCatalog{}, domain.ErrNotFound
	}
	return f.catalog, nil
}

type topologyBuilderFake struct{}

func (topologyBuilderFake) Build(_ context.Context, sources []domain.TopologySource) (domain.TopologyCatalog, error) {
	if len(sources) == 0 {
		return domain.TopologyCatalog{}, errors.New("no sources")
	}
	return domain.TopologyCatalog{
		Revision: domain.TopologyRevision{Fingerprint: "fingerprint", ProjectCount: len(sources), ServiceCount: len(sources)},
		Services: []domain.TopologyService{{ProjectID: sources[0].Project.ID}},
	}, nil
}
