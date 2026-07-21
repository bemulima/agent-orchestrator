package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestProjectSource_ConnectLocalValidatesAndCanonicalizes(t *testing.T) {
	allowedRoot := t.TempDir()
	repositoryPath := filepath.Join(allowedRoot, "service")
	initRepository(t, repositoryPath)
	subdirectory := filepath.Join(repositoryPath, "internal")
	if err := os.Mkdir(subdirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	sourceManager := ProjectSource{AllowedRoots: []string{allowedRoot}, StoragePath: filepath.Join(t.TempDir(), "storage")}

	source, err := sourceManager.ConnectLocal(context.Background(), subdirectory)
	if err != nil {
		t.Fatalf("ConnectLocal() error = %v", err)
	}
	canonicalRepository, err := filepath.EvalSymlinks(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if source.LocalPath != canonicalRepository || source.CurrentBranch != "main" || source.HeadCommit == "" || source.IsDirty {
		t.Fatalf("source = %#v", source)
	}
	if source.Identity != "local:"+filepath.Join(canonicalRepository, ".git") {
		t.Fatalf("identity = %q", source.Identity)
	}
}

func TestProjectSource_ConnectLocalRejectsUnsafePaths(t *testing.T) {
	allowedRoot := t.TempDir()
	outside := t.TempDir()
	outsideRepository := filepath.Join(outside, "outside")
	initRepository(t, outsideRepository)
	sourceManager := ProjectSource{AllowedRoots: []string{allowedRoot}, StoragePath: filepath.Join(t.TempDir(), "storage")}

	_, err := sourceManager.ConnectLocal(context.Background(), outsideRepository)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("outside error = %v, want forbidden", err)
	}
	_, err = sourceManager.ConnectLocal(context.Background(), "relative/path")
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("relative error = %v, want validation", err)
	}
	notRepository := filepath.Join(allowedRoot, "not-a-repository")
	if err := os.Mkdir(notRepository, 0o750); err != nil {
		t.Fatal(err)
	}
	_, err = sourceManager.ConnectLocal(context.Background(), notRepository)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("non-Git error = %v, want validation", err)
	}

	symlink := filepath.Join(allowedRoot, "escape")
	if err := os.Symlink(outsideRepository, symlink); err != nil {
		t.Fatal(err)
	}
	_, err = sourceManager.ConnectLocal(context.Background(), symlink)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("symlink escape error = %v, want forbidden", err)
	}
}

func TestProjectSource_WorktreesShareIdentity(t *testing.T) {
	allowedRoot := t.TempDir()
	repositoryPath := filepath.Join(allowedRoot, "service")
	worktreePath := filepath.Join(allowedRoot, "service-issue")
	initRepository(t, repositoryPath)
	runTestGit(t, repositoryPath, "worktree", "add", "-b", "feature", worktreePath)
	sourceManager := ProjectSource{AllowedRoots: []string{allowedRoot}, StoragePath: filepath.Join(t.TempDir(), "storage")}

	canonical, err := sourceManager.ConnectLocal(context.Background(), repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := sourceManager.ConnectLocal(context.Background(), worktreePath)
	if err != nil {
		t.Fatal(err)
	}
	if canonical.Identity != worktree.Identity {
		t.Fatalf("identities differ: %q != %q", canonical.Identity, worktree.Identity)
	}
}

func TestProjectSource_DetectsRemoteMainAsDefaultFromFeatureBranch(t *testing.T) {
	allowedRoot := t.TempDir()
	repositoryPath := filepath.Join(allowedRoot, "service")
	bareRepository := filepath.Join(allowedRoot, "remote.git")
	initRepository(t, repositoryPath)
	runTestGit(t, allowedRoot, "clone", "--bare", repositoryPath, bareRepository)
	runTestGit(t, repositoryPath, "remote", "add", "origin", bareRepository)
	runTestGit(t, repositoryPath, "fetch", "origin")
	runTestGit(t, repositoryPath, "checkout", "-b", "feature/test")
	manager := ProjectSource{AllowedRoots: []string{allowedRoot}, StoragePath: filepath.Join(t.TempDir(), "storage")}

	source, err := manager.ConnectLocal(context.Background(), repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if source.CurrentBranch != "feature/test" || source.DefaultBranch != "main" {
		t.Fatalf("branches = current %q default %q", source.CurrentBranch, source.DefaultBranch)
	}
}

func TestProjectSource_ConnectGitClonesIdempotently(t *testing.T) {
	allowedRoot := t.TempDir()
	sourceRepository := filepath.Join(allowedRoot, "source")
	bareRepository := filepath.Join(allowedRoot, "remote.git")
	initRepository(t, sourceRepository)
	runTestGit(t, allowedRoot, "clone", "--bare", sourceRepository, bareRepository)
	manager := ProjectSource{
		AllowedRoots:         []string{allowedRoot},
		StoragePath:          filepath.Join(t.TempDir(), "managed"),
		AllowFileURLsForTest: true,
	}
	gitURL := "file://" + bareRepository

	first, err := manager.ConnectGit(context.Background(), gitURL)
	if err != nil {
		t.Fatalf("first ConnectGit() error = %v", err)
	}
	second, err := manager.ConnectGit(context.Background(), gitURL)
	if err != nil {
		t.Fatalf("second ConnectGit() error = %v", err)
	}
	if first.LocalPath != second.LocalPath || first.Identity != second.Identity || first.HeadCommit != second.HeadCommit {
		t.Fatalf("managed clones not idempotent: first=%#v second=%#v", first, second)
	}
}

func TestProjectSource_NormalizesRemoteIdentityAndRejectsCredentials(t *testing.T) {
	manager := ProjectSource{}
	sshIdentity, _, _, err := manager.normalizeGitURL("git@github.com:bemulima/example.git")
	if err != nil {
		t.Fatal(err)
	}
	httpsIdentity, _, _, err := manager.normalizeGitURL("https://github.com/bemulima/example.git")
	if err != nil {
		t.Fatal(err)
	}
	if sshIdentity != httpsIdentity {
		t.Fatalf("identities differ: %q != %q", sshIdentity, httpsIdentity)
	}
	_, _, _, err = manager.normalizeGitURL("https://user:secret@github.com/bemulima/example.git")
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("credential URL error = %v, want validation", err)
	}
	_, _, _, err = manager.normalizeGitURL("file:///tmp/repository")
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("file URL error = %v, want validation", err)
	}
}

func initRepository(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, path, "init", "-b", "main")
	runTestGit(t, path, "config", "user.email", "fixture@example.test")
	runTestGit(t, path, "config", "user.name", "Fixture")
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("# Fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, path, "add", "README.md")
	runTestGit(t, path, "commit", "-m", "initial")
}

func runTestGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func TestSanitizeName(t *testing.T) {
	if got, want := sanitizeName("Example Service.git"), "example-service"; got != want {
		t.Fatalf("sanitizeName() = %q, want %q", got, want)
	}
	if got := fmt.Sprint(sanitizeName("!!!")); got != "repository" {
		t.Fatalf("sanitizeName(!!!) = %q", got)
	}
}
