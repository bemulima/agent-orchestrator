package activities

import (
	"context"
	"time"

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

type ExecutePlanTaskInput struct {
	RunID      string `json:"run_id"`
	PlanID     string `json:"plan_id"`
	TaskID     string `json:"task_id"`
	WorkflowID string `json:"workflow_id"`
}

type RetryPlanTaskInput struct {
	TaskID      string `json:"task_id"`
	MaxAttempts int    `json:"max_attempts"`
}

type TaskExecutor interface {
	Execute(context.Context, string, string) (domain.TaskExecutionOutcome, error)
}

// PlanActivities persists the durable boundary between the Stage 5 scheduler
// and the Stage 6 task executor. Dispatch makes a task ready; it does not claim
// that an agent has started or completed work.
type PlanActivities struct {
	Plans          repository.PlanningRepository
	TaskExecutions repository.TaskExecutionRepository
	Executor       TaskExecutor
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

func (a PlanActivities) ExecutePlanTask(
	ctx context.Context,
	input ExecutePlanTaskInput,
) (domain.TaskExecutionOutcome, error) {
	activity.RecordHeartbeat(ctx, map[string]string{
		"run_id": input.RunID, "plan_id": input.PlanID, "task_id": input.TaskID,
	})
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				activity.RecordHeartbeat(ctx, map[string]string{"task_id": input.TaskID})
			}
		}
	}()
	return a.Executor.Execute(ctx, input.TaskID, input.WorkflowID)
}

func (a PlanActivities) RetryPlanTask(ctx context.Context, input RetryPlanTaskInput) (domain.Task, error) {
	return a.TaskExecutions.ResetTaskForRetry(ctx, input.TaskID, input.MaxAttempts)
}
