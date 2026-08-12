package project

import (
	"context"
	"fmt"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type ArchiveProject struct {
	Projects repository.ProjectLifecycleRepository
}

func (uc ArchiveProject) Handle(ctx context.Context, projectID string) (domain.Project, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return domain.Project{}, fmt.Errorf("project id is required: %w", domain.ErrValidation)
	}
	return uc.Projects.Archive(ctx, projectID)
}

type RestoreProject struct {
	Projects repository.ProjectLifecycleRepository
}

func (uc RestoreProject) Handle(ctx context.Context, projectID string) (domain.Project, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return domain.Project{}, fmt.Errorf("project id is required: %w", domain.ErrValidation)
	}
	return uc.Projects.Restore(ctx, projectID)
}
