package workflow

import (
	"fmt"
	"sort"
	"time"

	"go.temporal.io/sdk/temporal"
	temporalworkflow "go.temporal.io/sdk/workflow"

	"github.com/bemulima/agent-orchestrator/internal/activities"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/usecase/shardexecution"
)

type ShardFanoutInput struct {
	Remediation      *shardexecution.RemediationRequest `json:"remediation,omitempty"`
	PlanID           string                             `json:"plan_id"`
	ParallelismLimit int                                `json:"parallelism_limit"`
}

type ShardFanoutOutput struct {
	PlanID                 string                   `json:"plan_id"`
	State                  string                   `json:"state"`
	Barrier                domain.ShardBarrierState `json:"barrier"`
	Attempts               []domain.ShardAttempt    `json:"attempts"`
	MaxSimultaneousWorkers int                      `json:"max_simultaneous_workers"`
	Composition            string                   `json:"composition"`
	AssemblyCommit         string                   `json:"assembly_commit,omitempty"`
	ReviewerThreadID       string                   `json:"reviewer_thread_id,omitempty"`
	ReviewerVerdict        string                   `json:"reviewer_verdict,omitempty"`
	Error                  string                   `json:"error,omitempty"`
}

type shardWorkerCompletion struct {
	index int
	err   string
}

func ShardFanoutWorkflow(ctx temporalworkflow.Context, input ShardFanoutInput) (ShardFanoutOutput, error) {
	if input.PlanID == "" || input.ParallelismLimit < 2 {
		return ShardFanoutOutput{}, fmt.Errorf("shard fan-out needs an approved plan ID and concurrency of at least two: %w", domain.ErrValidation)
	}
	prepareCtx := temporalworkflow.WithActivityOptions(ctx, temporalworkflow.ActivityOptions{
		StartToCloseTimeout: 20 * time.Minute, HeartbeatTimeout: time.Minute,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: time.Second, BackoffCoefficient: 2, MaximumInterval: 10 * time.Second, MaximumAttempts: 3},
	})
	workerCtx := temporalworkflow.WithActivityOptions(ctx, temporalworkflow.ActivityOptions{
		StartToCloseTimeout: 4 * time.Hour, HeartbeatTimeout: time.Minute,
		WaitForCancellation: true,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	finalizeCtx := temporalworkflow.WithActivityOptions(ctx, temporalworkflow.ActivityOptions{
		StartToCloseTimeout: 40 * time.Minute, HeartbeatTimeout: time.Minute,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: time.Second, BackoffCoefficient: 2, MaximumInterval: 10 * time.Second, MaximumAttempts: 2},
	})

	var prepared shardexecution.PreparedFanout
	if err := temporalworkflow.ExecuteActivity(prepareCtx, "PrepareShardFanout", activities.PrepareShardFanoutInput{
		PlanID: input.PlanID, ParallelismLimit: input.ParallelismLimit, Remediation: input.Remediation,
	}).Get(prepareCtx, &prepared); err != nil {
		return ShardFanoutOutput{PlanID: input.PlanID, State: "BARRIER_BLOCKED", Barrier: domain.ShardBarrierBlocked, Error: err.Error()}, nil
	}
	if len(prepared.Workers) < 2 || prepared.ParallelismLimit < 2 {
		return ShardFanoutOutput{PlanID: input.PlanID, State: "BARRIER_BLOCKED", Barrier: domain.ShardBarrierBlocked,
			Error: "approved fan-out has fewer than two independent workers or an insufficient concurrency limit"}, nil
	}
	workers := append([]shardexecution.PreparedShard(nil), prepared.Workers...)
	sort.Slice(workers, func(i, j int) bool {
		if workers[i].WorkPackage.Route != workers[j].WorkPackage.Route {
			return workers[i].WorkPackage.Route < workers[j].WorkPackage.Route
		}
		return workers[i].WorkPackage.ShardID < workers[j].WorkPackage.ShardID
	})
	futures := make([]temporalworkflow.Future, len(workers))
	for index, worker := range workers {
		// Every independent activity is scheduled before any result is awaited;
		// Temporal's existing worker concurrency cap controls actual admission.
		futures[index] = temporalworkflow.ExecuteActivity(workerCtx, "RunShardWorker", activities.RunShardWorkerInput{Prepared: worker})
	}
	completionChannel := temporalworkflow.NewChannel(ctx)
	for index, future := range futures {
		index, future := index, future
		temporalworkflow.Go(ctx, func(coroCtx temporalworkflow.Context) {
			var attempt domain.ShardAttempt
			err := future.Get(coroCtx, &attempt)
			completion := shardWorkerCompletion{index: index}
			if err != nil {
				completion.err = err.Error()
			}
			completionChannel.Send(coroCtx, completion)
		})
	}
	workerErrors := []string{}
	for range futures {
		var completion shardWorkerCompletion
		completionChannel.Receive(ctx, &completion)
		if completion.err != "" {
			workerErrors = append(workerErrors, workers[completion.index].WorkPackage.Route+": "+completion.err)
		}
	}

	var execution domain.ShardFanoutExecution
	if err := temporalworkflow.ExecuteActivity(finalizeCtx, "EvaluateShardBarrier", prepared).Get(finalizeCtx, &execution); err != nil {
		return ShardFanoutOutput{PlanID: input.PlanID, State: "BARRIER_BLOCKED", Barrier: domain.ShardBarrierBlocked,
			Error: err.Error()}, nil
	}
	if execution.Barrier != domain.ShardBarrierReady {
		return fanoutOutput(execution, joinErrors(workerErrors)), nil
	}
	if len(workerErrors) > 0 {
		if err := temporalworkflow.ExecuteActivity(finalizeCtx, "BlockShardFanout", activities.BlockShardFanoutInput{
			PlanID: input.PlanID, Reasons: workerErrors,
		}).Get(finalizeCtx, &execution); err != nil {
			return fanoutOutput(execution, err.Error()), nil
		}
		return fanoutOutput(execution, joinErrors(workerErrors)), nil
	}
	if prepared.CompositionRequired {
		if err := temporalworkflow.ExecuteActivity(finalizeCtx, "AssembleWorkersForComposition", prepared).Get(finalizeCtx, &execution); err != nil {
			return fanoutOutput(execution, err.Error()), nil
		}
		if execution.State != "WORKERS_ASSEMBLED" {
			return fanoutOutput(execution, "composition input assembly failed"), nil
		}
		if err := temporalworkflow.ExecuteActivity(workerCtx, "RunSerializedComposition", prepared).Get(workerCtx, &execution); err != nil {
			return fanoutOutput(execution, err.Error()), nil
		}
		if execution.Composition != "VERIFIED" {
			return fanoutOutput(execution, "serialized composition not verified"), nil
		}
	} else if execution.Composition != "SKIPPED_NOT_REQUIRED" {
		return fanoutOutput(execution, "composition state was not resolved after the barrier"), nil
	}
	if err := temporalworkflow.ExecuteActivity(finalizeCtx, "AssembleShardCommits", prepared).Get(finalizeCtx, &execution); err != nil {
		return fanoutOutput(execution, err.Error()), nil
	}
	if execution.State != "ASSEMBLED" {
		return fanoutOutput(execution, joinErrors(workerErrors)), nil
	}
	if err := temporalworkflow.ExecuteActivity(finalizeCtx, "VerifyShardIntegration", prepared).Get(finalizeCtx, &execution); err != nil {
		return fanoutOutput(execution, err.Error()), nil
	}
	if execution.State != "INTEGRATION_MECHANICALLY_VERIFIED" {
		return fanoutOutput(execution, joinErrors(workerErrors)), nil
	}
	if err := temporalworkflow.ExecuteActivity(finalizeCtx, "ReviewShardIntegration", prepared).Get(finalizeCtx, &execution); err != nil {
		return fanoutOutput(execution, err.Error()), nil
	}
	return fanoutOutput(execution, joinErrors(workerErrors)), nil
}

func fanoutOutput(execution domain.ShardFanoutExecution, message string) ShardFanoutOutput {
	return ShardFanoutOutput{
		PlanID: execution.PlanID, State: execution.State, Barrier: execution.Barrier,
		Attempts: execution.Attempts, MaxSimultaneousWorkers: execution.MaxSimultaneousWorkers,
		Composition: execution.Composition, AssemblyCommit: execution.AssemblyCommit,
		ReviewerThreadID: execution.ReviewerThreadID, ReviewerVerdict: execution.ReviewerVerdict,
		Error: message,
	}
}

func joinErrors(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return fmt.Sprint(values)
}
