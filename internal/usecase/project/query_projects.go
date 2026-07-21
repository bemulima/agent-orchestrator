package project

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type GetProject struct {
	Projects repository.ProjectRepository
}

func (uc GetProject) Handle(ctx context.Context, identifier string) (domain.Project, error) {
	identifier = strings.TrimSpace(identifier)
	if _, err := uuid.Parse(identifier); err == nil {
		return uc.Projects.Get(ctx, identifier)
	}
	return uc.Projects.GetByName(ctx, identifier)
}

type ListProjects struct {
	Projects repository.ProjectRepository
}

func (uc ListProjects) Handle(ctx context.Context) ([]domain.Project, error) {
	return uc.Projects.List(ctx)
}

type LatestDiscoveryResult struct {
	Snapshot domain.ServiceSnapshot `json:"snapshot"`
	Report   domain.DiscoveryReport `json:"report"`
}

type GetLatestDiscoveryReport struct {
	Projects repository.ProjectRepository
}

func (uc GetLatestDiscoveryReport) Handle(ctx context.Context, projectID string) (LatestDiscoveryResult, error) {
	snapshot, report, err := uc.Projects.GetLatestDiscovery(ctx, projectID)
	if err != nil {
		return LatestDiscoveryResult{}, err
	}
	return LatestDiscoveryResult{Snapshot: snapshot, Report: report}, nil
}
