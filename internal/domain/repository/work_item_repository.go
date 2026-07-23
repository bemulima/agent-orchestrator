package repository

import (
	"context"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type WorkItemRepository interface {
	SaveIssueProposals(context.Context, domain.PlanBundle, string, []domain.IssueDraft) ([]domain.WorkItem, error)
	SavePullRequestProposal(context.Context, domain.PlanBundle, domain.Task, string, domain.PullRequestDraft) (domain.WorkItem, error)
	ListPlanWorkItems(context.Context, string) ([]domain.WorkItem, error)
	GetWorkItem(context.Context, string) (domain.WorkItem, error)
	MarkWorkItemPublished(context.Context, string, domain.WorkItemPublication) (domain.WorkItem, error)
}

type ProjectIssueMetadata struct {
	Labels     []string `json:"labels"`
	Milestones []string `json:"milestones"`
	Assignees  []string `json:"assignees"`
	Reviewers  []string `json:"reviewers"`
}

// WorkItemGateway is the only boundary allowed to create external issues or
// pull requests. Coder/reviewer agents never receive this capability.
type WorkItemGateway interface {
	Configured() bool
	DryRun() bool
	Metadata(context.Context, domain.Project) (ProjectIssueMetadata, error)
	GetIssue(context.Context, domain.Project, int64) (domain.WorkItemPublication, error)
	PublishIssue(context.Context, domain.Project, domain.WorkItem) (domain.WorkItemPublication, error)
	PublishBranch(context.Context, domain.Project, string, string) error
	PublishPullRequest(context.Context, domain.Project, domain.WorkItem) (domain.WorkItemPublication, error)
}
