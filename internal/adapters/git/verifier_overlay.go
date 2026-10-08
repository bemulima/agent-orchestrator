package git

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// RunVerifierGoOverlay executes an integration-owned ephemeral test. Neither the
// assembled tree nor shard commits receive a persistent test file.
func (w TaskWorktree) RunVerifierGoOverlay(ctx context.Context, workspace domain.TaskWorkspace, source []byte) (domain.WorkspaceCheckResult, error) {
	const relative = "internal/transport/http/cdo_utc_verifier_test.go"
	if len(source) == 0 || len(source) > 64<<10 || !filepath.IsAbs(workspace.Path) {
		return domain.WorkspaceCheckResult{}, domain.ErrValidation
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), relative, source, 0)
	if err != nil || parsed.Name.Name != "http_test" {
		return domain.WorkspaceCheckResult{}, fmt.Errorf("verifier test source invalid: %w", domain.ErrValidation)
	}
	canonical, err := filepath.EvalSymlinks(workspace.Path)
	if err != nil || canonical != filepath.Clean(workspace.Path) {
		return domain.WorkspaceCheckResult{}, domain.ErrForbidden
	}
	// Resolve and check the existing package, then add only an in-memory overlay
	// file to Go's package view. A pre-existing path is never replaced.
	directory := filepath.Join(canonical, "internal", "transport", "http")
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || resolved != directory {
		return domain.WorkspaceCheckResult{}, domain.ErrForbidden
	}
	virtual := filepath.Join(canonical, filepath.FromSlash(relative))
	if _, err := os.Lstat(virtual); !os.IsNotExist(err) {
		return domain.WorkspaceCheckResult{}, fmt.Errorf("verifier virtual path already exists: %w", domain.ErrConflict)
	}
	for _, imp := range parsed.Imports {
		if strings.Contains(imp.Path.Value, `"unsafe"`) || strings.Contains(imp.Path.Value, `"reflect"`) {
			return domain.WorkspaceCheckResult{}, domain.ErrForbidden
		}
	}
	temp, err := os.MkdirTemp("", "cdo-utc-verifier-")
	if err != nil {
		return domain.WorkspaceCheckResult{}, err
	}
	defer os.RemoveAll(temp)
	test := filepath.Join(temp, "regression_test.go")
	if err := os.WriteFile(test, source, 0600); err != nil {
		return domain.WorkspaceCheckResult{}, err
	}
	overlay, err := json.Marshal(map[string]any{"Replace": map[string]string{virtual: test}})
	if err != nil {
		return domain.WorkspaceCheckResult{}, err
	}
	overlayPath := filepath.Join(temp, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0600); err != nil {
		return domain.WorkspaceCheckResult{}, err
	}
	command := exec.CommandContext(ctx, "go", "test", "-overlay", overlayPath, "-count=1", "-v", "-run", "^TestVerifier(HTTPDelegationUTC|RealHTTPUsecaseUTC)$", "./internal/transport/http")
	command.Dir = canonical
	command.Env = safeCommandEnvironment(os.Environ())
	var output limitedBuffer
	output.limit = maxVerificationOutput
	command.Stdout = &output
	command.Stderr = &output
	runErr := command.Run()
	exitCode := 0
	if runErr != nil {
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			exitCode = exit.ExitCode()
		} else {
			return domain.WorkspaceCheckResult{}, runErr
		}
	}
	return domain.WorkspaceCheckResult{Command: "go test -overlay <verifier-owned UTC regression> -count=1 -v -run ^TestVerifier(HTTPDelegationUTC|RealHTTPUsecaseUTC)$ ./internal/transport/http", ExitCode: exitCode, Output: output.String()}, nil
}
