package onboarding

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

func TestSemanticGeneratorProducesApprovalGatedEvidenceFiles(t *testing.T) {
	root := t.TempDir()
	readme := "# Lessons\n\nOnly reviewed lessons can be published.\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(readme), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Taskfile.yml"), []byte("tasks:\n  test:\n    cmds:\n      - go test ./...\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("RUN go build -o /bin/lessons ./cmd/lessons\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{
		ID: "project-1", Name: "lessons", RepositoryRole: domain.RepositoryRoleService,
		LocalPath: &root, DefaultBranch: "main", HeadCommit: "abc123",
	}
	snapshot := domain.ServiceSnapshot{
		ID: "snapshot-1", ProjectID: project.ID, CommitSHA: "abc123", Branch: "main",
		ServiceKind: domain.ServiceKindBackendService, Purpose: "Manage lessons",
	}
	report := domain.DiscoveryReport{
		SchemaVersion: 6, ProjectID: project.ID, ProjectName: project.Name, CommitSHA: snapshot.CommitSHA,
		Facts: []domain.Evidence{{Category: "purpose", Name: "summary", Value: snapshot.Purpose, SourcePath: "README.md"}},
	}
	runner := &semanticRunnerFake{result: json.RawMessage(`{
  "summary":"Lesson publication rules are explicit.",
  "facts":[{
    "category":"business_rule","name":"publish_reviewed_only",
    "value":"Only reviewed lessons can be published","confidence":0.95,
    "source_path":"README.md","evidence_quote":"Only reviewed lessons can be published.",
    "explanation":"Publication requires prior review."
  },{
    "category":"command","name":"test","value":"go test ./...","confidence":0.95,
    "source_path":"Taskfile.yml","evidence_quote":"- go test ./...",
    "explanation":"Taskfile documents the test command."
  },{
    "category":"relation","name":"deploys","value":"lessons","confidence":0.9,
    "source_path":"README.md","evidence_quote":"# Lessons",
    "explanation":"The image packages this same service."
  },{
    "category":"command","name":"container_build","value":"go build -o /bin/lessons ./cmd/lessons","confidence":0.9,
    "source_path":"Dockerfile","evidence_quote":"RUN go build -o /bin/lessons ./cmd/lessons",
    "explanation":"Docker image build step."
  }],
  "open_questions":[]
}`)}
	generator := SemanticGenerator{Base: NewGenerator(GeneratorConfig{}), Runner: runner}
	proposal, diff, err := generator.Generate(context.Background(), project, snapshot, report)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if runner.role != domain.AgentRunAnalyst || proposal.Semantic == nil || proposal.Generator != semanticGeneratorName {
		t.Fatalf("semantic proposal metadata = %#v, role = %q", proposal.Semantic, runner.role)
	}
	rejectedReasons := make(map[string]bool)
	for _, fact := range proposal.Semantic.Analysis.RejectedFacts {
		rejectedReasons[fact.Reason] = true
	}
	if len(proposal.Semantic.Analysis.RejectedFacts) != 2 || !rejectedReasons["self_relation_not_allowed"] ||
		!rejectedReasons["command_source_not_approved"] {
		t.Fatalf("self relation was not isolated: %#v", proposal.Semantic.Analysis.RejectedFacts)
	}
	if !hasProposed(proposal, ".ai/discovery/semantic-report.json") ||
		!containsProposed(proposal, ".ai/commands.yaml", "go test ./...") ||
		!hasProposed(proposal, ".ai/workflows/test.yaml") ||
		!containsProposed(proposal, ".ai/service.yaml", "business_rules") ||
		!containsProposed(proposal, ".ai/service.yaml", "publish_reviewed_only") {
		t.Fatalf("semantic files missing from proposal: %#v", proposal.Files)
	}
	if diff == "" {
		t.Fatal("semantic proposal has no diff")
	}
	if _, err := os.Stat(filepath.Join(root, ".ai")); !os.IsNotExist(err) {
		t.Fatalf("semantic generator modified source repository: %v", err)
	}
}

func TestSemanticGeneratorExcludesUnverifiableQuote(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("documented fact\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "project-1", Name: "fixture", LocalPath: &root}
	snapshot := domain.ServiceSnapshot{ID: "snapshot-1", ProjectID: project.ID, CommitSHA: "abc"}
	report := domain.DiscoveryReport{
		ProjectID: project.ID, ProjectName: project.Name, CommitSHA: snapshot.CommitSHA,
		Facts: []domain.Evidence{{Category: "purpose", Name: "summary", Value: "fixture", SourcePath: "README.md"}},
	}
	runner := &semanticRunnerFake{result: json.RawMessage(`{
  "summary":"fixture","facts":[{
    "category":"business_rule","name":"invented","value":"invented","confidence":0.9,
    "source_path":"README.md","evidence_quote":"this quote is not in the file",
    "explanation":"invented"
  }],"open_questions":[]
}`)}
	proposal, _, err := (SemanticGenerator{Base: NewGenerator(GeneratorConfig{}), Runner: runner}).
		Generate(context.Background(), project, snapshot, report)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if proposal.Semantic == nil || len(proposal.Semantic.Analysis.Facts) != 0 ||
		len(proposal.Semantic.Analysis.RejectedFacts) != 1 ||
		proposal.Semantic.Analysis.RejectedFacts[0].Reason != "evidence_quote_not_verified_against_current_source" {
		t.Fatalf("unverifiable semantic fact was not isolated: %#v", proposal.Semantic)
	}
	if containsProposed(proposal, ".ai/service.yaml", "invented") {
		t.Fatal("unverifiable semantic fact entered the service manifest")
	}
}

func TestSemanticGeneratorAcceptsRepositoryWideOpenQuestion(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("Documented fixture.\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "project-1", Name: "fixture", LocalPath: &root}
	snapshot := domain.ServiceSnapshot{ID: "snapshot-1", ProjectID: project.ID, CommitSHA: "abc"}
	report := domain.DiscoveryReport{
		ProjectID: project.ID, ProjectName: project.Name, CommitSHA: snapshot.CommitSHA,
		Facts: []domain.Evidence{{Category: "purpose", Name: "summary", Value: "fixture", SourcePath: "README.md"}},
	}
	runner := &semanticRunnerFake{result: json.RawMessage(`{
  "summary":"fixture","facts":[],"open_questions":[{
    "question":"Which service owns deployment?","reason":"No deployment owner is documented.","source_paths":["."]
  }]
}`)}
	proposal, _, err := (SemanticGenerator{Base: NewGenerator(GeneratorConfig{}), Runner: runner}).
		Generate(context.Background(), project, snapshot, report)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if proposal.Semantic == nil || len(proposal.Semantic.Analysis.OpenQuestions) != 1 ||
		len(proposal.Semantic.Analysis.OpenQuestions[0].SourcePaths) != 0 {
		t.Fatalf("open questions = %#v", proposal.Semantic)
	}
}

func TestValidateSemanticAnalysisRejectsRelationOutsideConnectedCatalog(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("Uses the shared ms-net Docker network.\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "project-1", Name: "fixture", LocalPath: &root}
	snapshot := domain.ServiceSnapshot{ID: "snapshot-1", ProjectID: project.ID, CommitSHA: "abc"}
	content := json.RawMessage(`{
  "summary":"fixture",
  "facts":[{
    "category":"relation","name":"deploys","value":"ms-net","confidence":0.9,
    "source_path":"README.md","evidence_quote":"Uses the shared ms-net Docker network.",
    "explanation":"The service uses this network."
  }],
  "open_questions":[]
}`)
	analysis, err := validateSemanticAnalysis(root, project, snapshot, content, []string{"fixture", "ms-go-sandbox"})
	if err != nil {
		t.Fatalf("validateSemanticAnalysis() error = %v", err)
	}
	if len(analysis.Facts) != 0 || len(analysis.RejectedFacts) != 1 ||
		analysis.RejectedFacts[0].Reason != "relation_target_not_connected" {
		t.Fatalf("analysis = %#v", analysis)
	}
}

type semanticRunnerFake struct {
	result json.RawMessage
	role   domain.AgentRunRole
}

func (r *semanticRunnerFake) Run(
	ctx context.Context,
	request domain.AgentRunRequest,
	callback repository.AgentThreadCallback,
) (domain.AgentRunResponse, error) {
	r.role = request.Role
	if callback != nil {
		if err := callback(ctx, "semantic-thread"); err != nil {
			return domain.AgentRunResponse{}, err
		}
	}
	return domain.AgentRunResponse{ThreadID: "semantic-thread", Result: r.result}, nil
}

func containsProposed(proposal domain.OnboardingProposal, path, value string) bool {
	for _, file := range proposal.Files {
		if file.Path == path && strings.Contains(file.Content, value) {
			return true
		}
	}
	return false
}
