package activities

import (
	"context"
	"time"

	"go.temporal.io/sdk/activity"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/usecase/shardexecution"
)

type PrepareShardFanoutInput struct {
	Remediation      *shardexecution.RemediationRequest `json:"remediation,omitempty"`
	PlanID           string                             `json:"plan_id"`
	ParallelismLimit int                                `json:"parallelism_limit"`
}

type RunShardWorkerInput struct {
	Prepared shardexecution.PreparedShard `json:"prepared"`
}

type BlockShardFanoutInput struct {
	PlanID  string   `json:"plan_id"`
	Reasons []string `json:"reasons"`
}

type ShardFanoutActivities struct {
	Execution shardexecution.Service
}

func (a ShardFanoutActivities) PrepareShardFanout(ctx context.Context, input PrepareShardFanoutInput) (shardexecution.PreparedFanout, error) {
	activity.RecordHeartbeat(ctx, map[string]string{"plan_id": input.PlanID, "phase": "validate_and_prepare"})
	if input.Remediation != nil {
		return a.Execution.PrepareRemediation(ctx, input.PlanID, input.ParallelismLimit, *input.Remediation)
	}
	return a.Execution.Prepare(ctx, input.PlanID, input.ParallelismLimit)
}

func (a ShardFanoutActivities) RunShardWorker(ctx context.Context, input RunShardWorkerInput) (domain.ShardAttempt, error) {
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				activity.RecordHeartbeat(ctx, map[string]string{
					"shard_id": input.Prepared.WorkPackage.ShardID,
					"route":    input.Prepared.WorkPackage.Route,
				})
			}
		}
	}()
	activity.RecordHeartbeat(ctx, map[string]string{
		"shard_id": input.Prepared.WorkPackage.ShardID,
		"route":    input.Prepared.WorkPackage.Route,
		"phase":    "worker_start",
	})
	return a.Execution.Execute(ctx, input.Prepared)
}

func (a ShardFanoutActivities) EvaluateShardBarrier(ctx context.Context, prepared shardexecution.PreparedFanout) (domain.ShardFanoutExecution, error) {
	activity.RecordHeartbeat(ctx, map[string]string{"plan_id": prepared.PlanID, "phase": "barrier"})
	return a.Execution.Barrier(ctx, prepared)
}

func (a ShardFanoutActivities) ResolveCompositionBoundary(ctx context.Context, prepared shardexecution.PreparedFanout) (domain.ShardFanoutExecution, error) {
	activity.RecordHeartbeat(ctx, map[string]string{"plan_id": prepared.PlanID, "phase": "serialized_composition_boundary"})
	return a.Execution.ResolveCompositionBoundary(ctx, prepared)
}

func (a ShardFanoutActivities) BlockShardFanout(ctx context.Context, input BlockShardFanoutInput) (domain.ShardFanoutExecution, error) {
	activity.RecordHeartbeat(ctx, map[string]string{"plan_id": input.PlanID, "phase": "persist_failure_barrier"})
	return a.Execution.BlockFanout(ctx, input.PlanID, input.Reasons)
}

func (a ShardFanoutActivities) AssembleShardCommits(ctx context.Context, prepared shardexecution.PreparedFanout) (domain.ShardFanoutExecution, error) {
	activity.RecordHeartbeat(ctx, map[string]string{"plan_id": prepared.PlanID, "phase": "deterministic_assembly"})
	return a.Execution.Assemble(ctx, prepared)
}

func (a ShardFanoutActivities) VerifyShardIntegration(ctx context.Context, prepared shardexecution.PreparedFanout) (domain.ShardFanoutExecution, error) {
	activity.RecordHeartbeat(ctx, map[string]string{"plan_id": prepared.PlanID, "phase": "integration_verification"})
	return a.Execution.VerifyIntegration(ctx, prepared)
}

func (a ShardFanoutActivities) ReviewShardIntegration(ctx context.Context, prepared shardexecution.PreparedFanout) (domain.ShardFanoutExecution, error) {
	stop := startShardHeartbeats(ctx, 20*time.Second, func() {
		activity.RecordHeartbeat(ctx, map[string]string{"plan_id": prepared.PlanID, "phase": "independent_review"})
	})
	defer stop()
	activity.RecordHeartbeat(ctx, map[string]string{"plan_id": prepared.PlanID, "phase": "independent_review"})
	return a.Execution.ReviewIntegration(ctx, prepared)
}

// startShardHeartbeats keeps long activity calls alive and joins the heartbeat
// goroutine on return, so it cannot outlive the review or its cancelled context.
func startShardHeartbeats(ctx context.Context, interval time.Duration, heartbeat func()) func() {
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				if loopCtx.Err() != nil {
					return
				}
				heartbeat()
			}
		}
	}()
	return func() { cancel(); <-done }
}

func (a ShardFanoutActivities) AssembleWorkersForComposition(ctx context.Context, prepared shardexecution.PreparedFanout) (domain.ShardFanoutExecution, error) {
	activity.RecordHeartbeat(ctx, map[string]string{"plan_id": prepared.PlanID, "phase": "composition_input_assembly"})
	return a.Execution.AssembleWorkersForComposition(ctx, prepared)
}

func (a ShardFanoutActivities) RunSerializedComposition(ctx context.Context, prepared shardexecution.PreparedFanout) (domain.ShardFanoutExecution, error) {
	stop := startShardHeartbeats(ctx, 20*time.Second, func() {
		activity.RecordHeartbeat(ctx, map[string]string{"plan_id": prepared.PlanID, "phase": "serialized_composition"})
	})
	defer stop()
	activity.RecordHeartbeat(ctx, map[string]string{"plan_id": prepared.PlanID, "phase": "serialized_composition"})
	return a.Execution.ExecuteComposition(ctx, prepared)
}
