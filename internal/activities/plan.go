package activities

import (
	"context"

	"go.temporal.io/sdk/activity"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type SetPlanRunStatusInput struct {
	RunID  string               `json:"run_id"`
	Status domain.PlanRunStatus `json:"status"`
	Error  string               `json:"error,omitempty"`
}

type DispatchPlanTaskInput struct {
	RunID  string `json:"run_id"`
	PlanID string `json:"plan_id"`
	TaskID string `json:"task_id"`
}

type RecordPlanTaskResultInput struct {
	RunID  string            `json:"run_id"`
	Result domain.TaskResult `json:"result"`
}

// PlanActivities persists the durable boundary between the Stage 5 scheduler
// and the Stage 6 task executor. Dispatch makes a task ready; it does not claim
// that an agent has started or completed work.
type PlanActivities struct {
	Plans repository.PlanningRepository
}

func (a PlanActivities) SetPlanRunStatus(ctx context.Context, input SetPlanRunStatusInput) (domain.PlanRun, error) {
	return a.Plans.UpdateRunStatus(ctx, input.RunID, input.Status, input.Error)
}

func (a PlanActivities) DispatchPlanTask(ctx context.Context, input DispatchPlanTaskInput) (domain.Task, error) {
	activity.RecordHeartbeat(ctx, map[string]string{
		"run_id": input.RunID, "plan_id": input.PlanID, "task_id": input.TaskID,
	})
	return a.Plans.MarkTaskReady(ctx, input.RunID, input.TaskID)
}

func (a PlanActivities) RecordPlanTaskResult(ctx context.Context, input RecordPlanTaskResultInput) (domain.Task, error) {
	return a.Plans.RecordTaskResult(ctx, input.RunID, input.Result)
}
