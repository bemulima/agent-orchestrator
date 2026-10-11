package activities

import (
	"context"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"go.temporal.io/sdk/activity"
)

// Bind directly observed Temporal identity before entering a use case. The
// payload cannot substitute a workflow, run, activity or retry identity.
func bindAgentRuntimeContext(ctx context.Context) context.Context {
	if !activity.IsActivity(ctx) {
		return ctx
	}
	info := activity.GetInfo(ctx)
	return domain.WithAgentRuntimeContext(ctx, domain.AgentRuntimeContext{
		WorkflowID: info.WorkflowExecution.ID, WorkflowRunID: info.WorkflowExecution.RunID,
		ActivityID: info.ActivityID, ActivityAttempt: info.Attempt,
	})
}
