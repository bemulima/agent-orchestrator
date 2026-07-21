package git

import (
	"context"
	"errors"
	"fmt"
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
	if task.ProjectID != project.ID || project.LocalPath == nil || project.HeadCommit == "" {
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
	storage, err := canonicalStoragePath(w.StoragePath)
	if err != nil {
		return domain.TaskWorkspace{}, err
	}
	if err := os.MkdirAll(storage, 0o750); err != nil {
		return domain.TaskWorkspace{}, fmt.Errorf("create task worktree storage: %w", err)
	}
	shortID := compactID(task.ID, 12)
	branchName := "ai/task-" + sanitizeName(project.Name) + "-" + shortID
	worktreePath := filepath.Join(storage, sanitizeName(project.Name)+"-task-"+shortID)
	if !pathWithin(storage, worktreePath) {
		return domain.TaskWorkspace{}, fmt.Errorf("task worktree escaped configured storage: %w", domain.ErrForbidden)
	}
	if _, statErr := os.Stat(worktreePath); errors.Is(statErr, os.ErrNotExist) {
		if _, branchErr := manager.run(ctx, *project.LocalPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branchName); branchErr == nil {
			if _, err := manager.run(ctx, *project.LocalPath, "worktree", "add", worktreePath, branchName); err != nil {
				return domain.TaskWorkspace{}, fmt.Errorf("restore task worktree: %w", err)
			}
		} else if _, err := manager.run(ctx, *project.LocalPath, "worktree", "add", "-b", branchName, worktreePath, project.HeadCommit); err != nil {
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
	if _, err := manager.run(ctx, worktreePath, "merge-base", "--is-ancestor", project.HeadCommit, "HEAD"); err != nil {
		return domain.TaskWorkspace{}, fmt.Errorf("task branch does not descend from planned base: %w", domain.ErrConflict)
	}
	return domain.TaskWorkspace{Path: worktreePath, BranchName: branchName, BaseCommit: project.HeadCommit}, nil
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

func (w TaskWorktree) validateWorkspace(
	ctx context.Context,
	project domain.Project,
	workspace domain.TaskWorkspace,
) error {
	if project.LocalPath == nil || workspace.Path == "" || workspace.BaseCommit != project.HeadCommit {
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
	if _, err := manager.run(ctx, canonical, "merge-base", "--is-ancestor", workspace.BaseCommit, "HEAD"); err != nil {
		return fmt.Errorf("task workspace base mismatch: %w", domain.ErrConflict)
	}
	return nil
}

func allowedVerificationCommand(command string) (string, []string, bool) {
	switch command {
	case "git diff --check":
		return "git", []string{"-c", "core.hooksPath=/dev/null", "diff", "--check"}, true
	case "go test ./...":
		return "go", []string{"test", "./..."}, true
	case "go vet ./...":
		return "go", []string{"vet", "./..."}, true
	case "npm test":
		return "npm", []string{"test"}, true
	case "npm run lint":
		return "npm", []string{"run", "lint"}, true
	default:
		return "", nil, false
	}
}

func safeCommandEnvironment(source []string) []string {
	allowed := map[string]struct{}{
		"PATH": {}, "HOME": {}, "USER": {}, "LOGNAME": {}, "SHELL": {}, "TMPDIR": {},
		"LANG": {}, "LC_ALL": {}, "TERM": {}, "CI": {}, "GOPATH": {}, "GOCACHE": {},
		"GOMODCACHE": {}, "npm_config_cache": {},
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
