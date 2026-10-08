package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestProcessRunnerPersistsThreadBeforeReturningResult(t *testing.T) {
	t.Setenv("GO_WANT_CODEX_HELPER", "success")
	runner, err := NewProcessRunner(fmt.Sprintf("%s -test.run=TestCodexRunnerHelper --", os.Args[0]))
	require.NoError(t, err)
	callbackCalled := false
	response, err := runner.Run(context.Background(), domain.AgentRunRequest{
		Role: domain.AgentRunCoder, WorkingDirectory: t.TempDir(), Prompt: "fixture",
		OutputSchema: map[string]any{"type": "object"},
	}, func(_ context.Context, threadID string) error {
		callbackCalled = true
		require.Equal(t, "thread-fixture", threadID)
		return nil
	})
	require.NoError(t, err)
	require.True(t, callbackCalled)
	require.Equal(t, "thread-fixture", response.ThreadID)
	require.JSONEq(t, `{"status":"completed"}`, string(response.Result))
	require.EqualValues(t, 120, response.Usage.InputTokens)
	require.EqualValues(t, 15, response.Usage.CachedInputTokens)
	require.EqualValues(t, 30, response.Usage.OutputTokens)
	require.EqualValues(t, 10, response.Usage.ReasoningOutputTokens)
}

func TestProcessRunnerRejectsUnsupportedProtocol(t *testing.T) {
	t.Setenv("GO_WANT_CODEX_HELPER", "unknown")
	runner, err := NewProcessRunner(fmt.Sprintf("%s -test.run=TestCodexRunnerHelper --", os.Args[0]))
	require.NoError(t, err)
	_, err = runner.Run(context.Background(), domain.AgentRunRequest{
		Role: domain.AgentRunReviewer, WorkingDirectory: t.TempDir(), Prompt: "fixture",
		OutputSchema: map[string]any{"type": "object"},
	}, nil)
	require.ErrorContains(t, err, "unknown Codex runner event")
}

func TestProcessRunnerRejectsUnsupportedReasoningEffort(t *testing.T) {
	runner, err := NewProcessRunner(fmt.Sprintf("%s -test.run=TestCodexRunnerHelper --", os.Args[0]))
	require.NoError(t, err)
	_, err = runner.Run(context.Background(), domain.AgentRunRequest{
		Role: domain.AgentRunCoder, WorkingDirectory: t.TempDir(), Prompt: "fixture",
		ReasoningEffort: "ultra", OutputSchema: map[string]any{"type": "object"},
	}, nil)
	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestProcessRunnerAcceptsReadOnlyAnalyst(t *testing.T) {
	t.Setenv("GO_WANT_CODEX_HELPER", "success")
	runner, err := NewProcessRunner(fmt.Sprintf("%s -test.run=TestCodexRunnerHelper --", os.Args[0]))
	require.NoError(t, err)
	response, err := runner.Run(context.Background(), domain.AgentRunRequest{
		Role: domain.AgentRunAnalyst, WorkingDirectory: t.TempDir(), Prompt: "analyze fixture",
		OutputSchema: map[string]any{"type": "object"},
	}, nil)
	require.NoError(t, err)
	require.JSONEq(t, `{"status":"completed"}`, string(response.Result))
}

func TestProcessRunnerReportsStructuredChildErrorForIncompleteProtocol(t *testing.T) {
	t.Setenv("GO_WANT_CODEX_HELPER", "incomplete")
	runner, err := NewProcessRunner(fmt.Sprintf("%s -test.run=TestCodexRunnerHelper --", os.Args[0]))
	require.NoError(t, err)
	_, err = runner.Run(context.Background(), domain.AgentRunRequest{
		Role: domain.AgentRunAnalyst, WorkingDirectory: t.TempDir(), Prompt: "analyze fixture",
		OutputSchema: map[string]any{"type": "object"},
	}, nil)
	require.ErrorContains(t, err, "incomplete Codex runner protocol")
	require.ErrorContains(t, err, "fixture structured result was invalid")
}

func TestProcessRunnerReturnsThreadForTransientStreamFailure(t *testing.T) {
	t.Setenv("GO_WANT_CODEX_HELPER", "transient")
	runner, err := NewProcessRunner(fmt.Sprintf("%s -test.run=TestCodexRunnerHelper --", os.Args[0]))
	require.NoError(t, err)
	response, err := runner.Run(context.Background(), domain.AgentRunRequest{
		Role: domain.AgentRunAnalyst, WorkingDirectory: t.TempDir(), Prompt: "analyze fixture",
		OutputSchema: map[string]any{"type": "object"},
	}, nil)
	require.Error(t, err)
	require.True(t, errors.Is(err, domain.ErrTransient))
	require.Equal(t, "thread-fixture", response.ThreadID)
}

func TestNewProcessRunnerRejectsShellSyntax(t *testing.T) {
	_, err := NewProcessRunner("node runner.js; printenv")
	require.Error(t, err)
}

func TestCodexRunnerHelper(t *testing.T) {
	mode := os.Getenv("GO_WANT_CODEX_HELPER")
	if mode == "" {
		return
	}
	var request domain.AgentRunRequest
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		os.Exit(2)
	}
	if !strings.Contains(request.Prompt, "fixture") {
		os.Exit(3)
	}
	if mode == "unknown" {
		fmt.Println(`{"type":"log","message":"forbidden"}`)
		os.Exit(0)
	}
	fmt.Println(`{"type":"thread_started","thread_id":"thread-fixture"}`)
	if mode == "incomplete" {
		fmt.Fprintln(os.Stderr, `{"type":"error","message":"fixture structured result was invalid"}`)
		os.Exit(1)
	}
	if mode == "transient" {
		fmt.Fprintln(os.Stderr, `{"type":"error","message":"stream disconnected before completion: unexpected-eof"}`)
		os.Exit(1)
	}
	b, _ := json.Marshal(map[string]any{"type": "result", "execution_id": request.ExecutionID, "attempt": request.Attempt, "thread_id": "thread-fixture", "result": map[string]string{"status": "completed"}, "usage": map[string]int{"input_tokens": 120, "cached_input_tokens": 15, "output_tokens": 30, "reasoning_output_tokens": 10}})
	fmt.Println(string(b))
	os.Exit(0)
}

func TestOutputLimitBreachCancelsAndCannotBeReportedSuccess(t *testing.T) {
	canceled := false
	buffer := boundedBuffer{limit: 4, onOverflow: func() { canceled = true }}
	_, err := buffer.Write([]byte("12345"))
	if err != nil || !buffer.exceeded || !canceled || buffer.String() != "1234" {
		t.Fatalf("limit breach not enforced: %#v %v", buffer, err)
	}
}

func TestRunnerRejectsStaleAttemptAndCancelledLateGreen(t *testing.T) {
	for _, identity := range []string{`"execution_id":"old","attempt":1`, `"execution_id":"current","attempt":2`} {
		protocol := "{\"type\":\"thread_started\",\"thread_id\":\"thread\"}\n" + "{\"type\":\"result\",\"thread_id\":\"thread\"," + identity + ",\"result\":{}}\n"
		_, err := readProtocol(context.Background(), strings.NewReader(protocol), "", "current", 1, nil)
		require.ErrorIs(t, err, domain.ErrConflict)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := readProtocol(ctx, strings.NewReader("{\"type\":\"thread_started\",\"thread_id\":\"thread\"}\n"), "", "current", 1, nil)
	require.ErrorIs(t, err, context.Canceled)
}

func TestEachRunnerInvocationHasFreshExecutionIdentity(t *testing.T) {
	t.Setenv("GO_WANT_CODEX_HELPER", "success")
	runner, err := NewProcessRunner(fmt.Sprintf("%s -test.run=TestCodexRunnerHelper --", os.Args[0]))
	require.NoError(t, err)
	req := domain.AgentRunRequest{Role: domain.AgentRunCoder, WorkingDirectory: t.TempDir(), Prompt: "fixture", OutputSchema: map[string]any{"type": "object"}}
	first, err := runner.Run(context.Background(), req, nil)
	require.NoError(t, err)
	second, err := runner.Run(context.Background(), req, nil)
	require.NoError(t, err)
	require.NotEmpty(t, first.ExecutionID)
	require.NotEqual(t, first.ExecutionID, second.ExecutionID)
	require.Equal(t, 1, second.Attempt)
}
