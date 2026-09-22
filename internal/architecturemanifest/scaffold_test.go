package architecturemanifest

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestScaffoldHTTPManifestsCreatesEvidenceBackedManifestsForManyRoutes(t *testing.T) {
	root := t.TempDir()
	report := scaffoldReport(
		domain.DiscoveredOperation{Type: domain.ArchitectureOperationHTTP, Protocol: "http", Method: "POST", Path: "/api/v1/orders", SourcePath: "internal/routes.go"},
		domain.DiscoveredOperation{Type: domain.ArchitectureOperationHTTP, Protocol: "http", Method: "GET", Path: "/api/v1/orders/{orderID}", SourcePath: "internal/routes.go"},
		domain.DiscoveredOperation{Type: domain.ArchitectureOperationHTTP, Protocol: "http", Method: "DELETE", Path: "/api/v1/orders/{orderID}", SourcePath: "internal/admin_routes.go"},
		// This is deliberately ignored: HTTP scaffolding must not invent NATS
		// manifests from the mixed discovery inventory.
		domain.DiscoveredOperation{Type: domain.ArchitectureOperationNATSEventSubscriber, Protocol: "nats", Subject: "orders.created.v1", SourcePath: "internal/consumer.go"},
	)

	result, err := ScaffoldHTTPManifests(root, report)
	if err != nil {
		t.Fatalf("ScaffoldHTTPManifests() error = %v", err)
	}
	if len(result.CreatedPaths) != 4 {
		t.Fatalf("CreatedPaths = %#v, want service plus three endpoints", result.CreatedPaths)
	}

	serviceContent, err := os.ReadFile(filepath.Join(root, ".ai", "architecture", "service.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := ParseService(serviceContent)
	if err != nil {
		t.Fatal(err)
	}
	if service.ID != "orders-api" || len(service.OperationManifests) != 3 || len(service.EndpointGroups) != 1 || len(service.EndpointGroups[0].Operations) != 3 {
		t.Fatalf("service = %#v", service)
	}
	if service.Purpose.Value != "unknown" || service.BusinessRules[0].Value != "unknown" || len(service.Evidence) != 2 {
		t.Fatalf("service includes invented semantics: %#v", service)
	}

	for _, relativePath := range service.OperationManifests {
		content, err := os.ReadFile(filepath.Join(root, ".ai", "architecture", filepath.FromSlash(relativePath)))
		if err != nil {
			t.Fatal(err)
		}
		operation, err := ParseOperation(content)
		if err != nil {
			t.Fatal(err)
		}
		if operation.Type != domain.ArchitectureOperationHTTP || operation.Identity.HTTP == nil || operation.ServiceID != service.ID {
			t.Fatalf("operation = %#v", operation)
		}
		if operation.Access.Authentication.Value != "unknown" || operation.BusinessTask.Value != "unknown" || operation.BusinessRules[0].Value != "unknown" || len(operation.Evidence) == 0 {
			t.Fatalf("operation includes invented semantics or lacks evidence: %#v", operation)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".ai", "architecture", "operations")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("non-HTTP operation directory exists: %v", err)
	}
}

func TestScaffoldHTTPManifestsPreservesExistingEndpointYAMLAndMermaid(t *testing.T) {
	root := t.TempDir()
	report := scaffoldReport(
		domain.DiscoveredOperation{Type: domain.ArchitectureOperationHTTP, Protocol: "http", Method: "GET", Path: "/api/v1/orders", SourcePath: "internal/routes.go"},
		domain.DiscoveredOperation{Type: domain.ArchitectureOperationHTTP, Protocol: "http", Method: "POST", Path: "/api/v1/orders", SourcePath: "internal/routes.go"},
	)
	existingDiscovered, err := scaffoldDiscoveredHTTP(report.Operations[:1])
	if err != nil {
		t.Fatal(err)
	}
	existing := scaffoldHTTPManifest("orders-api", "existing-orders-list", existingDiscovered[0])
	existingContent, err := yaml.Marshal(existing)
	if err != nil {
		t.Fatal(err)
	}
	endpointDirectory := filepath.Join(root, ".ai", "architecture", "endpoints")
	if err := os.MkdirAll(endpointDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	existingPath := filepath.Join(endpointDirectory, "existing-orders-list.yaml")
	mermaidPath := filepath.Join(endpointDirectory, "existing-orders-list.mmd")
	if err := os.WriteFile(existingPath, existingContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mermaidPath, []byte("user-owned diagram\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := ScaffoldHTTPManifests(root, report)
	if err != nil {
		t.Fatalf("ScaffoldHTTPManifests() error = %v", err)
	}
	if len(result.CreatedPaths) != 2 {
		t.Fatalf("CreatedPaths = %#v, want service and only the missing endpoint", result.CreatedPaths)
	}
	assertFileEquals(t, existingPath, string(existingContent))
	assertFileEquals(t, mermaidPath, "user-owned diagram\n")

	serviceContent, err := os.ReadFile(filepath.Join(root, ".ai", "architecture", "service.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := ParseService(serviceContent)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(service.OperationManifests, "endpoints/existing-orders-list.yaml") || len(service.OperationManifests) != 2 ||
		!containsString(service.EndpointGroups[0].Operations, "existing-orders-list") {
		t.Fatalf("service does not safely include existing endpoint: %#v", service)
	}
}

func TestScaffoldHTTPManifestsRejectsInvalidOrMismatchedInputsAtomically(t *testing.T) {
	t.Run("invalid existing service", func(t *testing.T) {
		root := t.TempDir()
		architectureDirectory := filepath.Join(root, ".ai", "architecture")
		if err := os.MkdirAll(architectureDirectory, 0o755); err != nil {
			t.Fatal(err)
		}
		servicePath := filepath.Join(architectureDirectory, "service.yaml")
		if err := os.WriteFile(servicePath, []byte("schema: architecture/v1\nkind: service\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ScaffoldHTTPManifests(root, scaffoldReport(domain.DiscoveredOperation{Type: domain.ArchitectureOperationHTTP, Protocol: "http", Method: "GET", Path: "/health", SourcePath: "main.go"})); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("ScaffoldHTTPManifests() error = %v, want ErrValidation", err)
		}
		assertFileEquals(t, servicePath, "schema: architecture/v1\nkind: service\n")
		if _, err := os.Stat(filepath.Join(architectureDirectory, "endpoints")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("endpoint directory exists after atomic failure: %v", err)
		}
	})

	t.Run("existing endpoint disagrees with discovery", func(t *testing.T) {
		root := t.TempDir()
		endpointDirectory := filepath.Join(root, ".ai", "architecture", "endpoints")
		if err := os.MkdirAll(endpointDirectory, 0o755); err != nil {
			t.Fatal(err)
		}
		report := scaffoldReport(domain.DiscoveredOperation{Type: domain.ArchitectureOperationHTTP, Protocol: "http", Method: "GET", Path: "/api/v1/orders", SourcePath: "internal/routes.go"})
		postOperation := scaffoldHTTPManifest("orders-api", "orders-post", discoveredHTTPOperation{
			method: "POST", path: "/api/v1/orders", evidence: []domain.ArchitectureEvidence{{SourcePath: "internal/routes.go"}},
		})
		content, err := yaml.Marshal(postOperation)
		if err != nil {
			t.Fatal(err)
		}
		endpointPath := filepath.Join(endpointDirectory, "orders-post.yaml")
		if err := os.WriteFile(endpointPath, content, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ScaffoldHTTPManifests(root, report); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("ScaffoldHTTPManifests() error = %v, want ErrValidation", err)
		}
		assertFileEquals(t, endpointPath, string(content))
		if _, err := os.Stat(filepath.Join(root, ".ai", "architecture", "service.yaml")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("service exists after atomic failure: %v", err)
		}
	})
}

func scaffoldReport(operations ...domain.DiscoveredOperation) domain.DiscoveryReport {
	return domain.DiscoveryReport{ProjectName: "Orders API", Operations: operations}
}

func containsString(values []string, wanted string) bool {
	values = append([]string(nil), values...)
	sort.Strings(values)
	index := sort.SearchStrings(values, wanted)
	return index < len(values) && values[index] == wanted
}
