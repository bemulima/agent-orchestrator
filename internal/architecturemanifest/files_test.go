package architecturemanifest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestGenerateFilesWritesOnlyDeterministicExpectedMermaid(t *testing.T) {
	root := t.TempDir()
	writeManifestFiles(t, root, validServiceManifest("endpoints/orders.create.yaml"), map[string]string{
		"orders.create.yaml": validHTTPOperationManifest("orders.create", "orders"),
	})

	result, err := GenerateFiles(root)
	if err != nil {
		t.Fatalf("GenerateFiles() error = %v", err)
	}
	wantPaths := []string{
		filepath.Join(root, ".ai", "architecture", "endpoints", "orders.create.mmd"),
		filepath.Join(root, ".ai", "architecture", "service.mmd"),
	}
	if len(result.OutputPaths) != len(wantPaths) {
		t.Fatalf("OutputPaths = %#v, want %#v", result.OutputPaths, wantPaths)
	}
	for index, want := range wantPaths {
		if result.OutputPaths[index] != want {
			t.Fatalf("OutputPaths[%d] = %q, want %q", index, result.OutputPaths[index], want)
		}
	}

	service, err := ParseService([]byte(validServiceManifest("endpoints/orders.create.yaml")))
	if err != nil {
		t.Fatalf("ParseService() error = %v", err)
	}
	operation, err := ParseOperation([]byte(validHTTPOperationManifest("orders.create", "orders")))
	if err != nil {
		t.Fatalf("ParseOperation() error = %v", err)
	}
	assertFileEquals(t, filepath.Join(root, ".ai", "architecture", "service.mmd"), ServiceMermaid(service, []domain.ArchitectureOperationManifest{operation}))
	assertFileEquals(t, filepath.Join(root, ".ai", "architecture", "endpoints", "orders.create.mmd"), OperationMermaid(operation))

	firstService, err := os.ReadFile(filepath.Join(root, ".ai", "architecture", "service.mmd"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateFiles(root); err != nil {
		t.Fatalf("second GenerateFiles() error = %v", err)
	}
	secondService, err := os.ReadFile(filepath.Join(root, ".ai", "architecture", "service.mmd"))
	if err != nil {
		t.Fatal(err)
	}
	if string(firstService) != string(secondService) {
		t.Fatal("generated service Mermaid changed between equal invocations")
	}
	if _, err := os.Stat(filepath.Join(root, ".ai", "architecture", "endpoints", "orders.create.yaml")); err != nil {
		t.Fatalf("source YAML changed or removed: %v", err)
	}
}

func TestGenerateFilesPreservesNonHTTPSourceLayout(t *testing.T) {
	root := t.TempDir()
	writeManifestFiles(t, root, validServiceManifest("operations/orders.consume.yaml"), map[string]string{
		"operations/orders.consume.yaml": validNATSOperationManifest("orders.consume", "orders"),
		// Generated Mermaid is presentation only and must never be parsed as a
		// second source manifest during a later scan or generation.
		"operations/ignored.mmd": "flowchart TD\n",
	})

	result, err := GenerateFiles(root)
	if err != nil {
		t.Fatalf("GenerateFiles() error = %v", err)
	}
	want := filepath.Join(root, ".ai", "architecture", "operations", "orders.consume.mmd")
	if len(result.OutputPaths) != 2 || result.OutputPaths[0] != filepath.Join(root, ".ai", "architecture", "operations", "orders.consume.mmd") ||
		result.OutputPaths[1] != filepath.Join(root, ".ai", "architecture", "service.mmd") {
		t.Fatalf("OutputPaths = %#v", result.OutputPaths)
	}
	operation, err := ParseOperation([]byte(validNATSOperationManifest("orders.consume", "orders")))
	if err != nil {
		t.Fatal(err)
	}
	assertFileEquals(t, want, OperationMermaid(operation))
	if _, err := os.Stat(filepath.Join(root, ".ai", "architecture", "endpoints", "orders.consume.mmd")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("generated Mermaid escaped source layout: %v", err)
	}
}

func TestGenerateFilesInvalidManifestDoesNotWrite(t *testing.T) {
	root := t.TempDir()
	writeManifestFiles(t, root, validServiceManifest("endpoints/orders.create.yaml"), map[string]string{
		"orders.create.yaml": "schema: architecture/v1\nkind: operation\nunexpected: true\n",
	})
	serviceOutput := filepath.Join(root, ".ai", "architecture", "service.mmd")
	if err := os.WriteFile(serviceOutput, []byte("existing service diagram\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := GenerateFiles(root)
	if err == nil {
		t.Fatal("GenerateFiles() error = nil, want invalid operation failure")
	}
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("GenerateFiles() error = %v, want ErrValidation", err)
	}
	assertFileEquals(t, serviceOutput, "existing service diagram\n")
	if _, err := os.Stat(filepath.Join(root, ".ai", "architecture", "endpoints", "orders.create.mmd")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("operation Mermaid exists after failed parse: %v", err)
	}
}

func TestGenerateFilesRejectsPathTraversalAndSymbolicLinks(t *testing.T) {
	t.Run("operation path traversal", func(t *testing.T) {
		root := t.TempDir()
		writeManifestFiles(t, root, validServiceManifest("endpoints/../outside.yaml"), nil)
		if _, err := GenerateFiles(root); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("GenerateFiles() error = %v, want ErrValidation", err)
		}
		if _, err := os.Stat(filepath.Join(root, ".ai", "architecture", "service.mmd")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("service Mermaid exists after unsafe path failure: %v", err)
		}
	})

	t.Run("operation directory symbolic link", func(t *testing.T) {
		root := t.TempDir()
		architectureDirectory := filepath.Join(root, ".ai", "architecture")
		if err := os.MkdirAll(architectureDirectory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(architectureDirectory, "service.yaml"), []byte(validServiceManifest("endpoints/orders.create.yaml")), 0o644); err != nil {
			t.Fatal(err)
		}
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(outside, "orders.create.yaml"), []byte(validHTTPOperationManifest("orders.create", "orders")), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(architectureDirectory, "endpoints")); err != nil {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		if _, err := GenerateFiles(root); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("GenerateFiles() error = %v, want ErrValidation", err)
		}
		if _, err := os.Stat(filepath.Join(outside, "orders.create.mmd")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("generator wrote through symbolic link: %v", err)
		}
	})

	t.Run("generated target symbolic link", func(t *testing.T) {
		root := t.TempDir()
		writeManifestFiles(t, root, validServiceManifest(""), nil)
		outside := filepath.Join(t.TempDir(), "outside.mmd")
		if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, ".ai", "architecture", "service.mmd")); err != nil {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		if _, err := GenerateFiles(root); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("GenerateFiles() error = %v, want ErrValidation", err)
		}
		assertFileEquals(t, outside, "outside\n")
	})
}

func writeManifestFiles(t *testing.T, root, service string, operations map[string]string) {
	t.Helper()
	directory := filepath.Join(root, ".ai", "architecture", "endpoints")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ai", "architecture", "service.yaml"), []byte(service), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range operations {
		relative := name
		if !strings.Contains(relative, "/") {
			relative = filepath.Join("endpoints", relative)
		}
		path := filepath.Join(root, ".ai", "architecture", relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func assertFileEquals(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s =\n%s\nwant:\n%s", path, got, want)
	}
}

func validServiceManifest(operationPath string) string {
	operations := "[]"
	if operationPath != "" {
		operations = "[" + operationPath + "]"
	}
	return `schema: architecture/v1
kind: service
id: orders
manifest_revision: 1
identity: {name: orders-api, kind: backend_service}
purpose: {value: Create orders, confidence: 0.8, evidence: [{source_path: README.md, start_line: 1, end_line: 1}]}
operation_manifests: ` + operations + `
evidence: [{source_path: README.md, start_line: 1, end_line: 1}]
confidence: 0.8
`
}

func validHTTPOperationManifest(id, serviceID string) string {
	return `schema: architecture/v1
kind: operation
id: ` + id + `
manifest_revision: 1
service_id: ` + serviceID + `
type: http
identity: {transport: http, http: {method: POST, path: /api/v1/orders}}
access:
  audience: {value: internal caller, confidence: 0.8, evidence: [{source_path: router.go, start_line: 1, end_line: 1}]}
  authentication: {value: unknown, confidence: 0, evidence: [{source_path: router.go, start_line: 1, end_line: 1}]}
  authorization: {value: unknown, confidence: 0, evidence: [{source_path: router.go, start_line: 1, end_line: 1}]}
  idempotency: {value: unknown, confidence: 0, evidence: [{source_path: router.go, start_line: 1, end_line: 1}]}
trigger:
  description: {value: HTTP request, confidence: 0.8, evidence: [{source_path: router.go, start_line: 1, end_line: 1}]}
business_task: {value: Create order, confidence: 0.8, evidence: [{source_path: handler.go, start_line: 1, end_line: 1}]}
evidence: [{source_path: handler.go, start_line: 1, end_line: 1}]
confidence: 0.8
`
}

func validNATSOperationManifest(id, serviceID string) string {
	return `schema: architecture/v1
kind: operation
id: ` + id + `
manifest_revision: 1
service_id: ` + serviceID + `
type: nats_event_subscriber
identity: {transport: nats, nats: {subject: orders.created.v1, role: event_subscriber}}
access:
  audience: {value: internal event producer, confidence: 0.8, evidence: [{source_path: internal/consumer.go, start_line: 1, end_line: 1}]}
  authentication: {value: unknown, confidence: 0, evidence: [{source_path: internal/consumer.go, start_line: 1, end_line: 1}]}
  authorization: {value: unknown, confidence: 0, evidence: [{source_path: internal/consumer.go, start_line: 1, end_line: 1}]}
  idempotency: {value: unknown, confidence: 0, evidence: [{source_path: internal/consumer.go, start_line: 1, end_line: 1}]}
trigger:
  description: {value: NATS event, confidence: 0.8, evidence: [{source_path: internal/consumer.go, start_line: 1, end_line: 1}]}
business_task: {value: Consume created order, confidence: 0.8, evidence: [{source_path: internal/consumer.go, start_line: 1, end_line: 1}]}
evidence: [{source_path: internal/consumer.go, start_line: 1, end_line: 1}]
confidence: 0.8
`
}
