package workflow

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"github.com/bemulima/agent-orchestrator/internal/activities"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestPlanWorkflowRespectsDependenciesAndParallelLimit(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	environment := suite.NewTestWorkflowEnvironment()
	environment.RegisterActivity(&activities.PlanActivities{})
	registerPlanStatusMocks(environment)
	var mu sync.Mutex
	dispatched := make([]string, 0)
	environment.OnActivity("DispatchPlanTask", mock.Anything, mock.Anything).Return(
		func(_ context.Context, input activities.DispatchPlanTaskInput) (domain.Task, error) {
			mu.Lock()
			dispatched = append(dispatched, input.TaskID)
			mu.Unlock()
			return domain.Task{ID: input.TaskID, Status: domain.TaskStatusReady}, nil
		},
	)
	environment.RegisterDelayedCallback(func() {
		environment.SignalWorkflow(PlanTaskResultSignal, domain.TaskResult{TaskID: "producer", Status: domain.TaskStatusCompleted})
	}, time.Second)
	environment.RegisterDelayedCallback(func() {
		environment.SignalWorkflow(PlanTaskResultSignal, domain.TaskResult{TaskID: "frontend", Status: domain.TaskStatusCompleted})
		environment.SignalWorkflow(PlanTaskResultSignal, domain.TaskResult{TaskID: "gateway", Status: domain.TaskStatusCompleted})
	}, 2*time.Second)

	environment.ExecuteWorkflow(PlanWorkflow, domain.PlanSchedule{
		RunID: "run", PlanID: "plan", MaxParallelTasks: 2, MaxActivityAttempts: 3,
		Tasks: []domain.ScheduledTask{
			{TaskID: "producer", Priority: 3},
			{TaskID: "frontend", Priority: 2, Dependencies: []string{"producer"}},
			{TaskID: "gateway", Priority: 1, Dependencies: []string{"producer"}},
		},
	})

	require.True(t, environment.IsWorkflowCompleted())
	require.NoError(t, environment.GetWorkflowError())
	var output PlanWorkflowOutput
	require.NoError(t, environment.GetWorkflowResult(&output))
	require.Equal(t, domain.PlanRunStatusCompleted, output.Status)
	mu.Lock()
	require.Equal(t, []string{"producer", "frontend", "gateway"}, dispatched)
	mu.Unlock()
}

func TestPlanWorkflowPauseResumeAndCancel(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	environment := suite.NewTestWorkflowEnvironment()
	environment.RegisterActivity(&activities.PlanActivities{})
	statuses := make([]domain.PlanRunStatus, 0)
	environment.OnActivity("SetPlanRunStatus", mock.Anything, mock.Anything).Return(
		func(_ context.Context, input activities.SetPlanRunStatusInput) (domain.PlanRun, error) {
			statuses = append(statuses, input.Status)
			return domain.PlanRun{ID: input.RunID, Status: input.Status}, nil
		},
	)
	environment.OnActivity("DispatchPlanTask", mock.Anything, mock.Anything).Return(domain.Task{}, nil)
	environment.OnActivity("RecordPlanTaskResult", mock.Anything, mock.Anything).Return(domain.Task{}, nil)
	environment.RegisterDelayedCallback(func() { environment.SignalWorkflow(PlanPauseSignal, true) }, time.Second)
	environment.RegisterDelayedCallback(func() { environment.SignalWorkflow(PlanResumeSignal, true) }, 2*time.Second)
	environment.RegisterDelayedCallback(func() { environment.SignalWorkflow(PlanCancelSignal, true) }, 3*time.Second)

	environment.ExecuteWorkflow(PlanWorkflow, domain.PlanSchedule{
		RunID: "run", PlanID: "plan", MaxParallelTasks: 1, MaxActivityAttempts: 3,
		Tasks: []domain.ScheduledTask{{TaskID: "task"}},
	})

	require.NoError(t, environment.GetWorkflowError())
	var output PlanWorkflowOutput
	require.NoError(t, environment.GetWorkflowResult(&output))
	require.Equal(t, domain.PlanRunStatusCancelled, output.Status)
	require.Equal(t, []domain.PlanRunStatus{
		domain.PlanRunStatusRunning, domain.PlanRunStatusPaused,
		domain.PlanRunStatusRunning, domain.PlanRunStatusCancelled,
	}, statuses)
}

func TestPlanWorkflowRetriesDispatchActivity(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	environment := suite.NewTestWorkflowEnvironment()
	environment.RegisterActivity(&activities.PlanActivities{})
	registerPlanStatusMocks(environment)
	attempts := 0
	environment.OnActivity("DispatchPlanTask", mock.Anything, mock.Anything).Return(
		func(_ context.Context, input activities.DispatchPlanTaskInput) (domain.Task, error) {
			attempts++
			if attempts < 3 {
				return domain.Task{}, errors.New("transient dispatch failure")
			}
			return domain.Task{ID: input.TaskID}, nil
		},
	)
	environment.RegisterDelayedCallback(func() {
		environment.SignalWorkflow(PlanTaskResultSignal, domain.TaskResult{TaskID: "task", Status: domain.TaskStatusCompleted})
	}, 10*time.Second)

	environment.ExecuteWorkflow(PlanWorkflow, domain.PlanSchedule{
		RunID: "run", PlanID: "plan", MaxParallelTasks: 1, MaxActivityAttempts: 3,
		Tasks: []domain.ScheduledTask{{TaskID: "task"}},
	})

	require.NoError(t, environment.GetWorkflowError())
	var output PlanWorkflowOutput
	require.NoError(t, environment.GetWorkflowResult(&output))
	require.Equal(t, domain.PlanRunStatusCompleted, output.Status)
	require.Equal(t, 3, attempts)
}

func TestPlanWorkflowAcceptsCancellationForUndispatchedTask(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	environment := suite.NewTestWorkflowEnvironment()
	environment.RegisterActivity(&activities.PlanActivities{})
	registerPlanStatusMocks(environment)
	environment.OnActivity("DispatchPlanTask", mock.Anything, mock.Anything).Return(domain.Task{}, nil)
	environment.RegisterDelayedCallback(func() {
		environment.SignalWorkflow(PlanTaskResultSignal, domain.TaskResult{
			TaskID: "dependent", Status: domain.TaskStatusCancelled, Error: "cancelled by owner",
		})
	}, time.Second)

	environment.ExecuteWorkflow(PlanWorkflow, domain.PlanSchedule{
		RunID: "run", PlanID: "plan", MaxParallelTasks: 1, MaxActivityAttempts: 3,
		Tasks: []domain.ScheduledTask{
			{TaskID: "producer", Priority: 2},
			{TaskID: "dependent", Priority: 1, Dependencies: []string{"producer"}},
		},
	})

	require.True(t, environment.IsWorkflowCompleted())
	require.NoError(t, environment.GetWorkflowError())
	var output PlanWorkflowOutput
	require.NoError(t, environment.GetWorkflowResult(&output))
	require.Equal(t, domain.PlanRunStatusFailed, output.Status)
	require.Equal(t, domain.TaskStatusCancelled, output.TaskStatus["dependent"])
}

func registerPlanStatusMocks(environment *testsuite.TestWorkflowEnvironment) {
	environment.OnActivity("SetPlanRunStatus", mock.Anything, mock.Anything).Return(domain.PlanRun{}, nil)
	environment.OnActivity("RecordPlanTaskResult", mock.Anything, mock.Anything).Return(domain.Task{}, nil)
}
