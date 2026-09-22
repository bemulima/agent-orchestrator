// Package architecturecatalog exposes the read-only, manifest-backed
// Architecture CURRENT hierarchy. It never writes a snapshot, topology, or
// manifest; a topology rebuild remains the boundary that selects CURRENT
// source material.
package architecturecatalog

import (
	"context"
	"fmt"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

// snapshotReader obtains the project identity and immutable discovery record
// selected by the materialized topology. Keeping this small interface makes
// the CURRENT projection testable without coupling it to a storage adapter.
type snapshotReader interface {
	Get(context.Context, string) (domain.Project, error)
	GetLatestDiscovery(context.Context, string) (domain.ServiceSnapshot, domain.DiscoveryReport, error)
}

// Current builds the ArchitectureCatalog from the persisted topology revision
// and its matching latest discovery snapshots. A changed snapshot requires a
// topology rebuild instead of silently mixing source revisions.
type Current struct {
	Topology repository.TopologyRepository
	Projects snapshotReader
	Builder  repository.ArchitectureCatalogBuilder
}

func (uc Current) Handle(ctx context.Context) (domain.ArchitectureCatalog, error) {
	if uc.Topology == nil || uc.Projects == nil || uc.Builder == nil {
		return domain.ArchitectureCatalog{}, fmt.Errorf("architecture catalog dependencies are not configured: %w", domain.ErrValidation)
	}
	topology, err := uc.Topology.Get(ctx)
	if err != nil {
		return domain.ArchitectureCatalog{}, err
	}
	sources := make([]domain.ArchitectureCatalogSource, 0, len(topology.Services))
	seen := make(map[string]struct{}, len(topology.Services))
	for _, service := range topology.Services {
		if err := ctx.Err(); err != nil {
			return domain.ArchitectureCatalog{}, err
		}
		if _, exists := seen[service.ProjectID]; exists {
			return domain.ArchitectureCatalog{}, fmt.Errorf("topology contains duplicate project %q: %w", service.ProjectID, domain.ErrConflict)
		}
		seen[service.ProjectID] = struct{}{}

		project, err := uc.Projects.Get(ctx, service.ProjectID)
		if err != nil {
			return domain.ArchitectureCatalog{}, err
		}
		snapshot, report, err := uc.Projects.GetLatestDiscovery(ctx, service.ProjectID)
		if err != nil {
			return domain.ArchitectureCatalog{}, err
		}
		if snapshot.ID != service.SnapshotID {
			return domain.ArchitectureCatalog{}, fmt.Errorf("topology snapshot %q for project %q is stale against latest discovery snapshot %q; rebuild topology is required before reading Architecture CURRENT: %w", service.SnapshotID, service.ProjectID, snapshot.ID, domain.ErrConflict)
		}
		if snapshot.ProjectID != project.ID || report.ProjectID != project.ID || report.CommitSHA != snapshot.CommitSHA {
			return domain.ArchitectureCatalog{}, fmt.Errorf("discovery snapshot does not match topology project %q: %w", project.ID, domain.ErrConflict)
		}

		source := domain.ArchitectureCatalogSource{
			Topology:             domain.TopologySource{Project: project, Snapshot: snapshot, Report: report},
			Operations:           append([]domain.ArchitectureOperationManifest(nil), report.ArchitectureOperations...),
			DiscoveredOperations: append([]domain.DiscoveredOperation(nil), report.Operations...),
		}
		if report.ArchitectureService != nil {
			manifest := *report.ArchitectureService
			source.ServiceManifest = manifest
		}
		sources = append(sources, source)
	}
	return uc.Builder.Build(ctx, sources)
}

// Service selects one service from CURRENT by canonical project ID.
type Service struct{ Current Current }

func (uc Service) Handle(ctx context.Context, projectID string) (domain.ArchitectureCatalogService, error) {
	catalog, err := uc.Current.Handle(ctx)
	if err != nil {
		return domain.ArchitectureCatalogService{}, err
	}
	for _, service := range catalog.Platform.Services {
		if service.Source.ProjectID == projectID {
			return service, nil
		}
	}
	return domain.ArchitectureCatalogService{}, domain.ErrNotFound
}

// Operation selects one operation manifest below one canonical service ID.
type Operation struct{ Service Service }

func (uc Operation) Handle(ctx context.Context, projectID, operationID string) (domain.ArchitectureCatalogOperation, error) {
	service, err := uc.Service.Handle(ctx, projectID)
	if err != nil {
		return domain.ArchitectureCatalogOperation{}, err
	}
	for _, group := range service.Groups {
		for _, operation := range group.Operations {
			if operation.Manifest.ID == operationID {
				return operation, nil
			}
		}
	}
	for _, operation := range service.Ungrouped {
		if operation.Manifest.ID == operationID {
			return operation, nil
		}
	}
	return domain.ArchitectureCatalogOperation{}, domain.ErrNotFound
}
