package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestCompositionRegressionMaterializationCopiesTestAndEmptySetupOnly(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source")
	require.NoError(t, os.MkdirAll(filepath.Join(sourcePath, "cmd/service"), 0750))
	mainPath := "cmd/service/main.go"
	testPath := "cmd/service/main_test.go"
	require.NoError(t, os.WriteFile(filepath.Join(sourcePath, mainPath), []byte("package main\nfunc main(){}\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(sourcePath, "go.mod"), []byte("module fixture\ngo 1.23\n"), 0644))
	runGit(t, sourcePath, "init", "-b", "main")
	runGit(t, sourcePath, "add", ".")
	runGit(t, sourcePath, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "input")
	base := runGit(t, sourcePath, "rev-parse", "HEAD")
	project := domain.Project{ID: "project", Name: "fixture", LocalPath: &sourcePath, HeadCommit: base}
	adapter := TaskWorktree{StoragePath: filepath.Join(root, "worktrees")}
	task := domain.Task{ID: "12345678-abcd-0000-0000-123456789012", ProjectID: project.ID, Title: "prior-composition"}
	previous, err := adapter.PrepareAtCommit(context.Background(), project, task, base)
	require.NoError(t, err)
	implementation := `package main
import "net/http"
func main(){}
func BuildAvailabilityRouter() http.Handler{mux:=http.NewServeMux();mux.HandleFunc("/availability",func(w http.ResponseWriter,r *http.Request){w.WriteHeader(200)});return mux}
`
	testSource := `package main
import("net/http/httptest";"testing")
func TestCompositionAvailabilityRoute(t *testing.T){response:=httptest.NewRecorder();BuildAvailabilityRouter().ServeHTTP(response,httptest.NewRequest("GET","/availability",nil));if response.Code!=200 {t.Fatalf("semantic RED: expected availability status200, actual%d",response.Code)}}
`
	require.NoError(t, os.WriteFile(filepath.Join(previous.Path, mainPath), []byte(implementation), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(previous.Path, testPath), []byte(testSource), 0644))
	commit, err := adapter.Commit(context.Background(), project, task, previous, []string{mainPath, testPath})
	require.NoError(t, err)
	priorGreen, err := adapter.RunCheck(context.Background(), previous, "go test ./... -count=1")
	require.NoError(t, err)
	require.Zero(t, priorGreen.ExitCode)
	scaffold := `package main
import "net/http"
func main(){}
func BuildAvailabilityRouter() http.Handler{return http.NewServeMux()}
`
	hash := sha256.Sum256([]byte(scaffold))
	prior := domain.CompositionAttempt{Workspace: previous, CommitSHA: commit, ChangedFiles: []string{mainPath, testPath}, WorkPackage: domain.CompositionWorkPackage{AssemblyCommit: base}, REDSetup: &domain.ShardREDSetupEvidence{Sources: map[string]string{mainPath: scaffold}, Files: map[string]string{mainPath: hex.EncodeToString(hash[:])}}}
	destinationTask := domain.Task{ID: "87654321-abcd-0000-0000-123456789012", ProjectID: project.ID, Title: "new-composition"}
	destination, err := adapter.PrepareAtCommit(context.Background(), project, destinationTask, base)
	require.NoError(t, err)
	wp := domain.CompositionWorkPackage{AssemblyCommit: base, WriteScope: domain.ShardWriteScope{Allow: []string{mainPath, testPath}}, Verification: domain.WorkPackageVerification{TestPaths: []string{testPath}}}
	copied, err := adapter.MaterializeCompositionRegression(context.Background(), project, prior, destination, wp)
	require.NoError(t, err)
	require.Equal(t, map[string]string{mainPath: scaffold, testPath: testSource}, copied)
	require.Equal(t, base, runGit(t, destination.Path, "rev-parse", "HEAD"))
	actual, err := os.ReadFile(filepath.Join(destination.Path, mainPath))
	require.NoError(t, err)
	require.Equal(t, scaffold, string(actual))
	require.NotEqual(t, implementation, string(actual))
	actualRed, err := adapter.RunCheck(context.Background(), destination, "go test ./... -count=1")
	require.NoError(t, err)
	require.NotZero(t, actualRed.ExitCode)
	require.Contains(t, actualRed.Output, "semantic RED:")
	require.Contains(t, actualRed.Output, "actual404")

	thirdTask := domain.Task{ID: "11111111-abcd-0000-0000-123456789012", ProjectID: project.ID, Title: "invalid-fixture"}
	pristine, err := adapter.PrepareAtCommit(context.Background(), project, thirdTask, base)
	require.NoError(t, err)
	badTarget := pristine
	badTarget.BaseCommit = "wrong-input"
	_, err = adapter.MaterializeCompositionRegression(context.Background(), project, prior, badTarget, wp)
	require.Error(t, err)
	outside := filepath.Join(root, "outside.go")
	require.NoError(t, os.WriteFile(outside, []byte("package outside\n"), 0644))
	require.NoError(t, os.Remove(filepath.Join(pristine.Path, mainPath)))
	require.NoError(t, os.Symlink(outside, filepath.Join(pristine.Path, mainPath)))
	_, err = adapter.MaterializeCompositionRegression(context.Background(), project, prior, pristine, wp)
	require.Error(t, err)
	require.NoError(t, os.Remove(filepath.Join(pristine.Path, mainPath)))
	require.NoError(t, os.WriteFile(filepath.Join(pristine.Path, mainPath), []byte("package main\nfunc main(){}\n"), 0644))
	bad := prior
	bad.REDSetup = &domain.ShardREDSetupEvidence{Sources: map[string]string{mainPath: scaffold}, Files: map[string]string{mainPath: "wrong-hash"}}
	_, err = adapter.MaterializeCompositionRegression(context.Background(), project, bad, pristine, wp)
	require.Error(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(previous.Path, testPath), []byte("package main\n// dirty-test\n"), 0644))
	_, err = adapter.MaterializeCompositionRegression(context.Background(), project, prior, pristine, wp)
	require.Error(t, err)
	assertSourceUnchanged(t, sourcePath, base)
}
