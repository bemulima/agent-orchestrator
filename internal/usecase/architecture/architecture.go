package architecture

import (
	"context"

	architectureprojection "github.com/bemulima/agent-orchestrator/internal/architecture"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type snapshotReader interface {
	GetLatestDiscovery(context.Context, string) (domain.ServiceSnapshot, domain.DiscoveryReport, error)
}

type Current struct {
	Catalog   repository.TopologyRepository
	Projects  snapshotReader
	Projector architectureprojection.Projector
}

func (uc Current) Handle(ctx context.Context) (domain.ArchitectureCurrent, error) {
	catalog, err := uc.Catalog.Get(ctx)
	if err != nil {
		return domain.ArchitectureCurrent{}, err
	}
	return uc.Projector.Current(catalog, topologyStale(ctx, uc.Projects, catalog)), nil
}

type Service struct{ Current Current }

func (uc Service) Handle(ctx context.Context, projectID string) (domain.ArchitectureServiceDetail, error) {
	current, err := uc.Current.Handle(ctx)
	if err != nil {
		return domain.ArchitectureServiceDetail{}, err
	}
	return uc.Current.Projector.Service(current, projectID)
}

type Contracts struct{ Current Current }

func (uc Contracts) Handle(ctx context.Context, projectID string) ([]domain.ArchitectureContract, error) {
	detail, err := Service{Current: uc.Current}.Handle(ctx, projectID)
	if err != nil {
		return nil, err
	}
	result := []domain.ArchitectureContract{}
	for _, contract := range detail.Contracts {
		if contract.ProjectID == projectID {
			result = append(result, contract)
		}
	}
	return result, nil
}

func topologyStale(ctx context.Context, projects snapshotReader, catalog domain.TopologyCatalog) bool {
	if projects == nil {
		return false
	}
	for _, service := range catalog.Services {
		snapshot, _, err := projects.GetLatestDiscovery(ctx, service.ProjectID)
		if err != nil || snapshot.ID != service.SnapshotID {
			return true
		}
	}
	return false
}
