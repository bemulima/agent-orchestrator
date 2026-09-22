package architecturemanifest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"gopkg.in/yaml.v3"
)

func TestReconcileHTTPPreservesSemanticServiceAndNonHTTP(t *testing.T) {
	root, oldReport := reconcileFixture(t)
	service, _, _, err := scaffoldService(root)
	if err != nil {
		t.Fatal(err)
	}
	service.Purpose.Value = "Own and manage orders"
	worker := scaffoldHTTPManifest(service.ID, "outbox", discoveredHTTPOperation{method: "GET", path: "/", evidence: service.Evidence})
	worker.Type = domain.ArchitectureOperationWorker
	worker.Identity = domain.ArchitectureOperationIdentity{Transport: "worker", Worker: &domain.ArchitectureWorkerIdentity{Name: "outbox"}}
	if err := ValidateOperation(worker); err != nil {
		t.Fatal(err)
	}
	workerBytes, _ := yaml.Marshal(worker)
	workerRelative := "operations/outbox.yaml"
	writeReconcileTestFile(t, root, ".ai/architecture/"+workerRelative, workerBytes)
	service.OperationManifests = append(service.OperationManifests, workerRelative)
	service.EndpointGroups = append(service.EndpointGroups, domain.ArchitectureEndpointGroup{ID: "workers", Name: "Workers", Description: service.Purpose, Operations: []string{"outbox"}})
	serviceBytes, _ := yaml.Marshal(service)
	writeReconcileTestFile(t, root, serviceManifestPath, serviceBytes)
	if _, err := GenerateFiles(root); err != nil {
		t.Fatal(err)
	}
	workerMermaid, _ := os.ReadFile(filepath.Join(root, ".ai/architecture/operations/outbox.mmd"))
	oldEndpoint := service.OperationManifests[0]
	newReport := oldReport
	newReport.Operations = []domain.DiscoveredOperation{
		{Type: domain.ArchitectureOperationHTTP, Protocol: "http", Method: "GET", Path: "/api/v1/items", SourcePath: "router.go"},
		{Type: domain.ArchitectureOperationHTTP, Protocol: "http", Method: "GET", Path: "/admin/v1/items", SourcePath: "router.go"},
	}
	result, err := ReconcileHTTPManifests(root, newReport)
	if err != nil {
		t.Fatal(err)
	}
	if result.DiscoveredHTTP != 2 || len(result.CreatedPaths) != 2 || len(result.RemovedPaths) != 2 || len(result.UpdatedPaths) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(root, ".ai/architecture", oldEndpoint)); !os.IsNotExist(err) {
		t.Fatalf("old endpoint remains: %v", err)
	}
	assertFileEquals(t, filepath.Join(root, ".ai/architecture/"+workerRelative), string(workerBytes))
	assertFileEquals(t, filepath.Join(root, ".ai/architecture/operations/outbox.mmd"), string(workerMermaid))
	updated, _, _, err := scaffoldService(root)
	if err != nil {
		t.Fatal(err)
	}
	service.EndpointGroups, service.OperationManifests = updated.EndpointGroups, updated.OperationManifests
	if !reflect.DeepEqual(service, updated) {
		t.Fatal("non-HTTP service semantics changed")
	}
	if _, err := GenerateFiles(root); err != nil {
		t.Fatal(err)
	}
	repeated, err := ReconcileHTTPManifests(root, newReport)
	if err != nil {
		t.Fatal(err)
	}
	if len(repeated.CreatedPaths)+len(repeated.UpdatedPaths)+len(repeated.RemovedPaths) != 0 {
		t.Fatalf("not idempotent: %#v", repeated)
	}
}

func TestReconcileHTTPRejectsAuthoredStaleManifestWithoutWrites(t *testing.T) {
	root, report := reconcileFixture(t)
	service, _, _, _ := scaffoldService(root)
	endpointPath := ".ai/architecture/" + service.OperationManifests[0]
	content, _ := readSecureFile(root, endpointPath)
	op, _ := ParseOperation(content)
	op.BusinessTask.Value = "Read known order business data"
	content, _ = yaml.Marshal(op)
	writeReconcileTestFile(t, root, endpointPath, content)
	serviceBytes, _ := readSecureFile(root, serviceManifestPath)
	report.Operations[0].Path = "/api/items"
	if _, err := ReconcileHTTPManifests(root, report); err == nil || !strings.Contains(err.Error(), "authored") {
		t.Fatalf("error = %v", err)
	}
	assertFileEquals(t, filepath.Join(root, endpointPath), string(content))
	assertFileEquals(t, filepath.Join(root, serviceManifestPath), string(serviceBytes))
}

func TestReconcileHTTPPreservesCurrentAuthoredManifest(t *testing.T) {
	root, report := reconcileFixture(t)
	service, _, _, _ := scaffoldService(root)
	endpointPath := ".ai/architecture/" + service.OperationManifests[0]
	content, _ := readSecureFile(root, endpointPath)
	op, _ := ParseOperation(content)
	op.BusinessTask.Value = "Read order business data"
	content, _ = yaml.Marshal(op)
	writeReconcileTestFile(t, root, endpointPath, content)
	result, err := ReconcileHTTPManifests(root, report)
	if err != nil {
		t.Fatal(err)
	}
	if result.PreservedHTTP != 1 || len(result.RemovedPaths) != 0 {
		t.Fatalf("result = %#v", result)
	}
	assertFileEquals(t, filepath.Join(root, endpointPath), string(content))
}

func TestReconcileHTTPPreflightRejectsUnsafeInputs(t *testing.T) {
	for _, scenario := range []string{"old-version", "wrong-root", "warnings", "empty", "symlink", "unlisted", "authored-mermaid"} {
		t.Run(scenario, func(t *testing.T) {
			root, report := reconcileFixture(t)
			serviceBefore, _ := readSecureFile(root, serviceManifestPath)
			service, _, _, _ := scaffoldService(root)
			report.Operations[0].Path = "/api/items"
			switch scenario {
			case "old-version":
				report.SchemaVersion = 18
			case "wrong-root":
				report.RepositoryPath = filepath.Dir(root)
			case "warnings":
				report.Inventory.Warnings = []string{"file count exceeded"}
			case "empty":
				report.Operations = nil
			case "symlink":
				if err := os.Symlink(filepath.Join(root, serviceManifestPath), filepath.Join(root, ".ai/architecture/endpoints/link.yaml")); err != nil {
					t.Fatal(err)
				}
			case "unlisted":
				writeReconcileTestFile(t, root, ".ai/architecture/endpoints/unlisted.yaml", []byte("authored: true\n"))
			case "authored-mermaid":
				writeReconcileTestFile(t, root, operationMermaidRelativePath(service.OperationManifests[0]), []byte("authored diagram"))
			}
			if _, err := ReconcileHTTPManifests(root, report); err == nil {
				t.Fatal("expected preflight failure")
			}
			assertFileEquals(t, filepath.Join(root, serviceManifestPath), string(serviceBefore))
			if _, err := os.Stat(filepath.Join(root, ".ai/architecture", service.OperationManifests[0])); err != nil {
				t.Fatal("preflight removed old endpoint", err)
			}
		})
	}
}

func reconcileFixture(t *testing.T) (string, domain.DiscoveryReport) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	report := scaffoldReport(domain.DiscoveredOperation{Type: domain.ArchitectureOperationHTTP, Protocol: "http", Method: "GET", Path: "/items", SourcePath: "router.go"})
	report.SchemaVersion, report.RepositoryPath = 19, root
	report.ProjectName = "orders-api"
	if _, err := ScaffoldHTTPManifests(root, report); err != nil {
		t.Fatal(err)
	}
	return root, report
}

func writeReconcileTestFile(t *testing.T, root, relative string, content []byte) {
	t.Helper()
	full := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, content, 0644); err != nil {
		t.Fatal(err)
	}
}
