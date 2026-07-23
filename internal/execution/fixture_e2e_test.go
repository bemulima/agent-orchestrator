package execution

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	gitadapter "github.com/bemulima/agent-orchestrator/internal/adapters/git"
	"github.com/bemulima/agent-orchestrator/internal/agent"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

func TestFixtureExecutionProducesRealIsolatedDiffAndStructuredResult(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source")
	require.NoError(t, os.MkdirAll(filepath.Join(sourcePath, "internal"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(sourcePath, "internal", "value.txt"), []byte("before\n"), 0o640))
	runFixtureGit(t, sourcePath, "init", "-b", "main")
	runFixtureGit(t, sourcePath, "add", "internal/value.txt")
	runFixtureGit(t, sourcePath, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "initial")
	baseCommit := runFixtureGit(t, sourcePath, "rev-parse", "HEAD")

	validator, err := agent.NewValidator()
	require.NoError(t, err)
	repo := newFakeExecutionRepository()
	repo.executionContext.Task.VerificationCommands = []string{"git diff --check"}
	repo.executionContext.Project.LocalPath = &sourcePath
	repo.executionContext.Project.HeadCommit = baseCommit
	worktrees := gitadapter.TaskWorktree{StoragePath: filepath.Join(root, "worktrees")}
	runner := &fileChangingRunner{responses: []json.RawMessage{fixtureCoderResult(), approvedReviewResult()}}
	service := Service{
		Repository: repo, Worktrees: worktrees, Runner: runner, Validator: validator,
		Verifier: Verifier{Worktrees: worktrees}, MaxTaskAttempts: 3, MaxReviewAttempts: 2,
	}

	outcome, err := service.Execute(context.Background(), "task-1", "workflow-1")
	require.NoError(t, err)
	require.Equal(t, domain.TaskStatusCompleted, outcome.Result.Status)
	require.Equal(t, baseCommit, runFixtureGit(t, sourcePath, "rev-parse", "HEAD"))
	require.Empty(t, runFixtureGit(t, sourcePath, "status", "--porcelain=v1"))
	require.NotEqual(t, baseCommit, runFixtureGit(t, runner.worktreePath, "rev-parse", "HEAD"))
	require.Equal(t, "after\n", readFixtureFile(t, filepath.Join(runner.worktreePath, "internal", "value.txt")))
}

func fixtureCoderResult() json.RawMessage {
	return json.RawMessage(`{
  "status":"completed","summary":"updated fixture","files_changed":["internal/value.txt"],
  "checks":[{"name":"git diff --check","status":"passed","details":"ok"}],
  "artifacts":[],"blockers":[],"required_tasks":[],"risks":[],"notes_for_reviewer":[]
}`)
}

type fileChangingRunner struct {
	responses    []json.RawMessage
	coderThread  string
	reviewNumber int
	worktreePath string
}

func (r *fileChangingRunner) Run(
	ctx context.Context,
	request domain.AgentRunRequest,
	callback repository.AgentThreadCallback,
) (domain.AgentRunResponse, error) {
	r.worktreePath = request.WorkingDirectory
	threadID := request.ThreadID
	if request.Role == domain.AgentRunCoder {
		if threadID == "" {
			threadID = "fixture-coder-thread"
			r.coderThread = threadID
		}
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "internal", "value.txt"), []byte("after\n"), 0o640); err != nil {
			return domain.AgentRunResponse{}, err
		}
	} else {
		r.reviewNumber++
		threadID = "fixture-review-thread"
	}
	if err := callback(ctx, threadID); err != nil {
		return domain.AgentRunResponse{}, err
	}
	result := r.responses[0]
	r.responses = r.responses[1:]
	return domain.AgentRunResponse{ThreadID: threadID, Result: result}, nil
}

func runFixtureGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	return strings.TrimSpace(string(output))
}

func readFixtureFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(content)
}
