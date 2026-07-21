package repository

import (
	"context"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// ProjectSource resolves and inspects local or managed Git checkouts without
// modifying user-owned repositories.
type ProjectSource interface {
	ConnectLocal(context.Context, string) (domain.RepositorySource, error)
	ConnectGit(context.Context, string) (domain.RepositorySource, error)
	Inspect(context.Context, string) (domain.RepositorySource, error)
}

// DiscoveryScanner reads a repository and returns evidence without writing to
// the repository.
type DiscoveryScanner interface {
	Scan(context.Context, domain.Project, domain.RepositorySource) (domain.DiscoveryReport, error)
}
