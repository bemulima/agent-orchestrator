package temporal

import (
	"context"
	"fmt"
	"strings"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
	"github.com/bemulima/agent-orchestrator/internal/usecase/shardexecution"
	orchestratorworkflow "github.com/bemulima/agent-orchestrator/internal/workflow"
	"github.com/google/uuid"
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

func (r PlanRunner) StartShardFanout(ctx context.Context, planID string, parallelismLimit int) (string, string, error) {
	if strings.TrimSpace(planID) == "" || parallelismLimit < 2 {
		return "", "", fmt.Errorf("shard fan-out requires plan ID and parallelism of at least two: %w", domain.ErrValidation)
	}
	workflowID := "shard-fanout-" + planID
	workflowRun, err := r.Client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: workflowID, TaskQueue: r.TaskQueue,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}, orchestratorworkflow.ShardFanoutWorkflow, orchestratorworkflow.ShardFanoutInput{
		PlanID: planID, ParallelismLimit: parallelismLimit,
	})
	if err != nil {
		return "", "", fmt.Errorf("start shard fan-out workflow: %w", err)
	}
	return workflowID, workflowRun.GetRunID(), nil
}

// StartShardRemediation starts a new logical execution while retaining the Plan
// and frozen baseline. Reusing the request ID is idempotent at Temporal admission.
func (r PlanRunner) StartShardRemediation(ctx context.Context, planID string, parallelismLimit int, request shardexecution.RemediationRequest) (string, string, error) {
	if strings.TrimSpace(planID) == "" || parallelismLimit < 2 || strings.TrimSpace(request.ID) == "" {
		return "", "", fmt.Errorf("remediation requires plan, request and concurrency: %w", domain.ErrValidation)
	}
	requestIdentity := uuid.NewSHA1(uuid.NameSpaceOID, []byte(request.ID)).String()
	workflowID := "shard-remediation-" + planID + "-" + requestIdentity
	workflowRun, err := r.Client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: workflowID, TaskQueue: r.TaskQueue,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}, orchestratorworkflow.ShardFanoutWorkflow, orchestratorworkflow.ShardFanoutInput{PlanID: planID, ParallelismLimit: parallelismLimit, Remediation: &request})
	if err != nil {
		return "", "", fmt.Errorf("start shard remediation: %w", err)
	}
	return workflowID, workflowRun.GetRunID(), nil
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

func (r PlanRunner) RetryTask(ctx context.Context, workflowID, taskID string) error {
	if taskID == "" {
		return fmt.Errorf("task ID is required: %w", domain.ErrValidation)
	}
	if err := r.Client.SignalWorkflow(ctx, workflowID, "", orchestratorworkflow.PlanTaskRetrySignal, taskID); err != nil {
		return fmt.Errorf("signal plan task retry: %w", err)
	}
	return nil
}

var _ repository.PlanRunner = PlanRunner{}
