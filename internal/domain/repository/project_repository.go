package repository

import (
	"context"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// ProjectRepository persists connected repositories and immutable discovery
// snapshots. Implementations must make Upsert idempotent by SourceIdentity.
type ProjectRepository interface {
	Upsert(context.Context, domain.Project) (domain.Project, error)
	Get(context.Context, string) (domain.Project, error)
	GetByName(context.Context, string) (domain.Project, error)
	List(context.Context) ([]domain.Project, error)
	UpdateSourceState(context.Context, string, domain.ProjectStatus, domain.RepositorySource) (domain.Project, error)
	UpdateStatus(context.Context, string, domain.ProjectStatus) error
	SaveDiscovery(context.Context, domain.Project, domain.ServiceSnapshot, domain.DiscoveryReport) (domain.ServiceSnapshot, error)
	GetLatestDiscovery(context.Context, string) (domain.ServiceSnapshot, domain.DiscoveryReport, error)
}
