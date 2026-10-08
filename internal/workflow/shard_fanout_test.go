package workflow

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"github.com/bemulima/agent-orchestrator/internal/activities"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/usecase/shardexecution"
)

func TestShardFanoutWorkflowSchedulesIndependentWorkersConcurrently(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	environment := suite.NewTestWorkflowEnvironment()
	fake := &parallelShardActivities{}
	environment.RegisterActivity(fake)
	environment.ExecuteWorkflow(ShardFanoutWorkflow, ShardFanoutInput{PlanID: "plan-1", ParallelismLimit: 3})
	require.True(t, environment.IsWorkflowCompleted())
	require.NoError(t, environment.GetWorkflowError())
	var output ShardFanoutOutput
	require.NoError(t, environment.GetWorkflowResult(&output))
	require.Equal(t, domain.ShardBarrierBlocked, output.Barrier)
	require.GreaterOrEqual(t, fake.maximum(), 2, "Temporal workflow executed independent activities serially")
	require.Equal(t, 3, fake.finishedCount())
}

type parallelShardActivities struct {
	mu            sync.Mutex
	active        int
	maximumActive int
	finished      int
}

func (a *parallelShardActivities) PrepareShardFanout(_ context.Context, input activities.PrepareShardFanoutInput) (shardexecution.PreparedFanout, error) {
	workers := []shardexecution.PreparedShard{}
	for index, route := range []string{"backend.infrastructure.persistence", "backend.transport.http", "backend.usecase"} {
		workers = append(workers, shardexecution.PreparedShard{WorkPackage: domain.WorkPackage{
			ShardID: fmt.Sprintf("shard-%d", index), Route: route,
			ExecutionBase: domain.ShardExecutionBase{Revision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		}})
	}
	return shardexecution.PreparedFanout{PlanID: input.PlanID, ParallelismLimit: input.ParallelismLimit,
		BaselineCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Workers: workers}, nil
}

func (a *parallelShardActivities) RunShardWorker(_ context.Context, input activities.RunShardWorkerInput) (domain.ShardAttempt, error) {
	a.mu.Lock()
	a.active++
	if a.active > a.maximumActive {
		a.maximumActive = a.active
	}
	a.mu.Unlock()
	time.Sleep(60 * time.Millisecond)
	a.mu.Lock()
	a.active--
	a.finished++
	a.mu.Unlock()
	return domain.ShardAttempt{ShardID: input.Prepared.WorkPackage.ShardID, Status: domain.ShardAttemptBlocked}, nil
}

func (a *parallelShardActivities) EvaluateShardBarrier(_ context.Context, prepared shardexecution.PreparedFanout) (domain.ShardFanoutExecution, error) {
	return domain.ShardFanoutExecution{PlanID: prepared.PlanID, State: "BARRIER_BLOCKED", Barrier: domain.ShardBarrierBlocked,
		BaselineCommit: prepared.BaselineCommit, Composition: "PENDING"}, nil
}

func (a *parallelShardActivities) maximum() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.maximumActive
}

func (a *parallelShardActivities) finishedCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.finished
}
