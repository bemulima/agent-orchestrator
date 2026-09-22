package architecturemanifest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestAuditReportsPlatformCompletenessAcrossRoots(t *testing.T) {
	coveredRoot := t.TempDir()
	coveredReport := domain.DiscoveryReport{
		ProjectID: "covered-service",
		Operations: []domain.DiscoveredOperation{
			{Type: domain.ArchitectureOperationHTTP, Protocol: "http", Method: "POST", Path: "/api/v1/orders", SourcePath: "internal/router.go"},
			{Type: domain.ArchitectureOperationNATSEventSubscriber, Protocol: "nats", Subject: "orders.created.v1", SourcePath: "internal/consumer.go"},
			{Type: domain.ArchitectureOperationWorker, Protocol: "worker", Name: "rebuild-index", SourcePath: "internal/worker.go"},
			{Type: domain.ArchitectureOperationScheduled, Protocol: "scheduled", Name: "cleanup", Schedule: "@every 1h", SourcePath: "internal/schedule.go"},
		},
	}
	if _, err := ScaffoldHTTPManifests(coveredRoot, coveredReport); err != nil {
		t.Fatalf("ScaffoldHTTPManifests() error = %v", err)
	}
	if _, err := GenerateFiles(coveredRoot); err != nil {
		t.Fatalf("GenerateFiles() error = %v", err)
	}

	missingRoot := t.TempDir()
	missingReport := domain.DiscoveryReport{
		ProjectID: "missing-service",
		Operations: []domain.DiscoveredOperation{
			{Type: domain.ArchitectureOperationHTTP, Protocol: "http", Method: "GET", Path: "/health", SourcePath: "main.go"},
			{Type: domain.ArchitectureOperationNATSRequestReply, Protocol: "nats", Subject: "orders.lookup.v1", SourcePath: "internal/rpc.go"},
		},
	}

	report, err := Audit([]AuditInput{
		{ServiceRoot: missingRoot, Report: missingReport},
		{ServiceRoot: coveredRoot, Report: coveredReport},
	})
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if report.TotalBackendServices != 2 || report.ServicesWithArchitectureManifests != 1 || report.ServicesMissingManifests != 1 {
		t.Fatalf("service completeness = %#v", report)
	}
	if report.TotalDiscoveredOperations != 6 || report.HTTPOperations != 2 || report.NATSRequestReplyOperations != 1 ||
		report.EventOperations != 1 || report.WorkerOperations != 1 || report.ScheduledOperations != 1 {
		t.Fatalf("operation inventory = %#v", report)
	}
	if report.OperationsWithManifests != 1 || report.OperationsMissingManifests != 5 || report.OperationsBlocked != 0 {
		t.Fatalf("operation completeness = %#v", report)
	}
	if report.GeneratedServiceMermaidCount != 1 || report.GeneratedOperationMermaidCount != 1 {
		t.Fatalf("Mermaid completeness = %#v", report)
	}
	if len(report.ValidationErrors) == 0 || len(report.UnknownAreas) == 0 {
		t.Fatalf("audit diagnostics should expose missing service and unproven operations: %#v", report)
	}
	if len(report.Services) != 2 || !report.Services[0].HasArchitectureManifest || report.Services[1].HasArchitectureManifest {
		t.Fatalf("services are not deterministic by project identity: %#v", report.Services)
	}
	if report.Services[1].OperationsMissingManifests != 2 {
		t.Fatalf("missing service operation count = %#v", report.Services[1])
	}
}

func TestAuditMarksMalformedManifestAndMissingGeneratedFilesBlocked(t *testing.T) {
	root := t.TempDir()
	architectureDir := filepath.Join(root, ".ai", "architecture")
	if err := os.MkdirAll(architectureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(architectureDir, "service.yaml"), []byte("schema: architecture/v1\nkind: service\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Audit([]AuditInput{{ServiceRoot: root, Report: domain.DiscoveryReport{ProjectID: "broken"}}})
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if report.ServicesBlocked != 1 || report.ServicesWithArchitectureManifests != 0 || len(report.ValidationErrors) != 1 {
		t.Fatalf("malformed service audit = %#v", report)
	}

	validRoot := t.TempDir()
	discovery := domain.DiscoveryReport{ProjectID: "valid-but-no-mermaid", Operations: []domain.DiscoveredOperation{{Type: domain.ArchitectureOperationHTTP, Protocol: "http", Method: "GET", Path: "/health", SourcePath: "main.go"}}}
	if _, err := ScaffoldHTTPManifests(validRoot, discovery); err != nil {
		t.Fatal(err)
	}
	validReport, err := Audit([]AuditInput{{ServiceRoot: validRoot, Report: discovery}})
	if err != nil {
		t.Fatal(err)
	}
	if validReport.ServicesBlocked != 1 || validReport.GeneratedServiceMermaidCount != 0 || validReport.GeneratedOperationMermaidCount != 0 {
		t.Fatalf("missing Mermaid audit = %#v", validReport)
	}
}

func TestAuditRejectsDuplicateProjectIdentity(t *testing.T) {
	root := t.TempDir()
	_, err := Audit([]AuditInput{
		{ServiceRoot: root, Report: domain.DiscoveryReport{ProjectID: "duplicate"}},
		{ServiceRoot: t.TempDir(), Report: domain.DiscoveryReport{ProjectID: "duplicate"}},
	})
	if err == nil {
		t.Fatal("Audit() error = nil, want duplicate identity error")
	}
}
