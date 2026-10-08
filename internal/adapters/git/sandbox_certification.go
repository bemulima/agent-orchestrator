package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// CertificationCommitObjects keeps bare objects only: no branch, worktree,
// checkout, push or delivery. Called by the trusted certification coordinator
// after independent verification. The model never receives this object store.
func CertificationCommitObjects(ctx context.Context, store string, before, after map[string][]byte, allowed string) (string, string, error) {
	if len(before) != len(after) || len(before) > 32 {
		return "", "", fmt.Errorf("certification snapshot cardinality changed")
	}
	changed := false
	for name, old := range before {
		value, ok := after[name]
		if !ok {
			return "", "", fmt.Errorf("certification path disappeared")
		}
		if !bytes.Equal(old, value) {
			if name != allowed {
				return "", "", fmt.Errorf("certification sibling changed")
			}
			changed = true
		}
		if filepath.IsAbs(name) || strings.Contains(name, "..") || strings.ContainsAny(name, "\x00\r\n\\") || len(value) > 262144 {
			return "", "", fmt.Errorf("certification object scope invalid")
		}
	}
	if !changed {
		return "", "", fmt.Errorf("certification has no approved edit")
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_AUTHOR_NAME=CDO certification", "GIT_AUTHOR_EMAIL=cdo-certification@example.invalid", "GIT_COMMITTER_NAME=CDO certification", "GIT_COMMITTER_EMAIL=cdo-certification@example.invalid", "GIT_AUTHOR_DATE=2026-10-05T00:00:00Z", "GIT_COMMITTER_DATE=2026-10-05T00:00:00Z"}
	run := func(input []byte, args ...string) (string, error) {
		c := exec.CommandContext(ctx, "git", args...)
		c.Env = env
		c.Stdin = bytes.NewReader(input)
		b, e := c.Output()
		if e != nil {
			return "", fmt.Errorf("certification object operation failed: %w", e)
		}
		return strings.TrimSpace(string(b)), nil
	}
	if _, err := run(nil, "init", "--bare", store); err != nil {
		return "", "", err
	}
	var tree func(map[string][]byte) (string, error)
	tree = func(files map[string][]byte) (string, error) {
		entries := map[string][]byte{}
		directories := map[string]map[string][]byte{}
		for n, b := range files {
			parts := strings.SplitN(n, "/", 2)
			if len(parts) == 1 {
				entries[n] = b
			} else {
				if directories[parts[0]] == nil {
					directories[parts[0]] = map[string][]byte{}
				}
				directories[parts[0]][parts[1]] = b
			}
		}
		names := []string{}
		for n := range entries {
			names = append(names, n)
		}
		for n := range directories {
			names = append(names, n)
		}
		sort.Strings(names)
		var input bytes.Buffer
		for _, n := range names {
			if children, ok := directories[n]; ok {
				h, e := tree(children)
				if e != nil {
					return "", e
				}
				fmt.Fprintf(&input, "040000 tree %s\t%s\x00", h, n)
			} else {
				h, e := run(entries[n], "--git-dir="+store, "hash-object", "-w", "--stdin")
				if e != nil {
					return "", e
				}
				fmt.Fprintf(&input, "100644 blob %s\t%s\x00", h, n)
			}
		}
		return run(input.Bytes(), "--git-dir="+store, "mktree", "-z")
	}
	oldTree, err := tree(before)
	if err != nil {
		return "", "", err
	}
	baseline, err := run([]byte("Disposable sandbox certification baseline\n"), "--git-dir="+store, "commit-tree", oldTree)
	if err != nil {
		return "", "", err
	}
	newTree, err := tree(after)
	if err != nil {
		return "", "", err
	}
	commit, err := run([]byte("Verified disposable broker edit after independent GREEN\n"), "--git-dir="+store, "commit-tree", newTree, "-p", baseline)
	return baseline, commit, err
}
