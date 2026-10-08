package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestMaterializeVerifiedProductionPreimageKeepsFrozenHEAD(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source")
	require.NoError(t, os.Mkdir(sourcePath, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(sourcePath, "handler.go"), []byte("package http\n"), 0640))
	runGit(t, sourcePath, "init", "-b", "main")
	runGit(t, sourcePath, "add", ".")
	runGit(t, sourcePath, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "frozen")
	base := runGit(t, sourcePath, "rev-parse", "HEAD")
	project := domain.Project{ID: "project-1", Name: "fixture", LocalPath: &sourcePath, HeadCommit: base}
	worktrees := TaskWorktree{StoragePath: filepath.Join(root, "worktrees")}
	task := domain.Task{ID: "12345678-abcd-0000-0000-123456789012", ProjectID: project.ID, Title: "prior"}
	prior, err := worktrees.PrepareAtCommit(context.Background(), project, task, base)
	require.NoError(t, err)
	production := "package http\n\nfunc OldBehavior() {}\n"
	require.NoError(t, os.WriteFile(filepath.Join(prior.Path, "handler.go"), []byte(production), 0640))
	require.NoError(t, os.WriteFile(filepath.Join(prior.Path, "handler_test.go"), []byte("package http\n"), 0640))
	paths := []string{"handler.go", "handler_test.go"}
	commit, err := worktrees.Commit(context.Background(), project, task, prior, paths)
	require.NoError(t, err)
	destinationTask := domain.Task{ID: "87654321-abcd-0000-0000-123456789012", ProjectID: project.ID, Title: "remediation"}
	destination, err := worktrees.PrepareAtCommit(context.Background(), project, destinationTask, base)
	require.NoError(t, err)
	evidence, err := worktrees.MaterializeVerifiedSources(context.Background(), project, prior, destination, commit, base, paths, []string{"handler.go"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"handler.go": production}, evidence)
	require.Equal(t, base, runGit(t, destination.Path, "rev-parse", "HEAD"))
	require.Equal(t, base, destination.BaseCommit)
	require.NoFileExists(t, filepath.Join(destination.Path, "handler_test.go"))
	bytes, err := os.ReadFile(filepath.Join(destination.Path, "handler.go"))
	require.NoError(t, err)
	require.Equal(t, production, string(bytes))
	assertSourceUnchanged(t, sourcePath, base)
	_, err = worktrees.MaterializeVerifiedSources(context.Background(), project, prior, destination, commit, base, paths, []string{"handler_test.go"})
	require.Error(t, err)
}
