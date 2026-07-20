package workflow

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"github.com/bemulima/agent-orchestrator/internal/activities"
)

func TestSystemProbeWorkflow(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	environment := suite.NewTestWorkflowEnvironment()
	environment.RegisterActivity(&activities.SystemActivities{})

	environment.ExecuteWorkflow(SystemProbeWorkflow, activities.SystemProbeInput{RequestID: "request-1"})

	require.True(t, environment.IsWorkflowCompleted())
	require.NoError(t, environment.GetWorkflowError())
	var result activities.SystemProbeOutput
	require.NoError(t, environment.GetWorkflowResult(&result))
	require.Equal(t, "request-1", result.RequestID)
	require.Equal(t, "ok", result.Status)
	require.False(t, result.ObservedAt.IsZero())
}
