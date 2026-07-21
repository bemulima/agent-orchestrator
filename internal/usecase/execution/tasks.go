package execution

import (
	"context"
	"fmt"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type GetAttempts struct {
	Tasks repository.TaskExecutionRepository
}

func (uc GetAttempts) Handle(ctx context.Context, taskID string) ([]domain.TaskAttempt, error) {
	return uc.Tasks.ListAttempts(ctx, strings.TrimSpace(taskID))
}

type GetArtifacts struct {
	Tasks repository.TaskExecutionRepository
}

func (uc GetArtifacts) Handle(ctx context.Context, taskID string) ([]domain.Artifact, error) {
	return uc.Tasks.ListArtifacts(ctx, strings.TrimSpace(taskID))
}

type TaskLog struct {
	Tasks repository.TaskExecutionRepository
}

type TaskLogResult struct {
	TaskID    string               `json:"task_id"`
	Attempts  []domain.TaskAttempt `json:"attempts"`
	Artifacts []domain.Artifact    `json:"artifacts"`
}

func (uc TaskLog) Handle(ctx context.Context, taskID string) (TaskLogResult, error) {
	taskID = strings.TrimSpace(taskID)
	attempts, err := uc.Tasks.ListAttempts(ctx, taskID)
	if err != nil {
		return TaskLogResult{}, err
	}
	artifacts, err := uc.Tasks.ListArtifacts(ctx, taskID)
	if err != nil {
		return TaskLogResult{}, err
	}
	return TaskLogResult{TaskID: taskID, Attempts: attempts, Artifacts: artifacts}, nil
}

type RetryTask struct {
	Plans       repository.PlanningRepository
	Tasks       repository.TaskExecutionRepository
	Runner      repository.PlanRunner
	MaxAttempts int
}

func (uc RetryTask) Handle(ctx context.Context, taskID string) (domain.Task, error) {
	task, err := uc.Plans.GetTask(ctx, strings.TrimSpace(taskID))
	if err != nil {
		return domain.Task{}, err
	}
	if task.Status != domain.TaskStatusBlocked && task.Status != domain.TaskStatusChangesRequested {
		return domain.Task{}, fmt.Errorf("task cannot be retried from %s: %w", task.Status, domain.ErrInvalidStatus)
	}
	attempts, err := uc.Tasks.ListAttempts(ctx, task.ID)
	if err != nil {
		return domain.Task{}, err
	}
	maxAttempts := uc.MaxAttempts
	if maxAttempts < 1 || maxAttempts > 3 {
		maxAttempts = 3
	}
	if len(attempts) >= maxAttempts {
		return domain.Task{}, fmt.Errorf("task reached maximum of %d attempts: %w", maxAttempts, domain.ErrInvalidStatus)
	}
	bundle, err := uc.Plans.GetPlan(ctx, task.PlanID)
	if err != nil {
		return domain.Task{}, err
	}
	if bundle.Run == nil || bundle.Run.Status != domain.PlanRunStatusPaused && bundle.Run.Status != domain.PlanRunStatusRunning {
		return domain.Task{}, fmt.Errorf("task plan has no retryable workflow: %w", domain.ErrInvalidStatus)
	}
	if err := uc.Runner.RetryTask(ctx, bundle.Run.WorkflowID, task.ID); err != nil {
		return domain.Task{}, err
	}
	return task, nil
}
