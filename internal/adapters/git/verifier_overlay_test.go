package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestVerifierOverlayAddsOnlyEphemeralGoTest(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "internal", "transport", "http")
	if err := os.MkdirAll(pkg, 0700); err != nil {
		t.Fatal(err)
	}
	for path, source := range map[string]string{"go.mod": "module fixture\ngo 1.22\n", "internal/transport/http/http.go": "package http\n"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	workspace := domain.TaskWorkspace{Path: canonical}
	source := []byte(`package http_test
import "testing"
func TestVerifierRealHTTPUsecaseUTC(t *testing.T) {t.Log("verifier overlay executed")}
func TestVerifierHTTPDelegationUTC(t *testing.T) {t.Log("delegation overlay executed")}
`)
	result, err := (TaskWorktree{}).RunVerifierGoOverlay(context.Background(), workspace, source)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("overlay verification failed: %+v %v", result, err)
	}
	if !strings.Contains(result.Output, "delegation overlay executed") || !strings.Contains(result.Output, "verifier overlay executed") {
		t.Fatalf("both verifier levels must run: %s", result.Output)
	}
	if _, err := os.Stat(filepath.Join(pkg, "cdo_utc_verifier_test.go")); !os.IsNotExist(err) {
		t.Fatalf("persistent verifier file was created: %v", err)
	}
	for _, bad := range []string{"package http_test\nimport \"unsafe\"\n", "package wrong\n"} {
		if _, err := (TaskWorktree{}).RunVerifierGoOverlay(context.Background(), workspace, []byte(bad)); err == nil {
			t.Fatalf("unsafe overlay accepted: %s", bad)
		}
	}
}
