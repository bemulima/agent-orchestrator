package currentverification

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/architecturemanifest"
	"github.com/bemulima/agent-orchestrator/internal/discovery"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestVerifyAcceptsUntrackedArchitectureRolloutArtifacts(t *testing.T) {
	root := fixtureRepository(t)
	writeCompleteArchitecture(t, root)

	report, err := Verify(context.Background(), []string{root})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if report.Status != StatusVerified || len(report.Services) != 1 {
		t.Fatalf("report status = %#v", report)
	}
	service := report.Services[0]
	if service.Status != StatusVerified || service.Audit == nil || service.Audit.OperationsMissingManifests != 0 {
		t.Fatalf("service = %#v", service)
	}
	if !service.Git.Dirty || service.Git.CodeDirty || len(service.Git.ArchitectureRolloutChanges) == 0 {
		t.Fatalf("architecture-only Git state = %#v", service.Git)
	}
	if report.Audit.TotalBackendServices != 1 || report.Audit.TotalDiscoveredOperations != 1 || report.Audit.OperationsWithManifests != 1 {
		t.Fatalf("audit counters = %#v", report.Audit)
	}
}

func TestVerifyRecordsUnexpectedWorkingTreeChangesAfterFreshRescan(t *testing.T) {
	root := fixtureRepository(t)
	writeCompleteArchitecture(t, root)
	if err := os.WriteFile(filepath.Join(root, "uncommitted.go"), []byte("package fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := Verify(context.Background(), []string{root})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if report.Status != StatusVerified || report.Services[0].Status != StatusVerified {
		t.Fatalf("statuses = %#v", report)
	}
	if !containsFinding(report.Services[0].Findings, "working_tree_dirty_rescanned", StatusVerified) {
		t.Fatalf("dirty-rescanned finding not present: %#v", report.Services[0].Findings)
	}
}

func TestVerifyClassifiesMissingArchitectureAsFailed(t *testing.T) {
	root := fixtureRepository(t)

	report, err := Verify(context.Background(), []string{root})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if report.Status != StatusFailed || report.Services[0].Status != StatusFailed {
		t.Fatalf("statuses = %#v", report)
	}
	if report.Audit.ServicesMissingManifests != 1 || report.Audit.OperationsMissingManifests != 1 {
		t.Fatalf("missing audit = %#v", report.Audit)
	}
	if !containsFinding(report.Services[0].Findings, "manifest_operations_missing", StatusFailed) {
		t.Fatalf("missing finding not present: %#v", report.Services[0].Findings)
	}
}

func TestVerifyRejectsDuplicateCanonicalRootWithoutHidingVerifiedService(t *testing.T) {
	root := fixtureRepository(t)
	writeCompleteArchitecture(t, root)

	report, err := Verify(context.Background(), []string{root, root})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if report.Status != StatusFailed || len(report.Services) != 2 || report.Audit.TotalBackendServices != 1 {
		t.Fatalf("duplicate report = %#v", report)
	}
	if !containsFinding(report.Findings, "duplicate_canonical_root", StatusFailed) {
		t.Fatalf("duplicate finding not present: %#v", report.Findings)
	}
}

func TestParsePorcelainV1ZRedactsPrivateEnvironmentNames(t *testing.T) {
	changes, err := parsePorcelainV1Z([]byte("?? .env.production\x00 M .ai/architecture/service.yaml\x00"))
	if err != nil {
		t.Fatalf("parsePorcelainV1Z() error = %v", err)
	}
	if len(changes) != 2 || changes[0].Path != ".ai/architecture/service.yaml" || changes[1].Path != "<private-environment-file>" {
		t.Fatalf("changes = %#v", changes)
	}
}

func TestConflictCodesAreStableAndDeduplicated(t *testing.T) {
	codes := conflictCodes([]domain.Evidence{
		{Name: "instruction_mismatch"}, {Name: "architecture_http_operation_mismatch"}, {Name: "instruction_mismatch"}, {Name: ""},
	})
	if got, want := strings.Join(codes, ","), "architecture_http_operation_mismatch,instruction_mismatch"; got != want {
		t.Fatalf("conflictCodes() = %q, want %q", got, want)
	}
}

func fixtureRepository(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "fixture")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, "go.mod", "module example.test/fixture\n\ngo 1.23\n")
	writeFixtureFile(t, root, "router.go", `package fixture

import "net/http"

func Routes() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(http.ResponseWriter, *http.Request) {})
}
`)
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.email", "fixture@example.test")
	runGit(t, root, "config", "user.name", "Fixture")
	runGit(t, root, "add", "go.mod", "router.go")
	runGit(t, root, "commit", "-m", "initial")
	return root
}

func writeCompleteArchitecture(t *testing.T, root string) {
	t.Helper()
	report, err := discovery.NewScanner(discovery.Config{}).Scan(context.Background(), domain.Project{
		ID: "fixture-current", Name: "fixture", RepositoryRole: domain.RepositoryRoleService,
	}, domain.RepositorySource{LocalPath: root})
	if err != nil {
		t.Fatalf("fixture discovery: %v", err)
	}
	if len(report.Operations) != 1 || report.Operations[0].Method != "GET" || report.Operations[0].Path != "/health" {
		t.Fatalf("fixture operations = %#v", report.Operations)
	}
	if _, err := architecturemanifest.ScaffoldHTTPManifests(root, report); err != nil {
		t.Fatalf("ScaffoldHTTPManifests() error = %v", err)
	}
	if _, err := architecturemanifest.GenerateFiles(root); err != nil {
		t.Fatalf("GenerateFiles() error = %v", err)
	}
}

func writeFixtureFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}

func containsFinding(findings []Finding, code string, status Status) bool {
	for _, finding := range findings {
		if finding.Code == code && finding.Status == status {
			return true
		}
	}
	return false
}
