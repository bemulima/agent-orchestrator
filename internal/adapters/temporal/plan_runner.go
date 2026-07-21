package temporal

import (
	"context"
	"fmt"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
	orchestratorworkflow "github.com/bemulima/agent-orchestrator/internal/workflow"
)

type PlanRunner struct {
	Client    client.Client
	TaskQueue string
}

func (r PlanRunner) Start(ctx context.Context, run domain.PlanRun, schedule domain.PlanSchedule) (string, error) {
	workflowRun, err := r.Client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: run.WorkflowID, TaskQueue: r.TaskQueue,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}, orchestratorworkflow.PlanWorkflow, schedule)
	if err != nil {
		return "", fmt.Errorf("start plan workflow: %w", err)
	}
	return workflowRun.GetRunID(), nil
}

func (r PlanRunner) Control(ctx context.Context, workflowID string, action domain.RunControlAction) error {
	var signal string
	switch action {
	case domain.RunControlPause:
		signal = orchestratorworkflow.PlanPauseSignal
	case domain.RunControlResume:
		signal = orchestratorworkflow.PlanResumeSignal
	case domain.RunControlCancel:
		signal = orchestratorworkflow.PlanCancelSignal
	default:
		return fmt.Errorf("unknown plan control action %q: %w", action, domain.ErrValidation)
	}
	if err := r.Client.SignalWorkflow(ctx, workflowID, "", signal, true); err != nil {
		return fmt.Errorf("signal plan workflow %s: %w", action, err)
	}
	return nil
}

func (r PlanRunner) ReportTaskResult(ctx context.Context, workflowID string, result domain.TaskResult) error {
	if err := r.Client.SignalWorkflow(ctx, workflowID, "", orchestratorworkflow.PlanTaskResultSignal, result); err != nil {
		return fmt.Errorf("signal plan task result: %w", err)
	}
	return nil
}

var _ repository.PlanRunner = PlanRunner{}
