package architectureprocess

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestParseValidEvidenceBackedManifest(t *testing.T) {
	manifest, err := Parse([]byte(validManifest()))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if manifest.ID != "current-exploration" || manifest.Provenance != ProvenanceEvidenceBacked {
		t.Fatalf("Parse() manifest = %#v", manifest)
	}
	if len(manifest.Steps) != 2 || manifest.Steps[0].ID != "load-platform" {
		t.Fatalf("Parse() steps = %#v", manifest.Steps)
	}
}

func TestParseRejectsUndeclaredOperationAndInvalidProvenance(t *testing.T) {
	for name, content := range map[string]string{
		"undeclared operation": strings.Replace(validManifest(), "operation_id: get-platform\n    contracts", "operation_id: missing-operation\n    contracts", 1),
		"invalid provenance":   strings.Replace(validManifest(), "provenance: evidence-backed", "provenance: semantic-guess", 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(content))
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("Parse() error = %v, want ErrValidation", err)
			}
		})
	}
}

func TestLoadCurrentUsesOnlyConfirmedVersionControlledManifests(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, ".ai", "architecture", "processes")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "current-exploration.yaml"), []byte(validManifest()), 0o644); err != nil {
		t.Fatal(err)
	}
	manifests, err := LoadCurrent(root)
	if err != nil {
		t.Fatalf("LoadCurrent() error = %v", err)
	}
	if len(manifests) != 1 || manifests[0].ID != "current-exploration" {
		t.Fatalf("LoadCurrent() = %#v", manifests)
	}

	candidate := strings.Replace(validManifest(), "provenance: evidence-backed", "provenance: candidate/unverified", 1)
	if err := os.WriteFile(filepath.Join(directory, "candidate.yaml"), []byte(candidate), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCurrent(root); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("LoadCurrent(candidate) error = %v, want ErrValidation", err)
	}
}

func TestRepositoryCurrentBusinessProcessManifest(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	manifests, err := LoadCurrent(root)
	if err != nil {
		t.Fatalf("LoadCurrent(repository) error = %v", err)
	}
	if len(manifests) == 0 || manifests[0].ID != "current-architecture-exploration" {
		t.Fatalf("repository manifests = %#v", manifests)
	}
}

func TestLoadCurrentRejectsSymbolicLinks(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, ".ai", "architecture", "processes")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "process.yaml")
	if err := os.WriteFile(outside, []byte(validManifest()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "link.yaml")); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if _, err := LoadCurrent(root); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("LoadCurrent() error = %v, want ErrValidation", err)
	}
}

func validManifest() string {
	return `schema: architecture/business-process/v1
kind: business_process
id: current-exploration
manifest_revision: 1
identity: {name: Current exploration}
goal: {value: Let an owner inspect current architecture, confidence: 0.9, evidence: [{source_path: internal/router.go}]}
trigger: {value: Owner opens architecture, confidence: 0.9, evidence: [{source_path: web/page.tsx}]}
outcome: {value: Owner sees the selected read model, confidence: 0.9, evidence: [{source_path: internal/catalog.go}]}
steps:
  - id: load-platform
    name: Load platform
    description: {value: Load the platform catalog, confidence: 0.9, evidence: [{source_path: internal/router.go}]}
    service_id: orchestrator
    operation_id: get-platform
    contracts: [platform-contract]
    events: []
    evidence: [{source_path: internal/router.go}]
    confidence: 0.9
  - id: load-operation
    name: Load operation
    description: {value: Load the operation detail, confidence: 0.9, evidence: [{source_path: internal/router.go}]}
    service_id: orchestrator
    operation_id: get-operation
    contracts: [operation-contract]
    events: []
    evidence: [{source_path: internal/router.go}]
    confidence: 0.9
participating_services:
  - service_id: orchestrator
    role: {value: Serves the catalog, confidence: 0.9, evidence: [{source_path: internal/router.go}]}
participating_operations:
  - service_id: orchestrator
    operation_id: get-platform
    role: {value: Returns platform, confidence: 0.9, evidence: [{source_path: internal/router.go}]}
  - service_id: orchestrator
    operation_id: get-operation
    role: {value: Returns operation, confidence: 0.9, evidence: [{source_path: internal/router.go}]}
contracts:
  - id: platform-contract
    transport: http
    direction: response
    description: {value: Platform response, confidence: 0.9, evidence: [{source_path: internal/router.go}]}
  - id: operation-contract
    transport: http
    direction: response
    description: {value: Operation response, confidence: 0.9, evidence: [{source_path: internal/router.go}]}
events: []
provenance: evidence-backed
evidence: [{source_path: internal/router.go}]
confidence: 0.9
`
}
