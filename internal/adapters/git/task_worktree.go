package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

const maxVerificationOutput = 1 << 20

type TaskWorktree struct {
	StoragePath string
	AuthorName  string
	AuthorEmail string
}

func (w TaskWorktree) Prepare(
	ctx context.Context,
	project domain.Project,
	task domain.Task,
) (domain.TaskWorkspace, error) {
	return w.PrepareAtCommit(ctx, project, task, project.HeadCommit)
}

// PrepareAtCommit creates a managed task workspace at an explicitly approved
// execution revision while still proving that the connected source checkout
// remains clean at its separately persisted project revision.
func (w TaskWorktree) PrepareAtCommit(
	ctx context.Context,
	project domain.Project,
	task domain.Task,
	baseCommit string,
) (domain.TaskWorkspace, error) {
	if task.ProjectID != project.ID || project.LocalPath == nil || project.HeadCommit == "" || !isFullCommitSHA(baseCommit) {
		return domain.TaskWorkspace{}, fmt.Errorf("task does not match a connected project: %w", domain.ErrConflict)
	}
	manager := ProjectSource{StoragePath: w.StoragePath}
	source, err := manager.inspectGit(ctx, *project.LocalPath)
	if err != nil {
		return domain.TaskWorkspace{}, err
	}
	if source.HeadCommit != project.HeadCommit || source.IsDirty {
		return domain.TaskWorkspace{}, fmt.Errorf("source checkout is not the planned clean base: %w", domain.ErrConflict)
	}
	resolvedBase, err := manager.run(ctx, *project.LocalPath, "rev-parse", "--verify", baseCommit+"^{commit}")
	if err != nil || strings.TrimSpace(resolvedBase) != baseCommit {
		return domain.TaskWorkspace{}, fmt.Errorf("approved workspace base commit is unavailable: %w", domain.ErrConflict)
	}
	if _, err := manager.run(ctx, *project.LocalPath, "merge-base", "--is-ancestor", project.HeadCommit, baseCommit); err != nil {
		return domain.TaskWorkspace{}, fmt.Errorf("approved workspace base does not descend from clean source revision: %w", domain.ErrConflict)
	}
	storage, err := canonicalStoragePath(w.StoragePath)
	if err != nil {
		return domain.TaskWorkspace{}, err
	}
	if err := os.MkdirAll(storage, 0o750); err != nil {
		return domain.TaskWorkspace{}, fmt.Errorf("create task worktree storage: %w", err)
	}
	shortID := compactID(task.ID, 12)
	branchName := "ai/task-" + sanitizeName(project.Name) + "-" + shortID
	worktreeDirectory := sanitizeName(project.Name) + "-task-" + shortID
	repositoryDirectory := filepath.Base(filepath.Clean(*project.LocalPath))
	if repositoryDirectory == "" || repositoryDirectory == "." || repositoryDirectory == string(filepath.Separator) {
		return domain.TaskWorkspace{}, fmt.Errorf("source checkout has no repository directory name: %w", domain.ErrValidation)
	}
	worktreePath := filepath.Join(storage, worktreeDirectory, repositoryDirectory)
	if !pathWithin(storage, worktreePath) {
		return domain.TaskWorkspace{}, fmt.Errorf("task worktree escaped configured storage: %w", domain.ErrForbidden)
	}
	if _, statErr := os.Stat(worktreePath); errors.Is(statErr, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(worktreePath), 0o750); err != nil {
			return domain.TaskWorkspace{}, fmt.Errorf("create task worktree directory: %w", err)
		}
		if _, branchErr := manager.run(ctx, *project.LocalPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branchName); branchErr == nil {
			if _, err := manager.run(ctx, *project.LocalPath, "worktree", "add", worktreePath, branchName); err != nil {
				return domain.TaskWorkspace{}, fmt.Errorf("restore task worktree: %w", err)
			}
		} else if _, err := manager.run(ctx, *project.LocalPath, "worktree", "add", "-b", branchName, worktreePath, baseCommit); err != nil {
			return domain.TaskWorkspace{}, fmt.Errorf("create task worktree: %w", err)
		}
	} else if statErr != nil {
		return domain.TaskWorkspace{}, fmt.Errorf("inspect task worktree: %w", statErr)
	}
	worktreeSource, err := manager.inspectGit(ctx, worktreePath)
	if err != nil {
		return domain.TaskWorkspace{}, fmt.Errorf("inspect task worktree: %w", err)
	}
	if worktreeSource.CurrentBranch != branchName {
		return domain.TaskWorkspace{}, fmt.Errorf("task worktree branch mismatch: %w", domain.ErrConflict)
	}
	sourceCommon, err := gitCommonDirectory(ctx, manager, *project.LocalPath)
	if err != nil {
		return domain.TaskWorkspace{}, err
	}
	worktreeCommon, err := gitCommonDirectory(ctx, manager, worktreePath)
	if err != nil {
		return domain.TaskWorkspace{}, err
	}
	if sourceCommon != worktreeCommon {
		return domain.TaskWorkspace{}, fmt.Errorf("task worktree belongs to another Git repository: %w", domain.ErrConflict)
	}
	if _, err := manager.run(ctx, worktreePath, "merge-base", "--is-ancestor", baseCommit, "HEAD"); err != nil {
		return domain.TaskWorkspace{}, fmt.Errorf("task branch does not descend from approved execution base: %w", domain.ErrConflict)
	}
	if err := prepareNodeDependencies(ctx, worktreePath, task.VerificationCommands); err != nil {
		return domain.TaskWorkspace{}, err
	}
	return domain.TaskWorkspace{Path: worktreePath, BranchName: branchName, BaseCommit: baseCommit}, nil
}

func isFullCommitSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func prepareNodeDependencies(ctx context.Context, worktreePath string, verificationCommands []string) error {
	needsNPM := false
	for _, command := range verificationCommands {
		if strings.HasPrefix(strings.TrimSpace(command), "npm ") {
			needsNPM = true
			break
		}
	}
	if !needsNPM {
		return nil
	}
	if info, err := os.Stat(filepath.Join(worktreePath, "node_modules", ".package-lock.json")); err == nil && info.Mode().IsRegular() {
		return nil
	}
	lockPath := filepath.Join(worktreePath, "package-lock.json")
	lockInfo, err := os.Lstat(lockPath)
	if err != nil {
		return fmt.Errorf("npm verification requires package-lock.json: %w", domain.ErrValidation)
	}
	if !lockInfo.Mode().IsRegular() || lockInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("npm package-lock.json must be a regular file: %w", domain.ErrValidation)
	}
	command := exec.CommandContext(ctx, "npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund")
	command.Dir = worktreePath
	command.Env = safeCommandEnvironment(os.Environ())
	var output limitedBuffer
	output.limit = maxVerificationOutput
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return fmt.Errorf("prepare deterministic npm dependencies: %w: %s", err, strings.TrimSpace(output.String()))
	}
	return nil
}

func (w TaskWorktree) Inspect(
	ctx context.Context,
	project domain.Project,
	workspace domain.TaskWorkspace,
) (domain.WorkspaceState, error) {
	if err := w.validateWorkspace(ctx, project, workspace); err != nil {
		return domain.WorkspaceState{}, err
	}
	manager := ProjectSource{StoragePath: w.StoragePath}
	tracked, err := manager.run(ctx, workspace.Path, "diff", "--name-only", "-z", workspace.BaseCommit, "--")
	if err != nil {
		return domain.WorkspaceState{}, fmt.Errorf("list tracked task changes: %w", err)
	}
	untracked, err := manager.run(ctx, workspace.Path, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return domain.WorkspaceState{}, fmt.Errorf("list untracked task changes: %w", err)
	}
	changed := uniqueNULPaths(tracked + untracked)
	diff, err := manager.run(ctx, workspace.Path, "diff", "--no-ext-diff", "--no-color", workspace.BaseCommit, "--")
	if err != nil {
		return domain.WorkspaceState{}, fmt.Errorf("read task diff: %w", err)
	}
	head, err := manager.run(ctx, workspace.Path, "rev-parse", "HEAD")
	if err != nil {
		return domain.WorkspaceState{}, fmt.Errorf("resolve task worktree HEAD: %w", err)
	}
	return domain.WorkspaceState{
		ChangedFiles: changed, Diff: diff, HeadCommit: strings.TrimSpace(head),
	}, nil
}

func (w TaskWorktree) Snapshot(
	ctx context.Context,
	project domain.Project,
	workspace domain.TaskWorkspace,
) (domain.WorkspaceSnapshot, error) {
	if err := w.validateWorkspace(ctx, project, workspace); err != nil {
		return domain.WorkspaceSnapshot{}, err
	}
	manager := ProjectSource{StoragePath: w.StoragePath}
	listed, err := manager.run(ctx, workspace.Path, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return domain.WorkspaceSnapshot{}, fmt.Errorf("list snapshot workspace files: %w", err)
	}
	paths := uniqueNULPaths(listed)
	if len(paths) > 100_000 {
		return domain.WorkspaceSnapshot{}, fmt.Errorf("workspace snapshot exceeds the file-count limit: %w", domain.ErrValidation)
	}
	snapshot := domain.WorkspaceSnapshot{Files: make(map[string]string, len(paths))}
	for _, relative := range paths {
		if err := ctx.Err(); err != nil {
			return domain.WorkspaceSnapshot{}, err
		}
		clean, err := taskRelativePath(relative)
		if err != nil {
			return domain.WorkspaceSnapshot{}, err
		}
		filename := filepath.Join(workspace.Path, clean)
		info, err := os.Lstat(filename)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return domain.WorkspaceSnapshot{}, fmt.Errorf("inspect snapshot file %s: %w", relative, err)
		}
		mode := info.Mode().Perm() & 0o111
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			resolved, resolveErr := filepath.EvalSymlinks(filename)
			if resolveErr != nil || !pathWithin(workspace.Path, resolved) {
				return domain.WorkspaceSnapshot{}, fmt.Errorf("snapshot symlink %s escaped managed workspace: %w", relative, domain.ErrForbidden)
			}
			target, readErr := os.Readlink(filename)
			if readErr != nil {
				return domain.WorkspaceSnapshot{}, fmt.Errorf("read snapshot symlink %s: %w", relative, readErr)
			}
			hash := sha256.Sum256([]byte("symlink\x00" + target))
			snapshot.Files[relative] = fmt.Sprintf("symlink:%03o:%s", mode, hex.EncodeToString(hash[:]))
		case info.Mode().IsRegular():
			file, openErr := os.Open(filename)
			if openErr != nil {
				return domain.WorkspaceSnapshot{}, fmt.Errorf("open snapshot file %s: %w", relative, openErr)
			}
			hash := sha256.New()
			_, copyErr := io.Copy(hash, file)
			closeErr := file.Close()
			if copyErr != nil {
				return domain.WorkspaceSnapshot{}, fmt.Errorf("hash snapshot file %s: %w", relative, copyErr)
			}
			if closeErr != nil {
				return domain.WorkspaceSnapshot{}, fmt.Errorf("close snapshot file %s: %w", relative, closeErr)
			}
			snapshot.Files[relative] = fmt.Sprintf("file:%03o:%s", mode, hex.EncodeToString(hash.Sum(nil)))
		default:
			return domain.WorkspaceSnapshot{}, fmt.Errorf("snapshot path %s is not a regular file or safe symlink: %w", relative, domain.ErrValidation)
		}
	}
	return snapshot, nil
}

func (w TaskWorktree) RunCheck(
	ctx context.Context,
	workspace domain.TaskWorkspace,
	requested string,
) (domain.WorkspaceCheckResult, error) {
	commandName, arguments, ok := allowedVerificationCommand(strings.TrimSpace(requested))
	if !ok {
		return domain.WorkspaceCheckResult{}, fmt.Errorf("verification command %q is not allowlisted: %w", requested, domain.ErrForbidden)
	}
	command := exec.CommandContext(ctx, commandName, arguments...)
	command.Dir = workspace.Path
	command.Env = safeCommandEnvironment(os.Environ())
	var output limitedBuffer
	output.limit = maxVerificationOutput
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	exitCode := 0
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		} else {
			return domain.WorkspaceCheckResult{}, fmt.Errorf("run verification command %q: %w", requested, err)
		}
	}
	return domain.WorkspaceCheckResult{
		Command: requested, ExitCode: exitCode, Output: output.String(),
	}, nil
}

func (w TaskWorktree) ReadArtifact(
	_ context.Context,
	workspace domain.TaskWorkspace,
	path string,
	maxBytes int64,
) ([]byte, error) {
	relative, err := taskRelativePath(path)
	if err != nil {
		return nil, err
	}
	if maxBytes < 1 || maxBytes > 10<<20 {
		return nil, fmt.Errorf("invalid artifact size limit: %w", domain.ErrValidation)
	}
	target := filepath.Join(workspace.Path, relative)
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return nil, fmt.Errorf("resolve artifact %s: %w", path, err)
	}
	if !pathWithin(workspace.Path, resolved) {
		return nil, fmt.Errorf("artifact escaped task worktree: %w", domain.ErrForbidden)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("stat artifact %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, fmt.Errorf("artifact %s is not a bounded regular file: %w", path, domain.ErrValidation)
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read artifact %s: %w", path, err)
	}
	return content, nil
}

func (w TaskWorktree) Commit(
	ctx context.Context,
	project domain.Project,
	task domain.Task,
	workspace domain.TaskWorkspace,
	verifiedFiles []string,
) (string, error) {
	state, err := w.Inspect(ctx, project, workspace)
	if err != nil {
		return "", err
	}
	if !samePaths(state.ChangedFiles, verifiedFiles) {
		return "", fmt.Errorf("verified files no longer match the task worktree: %w", domain.ErrWriteScope)
	}
	manager := ProjectSource{StoragePath: w.StoragePath}
	status, err := manager.run(ctx, workspace.Path, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return "", fmt.Errorf("inspect task commit state: %w", err)
	}
	if strings.TrimSpace(status) == "" {
		if state.HeadCommit != workspace.BaseCommit && len(state.ChangedFiles) > 0 {
			return state.HeadCommit, nil
		}
		return "", fmt.Errorf("task has no changes to commit: %w", domain.ErrValidation)
	}
	if len(verifiedFiles) == 0 {
		return "", fmt.Errorf("task has no verified files to commit: %w", domain.ErrValidation)
	}
	arguments := append([]string{"add", "--"}, verifiedFiles...)
	if _, err := manager.run(ctx, workspace.Path, arguments...); err != nil {
		return "", fmt.Errorf("stage verified task files: %w", err)
	}
	staged, err := manager.run(ctx, workspace.Path, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return "", fmt.Errorf("list staged task files: %w", err)
	}
	if !samePaths(uniqueNULPaths(staged), verifiedFiles) {
		return "", fmt.Errorf("staged task files do not match verification: %w", domain.ErrWriteScope)
	}
	if _, err := manager.run(ctx, workspace.Path, "diff", "--cached", "--check"); err != nil {
		return "", fmt.Errorf("staged task diff check failed: %w", err)
	}
	authorName := strings.TrimSpace(w.AuthorName)
	if authorName == "" {
		authorName = "Course Dev Orchestrator"
	}
	authorEmail := strings.TrimSpace(w.AuthorEmail)
	if authorEmail == "" {
		authorEmail = "orchestrator@local.invalid"
	}
	message := "feat(ai): " + strings.TrimSpace(task.Title)
	if len(message) > 200 {
		message = message[:200]
	}
	if _, err := manager.run(ctx, workspace.Path,
		"-c", "user.name="+authorName, "-c", "user.email="+authorEmail,
		"commit", "--no-gpg-sign", "-m", message,
	); err != nil {
		return "", fmt.Errorf("commit verified task changes: %w", err)
	}
	commitSHA, err := manager.run(ctx, workspace.Path, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve task commit: %w", err)
	}
	sourceAfter, err := manager.inspectGit(ctx, *project.LocalPath)
	if err != nil {
		return "", err
	}
	if sourceAfter.HeadCommit != project.HeadCommit || sourceAfter.IsDirty {
		return "", fmt.Errorf("source checkout changed during task execution: %w", domain.ErrWriteScope)
	}
	return strings.TrimSpace(commitSHA), nil
}

// VerifyCommit mechanically proves that an orchestrator-created commit is a
// single-parent child of the approved baseline and contains exactly the
// verified path set.
func (w TaskWorktree) VerifyCommit(
	ctx context.Context,
	project domain.Project,
	workspace domain.TaskWorkspace,
	commit, expectedParent string,
	expectedFiles []string,
) error {
	if err := w.validateWorkspace(ctx, project, workspace); err != nil {
		return err
	}
	manager := ProjectSource{StoragePath: w.StoragePath}
	parents, err := manager.run(ctx, workspace.Path, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return fmt.Errorf("inspect shard commit parents: %w", err)
	}
	fields := strings.Fields(parents)
	if len(fields) != 2 || fields[0] != commit || fields[1] != expectedParent {
		return fmt.Errorf("shard commit is not a direct child of its frozen baseline: %w", domain.ErrConflict)
	}
	changed, err := manager.run(ctx, workspace.Path, "diff-tree", "--no-commit-id", "--name-only", "-r", "-z", commit)
	if err != nil {
		return fmt.Errorf("inspect shard commit paths: %w", err)
	}
	if !samePaths(uniqueNULPaths(changed), expectedFiles) {
		return fmt.Errorf("shard commit paths differ from the verified diff: %w", domain.ErrWriteScope)
	}
	if _, err := manager.run(ctx, workspace.Path, "diff", "--check", expectedParent, commit); err != nil {
		return fmt.Errorf("shard commit diff check failed: %w", err)
	}
	head, err := manager.run(ctx, workspace.Path, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(head) != commit {
		return fmt.Errorf("managed worktree HEAD differs from created shard commit: %w", domain.ErrConflict)
	}
	return nil
}

// ApplyVerifiedCommit replays one already verified sibling commit into a
// managed integration worktree. Calls are made sequentially in deterministic
// route/shard order by the orchestrator activity.
func (w TaskWorktree) ApplyVerifiedCommit(
	ctx context.Context,
	project domain.Project,
	workspace domain.TaskWorkspace,
	commit, commonBaseline string,
	expectedFiles []string,
) (string, error) {
	if err := w.validateWorkspace(ctx, project, workspace); err != nil {
		return "", err
	}
	manager := ProjectSource{StoragePath: w.StoragePath}
	parents, err := manager.run(ctx, workspace.Path, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return "", fmt.Errorf("inspect verified shard commit: %w", err)
	}
	fields := strings.Fields(parents)
	if len(fields) != 2 || fields[0] != commit || fields[1] != commonBaseline {
		return "", fmt.Errorf("assembly received a shard commit from another baseline: %w", domain.ErrConflict)
	}
	changed, err := manager.run(ctx, workspace.Path, "diff-tree", "--no-commit-id", "--name-only", "-r", "-z", commit)
	if err != nil || !samePaths(uniqueNULPaths(changed), expectedFiles) {
		return "", fmt.Errorf("assembly shard paths differ from verified attempt: %w", domain.ErrWriteScope)
	}
	status, err := manager.run(ctx, workspace.Path, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil || strings.TrimSpace(status) != "" {
		return "", fmt.Errorf("integration workspace is not clean before serialized commit application: %w", domain.ErrConflict)
	}
	previousHead, err := manager.run(ctx, workspace.Path, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve integration tip before shard application: %w", err)
	}
	if _, err := manager.run(ctx, workspace.Path, "-c", "user.name=Course Dev Orchestrator", "-c", "user.email=orchestrator@local.invalid", "cherry-pick", "--no-edit", commit); err != nil {
		_, _ = manager.run(ctx, workspace.Path, "cherry-pick", "--abort")
		return "", fmt.Errorf("serialized shard application conflict: %w", domain.ErrConflict)
	}
	newHead, err := manager.run(ctx, workspace.Path, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve integration tip after shard application: %w", err)
	}
	newParents, err := manager.run(ctx, workspace.Path, "rev-list", "--parents", "-n", "1", strings.TrimSpace(newHead))
	newParentFields := strings.Fields(newParents)
	if err != nil || len(newParentFields) != 2 || newParentFields[1] != strings.TrimSpace(previousHead) {
		return "", fmt.Errorf("serialized shard application did not create a direct integration child: %w", domain.ErrConflict)
	}
	return strings.TrimSpace(newHead), nil
}

func (w TaskWorktree) validateWorkspace(
	ctx context.Context,
	project domain.Project,
	workspace domain.TaskWorkspace,
) error {
	if project.LocalPath == nil || workspace.Path == "" || !isFullCommitSHA(workspace.BaseCommit) {
		return fmt.Errorf("invalid task workspace identity: %w", domain.ErrConflict)
	}
	storage, err := canonicalStoragePath(w.StoragePath)
	if err != nil {
		return err
	}
	canonical, err := canonicalExistingPath(workspace.Path)
	if err != nil {
		return err
	}
	if !pathWithin(storage, canonical) {
		return fmt.Errorf("task workspace is outside configured storage: %w", domain.ErrForbidden)
	}
	manager := ProjectSource{StoragePath: w.StoragePath}
	common, err := gitCommonDirectory(ctx, manager, canonical)
	if err != nil {
		return err
	}
	sourceCommon, err := gitCommonDirectory(ctx, manager, *project.LocalPath)
	if err != nil {
		return err
	}
	if common != sourceCommon {
		return fmt.Errorf("task workspace repository mismatch: %w", domain.ErrConflict)
	}
	source, err := manager.inspectGit(ctx, *project.LocalPath)
	if err != nil || source.HeadCommit != project.HeadCommit || source.IsDirty {
		return fmt.Errorf("connected source changed while shard work was active: %w", domain.ErrConflict)
	}
	if _, err := manager.run(ctx, *project.LocalPath, "merge-base", "--is-ancestor", project.HeadCommit, workspace.BaseCommit); err != nil {
		return fmt.Errorf("task workspace base does not descend from the clean source revision: %w", domain.ErrConflict)
	}
	if _, err := manager.run(ctx, canonical, "merge-base", "--is-ancestor", workspace.BaseCommit, "HEAD"); err != nil {
		return fmt.Errorf("task workspace base mismatch: %w", domain.ErrConflict)
	}
	return nil
}

func allowedVerificationCommand(command string) (string, []string, bool) {
	if name, arguments, ok := allowedTestingPolicyCommand(command); ok {
		return name, arguments, true
	}
	switch command {
	case "git diff --check":
		return "git", []string{"-c", "core.hooksPath=/dev/null", "diff", "--check"}, true
	case "go test ./...":
		return "go", []string{"test", "./..."}, true
	case "go test ./... -count=1":
		return "go", []string{"test", "./...", "-count=1"}, true
	case "go vet ./...":
		return "go", []string{"vet", "./..."}, true
	case "npm test":
		return "npm", []string{"test"}, true
	case "npm run lint":
		return "npm", []string{"run", "lint"}, true
	case "npm run build":
		return "npm", []string{"run", "build"}, true
	default:
		return "", nil, false
	}
}

func allowedTestingPolicyCommand(command string) (string, []string, bool) {
	parts := strings.Fields(command)
	if len(parts) == 5 && parts[0] == "node20" && parts[1] == ".ai/testing/policy/policy-runner.cjs" &&
		parts[2] == "verify-lock" && parts[3] == "--lock" && parts[4] == ".ai/testing/policy/policy-lock.json" {
		return "/usr/local/bin/node20", parts[1:], true
	}
	if len(parts) == 15 && parts[0] == "node20" && parts[1] == ".ai/testing/policy/policy-runner.cjs" &&
		parts[2] == "verify" && parts[3] == "--repo" && parts[4] == "." &&
		parts[5] == "--command" && parts[6] == "verify:pr" && parts[7] == "--output-dir" && parts[8] == "." &&
		parts[9] == "--run-id" && safePolicyRunID(parts[10]) && parts[11] == "--base" && fullGitSHA(parts[12]) &&
		parts[13] == "--head" && fullGitSHA(parts[14]) {
		return "/usr/local/bin/node20", parts[1:], true
	}
	if (len(parts) == 13 || len(parts) == 15) && parts[0] == "node20" && parts[1] == ".ai/testing/policy/policy-runner.cjs" &&
		parts[2] == "agent-dod-from-run" && parts[3] == "--repo" && parts[4] == "." && parts[5] == "--aggregate" {
		runID := safePolicyRunIDValue(parts[6])
		if safePolicyRunID(runID) && parts[6] == fmt.Sprintf("test-results/%s/verify/pr/test-result.v1.json", runID) &&
			parts[7] == "--base" && fullGitSHA(parts[8]) && parts[9] == "--head" && fullGitSHA(parts[10]) {
			outputIndex := 11
			if len(parts) == 15 {
				if parts[11] != "--business-acceptance-evidence" || !safePolicyEvidenceValue(parts[12]) {
					return "", nil, false
				}
				outputIndex = 13
			}
			if parts[outputIndex] == "--output" && parts[outputIndex+1] == fmt.Sprintf("test-results/%s/agent-dod.v1.json", runID) {
				return "/usr/local/bin/node20", parts[1:], true
			}
		}
	}
	return "", nil, false
}

func safePolicyEvidenceValue(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("._:/#?=&%-", character)) {
			return false
		}
	}
	return true
}

func safePolicyRunID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || (index > 0 && (character == '.' || character == '_' || character == '-')) {
			continue
		}
		return false
	}
	return true
}

func safePolicyRunIDValue(path string) string {
	if !strings.HasPrefix(path, "test-results/") || !strings.HasSuffix(path, "/verify/pr/test-result.v1.json") {
		return "invalid"
	}
	value := strings.TrimPrefix(path, "test-results/")
	return strings.TrimSuffix(value, "/verify/pr/test-result.v1.json")
}

func fullGitSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func safeCommandEnvironment(source []string) []string {
	allowed := map[string]struct{}{
		"PATH": {}, "HOME": {}, "USER": {}, "LOGNAME": {}, "SHELL": {}, "TMPDIR": {},
		"LANG": {}, "LC_ALL": {}, "TERM": {}, "CI": {}, "GOPATH": {}, "GOCACHE": {},
		"GOMODCACHE": {}, "npm_config_cache": {}, "TEST_DATABASE_URL": {},
	}
	result := make([]string, 0, len(allowed)+4)
	for _, pair := range source {
		key, _, found := strings.Cut(pair, "=")
		if _, ok := allowed[key]; found && ok {
			result = append(result, pair)
		}
	}
	return append(result,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never",
	)
}

func compactID(value string, limit int) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "-", "")
	value = sanitizeName(value)
	if len(value) > limit {
		value = value[:limit]
	}
	return value
}

func taskRelativePath(value string) (string, error) {
	relative := filepath.Clean(filepath.FromSlash(strings.TrimSpace(value)))
	if value == "" || filepath.IsAbs(relative) || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) || strings.ContainsRune(value, '\x00') {
		return "", fmt.Errorf("unsafe task path %q: %w", value, domain.ErrWriteScope)
	}
	return relative, nil
}

func uniqueNULPaths(value string) []string {
	seen := make(map[string]struct{})
	for _, item := range strings.Split(value, "\x00") {
		item = filepath.ToSlash(filepath.Clean(item))
		if item != "" && item != "." {
			seen[item] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for item := range seen {
		result = append(result, item)
	}
	sort.Strings(result)
	return result
}

func samePaths(left, right []string) bool {
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	for index := range left {
		left[index] = filepath.ToSlash(filepath.Clean(left[index]))
	}
	for index := range right {
		right[index] = filepath.ToSlash(filepath.Clean(right[index]))
	}
	sort.Strings(left)
	sort.Strings(right)
	return strings.Join(left, "\x00") == strings.Join(right, "\x00")
}

var _ repository.TaskWorktree = TaskWorktree{}
