package discovery

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestScanner_Fixtures(t *testing.T) {
	fixedTime := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	scanner := NewScanner(Config{
		MaxFiles: 1000, MaxFileBytes: 1 << 20, MaxTotalBytes: 10 << 20, MaxDepth: 20,
		Now: func() time.Time { return fixedTime },
	})
	tests := []struct {
		name          string
		role          domain.RepositoryRole
		wantKind      string
		wantCategory  string
		wantName      string
		wantValue     string
		projectName   string
		wantConflicts bool
	}{
		{name: "go-service", role: domain.RepositoryRoleService, wantKind: "backend_service", wantCategory: "ownership", wantName: "database_table", wantValue: "orders"},
		{name: "nextjs", role: domain.RepositoryRoleFrontend, wantKind: "frontend_application", wantCategory: "stack", wantName: "framework", wantValue: "nextjs"},
		{name: "gateway", role: domain.RepositoryRoleService, wantKind: "gateway", wantCategory: "relation", wantName: "gateway_routes_to", wantValue: "http://ms-go-auth:8080"},
		{name: "infrastructure", role: domain.RepositoryRoleInfrastructure, wantKind: "infrastructure", wantCategory: "infrastructure", wantName: "compose_service", wantValue: "nats=nats:2.10-alpine"},
		{name: "prompts", role: domain.RepositoryRolePolicy, wantKind: "unknown", wantCategory: "instruction", wantName: "instruction_file"},
		{name: "existing-ai", projectName: "backend-existing", role: domain.RepositoryRoleService, wantKind: "backend_service", wantCategory: "instruction", wantName: "existing_service_manifest", wantConflicts: true},
		{name: "conflicts", role: domain.RepositoryRoleUnknown, wantKind: "unknown", wantCategory: "instruction", wantName: "instruction_file", wantConflicts: true},
		{name: "unknown", role: domain.RepositoryRoleUnknown, wantKind: "unknown", wantCategory: "purpose", wantName: "summary"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := fixturePath(t, test.name)
			projectName := test.projectName
			if projectName == "" {
				projectName = test.name
			}
			report, err := scanner.Scan(context.Background(), domain.Project{
				ID: "project-id", Name: projectName, RepositoryRole: test.role,
			}, domain.RepositorySource{
				LocalPath: root, HeadCommit: strings.Repeat("a", 40), CurrentBranch: "main",
			})
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}
			if report.SchemaVersion != reportSchemaVersion || report.StartedAt != fixedTime || report.CompletedAt != fixedTime {
				t.Fatalf("report metadata = %#v", report)
			}
			assertFact(t, report.Facts, "classification", "service_kind", test.wantKind)
			assertFact(t, report.Facts, test.wantCategory, test.wantName, test.wantValue)
			if test.wantConflicts && len(report.Conflicts) == 0 {
				t.Fatal("conflicts = empty, want at least one conflict")
			}
			for _, fact := range append(append([]domain.Evidence(nil), report.Facts...), report.Conflicts...) {
				if fact.SourcePath == "" || fact.Explanation == "" || fact.Confidence <= 0 || fact.Confidence > 1 {
					t.Fatalf("fact lacks evidence metadata: %#v", fact)
				}
			}
		})
	}
}

func TestScanner_DoesNotReadEnvironmentSecrets(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SUPER_SECRET=must-not-appear"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.example"), []byte("PUBLIC_PORT=8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scanner := NewScanner(Config{MaxFiles: 10, MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxDepth: 4})
	report, err := scanner.Scan(context.Background(), domain.Project{
		ID: "id", Name: "secrets", RepositoryRole: domain.RepositoryRoleUnknown,
	}, domain.RepositorySource{LocalPath: root, HeadCommit: "commit", CurrentBranch: "main"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "must-not-appear") || strings.Contains(string(raw), "SUPER_SECRET") {
		t.Fatalf("report leaked .env content: %s", raw)
	}
	assertFact(t, report.Facts, "configuration", "environment_key", "PUBLIC_PORT")
}

func TestScanner_ContentRoleDoesNotBecomeRuntimeService(t *testing.T) {
	scanner := NewScanner(Config{MaxFiles: 100, MaxFileBytes: 1 << 20, MaxTotalBytes: 4 << 20, MaxDepth: 10})
	report, err := scanner.Scan(context.Background(), domain.Project{
		ID: "content", Name: "content-with-next", RepositoryRole: domain.RepositoryRoleContent,
	}, domain.RepositorySource{LocalPath: fixturePath(t, "nextjs"), HeadCommit: "commit", CurrentBranch: "main"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	assertFact(t, report.Facts, "classification", "service_kind", "unknown")
}

func TestScanner_EnforcesInventoryLimits(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one.md", "two.md", "three.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	scanner := NewScanner(Config{MaxFiles: 1, MaxFileBytes: 1024, MaxTotalBytes: 4096, MaxDepth: 4})
	report, err := scanner.Scan(context.Background(), domain.Project{
		ID: "id", Name: "bounded", RepositoryRole: domain.RepositoryRoleUnknown,
	}, domain.RepositorySource{LocalPath: root, HeadCommit: "commit"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if !report.Inventory.Truncated || report.Inventory.FilesVisited != 1 {
		t.Fatalf("inventory = %#v, want one visited file and truncated", report.Inventory)
	}
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "test", "fixtures", "discovery", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func assertFact(t *testing.T, facts []domain.Evidence, category, name, value string) {
	t.Helper()
	for _, fact := range facts {
		if fact.Category == category && fact.Name == name && (value == "" || fact.Value == value) {
			return
		}
	}
	t.Fatalf("missing fact category=%q name=%q value=%q in %#v", category, name, value, facts)
}
