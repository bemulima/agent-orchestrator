package workflow

import (
	"context"
	"github.com/bemulima/agent-orchestrator/internal/activities"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/usecase/shardexecution"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"testing"
)

func TestCompositionIsSerializedAfterBarrierAndBeforeFinalAssembly(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	fake := &compositionOrderActivities{t: t}
	env.RegisterActivity(fake)
	env.ExecuteWorkflow(ShardFanoutWorkflow, ShardFanoutInput{PlanID: "plan", ParallelismLimit: 3})
	require.NoError(t, env.GetWorkflowError())
	var out ShardFanoutOutput
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Equal(t, "INTEGRATION_VERIFIED", out.State)
	require.Equal(t, "VERIFIED", out.Composition)
	require.Equal(t, []string{"barrier", "worker_assembly", "composition", "final_assembly", "mechanical", "review"}, fake.events)
}

type compositionOrderActivities struct {
	parallelShardActivities
	t           *testing.T
	events      []string
	composition string
}

func (a *compositionOrderActivities) PrepareShardFanout(ctx context.Context, in activities.PrepareShardFanoutInput) (shardexecution.PreparedFanout, error) {
	p, e := a.parallelShardActivities.PrepareShardFanout(ctx, in)
	p.CompositionRequired = true
	return p, e
}
func (a *compositionOrderActivities) EvaluateShardBarrier(_ context.Context, p shardexecution.PreparedFanout) (domain.ShardFanoutExecution, error) {
	require.Equal(a.t, 3, a.finishedCount())
	a.events = append(a.events, "barrier")
	return domain.ShardFanoutExecution{State: "BARRIER_READY", Barrier: domain.ShardBarrierReady, Composition: "COMPOSITION_REQUIRED"}, nil
}
func (a *compositionOrderActivities) AssembleWorkersForComposition(_ context.Context, p shardexecution.PreparedFanout) (domain.ShardFanoutExecution, error) {
	require.Equal(a.t, []string{"barrier"}, a.events)
	a.events = append(a.events, "worker_assembly")
	return domain.ShardFanoutExecution{State: "WORKERS_ASSEMBLED", Barrier: domain.ShardBarrierReady, Composition: "COMPOSITION_REQUIRED"}, nil
}
func (a *compositionOrderActivities) RunSerializedComposition(_ context.Context, p shardexecution.PreparedFanout) (domain.ShardFanoutExecution, error) {
	a.mu.Lock()
	require.Zero(a.t, a.active)
	a.mu.Unlock()
	require.Equal(a.t, 3, a.finishedCount())
	a.events = append(a.events, "composition")
	a.composition = "VERIFIED"
	return domain.ShardFanoutExecution{State: "COMPOSITION_VERIFIED", Barrier: domain.ShardBarrierReady, Composition: "VERIFIED"}, nil
}
func (a *compositionOrderActivities) AssembleShardCommits(_ context.Context, p shardexecution.PreparedFanout) (domain.ShardFanoutExecution, error) {
	require.Equal(a.t, "VERIFIED", a.composition)
	a.events = append(a.events, "final_assembly")
	return domain.ShardFanoutExecution{State: "ASSEMBLED", Barrier: domain.ShardBarrierReady, Composition: "VERIFIED"}, nil
}
func (a *compositionOrderActivities) VerifyShardIntegration(_ context.Context, p shardexecution.PreparedFanout) (domain.ShardFanoutExecution, error) {
	a.events = append(a.events, "mechanical")
	return domain.ShardFanoutExecution{State: "INTEGRATION_MECHANICALLY_VERIFIED", Barrier: domain.ShardBarrierReady, Composition: "VERIFIED"}, nil
}
func (a *compositionOrderActivities) ReviewShardIntegration(_ context.Context, p shardexecution.PreparedFanout) (domain.ShardFanoutExecution, error) {
	a.events = append(a.events, "review")
	return domain.ShardFanoutExecution{State: "INTEGRATION_VERIFIED", Barrier: domain.ShardBarrierReady, Composition: "VERIFIED", ReviewerVerdict: "PASS"}, nil
}
