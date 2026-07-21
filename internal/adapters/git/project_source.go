package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

const maxGitOutput = 1 << 20

var scpGitURL = regexp.MustCompile(`^([^/@\s]+)@([^:/\s]+):(.+)$`)

// ProjectSource resolves user-owned local repositories and collision-safe
// managed clones. It never runs commands that change a user-owned checkout.
type ProjectSource struct {
	AllowedRoots         []string
	StoragePath          string
	GitBinary            string
	AllowFileURLsForTest bool
}

func (s ProjectSource) ConnectLocal(ctx context.Context, path string) (domain.RepositorySource, error) {
	if !filepath.IsAbs(path) {
		return domain.RepositorySource{}, fmt.Errorf("local repository path must be absolute: %w", domain.ErrValidation)
	}
	canonical, err := canonicalExistingPath(path)
	if err != nil {
		return domain.RepositorySource{}, err
	}
	if !s.isAllowed(canonical) {
		return domain.RepositorySource{}, fmt.Errorf("repository path is outside allowed roots: %w", domain.ErrForbidden)
	}
	source, err := s.inspectGit(ctx, canonical)
	if err != nil {
		return domain.RepositorySource{}, err
	}
	if !s.isAllowed(source.LocalPath) {
		return domain.RepositorySource{}, fmt.Errorf("Git root is outside allowed roots: %w", domain.ErrForbidden)
	}
	return source, nil
}

func (s ProjectSource) ConnectGit(ctx context.Context, rawURL string) (domain.RepositorySource, error) {
	identity, safeURL, name, err := s.normalizeGitURL(rawURL)
	if err != nil {
		return domain.RepositorySource{}, err
	}
	storage, err := canonicalStoragePath(s.StoragePath)
	if err != nil {
		return domain.RepositorySource{}, err
	}
	if err := os.MkdirAll(storage, 0o750); err != nil {
		return domain.RepositorySource{}, fmt.Errorf("create repository storage: %w", err)
	}
	hash := sha256.Sum256([]byte(identity))
	destination := filepath.Join(storage, sanitizeName(name)+"-"+hex.EncodeToString(hash[:6]))
	if !pathWithin(storage, destination) {
		return domain.RepositorySource{}, fmt.Errorf("managed clone path escaped storage: %w", domain.ErrForbidden)
	}

	if info, statErr := os.Stat(destination); statErr == nil {
		if !info.IsDir() {
			return domain.RepositorySource{}, fmt.Errorf("managed clone destination is not a directory: %w", domain.ErrConflict)
		}
		source, inspectErr := s.inspectGit(ctx, destination)
		if inspectErr != nil {
			return domain.RepositorySource{}, fmt.Errorf("inspect existing managed clone: %w", inspectErr)
		}
		if source.Identity != identity {
			return domain.RepositorySource{}, fmt.Errorf("managed clone collision: %w", domain.ErrConflict)
		}
		source.GitURL = safeURL
		return source, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return domain.RepositorySource{}, fmt.Errorf("stat managed clone destination: %w", statErr)
	}

	temporaryRoot, err := os.MkdirTemp(storage, ".clone-*")
	if err != nil {
		return domain.RepositorySource{}, fmt.Errorf("create temporary clone directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporaryRoot) }()
	temporaryCheckout := filepath.Join(temporaryRoot, "checkout")
	if _, err := s.run(ctx, "", "clone", "--quiet", "--no-tags", "--single-branch", "--", safeURL, temporaryCheckout); err != nil {
		return domain.RepositorySource{}, fmt.Errorf("clone Git repository: %w", err)
	}
	if err := os.Rename(temporaryCheckout, destination); err != nil {
		if _, statErr := os.Stat(destination); statErr != nil {
			return domain.RepositorySource{}, fmt.Errorf("install managed clone: %w", err)
		}
	}
	source, err := s.inspectGit(ctx, destination)
	if err != nil {
		return domain.RepositorySource{}, fmt.Errorf("inspect managed clone: %w", err)
	}
	if source.Identity != identity {
		return domain.RepositorySource{}, fmt.Errorf("cloned repository identity mismatch: %w", domain.ErrConflict)
	}
	source.GitURL = safeURL
	return source, nil
}

func (s ProjectSource) Inspect(ctx context.Context, path string) (domain.RepositorySource, error) {
	canonical, err := canonicalExistingPath(path)
	if err != nil {
		return domain.RepositorySource{}, err
	}
	if !s.isAllowed(canonical) && !pathWithinCanonical(s.StoragePath, canonical) {
		return domain.RepositorySource{}, fmt.Errorf("repository path is outside managed and allowed roots: %w", domain.ErrForbidden)
	}
	return s.inspectGit(ctx, canonical)
}

func (s ProjectSource) inspectGit(ctx context.Context, path string) (domain.RepositorySource, error) {
	inside, err := s.run(ctx, path, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(inside) != "true" {
		return domain.RepositorySource{}, fmt.Errorf("path is not a Git worktree: %w", domain.ErrValidation)
	}
	root, err := s.run(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return domain.RepositorySource{}, fmt.Errorf("resolve Git root: %w", domain.ErrValidation)
	}
	root, err = canonicalExistingPath(strings.TrimSpace(root))
	if err != nil {
		return domain.RepositorySource{}, err
	}
	head, err := s.run(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return domain.RepositorySource{}, fmt.Errorf("repository has no readable HEAD commit: %w", domain.ErrValidation)
	}
	branch, _ := s.run(ctx, root, "symbolic-ref", "--short", "-q", "HEAD")
	branch = strings.TrimSpace(branch)
	defaultBranch := branch
	if originHead, originErr := s.run(ctx, root, "symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD"); originErr == nil {
		defaultBranch = strings.TrimPrefix(strings.TrimSpace(originHead), "origin/")
	} else if _, mainErr := s.run(ctx, root, "show-ref", "--verify", "--quiet", "refs/remotes/origin/main"); mainErr == nil {
		defaultBranch = "main"
	} else if _, masterErr := s.run(ctx, root, "show-ref", "--verify", "--quiet", "refs/remotes/origin/master"); masterErr == nil {
		defaultBranch = "master"
	}
	if defaultBranch == "" {
		defaultBranch = "main"
	}

	rawOrigin, _ := s.run(ctx, root, "remote", "get-url", "origin")
	rawOrigin = strings.TrimSpace(rawOrigin)
	identity := ""
	safeURL := ""
	name := filepath.Base(root)
	if rawOrigin != "" {
		if normalizedIdentity, normalizedURL, remoteName, normalizeErr := s.normalizeGitURL(rawOrigin); normalizeErr == nil {
			identity = normalizedIdentity
			safeURL = normalizedURL
			name = remoteName
		}
	}
	if identity == "" {
		commonDirectory, commonErr := s.run(ctx, root, "rev-parse", "--git-common-dir")
		if commonErr != nil {
			return domain.RepositorySource{}, fmt.Errorf("resolve Git common directory: %w", commonErr)
		}
		commonDirectory = strings.TrimSpace(commonDirectory)
		if !filepath.IsAbs(commonDirectory) {
			commonDirectory = filepath.Join(root, commonDirectory)
		}
		commonDirectory, commonErr = canonicalExistingPath(commonDirectory)
		if commonErr != nil {
			return domain.RepositorySource{}, commonErr
		}
		identity = "local:" + commonDirectory
	}

	status, statusErr := s.run(ctx, root, "status", "--porcelain=v1", "--untracked-files=normal")
	if statusErr != nil {
		return domain.RepositorySource{}, fmt.Errorf("inspect worktree status: %w", statusErr)
	}
	return domain.RepositorySource{
		Name:          sanitizeName(name),
		Identity:      identity,
		LocalPath:     root,
		GitURL:        safeURL,
		DefaultBranch: defaultBranch,
		CurrentBranch: branch,
		HeadCommit:    strings.TrimSpace(head),
		IsDirty:       strings.TrimSpace(status) != "",
	}, nil
}

func (s ProjectSource) normalizeGitURL(raw string) (identity, safeURL, name string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 || strings.HasPrefix(raw, "-") || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", "", "", fmt.Errorf("invalid Git URL: %w", domain.ErrValidation)
	}
	if match := scpGitURL.FindStringSubmatch(raw); match != nil {
		repositoryPath := cleanRepositoryPath(match[3])
		if repositoryPath == "" {
			return "", "", "", fmt.Errorf("invalid Git URL path: %w", domain.ErrValidation)
		}
		host := strings.ToLower(match[2])
		return "git:" + host + "/" + repositoryPath, raw, filepath.Base(repositoryPath), nil
	}
	parsed, parseErr := url.Parse(raw)
	if parseErr != nil || parsed.Scheme == "" || parsed.Host == "" && parsed.Scheme != "file" {
		return "", "", "", fmt.Errorf("invalid Git URL: %w", domain.ErrValidation)
	}
	allowedScheme := parsed.Scheme == "https" || parsed.Scheme == "http" || parsed.Scheme == "ssh" || parsed.Scheme == "git"
	if parsed.Scheme == "file" && s.AllowFileURLsForTest {
		allowedScheme = true
	}
	if !allowedScheme {
		return "", "", "", fmt.Errorf("unsupported Git URL scheme: %w", domain.ErrValidation)
	}
	if (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.User != nil {
		return "", "", "", fmt.Errorf("credentials in Git URL are forbidden: %w", domain.ErrValidation)
	}
	if parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); hasPassword {
			return "", "", "", fmt.Errorf("password in Git URL is forbidden: %w", domain.ErrValidation)
		}
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", "", fmt.Errorf("query and fragment in Git URL are forbidden: %w", domain.ErrValidation)
	}
	repositoryPath := cleanRepositoryPath(parsed.Path)
	if repositoryPath == "" {
		return "", "", "", fmt.Errorf("invalid Git URL path: %w", domain.ErrValidation)
	}
	host := strings.ToLower(parsed.Host)
	if parsed.Scheme == "file" {
		canonical, pathErr := canonicalExistingPath(parsed.Path)
		if pathErr != nil || !s.isAllowed(canonical) {
			return "", "", "", fmt.Errorf("test file Git URL is outside allowed roots: %w", domain.ErrForbidden)
		}
		host = "file"
		repositoryPath = strings.TrimPrefix(filepath.ToSlash(canonical), "/")
	}
	return "git:" + host + "/" + repositoryPath, raw, filepath.Base(repositoryPath), nil
}

func (s ProjectSource) run(ctx context.Context, directory string, args ...string) (string, error) {
	binary := s.GitBinary
	if binary == "" {
		binary = "git"
	}
	safeArgs := []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}
	if s.AllowFileURLsForTest {
		safeArgs = append(safeArgs, "-c", "protocol.file.allow=always")
	}
	safeArgs = append(safeArgs, args...)
	command := exec.CommandContext(ctx, binary, safeArgs...)
	command.Dir = directory
	command.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=never",
		"GIT_ASKPASS=/bin/false",
		"SSH_ASKPASS=/bin/false",
	)
	var stdout limitedBuffer
	var stderr limitedBuffer
	stdout.limit = maxGitOutput
	stderr.limit = maxGitOutput
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", errors.New(message)
	}
	// Preserve leading spaces: porcelain status uses them as part of its
	// two-column state machine. Git's trailing line ending is not meaningful.
	return strings.TrimRight(stdout.String(), "\r\n"), nil
}

func (s ProjectSource) isAllowed(path string) bool {
	for _, root := range s.AllowedRoots {
		if pathWithinCanonical(root, path) {
			return true
		}
	}
	return false
}

func canonicalExistingPath(path string) (string, error) {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("repository path does not exist: %w", domain.ErrNotFound)
		}
		return "", fmt.Errorf("resolve repository symlinks: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("stat repository path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repository path is not a directory: %w", domain.ErrValidation)
	}
	return filepath.Clean(canonical), nil
}

func canonicalStoragePath(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return "", fmt.Errorf("invalid repository storage path: %w", domain.ErrValidation)
	}
	cleaned := filepath.Clean(path)
	if canonical, err := filepath.EvalSymlinks(cleaned); err == nil {
		if canonical == string(filepath.Separator) {
			return "", fmt.Errorf("repository storage cannot resolve to filesystem root: %w", domain.ErrForbidden)
		}
		return canonical, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("resolve repository storage: %w", err)
	}
	parent := filepath.Dir(cleaned)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return "", fmt.Errorf("create repository storage parent: %w", err)
	}
	canonicalParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", fmt.Errorf("resolve repository storage parent: %w", err)
	}
	return filepath.Join(canonicalParent, filepath.Base(cleaned)), nil
}

func pathWithinCanonical(root, target string) bool {
	canonicalRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return false
	}
	return pathWithin(canonicalRoot, target)
}

func pathWithin(root, target string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil {
		return false
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func cleanRepositoryPath(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	if path == "" || path == "." || strings.Contains(path, "../") || strings.HasPrefix(path, "..") {
		return ""
	}
	return path
}

func sanitizeName(name string) string {
	name = strings.TrimSuffix(filepath.Base(strings.TrimSpace(name)), ".git")
	var result strings.Builder
	for _, character := range strings.ToLower(name) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '-' || character == '_' {
			result.WriteRune(character)
		} else {
			result.WriteByte('-')
		}
	}
	cleaned := strings.Trim(result.String(), "-_")
	if cleaned == "" {
		return "repository"
	}
	return cleaned
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = true
		return originalLength, nil
	}
	if len(data) > remaining {
		data = data[:remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(data)
	return originalLength, nil
}

func (b *limitedBuffer) String() string { return b.buffer.String() }
