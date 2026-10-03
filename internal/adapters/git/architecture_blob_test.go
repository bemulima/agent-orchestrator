package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchitectureBlobReadsPinnedCommitDespiteWorkingTreeEdits(t *testing.T) {
	root := filepath.Join(t.TempDir(), "owner")
	initRepository(t, root)
	path := "declaration.json"
	raw := []byte("{\"declared\":true}\n")
	if err := os.WriteFile(filepath.Join(root, path), raw, 0600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, root, "add", path)
	runTestGit(t, root, "commit", "-m", "owner declaration")
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	commit := strings.TrimSpace(string(output))
	if err := os.WriteFile(filepath.Join(root, path), []byte("uncommitted owner edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked.json"), []byte("untracked declaration"), 0600); err != nil {
		t.Fatal(err)
	}
	resolver := ArchitectureBlobResolver{AllowedRoots: []string{filepath.Dir(root)}}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	identity := "local:" + filepath.Join(canonicalRoot, ".git")
	// Inherited Git overrides must not redirect immutable source reads.
	t.Setenv("GIT_DIR", filepath.Join(root, "absent"))
	pin, data, err := resolver.Read(context.Background(), root, identity, commit, path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	if string(data) != string(raw) || pin.ContentSHA256 != hex.EncodeToString(hash[:]) {
		t.Fatal("resolver read working tree rather than pinned blob")
	}
	if _, err := resolver.Resolve(context.Background(), root, identity, commit, "untracked.json"); err == nil {
		t.Fatal("untracked file acquired immutable pin")
	}
	for _, bad := range []string{"../outside", ".env", ".env.dist", "/absolute", "a/../declaration.json"} {
		if _, err := resolver.Resolve(context.Background(), root, identity, commit, bad); err == nil {
			t.Fatalf("unsafe path accepted: %s", bad)
		}
	}
	if _, err := resolver.Resolve(context.Background(), root, identity, "HEAD", path); err == nil {
		t.Fatal("floating commit accepted")
	}
	if _, err := resolver.Resolve(context.Background(), root, "git:github.com/other/owner", commit, path); err == nil {
		t.Fatal("wrong source identity accepted")
	}
	if _, err := (ArchitectureBlobResolver{AllowedRoots: []string{t.TempDir()}}).Resolve(context.Background(), root, identity, commit, path); err == nil {
		t.Fatal("outside-root repository accepted")
	}
	if _, err := (ArchitectureBlobResolver{AllowedRoots: resolver.AllowedRoots, MaxBytes: 1}).Resolve(context.Background(), root, identity, commit, path); err == nil {
		t.Fatal("bounded read overflow accepted")
	}
}
func TestArchitectureBlobRejectsSymlinkObjects(t *testing.T) {
	root := filepath.Join(t.TempDir(), "owner")
	initRepository(t, root)
	if err := os.Symlink("README.md", filepath.Join(root, "link.json")); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, root, "add", "link.json")
	runTestGit(t, root, "commit", "-m", "link fixture")
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = root
	raw, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (ArchitectureBlobResolver{AllowedRoots: []string{filepath.Dir(root)}}).Resolve(context.Background(), root, "local:"+filepath.Join(canonicalFixtureRoot(t, root), ".git"), strings.TrimSpace(string(raw)), "link.json"); err == nil {
		t.Fatal("symlink blob accepted as regular declaration")
	}
}

func canonicalFixtureRoot(t *testing.T, root string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func TestArchitectureBlobGitEnvironmentForbidsLazyFetch(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "inspect-git-env")
	script := `#!/bin/sh
[ "$GIT_NO_LAZY_FETCH" = 1 ] || exit 20
[ "$GIT_NO_REPLACE_OBJECTS" = 1 ] || exit 21
[ -z "$GIT_DIR" ] || exit 22
[ "$GIT_CONFIG_NOSYSTEM" = 1 ] || exit 23
printf 'safe immutable read'
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_NO_LAZY_FETCH", "0")
	t.Setenv("GIT_DIR", "must-be-removed")
	data, err := (ArchitectureBlobResolver{GitBinary: binary}).read(context.Background(), root, "cat-file", "blob", strings.Repeat("a", 40))
	if err != nil || string(data) != "safe immutable read" {
		t.Fatalf("immutable Git environment did not forbid lazy fetch: data=%q err=%v", data, err)
	}
}
