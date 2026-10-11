package activities

import (
	"context"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

func TestActivityRuntimeContextUsesTemporalMetadata(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	probe := func(ctx context.Context) (domain.AgentRuntimeContext, error) {
		ctx = domain.WithAgentRuntimeContext(ctx, domain.AgentRuntimeContext{WorkflowID: "spoof"})
		got, ok := domain.AgentRuntimeContextFromContext(bindAgentRuntimeContext(ctx))
		require.True(t, ok)
		info := activity.GetInfo(ctx)
		require.Equal(t, info.WorkflowExecution.ID, got.WorkflowID)
		require.Equal(t, info.WorkflowExecution.RunID, got.WorkflowRunID)
		require.Equal(t, info.ActivityID, got.ActivityID)
		require.Equal(t, info.Attempt, got.ActivityAttempt)
		return got, nil
	}
	env.RegisterActivity(probe)
	result, err := env.ExecuteActivity(probe)
	require.NoError(t, err)
	var got domain.AgentRuntimeContext
	require.NoError(t, result.Get(&got))
	require.NotEqual(t, "spoof", got.WorkflowID)
	require.NotEmpty(t, got.ActivityID)
	require.Greater(t, got.ActivityAttempt, int32(0))
}
