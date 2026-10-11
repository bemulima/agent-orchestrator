package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestProcessRunnerOnlySerializesTrustedActivityContext(t *testing.T) {
	t.Setenv("GO_WANT_CODEX_HELPER", "runtime-context")
	runner, err := NewProcessRunner(fmt.Sprintf("%s -test.run=TestCodexRunnerHelper --", os.Args[0]))
	require.NoError(t, err)
	request := domain.AgentRunRequest{
		Role: domain.AgentRunCoder, WorkingDirectory: t.TempDir(), Prompt: "fixture",
		OutputSchema:   map[string]any{"type": "object"},
		RuntimeContext: &domain.AgentRuntimeContext{WorkflowID: "caller-spoof"},
	}
	trusted := domain.AgentRuntimeContext{
		WorkflowID: "actual-workflow", WorkflowRunID: "actual-run",
		ActivityID: "actual-activity", ActivityAttempt: 2,
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want *domain.AgentRuntimeContext
	}{
		{"without activity", context.Background(), nil},
		{"with activity", domain.WithAgentRuntimeContext(context.Background(), trusted), &trusted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, err := runner.Run(tc.ctx, request, nil)
			require.NoError(t, err)
			var result struct {
				RuntimeContext *domain.AgentRuntimeContext `json:"runtime_context"`
			}
			require.NoError(t, json.Unmarshal(response.Result, &result))
			require.Equal(t, tc.want, result.RuntimeContext)
		})
	}
}
