package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestVerifierAcceptsSupportedFixtureResult(t *testing.T) {
	worktrees := &fakeWorktree{
		state: domain.WorkspaceState{ChangedFiles: []string{"internal/order.go"}, Diff: "+fixture"},
		checks: map[string]domain.WorkspaceCheckResult{
			"git diff --check": {Command: "git diff --check"},
			"go test ./...":    {Command: "go test ./..."},
		},
	}
	verifier := Verifier{Worktrees: worktrees, Now: func() time.Time { return time.Unix(1, 0) }}
	report, state, err := verifier.Verify(context.Background(), verificationContext(), domain.TaskWorkspace{}, domain.AgentResult{
		Status: domain.AgentResultCompleted, FilesChanged: []string{"internal/order.go"},
		Checks: []domain.AgentCheck{{Name: "go test ./...", Status: domain.AgentCheckPassed}},
	})
	require.NoError(t, err)
	require.Equal(t, "passed", report.Status)
	require.Equal(t, []string{"internal/order.go"}, state.ChangedFiles)
}

func TestVerifierRejectsUnsupportedClaimAndScopeViolation(t *testing.T) {
	worktrees := &fakeWorktree{
		state: domain.WorkspaceState{ChangedFiles: []string{"outside.txt"}},
		checks: map[string]domain.WorkspaceCheckResult{
			"git diff --check": {Command: "git diff --check"},
			"go test ./...":    {Command: "go test ./..."},
		},
	}
	report, _, err := (Verifier{Worktrees: worktrees}).Verify(
		context.Background(), verificationContext(), domain.TaskWorkspace{}, domain.AgentResult{
			Status: domain.AgentResultCompleted, FilesChanged: []string{"outside.txt"},
			Checks: []domain.AgentCheck{{Name: "security scan", Status: domain.AgentCheckPassed}},
		},
	)
	require.Error(t, err)
	require.Equal(t, "failed", report.Status)
	require.Contains(t, verificationNames(report), "write_scope")
	require.Contains(t, verificationNames(report), "agent_claim:security scan")
}

func TestVerifierRequiresMigrationPairAndContractPath(t *testing.T) {
	worktrees := &fakeWorktree{
		state: domain.WorkspaceState{ChangedFiles: []string{"db/migrations/008_change.up.sql"}},
		checks: map[string]domain.WorkspaceCheckResult{
			"git diff --check": {Command: "git diff --check"},
			"go test ./...":    {Command: "go test ./..."},
		},
	}
	executionContext := verificationContext()
	executionContext.Task.WriteScope = []string{"**"}
	executionContext.Task.RequiresMigration = true
	executionContext.Task.ChangesContracts = true
	report, _, err := (Verifier{Worktrees: worktrees}).Verify(
		context.Background(), executionContext, domain.TaskWorkspace{},
		domain.AgentResult{Status: domain.AgentResultCompleted, FilesChanged: worktrees.state.ChangedFiles},
	)
	require.Error(t, err)
	require.Contains(t, verificationNames(report), "migration_pair")
	require.Contains(t, verificationNames(report), "contract_change")
}

func TestMatchScopeSupportsDoubleStar(t *testing.T) {
	require.True(t, matchScope("internal/**", "internal/order/service.go"))
	require.True(t, matchScope("**/*.go", "main.go"))
	require.False(t, matchScope("internal/**", "cmd/main.go"))
}

func verificationContext() domain.TaskExecutionContext {
	return domain.TaskExecutionContext{Task: domain.Task{
		WriteScope: []string{"internal/**"}, VerificationCommands: []string{"go test ./..."},
	}}
}

func verificationNames(report domain.VerificationReport) []string {
	result := make([]string, 0, len(report.Checks))
	for _, check := range report.Checks {
		if check.Status == "failed" {
			result = append(result, check.Name)
		}
	}
	return result
}

type fakeWorktree struct {
	workspace domain.TaskWorkspace
	state     domain.WorkspaceState
	checks    map[string]domain.WorkspaceCheckResult
	committed bool
}

func (f *fakeWorktree) Prepare(context.Context, domain.Project, domain.Task) (domain.TaskWorkspace, error) {
	if f.workspace.Path == "" {
		f.workspace = domain.TaskWorkspace{Path: "/fixture/worktree", BranchName: "ai/task-fixture", BaseCommit: "base"}
	}
	return f.workspace, nil
}
func (f *fakeWorktree) Inspect(context.Context, domain.Project, domain.TaskWorkspace) (domain.WorkspaceState, error) {
	return f.state, nil
}
func (f *fakeWorktree) RunCheck(_ context.Context, _ domain.TaskWorkspace, command string) (domain.WorkspaceCheckResult, error) {
	result, exists := f.checks[command]
	if !exists {
		return domain.WorkspaceCheckResult{}, errors.New("command not configured")
	}
	return result, nil
}
func (f *fakeWorktree) ReadArtifact(context.Context, domain.TaskWorkspace, string, int64) ([]byte, error) {
	return []byte("artifact"), nil
}
func (f *fakeWorktree) Commit(context.Context, domain.Project, domain.Task, domain.TaskWorkspace, []string) (string, error) {
	f.committed = true
	return "commit", nil
}
