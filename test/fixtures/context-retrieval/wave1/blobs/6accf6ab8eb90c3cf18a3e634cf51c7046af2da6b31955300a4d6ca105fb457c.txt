package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestAvatarE2E runs the cross-service avatar flow using the bash helper script.
// Requires Docker/Compose, jq, and sibling service directories present.
func TestAvatarE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in short mode")
	}
	if os.Getenv("RUN_E2E_DOCKER") != "1" {
		t.Skip("skipping e2e: set RUN_E2E_DOCKER=1 to enable Docker-dependent flow")
	}
	script, err := findScript()
	if err != nil {
		t.Fatalf("locate script: %v", err)
	}
	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(), "GOCACHE=/tmp/gocache")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("e2e script failed: %v\nOutput:\n%s", err, string(out))
	}
	t.Logf("e2e output:\n%s", string(out))
}

func findScript() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		script := filepath.Join(dir, "test", "e2e", "run-avatar-e2e.sh")
		if _, err := os.Stat(script); err == nil {
			return script, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", os.ErrNotExist
}
