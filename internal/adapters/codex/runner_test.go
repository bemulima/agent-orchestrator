package codex

import (
	"context"
	"encoding/json"
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
	fmt.Println(`{"type":"result","thread_id":"thread-fixture","result":{"status":"completed"}}`)
	os.Exit(0)
}
