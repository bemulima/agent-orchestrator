package architecturemanifest

import (
	"strings"
	"testing"
)

func TestParseServiceAcceptsStrictArchitectureV1(t *testing.T) {
	manifest, err := ParseService([]byte(`
schema: architecture/v1
kind: service
id: teacher-agent
manifest_revision: 1
identity: {name: ms-go-teacher-agent, kind: backend_service}
purpose:
  value: Decide how a personal teacher guides a student.
  confidence: 0.9
  evidence: [{source_path: README.md, start_line: 3, end_line: 3}]
operation_manifests: [endpoints/teacher.ensure.yaml]
evidence: [{source_path: .ai/service.yaml, start_line: 2, end_line: 2}]
confidence: 0.9
`))
	if err != nil {
		t.Fatalf("ParseService() error = %v", err)
	}
	if manifest.ID != "teacher-agent" || len(manifest.OperationManifests) != 1 {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
}

func TestParseServiceAcceptsOnlyOneLevelOperationLayouts(t *testing.T) {
	for _, operationPath := range []string{
		"endpoints/teacher.ensure.yaml",
		"endpoints/teacher.ensure.yml",
		"operations/teacher.consume.yaml",
		"operations/teacher.consume.yml",
	} {
		t.Run(operationPath, func(t *testing.T) {
			content := strings.Replace(`
schema: architecture/v1
kind: service
id: teacher-agent
manifest_revision: 1
identity: {name: ms-go-teacher-agent, kind: backend_service}
purpose: {value: Decide next action, confidence: 0.9, evidence: [{source_path: README.md}]}
operation_manifests: [OPERATION_PATH]
evidence: [{source_path: README.md}]
confidence: 0.9
`, "OPERATION_PATH", operationPath, 1)
			if _, err := ParseService([]byte(content)); err != nil {
				t.Fatalf("ParseService() error = %v", err)
			}
		})
	}

	for _, operationPath := range []string{
		"endpoints/nested/teacher.ensure.yaml",
		"operations/nested/teacher.consume.yaml",
		"operations/teacher.consume.mmd",
		"workers/teacher.consume.yaml",
		"../operations/teacher.consume.yaml",
	} {
		t.Run("reject "+operationPath, func(t *testing.T) {
			content := strings.Replace(`
schema: architecture/v1
kind: service
id: teacher-agent
manifest_revision: 1
identity: {name: ms-go-teacher-agent, kind: backend_service}
purpose: {value: Decide next action, confidence: 0.9, evidence: [{source_path: README.md}]}
operation_manifests: [OPERATION_PATH]
evidence: [{source_path: README.md}]
confidence: 0.9
`, "OPERATION_PATH", operationPath, 1)
			if _, err := ParseService([]byte(content)); err == nil {
				t.Fatal("ParseService() error = nil, want validation failure")
			}
		})
	}
}

func TestParseOperationAcceptsEveryArchitectureOperationKind(t *testing.T) {
	tests := []struct {
		name     string
		typeName string
		identity string
	}{
		{"http", "http", "transport: http\nhttp: {method: POST, path: /api/v1/teacher/me}"},
		{"nats request reply", "nats_request_reply", "transport: nats\nnats: {subject: teacher.lookup.v1, role: request_handler}"},
		{"nats event subscriber", "nats_event_subscriber", "transport: nats\nnats: {subject: learning.action.closed, role: event_subscriber}"},
		{"worker", "worker", "transport: worker\nworker: {name: teacher-dialog-worker}"},
		{"scheduled", "scheduled", "transport: scheduled\nscheduled: {name: outbox-relay, schedule: '@every 5s'}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := "schema: architecture/v1\nkind: operation\nid: " + strings.ReplaceAll(test.typeName, "_", "-") + ".v1\n" +
				"manifest_revision: 1\nservice_id: teacher-agent\ntype: " + test.typeName + "\nidentity:\n  " + strings.ReplaceAll(test.identity, "\n", "\n  ") + `
access:
  audience: {value: internal callers, confidence: 0.8, evidence: [{source_path: router.go, start_line: 1, end_line: 1}]}
  authentication: {value: unknown, confidence: 0, evidence: [{source_path: router.go, start_line: 1, end_line: 1}]}
  authorization: {value: unknown, confidence: 0, evidence: [{source_path: router.go, start_line: 1, end_line: 1}]}
  idempotency: {value: unknown, confidence: 0, evidence: [{source_path: router.go, start_line: 1, end_line: 1}]}
trigger:
  description: {value: Trigger operation, confidence: 0.8, evidence: [{source_path: internal/worker.go, start_line: 1, end_line: 1}]}
business_task:
  value: Perform the requested operation.
  confidence: 0.8
  evidence: [{source_path: internal/usecase.go, start_line: 1, end_line: 1}]
evidence: [{source_path: internal/transport.go, start_line: 1, end_line: 1}]
confidence: 0.8
`
			manifest, err := ParseOperation([]byte(content))
			if err != nil {
				t.Fatalf("ParseOperation() error = %v\n%s", err, content)
			}
			if string(manifest.Type) != test.typeName {
				t.Fatalf("type = %q, want %q", manifest.Type, test.typeName)
			}
		})
	}
}

func TestParseManifestRejectsUnsafeOrUndeclaredData(t *testing.T) {
	valid := `
schema: architecture/v1
kind: service
id: teacher-agent
manifest_revision: 1
identity: {name: ms-go-teacher-agent, kind: backend_service}
purpose: {value: unknown, confidence: 0, evidence: [{source_path: README.md}]}
evidence: [{source_path: README.md}]
confidence: 0
`
	for name, content := range map[string]string{
		"unknown field":      valid + "unexpected: true\n",
		"multiple documents": valid + "---\n" + valid,
		"secret key":         strings.Replace(valid, "confidence: 0\n", "confidence: 0\napi_token: must-not-be-present\n", 1),
		"path traversal":     strings.Replace(valid, "source_path: README.md", "source_path: ../README.md", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseService([]byte(content)); err == nil {
				t.Fatal("ParseService() error = nil, want validation failure")
			}
		})
	}
}

func TestParseOperationRejectsTransportMismatch(t *testing.T) {
	_, err := ParseOperation([]byte(`
schema: architecture/v1
kind: operation
id: invalid.v1
manifest_revision: 1
service_id: teacher-agent
type: http
identity:
  transport: nats
  nats: {subject: teacher.lookup.v1, role: request_handler}
trigger:
  description: {value: Trigger operation, confidence: 0.8, evidence: [{source_path: router.go}]}
business_task:
  value: unknown
  confidence: 0
  evidence: [{source_path: router.go}]
evidence: [{source_path: router.go}]
confidence: 0.8
`))
	if err == nil {
		t.Fatal("ParseOperation() error = nil, want validation failure")
	}
}
