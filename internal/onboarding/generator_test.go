package onboarding

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestGeneratorBuildsEvidenceBackedProposalWithoutWritingSource(t *testing.T) {
	root := fixturePath(t, "go-service")
	agentsBefore := fileDigest(t, filepath.Join(root, "AGENTS.md"))
	project, snapshot, report := generatorFixture(root)
	firstTime := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	generator := NewGenerator(GeneratorConfig{Now: func() time.Time { return firstTime }})

	proposal, diff, err := generator.Generate(context.Background(), project, snapshot, report)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if fileDigest(t, filepath.Join(root, "AGENTS.md")) != agentsBefore {
		t.Fatal("Generate() changed the source AGENTS.md")
	}
	if _, err := os.Stat(filepath.Join(root, ".ai")); !os.IsNotExist(err) {
		t.Fatalf("Generate() created .ai in source, stat error = %v", err)
	}
	if proposal.Checksum == "" || proposal.BaseCommit != snapshot.CommitSHA || diff == "" {
		t.Fatalf("proposal metadata/diff is incomplete: %#v", proposal)
	}
	for _, file := range proposal.Files {
		if file.Explanation == "" || file.Checksum == "" || len(file.EvidencePaths) == 0 {
			t.Fatalf("proposed file lacks provenance: %#v", file)
		}
		if file.Path != "AGENTS.md" && !strings.HasPrefix(file.Path, ".ai/") {
			t.Fatalf("proposed path escaped write scope: %q", file.Path)
		}
		if strings.Contains(file.Path, "prompts/") {
			t.Fatalf("prompt was copied into proposal: %q", file.Path)
		}
	}
	agents := proposedFile(t, proposal, "AGENTS.md")
	if !strings.Contains(agents.Content, "Run tests before proposing a change.") ||
		!strings.Contains(agents.Content, managedStart) {
		t.Fatalf("AGENTS.md did not preserve user rules and managed block:\n%s", agents.Content)
	}
	service := proposedFile(t, proposal, ".ai/service.yaml")
	var manifest map[string]any
	if err := yaml.Unmarshal([]byte(service.Content), &manifest); err != nil {
		t.Fatalf("service manifest is invalid YAML: %v", err)
	}
	repository, ok := manifest["repository"].(map[string]any)
	if !ok || repository["git_url"] == "" {
		t.Fatalf("service repository is incomplete: %#v", manifest["repository"])
	}
	if _, leaked := repository["local_path"]; leaked {
		t.Fatalf("service manifest leaked host local path: %#v", repository)
	}
	assertProposed(t, proposal, ".ai/contracts/http.yaml")
	assertProposed(t, proposal, ".ai/contracts/database.yaml")
	assertProposed(t, proposal, ".ai/agents/backend-coder.md")
	assertProposed(t, proposal, ".ai/agents/migration-agent.md")
	if hasProposed(proposal, ".ai/commands.yaml") || hasProposed(proposal, ".ai/workflows/test.yaml") {
		t.Fatal("generator invented commands without command evidence")
	}
	if !containsProposed(proposal, ".ai/agents/backend-coder.md", "No repository commands have been approved") {
		t.Fatal("backend agent did not fail closed when command evidence was absent")
	}

	second := NewGenerator(GeneratorConfig{Now: func() time.Time { return firstTime.Add(time.Hour) }})
	repeated, repeatedDiff, err := second.Generate(context.Background(), project, snapshot, report)
	if err != nil {
		t.Fatalf("repeated Generate() error = %v", err)
	}
	if repeated.Checksum != proposal.Checksum || repeatedDiff != diff {
		t.Fatalf("proposal is not deterministic: %q/%q", proposal.Checksum, repeated.Checksum)
	}
}

func TestGeneratorRejectsSymlinkedAITree(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".ai")); err != nil {
		t.Fatal(err)
	}
	project, snapshot, report := generatorFixture(root)
	if _, _, err := NewGenerator(GeneratorConfig{}).Generate(context.Background(), project, snapshot, report); err == nil {
		t.Fatal("Generate() followed a symlinked .ai directory")
	}
}

func TestCommandManifestRequiresApprovalForOperationalCommands(t *testing.T) {
	report := domain.DiscoveryReport{Facts: []domain.Evidence{
		{Category: "command", Name: "test", Value: "go test ./...", SourcePath: "Taskfile.yml", Confidence: .95},
		{Category: "command", Name: "cleanup_exited_sandboxes", Value: "./scripts/cleanup_exited_sandboxes.sh", SourcePath: "Taskfile.yml", Confidence: .95},
		{Category: "command", Name: "stop_containers", Value: "docker compose down", SourcePath: "Taskfile.yml", Confidence: .95},
	}}
	manifest := buildCommandsManifest(report)
	if len(manifest.Commands) != 3 {
		t.Fatalf("commands = %#v", manifest.Commands)
	}
	approval := make(map[string]bool, len(manifest.Commands))
	for _, command := range manifest.Commands {
		approval[command.Name] = command.RequiresApproval
	}
	if approval["test"] || !approval["cleanup_exited_sandboxes"] || !approval["stop_containers"] {
		t.Fatalf("command approval classification = %#v", approval)
	}
	workflow := buildTestWorkflow(manifest)
	if len(workflow.Steps) != 1 || workflow.Steps[0] != "go test ./..." {
		t.Fatalf("test workflow included operational commands: %#v", workflow.Steps)
	}
	if !strings.Contains(backendAgent(true), "requires_approval: false") {
		t.Fatal("backend agent does not enforce command approval metadata")
	}
}

func TestGeneratorPreservesExistingAIValuesAndReportsConflict(t *testing.T) {
	root := fixturePath(t, "existing-ai")
	project, snapshot, report := generatorFixture(root)
	proposal, _, err := NewGenerator(GeneratorConfig{}).Generate(context.Background(), project, snapshot, report)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	service := proposedFile(t, proposal, ".ai/service.yaml")
	if !strings.Contains(service.Content, "service_kind: frontend_application") {
		t.Fatalf("existing user value was overwritten:\n%s", service.Content)
	}
	found := false
	for _, conflict := range proposal.Conflicts {
		if conflict.Path == ".ai/service.yaml" && conflict.Field == "service_kind" {
			found = true
		}
	}
	if !found {
		t.Fatalf("existing service_kind conflict was not surfaced: %#v", proposal.Conflicts)
	}
	original, err := os.ReadFile(filepath.Join(root, ".ai", "service.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(original), "backend_service") {
		t.Fatal("Generate() wrote the source .ai/service.yaml")
	}
}

func generatorFixture(root string) (domain.Project, domain.ServiceSnapshot, domain.DiscoveryReport) {
	localPath := root
	gitURL := "https://example.test/course/orders.git"
	project := domain.Project{
		ID: "project-id", Name: "orders", Status: domain.ProjectStatusAnalyzed,
		RepositoryRole: domain.RepositoryRoleService, LocalPath: &localPath, GitURL: &gitURL,
		DefaultBranch: "main", CurrentBranch: "main", HeadCommit: "abc123",
	}
	snapshot := domain.ServiceSnapshot{
		ID: "snapshot-id", ProjectID: project.ID, CommitSHA: "abc123", Branch: "main",
		ContentChecksum: "content-checksum", ServiceKind: domain.ServiceKindBackendService,
		Language: "go", Framework: "chi", Purpose: "Course order service", Confidence: .95,
	}
	now := time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC)
	report := domain.DiscoveryReport{
		SchemaVersion: 1, ProjectID: project.ID, ProjectName: project.Name,
		RepositoryRole: project.RepositoryRole, RepositoryPath: root,
		CommitSHA: snapshot.CommitSHA, Branch: snapshot.Branch, ContentChecksum: snapshot.ContentChecksum,
		StartedAt: now, CompletedAt: now,
		Facts: []domain.Evidence{
			{Category: "classification", Name: "service_kind", Value: "backend_service", Confidence: .95, SourcePath: "go.mod", Explanation: "Go service"},
			{Category: "stack", Name: "language", Value: "go", Confidence: 1, SourcePath: "go.mod", Explanation: "Go module"},
			{Category: "stack", Name: "framework", Value: "chi", Confidence: .9, SourcePath: "go.mod", Explanation: "Chi dependency"},
			{Category: "instruction", Name: "instruction_file", Value: "AGENTS.md", Confidence: 1, SourcePath: "AGENTS.md", Explanation: "Repository rules"},
			{Category: "capability", Name: "http_route", Value: "GET /orders", Confidence: .8, SourcePath: "internal/routes.go", Explanation: "HTTP route"},
			{Category: "ownership", Name: "database_table", Value: "orders", Confidence: .9, SourcePath: "db/migrations/001_orders.up.sql", Explanation: "Migration"},
		},
	}
	return project, snapshot, report
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(workingDirectory, "..", "..", "test", "fixtures", "discovery", name)
}

func fileDigest(t *testing.T, path string) [32]byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(content)
}

func proposedFile(t *testing.T, proposal domain.OnboardingProposal, path string) domain.ProposedFile {
	t.Helper()
	for _, file := range proposal.Files {
		if file.Path == path {
			return file
		}
	}
	t.Fatalf("proposal does not contain %s", path)
	return domain.ProposedFile{}
}

func assertProposed(t *testing.T, proposal domain.OnboardingProposal, path string) {
	t.Helper()
	_ = proposedFile(t, proposal, path)
}

func hasProposed(proposal domain.OnboardingProposal, path string) bool {
	for _, file := range proposal.Files {
		if file.Path == path {
			return true
		}
	}
	return false
}
