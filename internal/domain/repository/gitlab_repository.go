package repository

import (
	"context"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// GitLabGateway is deliberately limited to issues, notes, issue links,
// branches, and merge requests. It has no merge or deploy operation.
type GitLabGateway interface {
	Configured() bool
	DryRun() bool
	ResolveProject(context.Context, string) (domain.GitLabProject, error)
	ResolveConnectedProject(context.Context, domain.Project) (domain.GitLabProject, error)
	EnsureIssue(context.Context, domain.GitLabIssueSpec) (domain.GitLabIssue, error)
	EnsureComment(context.Context, domain.GitLabIssue, string, string) error
	EnsureIssueLink(context.Context, domain.GitLabIssue, domain.GitLabIssue) error
	PushBranch(context.Context, domain.Project, string, string) error
	EnsureMergeRequest(context.Context, domain.GitLabMergeRequestSpec) (domain.GitLabMergeRequest, error)
}

type GitLabLinkRepository interface {
	GetGitLabLink(context.Context, string, string, int64) (domain.GitLabLink, error)
	SaveGitLabLink(context.Context, domain.GitLabLink) (domain.GitLabLink, error)
	ListGitLabLinksForPlan(context.Context, string) ([]domain.GitLabLink, error)
	ApplyGitLabWebhook(context.Context, domain.GitLabWebhookEvent) (domain.GitLabWebhookResult, error)
}
