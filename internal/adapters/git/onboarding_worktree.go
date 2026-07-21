package git

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type OnboardingWorktree struct {
	StoragePath string
	AuthorName  string
	AuthorEmail string
}

func (w OnboardingWorktree) DryRun(
	ctx context.Context,
	project domain.Project,
	run domain.OnboardingRun,
) (domain.OnboardingApplyResult, error) {
	checks, err := w.validate(ctx, project, run)
	if err != nil {
		return domain.OnboardingApplyResult{}, err
	}
	checks = append(checks, domain.OnboardingCheck{
		Name: "source_write", Status: "skipped", Details: "dry-run does not create a worktree or write files",
	})
	return domain.OnboardingApplyResult{Checks: checks, DryRun: true}, nil
}

func (w OnboardingWorktree) Apply(
	ctx context.Context,
	project domain.Project,
	run domain.OnboardingRun,
) (domain.OnboardingApplyResult, error) {
	checks, err := w.validate(ctx, project, run)
	if err != nil {
		return domain.OnboardingApplyResult{}, err
	}
	if project.LocalPath == nil {
		return domain.OnboardingApplyResult{}, fmt.Errorf("project has no checkout: %w", domain.ErrInvalidStatus)
	}
	sourcePath := *project.LocalPath
	manager := ProjectSource{StoragePath: w.StoragePath}
	worktreeRoot, err := canonicalStoragePath(w.StoragePath)
	if err != nil {
		return domain.OnboardingApplyResult{}, err
	}
	if err := os.MkdirAll(worktreeRoot, 0o750); err != nil {
		return domain.OnboardingApplyResult{}, fmt.Errorf("create worktree storage: %w", err)
	}
	shortID := strings.ReplaceAll(run.ID, "-", "")
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	branchName := "ai/onboard-" + sanitizeName(project.Name) + "-" + shortID
	worktreePath := filepath.Join(worktreeRoot, sanitizeName(project.Name)+"-"+shortID)
	if !pathWithin(worktreeRoot, worktreePath) {
		return domain.OnboardingApplyResult{}, fmt.Errorf("worktree escaped configured storage: %w", domain.ErrForbidden)
	}
	if _, statErr := os.Stat(worktreePath); errors.Is(statErr, os.ErrNotExist) {
		if _, branchErr := manager.run(ctx, sourcePath, "show-ref", "--verify", "--quiet", "refs/heads/"+branchName); branchErr == nil {
			if _, err := manager.run(ctx, sourcePath, "worktree", "add", worktreePath, branchName); err != nil {
				return domain.OnboardingApplyResult{}, fmt.Errorf("restore onboarding worktree: %w", err)
			}
		} else if _, err := manager.run(ctx, sourcePath, "worktree", "add", "-b", branchName, worktreePath, run.BaseCommit); err != nil {
			return domain.OnboardingApplyResult{}, fmt.Errorf("create onboarding worktree: %w", err)
		}
	} else if statErr != nil {
		return domain.OnboardingApplyResult{}, fmt.Errorf("inspect onboarding worktree: %w", statErr)
	}
	worktreeSource, err := manager.inspectGit(ctx, worktreePath)
	if err != nil {
		return domain.OnboardingApplyResult{}, fmt.Errorf("inspect onboarding worktree: %w", err)
	}
	if worktreeSource.CurrentBranch != branchName {
		return domain.OnboardingApplyResult{}, fmt.Errorf("worktree branch mismatch: %w", domain.ErrConflict)
	}
	sourceCommonDirectory, err := gitCommonDirectory(ctx, manager, sourcePath)
	if err != nil {
		return domain.OnboardingApplyResult{}, err
	}
	worktreeCommonDirectory, err := gitCommonDirectory(ctx, manager, worktreePath)
	if err != nil {
		return domain.OnboardingApplyResult{}, err
	}
	if sourceCommonDirectory != worktreeCommonDirectory {
		return domain.OnboardingApplyResult{}, fmt.Errorf("onboarding path belongs to another Git repository: %w", domain.ErrConflict)
	}
	if _, err := manager.run(ctx, worktreePath, "merge-base", "--is-ancestor", run.BaseCommit, "HEAD"); err != nil {
		return domain.OnboardingApplyResult{}, fmt.Errorf("onboarding branch does not descend from approved base: %w", domain.ErrConflict)
	}
	approvedPaths := proposedWritePaths(run.Proposal)
	committedPaths, err := manager.run(ctx, worktreePath, "diff", "--name-only", "-z", run.BaseCommit, "HEAD")
	if err != nil {
		return domain.OnboardingApplyResult{}, fmt.Errorf("inspect existing onboarding commits: %w", err)
	}
	if err := validateNULPaths(committedPaths, approvedPaths); err != nil {
		return domain.OnboardingApplyResult{}, err
	}
	checks = append(checks, domain.OnboardingCheck{Name: "worktree_isolation", Status: "passed", Details: branchName})

	for _, file := range run.Proposal.Files {
		if file.Action == domain.ProposalFileUnchanged {
			continue
		}
		if err := writeProposedFile(worktreePath, file); err != nil {
			return domain.OnboardingApplyResult{}, err
		}
	}
	status, err := manager.run(ctx, worktreePath, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return domain.OnboardingApplyResult{}, fmt.Errorf("inspect onboarding changes: %w", err)
	}
	if err := validateStatusScope(status, approvedPaths); err != nil {
		return domain.OnboardingApplyResult{}, err
	}
	if _, err := manager.run(ctx, worktreePath, "add", "--", "AGENTS.md", ".ai"); err != nil {
		return domain.OnboardingApplyResult{}, fmt.Errorf("stage onboarding files: %w", err)
	}
	staged, err := manager.run(ctx, worktreePath, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return domain.OnboardingApplyResult{}, fmt.Errorf("list staged onboarding files: %w", err)
	}
	if err := validateNULPaths(staged, approvedPaths); err != nil {
		return domain.OnboardingApplyResult{}, err
	}
	if _, err := manager.run(ctx, worktreePath, "diff", "--cached", "--check"); err != nil {
		return domain.OnboardingApplyResult{}, fmt.Errorf("onboarding diff check failed: %w", err)
	}
	checks = append(checks, domain.OnboardingCheck{Name: "write_scope", Status: "passed", Details: "only AGENTS.md and .ai/** are staged"})
	checks = append(checks, domain.OnboardingCheck{Name: "git_diff_check", Status: "passed"})

	commitSHA := worktreeSource.HeadCommit
	if staged != "" {
		authorName := strings.TrimSpace(w.AuthorName)
		if authorName == "" {
			authorName = "Course Dev Orchestrator"
		}
		authorEmail := strings.TrimSpace(w.AuthorEmail)
		if authorEmail == "" {
			authorEmail = "orchestrator@local.invalid"
		}
		if _, err := manager.run(ctx, worktreePath,
			"-c", "user.name="+authorName, "-c", "user.email="+authorEmail,
			"commit", "--no-gpg-sign", "-m", "chore(ai): onboard "+project.Name,
		); err != nil {
			return domain.OnboardingApplyResult{}, fmt.Errorf("commit onboarding proposal: %w", err)
		}
		commitSHA, err = manager.run(ctx, worktreePath, "rev-parse", "HEAD")
		if err != nil {
			return domain.OnboardingApplyResult{}, fmt.Errorf("resolve onboarding commit: %w", err)
		}
		checks = append(checks, domain.OnboardingCheck{Name: "commit", Status: "passed", Details: commitSHA})
	} else {
		checks = append(checks, domain.OnboardingCheck{Name: "commit", Status: "skipped", Details: "proposal already matches the worktree"})
	}
	sourceAfter, err := manager.inspectGit(ctx, sourcePath)
	if err != nil {
		return domain.OnboardingApplyResult{}, fmt.Errorf("verify source checkout after apply: %w", err)
	}
	if sourceAfter.HeadCommit != run.BaseCommit || sourceAfter.IsDirty {
		return domain.OnboardingApplyResult{}, fmt.Errorf("source checkout changed during onboarding: %w", domain.ErrWriteScope)
	}
	checks = append(checks, domain.OnboardingCheck{Name: "source_checkout_unchanged", Status: "passed"})
	return domain.OnboardingApplyResult{
		WorktreePath: worktreePath, BranchName: branchName, CommitSHA: strings.TrimSpace(commitSHA), Checks: checks,
	}, nil
}

func (w OnboardingWorktree) validate(
	ctx context.Context,
	project domain.Project,
	run domain.OnboardingRun,
) ([]domain.OnboardingCheck, error) {
	if project.ID != run.ProjectID || project.LocalPath == nil {
		return nil, fmt.Errorf("onboarding run does not match project: %w", domain.ErrConflict)
	}
	manager := ProjectSource{StoragePath: w.StoragePath}
	source, err := manager.inspectGit(ctx, *project.LocalPath)
	if err != nil {
		return nil, err
	}
	if source.HeadCommit != run.BaseCommit {
		return nil, fmt.Errorf("source HEAD changed since discovery: %w", domain.ErrConflict)
	}
	if source.IsDirty {
		return nil, fmt.Errorf("source checkout is dirty; approved apply requires a clean base: %w", domain.ErrConflict)
	}
	calculatedProposalChecksum, err := domain.OnboardingProposalChecksum(run.Proposal)
	if err != nil {
		return nil, err
	}
	if run.Proposal.Checksum == "" || run.Proposal.Checksum != run.ProposalChecksum ||
		calculatedProposalChecksum != run.ProposalChecksum {
		return nil, fmt.Errorf("proposal checksum mismatch: %w", domain.ErrConflict)
	}
	if len(run.Proposal.Files) == 0 {
		return nil, fmt.Errorf("onboarding proposal has no files: %w", domain.ErrValidation)
	}
	seenPaths := make(map[string]struct{}, len(run.Proposal.Files))
	for _, file := range run.Proposal.Files {
		relative, err := scopedRelativePath(file.Path)
		if err != nil {
			return nil, err
		}
		normalizedPath := filepath.ToSlash(relative)
		if _, exists := seenPaths[normalizedPath]; exists {
			return nil, fmt.Errorf("duplicate proposed path %s: %w", file.Path, domain.ErrValidation)
		}
		seenPaths[normalizedPath] = struct{}{}
		if file.Action != domain.ProposalFileCreate && file.Action != domain.ProposalFileUpdate &&
			file.Action != domain.ProposalFileUnchanged {
			return nil, fmt.Errorf("invalid action for proposed file %s: %w", file.Path, domain.ErrValidation)
		}
		if contentChecksum(file.Content) != file.Checksum {
			return nil, fmt.Errorf("proposed file checksum mismatch for %s: %w", file.Path, domain.ErrConflict)
		}
		if err := validateProposedContent(file); err != nil {
			return nil, err
		}
		if file.Action == domain.ProposalFileUnchanged {
			content, err := readSafeRegularFile(*project.LocalPath, relative)
			if err != nil {
				return nil, err
			}
			if contentChecksum(content) != file.Checksum {
				return nil, fmt.Errorf("unchanged proposed file no longer matches %s: %w", file.Path, domain.ErrConflict)
			}
		}
	}
	return []domain.OnboardingCheck{
		{Name: "base_commit", Status: "passed", Details: run.BaseCommit},
		{Name: "proposal_checksum", Status: "passed", Details: run.ProposalChecksum},
		{Name: "generated_formats", Status: "passed"},
	}, nil
}

func validateProposedContent(file domain.ProposedFile) error {
	switch strings.ToLower(filepath.Ext(file.Path)) {
	case ".yaml", ".yml":
		var value any
		if err := yaml.Unmarshal([]byte(file.Content), &value); err != nil {
			return fmt.Errorf("invalid proposed YAML %s: %w", file.Path, domain.ErrValidation)
		}
	case ".json":
		if !json.Valid([]byte(file.Content)) {
			return fmt.Errorf("invalid proposed JSON %s: %w", file.Path, domain.ErrValidation)
		}
	}
	return nil
}

func writeProposedFile(root string, file domain.ProposedFile) error {
	relative, err := scopedRelativePath(file.Path)
	if err != nil {
		return err
	}
	target := filepath.Join(root, relative)
	if !pathWithin(root, target) {
		return fmt.Errorf("proposed file escaped worktree: %w", domain.ErrForbidden)
	}
	if err := ensureSafeDirectories(root, filepath.Dir(relative)); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info, statErr := os.Lstat(target); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("onboarding target %s is not a regular file: %w", file.Path, domain.ErrForbidden)
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect onboarding target %s: %w", file.Path, statErr)
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".onboarding-*")
	if err != nil {
		return fmt.Errorf("create temporary onboarding file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set onboarding file mode: %w", err)
	}
	if _, err := temporary.WriteString(file.Content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write onboarding file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync onboarding file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close onboarding file: %w", err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("install onboarding file: %w", err)
	}
	return nil
}

func ensureSafeDirectories(root, relativeDirectory string) error {
	if relativeDirectory == "." {
		return nil
	}
	current := root
	for _, component := range strings.Split(filepath.Clean(relativeDirectory), string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o750); err != nil {
				return fmt.Errorf("create onboarding directory: %w", err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect onboarding directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("onboarding directory is unsafe: %w", domain.ErrForbidden)
		}
	}
	return nil
}

func scopedRelativePath(value string) (string, error) {
	relative := filepath.Clean(filepath.FromSlash(value))
	if filepath.IsAbs(relative) || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe onboarding path %q: %w", value, domain.ErrWriteScope)
	}
	normalized := filepath.ToSlash(relative)
	if normalized != "AGENTS.md" && !strings.HasPrefix(normalized, ".ai/") {
		return "", fmt.Errorf("onboarding path %q is outside write scope: %w", value, domain.ErrWriteScope)
	}
	return relative, nil
}

func validateStatusScope(status string, approved map[string]struct{}) error {
	for _, entry := range strings.Split(status, "\x00") {
		if entry == "" {
			continue
		}
		if len(entry) < 4 {
			return fmt.Errorf("unexpected Git status entry: %w", domain.ErrWriteScope)
		}
		path := entry[3:]
		if err := validateApprovedPath(path, approved); err != nil {
			return err
		}
	}
	return nil
}

func validateNULPaths(paths string, approved map[string]struct{}) error {
	for _, path := range strings.Split(paths, "\x00") {
		if path == "" {
			continue
		}
		if err := validateApprovedPath(path, approved); err != nil {
			return err
		}
	}
	return nil
}

func validateApprovedPath(path string, approved map[string]struct{}) error {
	relative, err := scopedRelativePath(path)
	if err != nil {
		return err
	}
	normalized := filepath.ToSlash(relative)
	if _, exists := approved[normalized]; !exists {
		return fmt.Errorf("Git change %q was not part of the approved proposal: %w", path, domain.ErrWriteScope)
	}
	return nil
}

func proposedWritePaths(proposal domain.OnboardingProposal) map[string]struct{} {
	paths := make(map[string]struct{}, len(proposal.Files))
	for _, file := range proposal.Files {
		if file.Action == domain.ProposalFileUnchanged {
			continue
		}
		if relative, err := scopedRelativePath(file.Path); err == nil {
			paths[filepath.ToSlash(relative)] = struct{}{}
		}
	}
	return paths
}

func gitCommonDirectory(ctx context.Context, manager ProjectSource, path string) (string, error) {
	commonDirectory, err := manager.run(ctx, path, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("resolve Git common directory: %w", err)
	}
	commonDirectory = strings.TrimSpace(commonDirectory)
	if !filepath.IsAbs(commonDirectory) {
		commonDirectory = filepath.Join(path, commonDirectory)
	}
	canonical, err := filepath.EvalSymlinks(commonDirectory)
	if err != nil {
		return "", fmt.Errorf("resolve Git common directory symlinks: %w", err)
	}
	return filepath.Clean(canonical), nil
}

func readSafeRegularFile(root, relative string) (string, error) {
	current := root
	parent := filepath.Dir(relative)
	if parent != "." {
		for _, component := range strings.Split(parent, string(filepath.Separator)) {
			current = filepath.Join(current, component)
			info, err := os.Lstat(current)
			if err != nil {
				return "", fmt.Errorf("inspect unchanged proposal parent: %w", err)
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return "", fmt.Errorf("unchanged proposal parent is unsafe: %w", domain.ErrForbidden)
			}
		}
	}
	target := filepath.Join(root, relative)
	info, err := os.Lstat(target)
	if err != nil {
		return "", fmt.Errorf("inspect unchanged proposal file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("unchanged proposal target is unsafe: %w", domain.ErrForbidden)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		return "", fmt.Errorf("read unchanged proposal file: %w", err)
	}
	return string(content), nil
}

func contentChecksum(content string) string {
	hash := sha256Sum([]byte(content))
	return hash
}

func sha256Sum(content []byte) string {
	// Kept local to this adapter to verify persisted proposals independently
	// from the generator implementation.
	return fmt.Sprintf("%x", sha256Bytes(content))
}

func sha256Bytes(content []byte) [32]byte {
	return sha256.Sum256(content)
}

var _ repository.OnboardingWorktree = OnboardingWorktree{}
