package repository

import (
	"context"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// ArchitectureCatalogRepository is the persistence boundary for the separate
// architecture-manifest CURRENT projection. It must not mutate topology.
type ArchitectureCatalogRepository interface {
	Replace(context.Context, domain.ArchitectureCatalog) (domain.ArchitectureCatalog, error)
	Get(context.Context) (domain.ArchitectureCatalog, error)
}

// ArchitectureCatalogBuilder is a deterministic projection over immutable
// topology and manifest sources.
type ArchitectureCatalogBuilder interface {
	Build(context.Context, []domain.ArchitectureCatalogSource) (domain.ArchitectureCatalog, error)
}
