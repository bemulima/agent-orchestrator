package project

import (
	"context"
	"fmt"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type ConnectInput struct {
	LocalPath      string                `json:"path,omitempty"`
	GitURL         string                `json:"git_url,omitempty"`
	RepositoryRole domain.RepositoryRole `json:"repository_role,omitempty"`
}

type ConnectResult struct {
	Project  domain.Project         `json:"project"`
	Snapshot domain.ServiceSnapshot `json:"snapshot"`
	Report   domain.DiscoveryReport `json:"report"`
}

// ConnectProject validates a source, persists it idempotently, and immediately
// runs the required read-only discovery scan.
type ConnectProject struct {
	Projects repository.ProjectRepository
	Sources  repository.ProjectSource
	Scan     ScanProject
}

func (uc ConnectProject) Handle(ctx context.Context, input ConnectInput) (ConnectResult, error) {
	input.LocalPath = strings.TrimSpace(input.LocalPath)
	input.GitURL = strings.TrimSpace(input.GitURL)
	if (input.LocalPath == "") == (input.GitURL == "") {
		return ConnectResult{}, fmt.Errorf("exactly one of path or git_url is required: %w", domain.ErrValidation)
	}
	role := input.RepositoryRole
	if role == "" {
		role = domain.RepositoryRoleService
	}
	if !validRepositoryRole(role) {
		return ConnectResult{}, fmt.Errorf("unsupported repository role %q: %w", role, domain.ErrValidation)
	}

	var source domain.RepositorySource
	var err error
	if input.LocalPath != "" {
		source, err = uc.Sources.ConnectLocal(ctx, input.LocalPath)
	} else {
		source, err = uc.Sources.ConnectGit(ctx, input.GitURL)
	}
	if err != nil {
		return ConnectResult{}, err
	}
	localPath := source.LocalPath
	var gitURL *string
	if source.GitURL != "" {
		value := source.GitURL
		gitURL = &value
	}
	project, err := uc.Projects.Upsert(ctx, domain.Project{
		Name:           source.Name,
		Status:         domain.ProjectStatusConnected,
		RepositoryRole: role,
		SourceIdentity: source.Identity,
		LocalPath:      &localPath,
		GitURL:         gitURL,
		DefaultBranch:  source.DefaultBranch,
		CurrentBranch:  source.CurrentBranch,
		HeadCommit:     source.HeadCommit,
		IsDirty:        source.IsDirty,
	})
	if err != nil {
		return ConnectResult{}, err
	}
	project, snapshot, report, err := uc.Scan.handleSource(ctx, project, source)
	if err != nil {
		return ConnectResult{}, err
	}
	return ConnectResult{Project: project, Snapshot: snapshot, Report: report}, nil
}

func validRepositoryRole(role domain.RepositoryRole) bool {
	switch role {
	case domain.RepositoryRoleService,
		domain.RepositoryRoleFrontend,
		domain.RepositoryRoleInfrastructure,
		domain.RepositoryRoleContent,
		domain.RepositoryRolePolicy,
		domain.RepositoryRoleDocumentation,
		domain.RepositoryRoleArchive,
		domain.RepositoryRoleUnknown:
		return true
	default:
		return false
	}
}
