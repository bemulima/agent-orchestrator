package git

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCertificationCommitObjectsNoBranchAndOwnedDiff(t *testing.T) {
	before := map[string][]byte{"internal/value.go": []byte("before"), "contracts/frozen.go": []byte("immutable")}
	after := map[string][]byte{"internal/value.go": []byte("after"), "contracts/frozen.go": []byte("immutable")}
	store := filepath.Join(t.TempDir(), "objects.git")
	baseline, commit, err := CertificationCommitObjects(context.Background(), store, before, after, "internal/value.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(commit) != 40 || len(baseline) != 40 {
		t.Fatal("missing commit objects")
	}
	refs, err := exec.Command("git", "--git-dir="+store, "for-each-ref").Output()
	if err != nil || len(refs) != 0 {
		t.Fatal("branch/ref created", err)
	}
	diff, err := exec.Command("git", "--git-dir="+store, "diff-tree", "--no-commit-id", "--name-only", "-r", commit).Output()
	if err != nil || string(diff) != "internal/value.go\n" {
		t.Fatal(string(diff), err)
	}
	after["contracts/frozen.go"] = []byte("bad")
	if _, _, err := CertificationCommitObjects(context.Background(), filepath.Join(t.TempDir(), "rejected.git"), before, after, "internal/value.go"); err == nil {
		t.Fatal("accepted frozen mutation")
	}
}
