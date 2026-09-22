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
			if test.name == "go-service" {
				assertFact(t, report.Facts, "contract", "http_produce", "GET /api/v1/orders")
				assertFact(t, report.Facts, "capability", "event_subject", "orders.created.v1")
			}
			if test.name == "nextjs" {
				assertFact(t, report.Facts, "contract", "http_consume", "GET /api/v1/courses")
			}
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

func TestScanner_DoesNotTreatGenericRequestValuesAsEventSubjects(t *testing.T) {
	root := t.TempDir()
	content := `package fixture

func validate(request Request) bool {
	return request.Engine == "nextjs.app" || request.File == "server.py"
}
`
	if err := os.WriteFile(filepath.Join(root, "validate.go"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"),
		[]byte("# Fixture\n\nThe request uses `workspace.root_path` for validation.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := NewScanner(Config{}).Scan(context.Background(), domain.Project{
		ID: "id", Name: "generic-request", RepositoryRole: domain.RepositoryRoleService,
	}, domain.RepositorySource{LocalPath: root, HeadCommit: "commit", CurrentBranch: "main"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	for _, fact := range report.Facts {
		if fact.Name == "event_subject" {
			t.Fatalf("unexpected event subject from generic request value: %#v", fact)
		}
	}
}

func TestScanner_DoesNotTreatPublishI18nKeysAsEventSubjects(t *testing.T) {
	root := t.TempDir()
	content := `export function View({ t }) {
  return t('actions.publish') + t('toast.publishSuccess') + t('table.publishedAt')
}
`
	if err := os.WriteFile(filepath.Join(root, "view.tsx"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := NewScanner(Config{}).Scan(context.Background(), domain.Project{
		ID: "id", Name: "frontend", RepositoryRole: domain.RepositoryRoleFrontend,
	}, domain.RepositorySource{LocalPath: root, HeadCommit: "commit", CurrentBranch: "main"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	for _, fact := range report.Facts {
		if fact.Name == "event_subject" || fact.Name == "event_publish" || fact.Name == "event_subscribe" {
			t.Fatalf("unexpected event fact from i18n key: %#v", fact)
		}
	}
}

func TestScanner_ClassifiesPythonServiceAsBackend(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.py"), []byte("print('fixture')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := NewScanner(Config{}).Scan(context.Background(), domain.Project{
		ID: "id", Name: "python-service", RepositoryRole: domain.RepositoryRoleService,
	}, domain.RepositorySource{LocalPath: root, HeadCommit: "commit", CurrentBranch: "main"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	assertFact(t, report.Facts, "stack", "language", "python")
	assertFact(t, report.Facts, "classification", "service_kind", "backend_service")
}

func TestScanner_ClassifiesPHPServiceAsBackend(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.php"), []byte("<?php echo 'fixture';\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := NewScanner(Config{}).Scan(context.Background(), domain.Project{
		ID: "id", Name: "php-service", RepositoryRole: domain.RepositoryRoleService,
	}, domain.RepositorySource{LocalPath: root, HeadCommit: "commit", CurrentBranch: "main"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	assertFact(t, report.Facts, "stack", "language", "php")
	assertFact(t, report.Facts, "classification", "service_kind", "backend_service")
}

func TestScanner_DetectsGoServeMuxRoutes(t *testing.T) {
	root := t.TempDir()
	content := `package fixture

import "net/http"

func routes(mux *http.ServeMux) {
	mux.HandleFunc("/health", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("/api/v1/validate", func(_ http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost { return }
	})
}
`
	if err := os.WriteFile(filepath.Join(root, "server.go"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := NewScanner(Config{}).Scan(context.Background(), domain.Project{
		ID: "id", Name: "go-http", RepositoryRole: domain.RepositoryRoleService,
	}, domain.RepositorySource{LocalPath: root, HeadCommit: "commit", CurrentBranch: "main"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	assertFact(t, report.Facts, "contract", "http_produce", "GET /health")
	assertFact(t, report.Facts, "contract", "http_produce", "POST /api/v1/validate")
}

func TestScanner_DetectsPythonBaseHTTPHandlerRoutes(t *testing.T) {
	root := t.TempDir()
	content := `from http.server import BaseHTTPRequestHandler

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path != "/health":
            return

    def do_POST(self):
        if self.path != "/api/v1/validate":
            return
`
	if err := os.WriteFile(filepath.Join(root, "server.py"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := NewScanner(Config{}).Scan(context.Background(), domain.Project{
		ID: "id", Name: "python-http", RepositoryRole: domain.RepositoryRoleService,
	}, domain.RepositorySource{LocalPath: root, HeadCommit: "commit", CurrentBranch: "main"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	assertFact(t, report.Facts, "contract", "http_produce", "GET /health")
	assertFact(t, report.Facts, "contract", "http_produce", "POST /api/v1/validate")
}

func TestScanner_IgnoresRuntimeEvidenceFromTestsAndExamples(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"internal/http/server.go": `package http
func routes(router Router) { router.Post("/api/v1/real", handler) }
`,
		"internal/http/server_test.go": `package http
func testRoutes(router Router) { router.Get("/health", handler) }
`,
		"internal/adapters/docker/compose.go": `package docker
func runCompose() {}
`,
		"docs/examples/postgres-runtime.json": `{"migration":"CREATE TABLE users (id UUID);"}`,
		"docs/GOLANG_ARCHITECTURE.md": `r.Get("/users/:id", handler.Get)
publisher.Publish("user.created", payload)
subscriber.Subscribe("user.create", handler)`,
		"tests/fixture.sql": `CREATE TABLE test_records (id UUID);`,
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := NewScanner(Config{}).Scan(context.Background(), domain.Project{
		ID: "id", Name: "production-only", RepositoryRole: domain.RepositoryRoleService,
	}, domain.RepositorySource{LocalPath: root, HeadCommit: "commit", CurrentBranch: "main"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	assertFact(t, report.Facts, "contract", "http_produce", "POST /api/v1/real")
	for _, fact := range report.Facts {
		if fact.Category != "capability" && fact.Category != "contract" && fact.Category != "ownership" &&
			fact.Category != "relation" && fact.Category != "infrastructure" {
			continue
		}
		if isNonProductionEvidencePath(fact.SourcePath) {
			t.Fatalf("non-production file emitted runtime evidence: %#v", fact)
		}
		if fact.Name == "database_table" && (fact.Value == "users" || fact.Value == "test_records") {
			t.Fatalf("example/test schema emitted ownership: %#v", fact)
		}
		if fact.SourcePath == "docs/GOLANG_ARCHITECTURE.md" {
			t.Fatalf("generic documentation emitted runtime evidence: %#v", fact)
		}
	}
	for _, conflict := range report.Conflicts {
		if conflict.Name == "invalid_manifest" && conflict.SourcePath == "internal/adapters/docker/compose.go" {
			t.Fatalf("Go source was parsed as a Compose manifest: %#v", conflict)
		}
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

func TestScanner_RuntimeNameWithoutRuntimeEvidenceRemainsUnknown(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Use go test ./...\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	scanner := NewScanner(Config{MaxFiles: 100, MaxFileBytes: 1 << 20, MaxTotalBytes: 4 << 20, MaxDepth: 10})
	report, err := scanner.Scan(context.Background(), domain.Project{
		ID: "placeholder", Name: "go-ms-ai-summary", RepositoryRole: domain.RepositoryRoleService,
	}, domain.RepositorySource{LocalPath: root, HeadCommit: "commit", CurrentBranch: "main"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	assertFact(t, report.Facts, "classification", "service_kind", "unknown")
}

func TestScanner_FrontendNginxDoesNotOwnGatewayRoutes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"dependencies":{"next":"15.1.5"}}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte("location /api { proxy_pass http://gateway; }\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	scanner := NewScanner(Config{MaxFiles: 100, MaxFileBytes: 1 << 20, MaxTotalBytes: 4 << 20, MaxDepth: 10})
	report, err := scanner.Scan(context.Background(), domain.Project{
		ID: "frontend", Name: "nextjs", RepositoryRole: domain.RepositoryRoleFrontend,
	}, domain.RepositorySource{LocalPath: root, HeadCommit: "commit", CurrentBranch: "main"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	for _, fact := range report.Facts {
		if fact.Category == "relation" && fact.Name == "gateway_routes_to" {
			t.Fatalf("frontend owns a gateway route: %#v", fact)
		}
	}
}

func TestScannerImportsApprovedSemanticReport(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".ai", "discovery"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"),
		[]byte("Only reviewed lessons can be published.\nPublishes orders.created.v1.\nGET /health returns status and service fields.\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Taskfile.yml"), []byte("tasks:\n  test:\n    cmds:\n      - go test ./...\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	analysis := domain.SemanticAnalysis{
		SchemaVersion: 1, ProjectID: "old-project-id", ProjectName: "lessons", BaseCommit: "base",
		Summary: "Lesson rules", Facts: []domain.SemanticFact{{
			Category: "business_rule", Name: "publish_reviewed_only",
			Value: "Only reviewed lessons can be published", Confidence: .9,
			SourcePath: "README.md", EvidenceQuote: "Only reviewed lessons can be published.",
			Explanation: "Publication requires review.",
		}, {
			Category: "command", Name: "test", Value: "go test ./...", Confidence: .9,
			SourcePath: "Taskfile.yml", EvidenceQuote: "- go test ./...",
			Explanation: "Taskfile documents the test command.",
		}, {
			Category: "contract", Name: "event_publish", Value: "orders.created.v1", Confidence: .9,
			SourcePath: "README.md", EvidenceQuote: "Publishes orders.created.v1.",
			Explanation: "The service publishes the subject.",
		}, {
			Category: "contract", Name: "http_produce", Value: "GET /health returns status", Confidence: .9,
			SourcePath: "README.md", EvidenceQuote: "GET /health returns status and service fields.",
			Explanation: "This is response prose, not a method/path reference.",
		}},
	}
	content, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ai", "discovery", "semantic-report.json"), content, 0o640); err != nil {
		t.Fatal(err)
	}
	report, err := NewScanner(Config{}).Scan(context.Background(), domain.Project{
		ID: "new-project-id", Name: "lessons", RepositoryRole: domain.RepositoryRoleService,
	}, domain.RepositorySource{LocalPath: root, HeadCommit: "merge-commit", CurrentBranch: "main"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	assertFact(t, report.Facts, "business_rule", "publish_reviewed_only", "Only reviewed lessons can be published")
	assertFact(t, report.Facts, "command", "test", "go test ./...")
	assertFact(t, report.Facts, "contract", "event_publish", "orders.created.v1")
	for _, fact := range report.Facts {
		if fact.Category == "contract" && fact.Name == "http_produce" && fact.Value == "GET /health returns status" {
			t.Fatal("scanner imported descriptive HTTP response prose as a contract")
		}
	}
	assertFact(t, report.Conflicts, "conflict", "invalid_semantic_fact", "contract:http_produce")
}

func TestScannerRejectsSemanticFactWithoutCurrentQuotedEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".ai", "discovery"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("Current documented rule.\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	content := `{
  "schema_version": 1,
  "project_id": "project-1",
  "project_name": "fixture",
  "base_commit": "abc123",
  "summary": "Fixture rules.",
  "facts": [{
    "category": "business_rule",
    "name": "invented_rule",
    "value": "Invented rule",
    "confidence": 0.9,
    "source_path": "README.md",
    "evidence_quote": "This text is not in the current README.",
    "explanation": "Stale evidence."
  }],
  "open_questions": []
}`
	if err := os.WriteFile(filepath.Join(root, ".ai", "discovery", "semantic-report.json"), []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	report, err := NewScanner(Config{}).Scan(context.Background(), domain.Project{
		ID: "project-1", Name: "fixture", RepositoryRole: domain.RepositoryRoleService,
	}, domain.RepositorySource{LocalPath: root, HeadCommit: "abc123", CurrentBranch: "main"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	for _, fact := range report.Facts {
		if fact.Category == "business_rule" && fact.Name == "invented_rule" {
			t.Fatal("scanner imported a semantic fact without current quoted evidence")
		}
	}
	assertFact(t, report.Conflicts, "conflict", "invalid_semantic_fact", "business_rule:invented_rule")
}

func TestScanner_NonRuntimeRolesSuppressRuntimeEvidence(t *testing.T) {
	root := t.TempDir()
	content := `# Runtime examples

router.Post("/users", handler)
publisher.Publish("users.created", payload)
CREATE TABLE users (id UUID);
proxy_pass http://users:8080;
`
	if err := os.WriteFile(filepath.Join(root, "examples.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tool.py"), []byte("print('policy tool')\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	roles := []domain.RepositoryRole{
		domain.RepositoryRolePolicy,
		domain.RepositoryRoleDocumentation,
		domain.RepositoryRoleArchive,
		domain.RepositoryRoleContent,
	}
	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			report, err := NewScanner(Config{}).Scan(context.Background(), domain.Project{
				ID: "id", Name: "non-runtime", RepositoryRole: role,
			}, domain.RepositorySource{LocalPath: root, HeadCommit: "commit", CurrentBranch: "main"})
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}
			assertFact(t, report.Facts, "classification", "service_kind", "unknown")
			assertFact(t, report.Facts, "stack", "language", "python")
			if role == domain.RepositoryRolePolicy {
				assertFact(t, report.Facts, "instruction", "instruction_file", "")
			}
			for _, fact := range report.Facts {
				switch fact.Category {
				case "capability", "contract", "infrastructure", "ownership", "relation":
					t.Fatalf("runtime fact for %s repository: %#v", role, fact)
				}
			}
		})
	}
}

func TestScannerInventoriesObservedHTTPSeparatelyFromArchitectureManifests(t *testing.T) {
	root := t.TempDir()
	writeDiscoveryFile(t, root, "internal/routes.go", `package api
func routes(router Router) { router.Get("/api/v1/orders", handler) }
`)
	writeDiscoveryFile(t, root, ".ai/architecture/service.yaml", architectureServiceYAML("orders", "endpoints/orders.list.yaml"))
	writeDiscoveryFile(t, root, ".ai/architecture/endpoints/orders.list.yaml", architectureHTTPOperationYAML("orders-list", "orders", "GET", "/api/v1/orders", "List open orders"))

	report, err := NewScanner(Config{}).Scan(context.Background(), domain.Project{
		ID: "orders-project", Name: "orders", RepositoryRole: domain.RepositoryRoleService,
	}, domain.RepositorySource{LocalPath: root, HeadCommit: "commit", CurrentBranch: "main"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(report.Operations) != 1 {
		t.Fatalf("operations = %#v, want one code-observed HTTP operation", report.Operations)
	}
	operation := report.Operations[0]
	if operation.Type != domain.ArchitectureOperationHTTP || operation.Protocol != "http" ||
		operation.Method != "GET" || operation.Path != "/api/v1/orders" || operation.SourcePath != "internal/routes.go" {
		t.Fatalf("operation = %#v", operation)
	}
	if len(report.ArchitectureManifests) != 2 {
		t.Fatalf("architecture manifests = %#v, want service and endpoint metadata", report.ArchitectureManifests)
	}
	for _, manifest := range report.ArchitectureManifests {
		if manifest.Checksum == "" || manifest.Schema != domain.ArchitectureManifestSchemaV1 ||
			manifest.ID == "" || manifest.ManifestRevision != 1 {
			t.Fatalf("unsafe or incomplete manifest metadata = %#v", manifest)
		}
	}
	if report.ArchitectureService == nil || report.ArchitectureService.Purpose.Value != "Service purpose" ||
		len(report.ArchitectureService.Responsibilities) != 1 ||
		report.ArchitectureService.Responsibilities[0].Value != "Own order lookup" {
		t.Fatalf("service semantics were not retained in the snapshot: %#v", report.ArchitectureService)
	}
	if len(report.ArchitectureOperations) != 1 || report.ArchitectureOperations[0].BusinessTask.Value != "List open orders" ||
		len(report.ArchitectureOperations[0].BusinessProcess) != 1 ||
		report.ArchitectureOperations[0].BusinessProcess[0].ID != "lookup" ||
		len(report.ArchitectureOperations[0].Evidence) != 1 {
		t.Fatalf("operation semantics were not retained in the snapshot: %#v", report.ArchitectureOperations)
	}
	for _, fact := range report.Facts {
		if fact.SourcePath == ".ai/architecture/endpoints/orders.list.yaml" &&
			(fact.Category == "capability" || fact.Category == "contract" || fact.Category == "relation") {
			t.Fatalf("manifest claim became a topology fact: %#v", fact)
		}
	}
	for _, conflict := range report.Conflicts {
		if strings.HasPrefix(conflict.Name, "architecture_") {
			t.Fatalf("matching manifest unexpectedly conflicted: %#v", conflict)
		}
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "business_task:\n") || strings.Contains(string(raw), "identity:\n") {
		t.Fatalf("report persisted raw YAML rather than its validated typed projection: %s", raw)
	}
}

func TestScannerIngestsNonHTTPOperationLayoutAndIgnoresMermaid(t *testing.T) {
	root := t.TempDir()
	writeDiscoveryFile(t, root, ".ai/architecture/service.yaml", architectureServiceYAML("orders", "operations/orders.consume.yaml"))
	writeDiscoveryFile(t, root, ".ai/architecture/operations/orders.consume.yaml", architectureNATSOperationYAML("orders-consume", "orders"))
	writeDiscoveryFile(t, root, ".ai/architecture/operations/orders.consume.mmd", "flowchart TD\n")

	report, err := NewScanner(Config{}).Scan(context.Background(), domain.Project{
		ID: "orders-project", Name: "orders", RepositoryRole: domain.RepositoryRoleService,
	}, domain.RepositorySource{LocalPath: root, HeadCommit: "commit", CurrentBranch: "main"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(report.ArchitectureManifests) != 2 {
		t.Fatalf("architecture manifests = %#v, want service and operation metadata only", report.ArchitectureManifests)
	}
	if len(report.ArchitectureOperations) != 1 || report.ArchitectureOperations[0].Type != domain.ArchitectureOperationNATSEventSubscriber ||
		report.ArchitectureOperations[0].Identity.NATS == nil || report.ArchitectureOperations[0].Identity.NATS.Subject != "orders.created.v1" {
		t.Fatalf("architecture operations = %#v", report.ArchitectureOperations)
	}
	for _, manifest := range report.ArchitectureManifests {
		if manifest.Path == ".ai/architecture/operations/orders.consume.mmd" {
			t.Fatalf("generated Mermaid was ingested as a manifest: %#v", manifest)
		}
	}
	for _, conflict := range report.Conflicts {
		if strings.HasPrefix(conflict.Name, "architecture_") {
			t.Fatalf("valid non-HTTP operation unexpectedly conflicted: %#v", conflict)
		}
	}
}

func TestScannerReportsArchitectureManifestErrorsAndHTTPMismatchesWithoutLeakingValues(t *testing.T) {
	root := t.TempDir()
	writeDiscoveryFile(t, root, "routes.go", `package api
func routes(router Router) { router.Get("/api/v1/orders", handler) }
`)
	writeDiscoveryFile(t, root, ".ai/architecture/service.yaml", architectureServiceYAML("orders", "endpoints/orders.list.yaml"))
	writeDiscoveryFile(t, root, ".ai/architecture/endpoints/orders.list.yaml", architectureHTTPOperationYAML("orders-list", "orders", "POST", "/api/v1/orders", "safe text"))
	writeDiscoveryFile(t, root, ".ai/architecture/endpoints/unsafe.yaml", "api_token: PLACEHOLDER_VALUE_THAT_MUST_NOT_LEAK\n")
	writeDiscoveryFile(t, root, ".ai/architecture/endpoints/nested/ignored.yaml", "api_token: NESTED_PLACEHOLDER\n")

	report, err := NewScanner(Config{}).Scan(context.Background(), domain.Project{
		ID: "orders-project", Name: "orders", RepositoryRole: domain.RepositoryRoleService,
	}, domain.RepositorySource{LocalPath: root, HeadCommit: "commit", CurrentBranch: "main"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	assertFact(t, report.Conflicts, "conflict", "architecture_http_operation_mismatch", "POST /api/v1/orders")
	assertFact(t, report.Conflicts, "conflict", "invalid_architecture_manifest", ".ai/architecture/endpoints/unsafe.yaml")
	for _, manifest := range report.ArchitectureManifests {
		if manifest.Path == ".ai/architecture/endpoints/unsafe.yaml" || manifest.Path == ".ai/architecture/endpoints/nested/ignored.yaml" {
			t.Fatalf("invalid or nested manifest was ingested: %#v", manifest)
		}
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "PLACEHOLDER_VALUE_THAT_MUST_NOT_LEAK") || strings.Contains(string(raw), "NESTED_PLACEHOLDER") {
		t.Fatalf("report leaked invalid manifest content: %s", raw)
	}
}

func writeDiscoveryFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func architectureServiceYAML(id, operationPath string) string {
	return "schema: architecture/v1\nkind: service\nid: " + id + `
manifest_revision: 1
identity: {name: ` + id + `, kind: backend_service}
purpose: {value: Service purpose, confidence: 0.8, evidence: [{source_path: README.md}]}
responsibilities: [{value: Own order lookup, confidence: 0.8, evidence: [{source_path: internal/handler.go}]}]
operation_manifests: [` + operationPath + `]
evidence: [{source_path: README.md}]
confidence: 0.8
`
}

func architectureHTTPOperationYAML(id, serviceID, method, path, businessTask string) string {
	return "schema: architecture/v1\nkind: operation\nid: " + id + "\nmanifest_revision: 1\nservice_id: " + serviceID + `
type: http
identity:
  transport: http
  http: {method: ` + method + `, path: ` + path + `}
access:
  audience: {value: internal, confidence: 0.8, evidence: [{source_path: internal/routes.go}]}
  authentication: {value: unknown, confidence: 0.8, evidence: [{source_path: internal/routes.go}]}
  authorization: {value: unknown, confidence: 0.8, evidence: [{source_path: internal/routes.go}]}
  idempotency: {value: unknown, confidence: 0.8, evidence: [{source_path: internal/routes.go}]}
trigger:
  description: {value: HTTP request, confidence: 0.8, evidence: [{source_path: internal/routes.go}]}
business_task:
  value: ` + businessTask + `
  confidence: 0.8
  evidence: [{source_path: internal/handler.go}]
business_process:
  - id: lookup
    description: {value: Find open orders, confidence: 0.8, evidence: [{source_path: internal/handler.go}]}
evidence: [{source_path: internal/routes.go}]
confidence: 0.8
`
}

func architectureNATSOperationYAML(id, serviceID string) string {
	return "schema: architecture/v1\nkind: operation\nid: " + id + "\nmanifest_revision: 1\nservice_id: " + serviceID + `
type: nats_event_subscriber
identity:
  transport: nats
  nats: {subject: orders.created.v1, role: event_subscriber}
access:
  audience: {value: internal, confidence: 0.8, evidence: [{source_path: internal/consumer.go}]}
  authentication: {value: unknown, confidence: 0, evidence: [{source_path: internal/consumer.go}]}
  authorization: {value: unknown, confidence: 0, evidence: [{source_path: internal/consumer.go}]}
  idempotency: {value: unknown, confidence: 0, evidence: [{source_path: internal/consumer.go}]}
trigger:
  description: {value: NATS event, confidence: 0.8, evidence: [{source_path: internal/consumer.go}]}
business_task: {value: Consume created order, confidence: 0.8, evidence: [{source_path: internal/consumer.go}]}
evidence: [{source_path: internal/consumer.go}]
confidence: 0.8
`
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

func TestSemanticPackageCommandDeclaredRequiresExactScriptName(t *testing.T) {
	files := map[string][]byte{
		"package.json": []byte(`{"scripts":{"test:watch":"jest --watchAll"}}`),
	}
	exact := domain.SemanticFact{Name: "test:watch", Value: "jest --watchAll", SourcePath: "package.json"}
	if !semanticPackageCommandDeclared(exact, files) {
		t.Fatal("exact package script was rejected")
	}
	renamed := exact
	renamed.Name = "test_watch"
	if semanticPackageCommandDeclared(renamed, files) {
		t.Fatal("renamed package script was accepted")
	}
}

func TestContainsUnexpandedCommandTemplate(t *testing.T) {
	if !containsUnexpandedCommandTemplate("docker build -t {{.SERVICE_NAME}} .") {
		t.Fatal("Task template was not detected")
	}
	if containsUnexpandedCommandTemplate("docker build -t ms-ts-react-validator .") {
		t.Fatal("literal command was treated as a template")
	}
}

func TestScannerClassifiesComposeNginxTemplateAsGateway(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte("services:\n  gateway:\n    image: nginx\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "templates"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "templates", "gateway.conf.template"), []byte("location /users { proxy_pass http://$users_upstream; }\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	report, err := NewScanner(Config{}).Scan(context.Background(), domain.Project{
		ID: "id", Name: "ms-gateway", RepositoryRole: domain.RepositoryRoleService,
	}, domain.RepositorySource{LocalPath: root, HeadCommit: "commit", CurrentBranch: "main"})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	assertFact(t, report.Facts, "classification", "service_kind", "gateway")
	assertFact(t, report.Facts, "relation", "gateway_routes_to", "http://$users_upstream")
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
