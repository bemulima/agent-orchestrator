package repository

import (
	"context"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// UIReadRepository exposes bounded read models for the owner interface.
type UIReadRepository interface {
	Dashboard(context.Context) (domain.Dashboard, error)
	ListPlans(context.Context, domain.PageQuery) ([]domain.PlanSummary, bool, error)
	ListRuns(context.Context, domain.PageQuery) ([]domain.RunSummary, bool, error)
	ListTasks(context.Context, domain.PageQuery) ([]domain.TaskSummary, bool, error)
	ListApprovals(context.Context, domain.PageQuery) ([]domain.ApprovalSummary, bool, error)
	ListActivity(context.Context, domain.PageQuery) ([]domain.ActivityEvent, bool, error)
}
