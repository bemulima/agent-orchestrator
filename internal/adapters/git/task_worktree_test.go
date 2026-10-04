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

func TestTaskWorktreeReadArtifactBoundsReviewedPolicyBundleSeparately(t *testing.T) {
	const bundlePath = ".ai/testing/policy/policy-runner.cjs"
	for _, test := range []struct {
		name, path  string
		size, limit int64
		wantError   bool
	}{
		{name: "canonical bundle at 32 MiB", path: bundlePath, size: 32 << 20, limit: 32 << 20},
		{name: "normalized canonical bundle", path: "./.ai/testing/policy/sub/../policy-runner.cjs", size: 16, limit: 32 << 20},
		{name: "canonical bundle above 32 MiB", path: bundlePath, size: (32 << 20) + 1, limit: 32 << 20, wantError: true},
		{name: "canonical limit above 32 MiB", path: bundlePath, size: 16, limit: (32 << 20) + 1, wantError: true},
		{name: "generic artifact limit stays 10 MiB", path: "result.txt", size: 16, limit: (10 << 20) + 1, wantError: true},
		{name: "generic artifact bytes stay bounded", path: "result.txt", size: (10 << 20) + 1, limit: 10 << 20, wantError: true},
		{name: "zero limit rejected", path: bundlePath, size: 16, limit: 0, wantError: true},
		{name: "negative limit rejected", path: bundlePath, size: 16, limit: -1, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			canonicalRoot, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			workspace := domain.TaskWorkspace{Path: canonicalRoot}
			relative, err := taskRelativePath(test.path)
			require.NoError(t, err)
			target := filepath.Join(workspace.Path, relative)
			require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o750))
			file, err := os.Create(target)
			require.NoError(t, err)
			require.NoError(t, file.Truncate(test.size))
			require.NoError(t, file.Close())
			content, err := (TaskWorktree{}).ReadArtifact(context.Background(), workspace, test.path, test.limit)
			if test.wantError {
				require.ErrorIs(t, err, domain.ErrValidation)
			} else {
				require.NoError(t, err)
				require.Len(t, content, int(test.size))
			}
		})
	}
}

func TestTaskWorktreeReviewedPolicyBundlePreservesPathContainment(t *testing.T) {
	canonicalRoot, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	workspace := domain.TaskWorkspace{Path: canonicalRoot}
	for _, path := range []string{"../policy-runner.cjs", "/tmp/policy-runner.cjs", "\x00policy-runner.cjs"} {
		_, err := (TaskWorktree{}).ReadArtifact(context.Background(), workspace, path, 32<<20)
		require.ErrorIs(t, err, domain.ErrWriteScope)
	}
	outside := filepath.Join(t.TempDir(), "outside.cjs")
	require.NoError(t, os.WriteFile(outside, []byte("outside"), 0o640))
	canonical := filepath.Join(workspace.Path, ".ai", "testing", "policy", "policy-runner.cjs")
	require.NoError(t, os.MkdirAll(filepath.Dir(canonical), 0o750))
	require.NoError(t, os.Symlink(outside, canonical))
	_, err = (TaskWorktree{}).ReadArtifact(context.Background(), workspace, ".ai/testing/policy/policy-runner.cjs", 32<<20)
	require.ErrorIs(t, err, domain.ErrForbidden)
}
