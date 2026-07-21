package repository

import (
	"context"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type TopologyRepository interface {
	Replace(context.Context, domain.TopologyCatalog) (domain.TopologyCatalog, error)
	Get(context.Context) (domain.TopologyCatalog, error)
}

type TopologyBuilder interface {
	Build(context.Context, []domain.TopologySource) (domain.TopologyCatalog, error)
}
