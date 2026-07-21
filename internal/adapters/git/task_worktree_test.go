package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestTaskWorktreeIsolatesVerifiesAndCommitsFixture(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source")
	require.NoError(t, os.Mkdir(sourcePath, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(sourcePath, "README.md"), []byte("before\n"), 0o640))
	runGit(t, sourcePath, "init", "-b", "main")
	runGit(t, sourcePath, "add", "README.md")
	runGit(t, sourcePath, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "initial")
	baseCommit := runGit(t, sourcePath, "rev-parse", "HEAD")
	localPath := sourcePath
	project := domain.Project{ID: "project-1", Name: "fixture", LocalPath: &localPath, HeadCommit: baseCommit}
	task := domain.Task{ID: "12345678-abcd-0000-0000-123456789012", ProjectID: project.ID, Title: "update fixture"}
	worktrees := TaskWorktree{StoragePath: filepath.Join(root, "worktrees")}

	workspace, err := worktrees.Prepare(context.Background(), project, task)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(workspace.Path, "README.md"), []byte("after\n"), 0o640))
	require.NoError(t, os.WriteFile(filepath.Join(workspace.Path, "result.txt"), []byte("artifact\n"), 0o640))

	state, err := worktrees.Inspect(context.Background(), project, workspace)
	require.NoError(t, err)
	require.Equal(t, []string{"README.md", "result.txt"}, state.ChangedFiles)
	check, err := worktrees.RunCheck(context.Background(), workspace, "git diff --check")
	require.NoError(t, err)
	require.Zero(t, check.ExitCode)
	_, err = worktrees.RunCheck(context.Background(), workspace, "sh -c printenv")
	require.Error(t, err)
	artifact, err := worktrees.ReadArtifact(context.Background(), workspace, "result.txt", 1024)
	require.NoError(t, err)
	require.Equal(t, "artifact\n", string(artifact))

	commit, err := worktrees.Commit(context.Background(), project, task, workspace, state.ChangedFiles)
	require.NoError(t, err)
	require.NotEqual(t, baseCommit, commit)
	assertSourceUnchanged(t, sourcePath, baseCommit)

	repeated, err := worktrees.Prepare(context.Background(), project, task)
	require.NoError(t, err)
	require.Equal(t, workspace, repeated)
	repeatedCommit, err := worktrees.Commit(context.Background(), project, task, repeated, state.ChangedFiles)
	require.NoError(t, err)
	require.Equal(t, commit, repeatedCommit)
}

func TestTaskWorktreeRejectsEscapingArtifactSymlink(t *testing.T) {
	root := t.TempDir()
	workspace := domain.TaskWorkspace{Path: filepath.Join(root, "worktree")}
	require.NoError(t, os.Mkdir(workspace.Path, 0o750))
	outside := filepath.Join(root, "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(workspace.Path, "result.txt")))
	_, err := (TaskWorktree{}).ReadArtifact(context.Background(), workspace, "result.txt", 1024)
	require.Error(t, err)
}
