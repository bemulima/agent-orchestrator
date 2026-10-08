package git

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	require.Equal(t, filepath.Base(sourcePath), filepath.Base(workspace.Path))
	preAgent, err := worktrees.Snapshot(context.Background(), project, workspace)
	require.NoError(t, err)
	require.Contains(t, preAgent.Files, "README.md")
	require.NotContains(t, preAgent.Files, "result.txt")
	require.NoError(t, os.WriteFile(filepath.Join(workspace.Path, "README.md"), []byte("after\n"), 0o640))
	require.NoError(t, os.WriteFile(filepath.Join(workspace.Path, "result.txt"), []byte("artifact\n"), 0o640))
	postAgent, err := worktrees.Snapshot(context.Background(), project, workspace)
	require.NoError(t, err)
	require.NotEqual(t, preAgent.Files["README.md"], postAgent.Files["README.md"])
	require.Contains(t, postAgent.Files, "result.txt")

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

func TestSafeCommandEnvironmentPassesOnlyExplicitTestDatabaseURL(t *testing.T) {
	result := strings.Join(safeCommandEnvironment([]string{
		"TEST_DATABASE_URL=postgres://canary@availability-postgres:5432/availability_test?sslmode=disable",
		"DATABASE_URL=postgres://user:password@production.example.invalid/service",
		"GITHUB_TOKEN=not-for-tests",
	}), "\n")
	if !strings.Contains(result, "TEST_DATABASE_URL=postgres://canary@availability-postgres:5432/availability_test?sslmode=disable") {
		t.Fatal("explicit test database URL was not passed to the test command")
	}
	if strings.Contains(result, "\nDATABASE_URL=") || strings.HasPrefix(result, "DATABASE_URL=") ||
		strings.Contains(result, "\nGITHUB_TOKEN=") || strings.HasPrefix(result, "GITHUB_TOKEN=") {
		t.Fatal("general database or external-service credentials leaked into the test environment")
	}
}

func TestTestingPolicyRunnerCommandsAreExactAndArgumentBound(t *testing.T) {
	runID := "123e4567-e89b-12d3-a456-426614174000"
	base := strings.Repeat("a", 40)
	head := strings.Repeat("b", 40)
	commands := []string{
		"node20 .ai/testing/policy/policy-runner.cjs verify-lock --lock .ai/testing/policy/policy-lock.json",
		"node20 .ai/testing/policy/policy-runner.cjs verify --repo . --command verify:pr --output-dir . --run-id " + runID + " --base " + base + " --head " + head,
		"node20 .ai/testing/policy/policy-runner.cjs agent-dod-from-run --repo . --aggregate test-results/" + runID + "/verify/pr/test-result.v1.json --base " + base + " --head " + head + " --output test-results/" + runID + "/agent-dod.v1.json",
		"node20 .ai/testing/policy/policy-runner.cjs agent-dod-from-run --repo . --aggregate test-results/" + runID + "/verify/pr/test-result.v1.json --base " + base + " --head " + head + " --business-acceptance-evidence acceptance-ref:ticket-123 --output test-results/" + runID + "/agent-dod.v1.json",
	}
	for _, requested := range commands {
		name, arguments, ok := allowedTestingPolicyCommand(requested)
		require.True(t, ok, requested)
		require.Equal(t, "/usr/local/bin/node20", name)
		require.NotEmpty(t, arguments)
	}

	for _, requested := range []string{
		"node20 .ai/testing/policy/policy-runner.cjs verify --repo . --command verify:pr --output-dir . --run-id " + runID + " --base " + base + " --head " + head + " ; touch /tmp/not-allowed",
		"node20 .ai/testing/policy/policy-runner.cjs agent-dod-from-run --repo . --aggregate test-results/../verify/pr/test-result.v1.json --base " + base + " --head " + head + " --output test-results/" + runID + "/agent-dod.v1.json",
		"node20 .ai/testing/policy/policy-runner.cjs agent-dod-from-run --repo . --aggregate test-results/" + runID + "/verify/pr/test-result.v1.json --base " + base + " --head " + head + " --business-acceptance not-required --output test-results/" + runID + "/agent-dod.v1.json",
		"node20 .ai/testing/policy/policy-runner.cjs agent-dod-from-run --repo . --aggregate test-results/" + runID + "/verify/pr/test-result.v1.json --base " + base + " --head " + head + " --business-acceptance-evidence accepted ticket 123 --output test-results/" + runID + "/agent-dod.v1.json",
	} {
		_, _, ok := allowedTestingPolicyCommand(requested)
		require.False(t, ok, requested)
	}
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

func TestPrepareAtCommitUsesFrozenDescendantWhileKeepingSourceCheckoutUntouched(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source")
	require.NoError(t, os.Mkdir(sourcePath, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(sourcePath, "contract.go"), []byte("package source\n"), 0o640))
	runGit(t, sourcePath, "init", "-b", "main")
	runGit(t, sourcePath, "add", "contract.go")
	runGit(t, sourcePath, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "source")
	sourceCommit := runGit(t, sourcePath, "rev-parse", "HEAD")
	runGit(t, sourcePath, "checkout", "-b", "contract-baseline")
	require.NoError(t, os.WriteFile(filepath.Join(sourcePath, "contract.go"), []byte("package source\n// frozen\n"), 0o640))
	runGit(t, sourcePath, "add", "contract.go")
	runGit(t, sourcePath, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "frozen contract")
	frozenCommit := runGit(t, sourcePath, "rev-parse", "HEAD")
	runGit(t, sourcePath, "checkout", "main")
	localPath := sourcePath
	project := domain.Project{ID: "project-1", Name: "fixture", LocalPath: &localPath, HeadCommit: sourceCommit}
	task := domain.Task{ID: "87654321-abcd-0000-0000-123456789012", ProjectID: project.ID, Title: "shard work"}
	worktrees := TaskWorktree{StoragePath: filepath.Join(root, "worktrees")}

	workspace, err := worktrees.PrepareAtCommit(context.Background(), project, task, frozenCommit)
	require.NoError(t, err)
	require.Equal(t, frozenCommit, workspace.BaseCommit)
	require.Equal(t, frozenCommit, runGit(t, workspace.Path, "rev-parse", "HEAD"))
	assertSourceUnchanged(t, sourcePath, sourceCommit)

	_, err = worktrees.PrepareAtCommit(context.Background(), project, task, strings.Repeat("f", 40))
	require.Error(t, err)
}

func TestApplyVerifiedCommitsSeriallyAndDeterministically(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source")
	require.NoError(t, os.Mkdir(sourcePath, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(sourcePath, "a.txt"), []byte("a0\n"), 0o640))
	require.NoError(t, os.WriteFile(filepath.Join(sourcePath, "b.txt"), []byte("b0\n"), 0o640))
	runGit(t, sourcePath, "init", "-b", "main")
	runGit(t, sourcePath, "add", ".")
	runGit(t, sourcePath, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "base")
	base := runGit(t, sourcePath, "rev-parse", "HEAD")

	runGit(t, sourcePath, "checkout", "-b", "worker-a")
	require.NoError(t, os.WriteFile(filepath.Join(sourcePath, "a.txt"), []byte("a1\n"), 0o640))
	runGit(t, sourcePath, "add", "a.txt")
	runGit(t, sourcePath, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "worker a")
	commitA := runGit(t, sourcePath, "rev-parse", "HEAD")
	runGit(t, sourcePath, "checkout", "main")
	runGit(t, sourcePath, "checkout", "-b", "worker-b")
	require.NoError(t, os.WriteFile(filepath.Join(sourcePath, "b.txt"), []byte("b1\n"), 0o640))
	runGit(t, sourcePath, "add", "b.txt")
	runGit(t, sourcePath, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "worker b")
	commitB := runGit(t, sourcePath, "rev-parse", "HEAD")
	runGit(t, sourcePath, "checkout", "main")

	localPath := sourcePath
	project := domain.Project{ID: "project-1", Name: "fixture", LocalPath: &localPath, HeadCommit: base}
	task := domain.Task{ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", ProjectID: project.ID, Title: "integration"}
	worktrees := TaskWorktree{StoragePath: filepath.Join(root, "worktrees")}
	workspace, err := worktrees.PrepareAtCommit(context.Background(), project, task, base)
	require.NoError(t, err)
	_, err = worktrees.ApplyVerifiedCommit(context.Background(), project, workspace, commitA, base, []string{"a.txt"})
	require.NoError(t, err)
	tip, err := worktrees.ApplyVerifiedCommit(context.Background(), project, workspace, commitB, base, []string{"b.txt"})
	require.NoError(t, err)
	require.Equal(t, tip, runGit(t, workspace.Path, "rev-parse", "HEAD"))
	a, err := os.ReadFile(filepath.Join(workspace.Path, "a.txt"))
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(workspace.Path, "b.txt"))
	require.NoError(t, err)
	require.Equal(t, "a1\n", string(a))
	require.Equal(t, "b1\n", string(b))
	assertSourceUnchanged(t, sourcePath, base)
}

func TestApplyVerifiedCommitConflictAbortsFailClosed(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source")
	require.NoError(t, os.Mkdir(sourcePath, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(sourcePath, "same.txt"), []byte("base\n"), 0o640))
	runGit(t, sourcePath, "init", "-b", "main")
	runGit(t, sourcePath, "add", "same.txt")
	runGit(t, sourcePath, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "base")
	base := runGit(t, sourcePath, "rev-parse", "HEAD")

	runGit(t, sourcePath, "checkout", "-b", "worker-a")
	require.NoError(t, os.WriteFile(filepath.Join(sourcePath, "same.txt"), []byte("worker a\n"), 0o640))
	runGit(t, sourcePath, "add", "same.txt")
	runGit(t, sourcePath, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "worker a")
	commitA := runGit(t, sourcePath, "rev-parse", "HEAD")
	runGit(t, sourcePath, "checkout", "main")
	runGit(t, sourcePath, "checkout", "-b", "worker-b")
	require.NoError(t, os.WriteFile(filepath.Join(sourcePath, "same.txt"), []byte("worker b\n"), 0o640))
	runGit(t, sourcePath, "add", "same.txt")
	runGit(t, sourcePath, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "worker b")
	commitB := runGit(t, sourcePath, "rev-parse", "HEAD")
	runGit(t, sourcePath, "checkout", "main")

	localPath := sourcePath
	project := domain.Project{ID: "project-1", Name: "fixture", LocalPath: &localPath, HeadCommit: base}
	task := domain.Task{ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", ProjectID: project.ID, Title: "integration"}
	worktrees := TaskWorktree{StoragePath: filepath.Join(root, "worktrees")}
	workspace, err := worktrees.PrepareAtCommit(context.Background(), project, task, base)
	require.NoError(t, err)
	firstTip, err := worktrees.ApplyVerifiedCommit(context.Background(), project, workspace, commitA, base, []string{"same.txt"})
	require.NoError(t, err)
	_, err = worktrees.ApplyVerifiedCommit(context.Background(), project, workspace, commitB, base, []string{"same.txt"})
	require.Error(t, err)
	require.Equal(t, firstTip, runGit(t, workspace.Path, "rev-parse", "HEAD"))
	require.Empty(t, runGit(t, workspace.Path, "status", "--porcelain=v1"))
	assertSourceUnchanged(t, sourcePath, base)
}

func TestPrepareNodeDependenciesUsesLockfileAndDisablesScripts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell script")
	}
	root := t.TempDir()
	binPath := filepath.Join(root, "bin")
	worktreePath := filepath.Join(root, "worktree")
	require.NoError(t, os.MkdirAll(binPath, 0o750))
	require.NoError(t, os.MkdirAll(worktreePath, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(worktreePath, "package-lock.json"), []byte("{}\n"), 0o640))
	fakeNPM := "#!/bin/sh\n/bin/mkdir -p node_modules\nprintf '%s\\n' \"$*\" > node_modules/.package-lock.json\n"
	require.NoError(t, os.WriteFile(filepath.Join(binPath, "npm"), []byte(fakeNPM), 0o750))
	t.Setenv("PATH", binPath)

	require.NoError(t, prepareNodeDependencies(context.Background(), worktreePath, []string{"npm test"}))
	arguments, err := os.ReadFile(filepath.Join(worktreePath, "node_modules", ".package-lock.json"))
	require.NoError(t, err)
	require.Equal(t, "ci --ignore-scripts --no-audit --no-fund\n", string(arguments))
}

func TestPrepareNodeDependenciesRequiresRegularLockfile(t *testing.T) {
	root := t.TempDir()
	require.Error(t, prepareNodeDependencies(context.Background(), root, []string{"npm run build"}))
	require.NoError(t, prepareNodeDependencies(context.Background(), root, []string{"go test ./..."}))
}
