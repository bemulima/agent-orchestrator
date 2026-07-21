package topology

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type Rebuild struct {
	Projects repository.ProjectRepository
	Catalog  repository.TopologyRepository
	Builder  repository.TopologyBuilder
}

func (uc Rebuild) Handle(ctx context.Context) (domain.TopologyCatalog, error) {
	projects, err := uc.Projects.List(ctx)
	if err != nil {
		return domain.TopologyCatalog{}, err
	}
	sources := make([]domain.TopologySource, 0, len(projects))
	for _, project := range projects {
		snapshot, report, discoveryErr := uc.Projects.GetLatestDiscovery(ctx, project.ID)
		if errors.Is(discoveryErr, domain.ErrNotFound) {
			continue
		}
		if discoveryErr != nil {
			return domain.TopologyCatalog{}, discoveryErr
		}
		sources = append(sources, domain.TopologySource{Project: project, Snapshot: snapshot, Report: report})
	}
	catalog, err := uc.Builder.Build(ctx, sources)
	if err != nil {
		return domain.TopologyCatalog{}, err
	}
	return uc.Catalog.Replace(ctx, catalog)
}

type Get struct {
	Catalog repository.TopologyRepository
}

func (uc Get) Handle(ctx context.Context) (domain.TopologyCatalog, error) {
	return uc.Catalog.Get(ctx)
}

type Services struct {
	Catalog repository.TopologyRepository
}

func (uc Services) Handle(ctx context.Context) ([]domain.TopologyService, error) {
	catalog, err := uc.Catalog.Get(ctx)
	return catalog.Services, err
}

type Contracts struct {
	Catalog repository.TopologyRepository
}

func (uc Contracts) Handle(ctx context.Context) ([]domain.Contract, error) {
	catalog, err := uc.Catalog.Get(ctx)
	return catalog.Contracts, err
}

type ContractDrift struct {
	Catalog repository.TopologyRepository
}

func (uc ContractDrift) Handle(ctx context.Context) ([]domain.ContractDrift, error) {
	catalog, err := uc.Catalog.Get(ctx)
	return catalog.Drifts, err
}

type ProjectQuery struct {
	Projects repository.ProjectRepository
	Catalog  repository.TopologyRepository
}

func (uc ProjectQuery) Handle(ctx context.Context, identifier string) (domain.ProjectTopology, error) {
	project, err := resolveProject(ctx, uc.Projects, identifier)
	if err != nil {
		return domain.ProjectTopology{}, err
	}
	catalog, err := uc.Catalog.Get(ctx)
	if err != nil {
		return domain.ProjectTopology{}, err
	}
	services := make(map[string]domain.TopologyService, len(catalog.Services))
	for _, service := range catalog.Services {
		services[service.ProjectID] = service
	}
	if _, exists := services[project.ID]; !exists {
		return domain.ProjectTopology{}, domain.ErrNotFound
	}
	view := domain.ProjectTopology{
		Project: project, Dependencies: []domain.TopologyService{}, Consumers: []domain.TopologyService{},
		Contracts: []domain.Contract{}, Impact: []domain.TopologyService{},
	}
	dependencyIDs := make(map[string]struct{})
	consumerIDs := make(map[string]struct{})
	incoming := make(map[string][]string)
	for _, relation := range catalog.Relations {
		incoming[relation.TargetProjectID] = append(incoming[relation.TargetProjectID], relation.SourceProjectID)
		if relation.SourceProjectID == project.ID {
			dependencyIDs[relation.TargetProjectID] = struct{}{}
		}
		if relation.TargetProjectID == project.ID {
			consumerIDs[relation.SourceProjectID] = struct{}{}
		}
	}
	view.Dependencies = servicesForIDs(services, dependencyIDs)
	view.Consumers = servicesForIDs(services, consumerIDs)
	for _, contract := range catalog.Contracts {
		if contract.ProjectID == project.ID {
			view.Contracts = append(view.Contracts, contract)
		}
	}
	visited := map[string]struct{}{project.ID: {}}
	queue := []string{project.ID}
	impactIDs := make(map[string]struct{})
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, dependent := range incoming[current] {
			if _, seen := visited[dependent]; seen {
				continue
			}
			visited[dependent] = struct{}{}
			impactIDs[dependent] = struct{}{}
			queue = append(queue, dependent)
		}
	}
	view.Impact = servicesForIDs(services, impactIDs)
	return view, nil
}

func resolveProject(ctx context.Context, projects repository.ProjectRepository, identifier string) (domain.Project, error) {
	identifier = strings.TrimSpace(identifier)
	project, err := projects.Get(ctx, identifier)
	if err == nil || !errors.Is(err, domain.ErrNotFound) {
		return project, err
	}
	return projects.GetByName(ctx, identifier)
}

func servicesForIDs(services map[string]domain.TopologyService, ids map[string]struct{}) []domain.TopologyService {
	result := make([]domain.TopologyService, 0, len(ids))
	for id := range ids {
		if service, exists := services[id]; exists {
			result = append(result, service)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name+result[i].ProjectID < result[j].Name+result[j].ProjectID
	})
	return result
}
