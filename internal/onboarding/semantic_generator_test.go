package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestSemanticGeneratorResumesThreadAfterTransientFailure(t *testing.T) {
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
	runner := &semanticRunnerFake{
		result:        json.RawMessage(`{"summary":"fixture","facts":[],"open_questions":[]}`),
		transientOnce: true,
	}
	proposal, _, err := (SemanticGenerator{Base: NewGenerator(GeneratorConfig{}), Runner: runner}).
		Generate(context.Background(), project, snapshot, report)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if proposal.Semantic == nil || runner.calls != 2 || runner.resumedThreadID != "semantic-thread" {
		t.Fatalf("proposal = %#v, runner = %#v", proposal.Semantic, runner)
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

func TestValidateSemanticAnalysisRejectsMissingRootCommandPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("Run ./missing.sh after changing directories.\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "project-1", Name: "fixture", LocalPath: &root}
	snapshot := domain.ServiceSnapshot{ID: "snapshot-1", ProjectID: project.ID, CommitSHA: "abc"}
	content := json.RawMessage(`{
  "summary":"fixture",
  "facts":[{
    "category":"command","name":"test","value":"./missing.sh","confidence":0.9,
    "source_path":"README.md","evidence_quote":"Run ./missing.sh after changing directories.",
    "explanation":"The README documents this command."
  }],
  "open_questions":[]
}`)
	analysis, err := validateSemanticAnalysis(root, project, snapshot, content, nil)
	if err != nil {
		t.Fatalf("validateSemanticAnalysis() error = %v", err)
	}
	if len(analysis.Facts) != 0 || len(analysis.RejectedFacts) != 1 ||
		analysis.RejectedFacts[0].Reason != "command_path_not_found_from_repository_root" {
		t.Fatalf("analysis = %#v", analysis)
	}
}

func TestValidateSemanticAnalysisRejectsRuntimeFactsForDocumentationRepository(t *testing.T) {
	root := t.TempDir()
	readme := "The wiki documents the course table and publication requires review.\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(readme), 0o640); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{
		ID: "project-1", Name: "course-wiki", RepositoryRole: domain.RepositoryRoleDocumentation, LocalPath: &root,
	}
	snapshot := domain.ServiceSnapshot{ID: "snapshot-1", ProjectID: project.ID, CommitSHA: "abc"}
	content := json.RawMessage(`{
  "summary":"Course platform documentation.",
  "facts":[{
    "category":"ownership","name":"database_table","value":"course","confidence":0.9,
    "source_path":"README.md","evidence_quote":"The wiki documents the course table",
    "explanation":"The wiki documents the table."
  },{
    "category":"business_rule","name":"publication_requires_review","value":"Publication requires review","confidence":0.9,
    "source_path":"README.md","evidence_quote":"publication requires review",
    "explanation":"The documentation states the rule."
  }],
  "open_questions":[]
}`)
	analysis, err := validateSemanticAnalysis(root, project, snapshot, content, nil)
	if err != nil {
		t.Fatalf("validateSemanticAnalysis() error = %v", err)
	}
	if len(analysis.Facts) != 1 || analysis.Facts[0].Category != "business_rule" ||
		len(analysis.RejectedFacts) != 1 ||
		analysis.RejectedFacts[0].Reason != "runtime_category_not_allowed_for_repository_role" {
		t.Fatalf("analysis = %#v", analysis)
	}
}

func TestValidateSemanticAnalysisRejectsNonProductionAndOperationalRelations(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "test", "e2e"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "test", "e2e", "README.md"),
		[]byte("Requests run through ms-gateway.\n"),
		0o640,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "Taskfile.yml"),
		[]byte("# Uses the ms-go-user shared database.\n"),
		0o640,
	); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "project-1", Name: "ms-go-auth", RepositoryRole: domain.RepositoryRoleService, LocalPath: &root}
	snapshot := domain.ServiceSnapshot{
		ID: "snapshot-1", ProjectID: project.ID, CommitSHA: "abc", ServiceKind: domain.ServiceKindBackendService,
	}
	content := json.RawMessage(`{
  "summary":"Authentication service.",
  "facts":[{
    "category":"relation","name":"gateway_routes_to","value":"ms-gateway","confidence":0.9,
    "source_path":"test/e2e/README.md","evidence_quote":"Requests run through ms-gateway.",
    "explanation":"E2E requests use the gateway."
  },{
    "category":"relation","name":"stores_in","value":"ms-go-user","confidence":0.9,
    "source_path":"Taskfile.yml","evidence_quote":"Uses the ms-go-user shared database.",
    "explanation":"The task manifest points to a local shared database."
  }],
  "open_questions":[]
}`)
	analysis, err := validateSemanticAnalysis(
		root,
		project,
		snapshot,
		content,
		[]string{"ms-go-auth", "ms-gateway", "ms-go-user"},
	)
	if err != nil {
		t.Fatalf("validateSemanticAnalysis() error = %v", err)
	}
	reasons := make(map[string]bool)
	for _, fact := range analysis.RejectedFacts {
		reasons[fact.Reason] = true
	}
	if len(analysis.Facts) != 0 || len(analysis.RejectedFacts) != 2 ||
		!reasons["non_production_evidence_not_allowed_for_runtime_category"] ||
		!reasons["relation_source_is_operational_manifest"] {
		t.Fatalf("analysis = %#v", analysis)
	}
}

func TestValidateSemanticAnalysisRequiresSQLForDatabaseOwnership(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "model.go"),
		[]byte("func (User) TableName() string { return \"user\" }\n"),
		0o640,
	); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "project-1", Name: "ms-go-user", RepositoryRole: domain.RepositoryRoleService, LocalPath: &root}
	snapshot := domain.ServiceSnapshot{
		ID: "snapshot-1", ProjectID: project.ID, CommitSHA: "abc", ServiceKind: domain.ServiceKindBackendService,
	}
	content := json.RawMessage(`{
  "summary":"User service.",
  "facts":[{
    "category":"ownership","name":"database_table","value":"user","confidence":0.9,
    "source_path":"model.go","evidence_quote":"TableName() string { return \"user\" }",
    "explanation":"The ORM model names the table."
  }],
  "open_questions":[]
}`)
	analysis, err := validateSemanticAnalysis(root, project, snapshot, content, nil)
	if err != nil {
		t.Fatalf("validateSemanticAnalysis() error = %v", err)
	}
	if len(analysis.Facts) != 0 || len(analysis.RejectedFacts) != 1 ||
		analysis.RejectedFacts[0].Reason != "database_ownership_requires_sql_source" {
		t.Fatalf("analysis = %#v", analysis)
	}
}

func TestValidateSemanticAnalysisRejectsCallerAllowlistAsDependency(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "main.go"),
		[]byte(`AllowedServices: []string{"ms-go-sandbox"}`+"\n"),
		0o640,
	); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "project-1", Name: "ms-go-student", RepositoryRole: domain.RepositoryRoleService, LocalPath: &root}
	snapshot := domain.ServiceSnapshot{
		ID: "snapshot-1", ProjectID: project.ID, CommitSHA: "abc", ServiceKind: domain.ServiceKindBackendService,
	}
	content := json.RawMessage(`{
  "summary":"Student service.",
  "facts":[{
    "category":"relation","name":"authenticates_through","value":"ms-go-sandbox","confidence":0.9,
    "source_path":"main.go","evidence_quote":"AllowedServices: []string{\"ms-go-sandbox\"}",
    "explanation":"The sandbox is an allowed caller."
  },{
    "category":"relation","name":"depends_on","value":"ms-go-sandbox","confidence":0.9,
    "source_path":"main.go","evidence_quote":"AllowedServices: []string{\"ms-go-sandbox\"}",
    "explanation":"The sandbox is an allowed caller."
  }],
  "open_questions":[]
}`)
	analysis, err := validateSemanticAnalysis(
		root,
		project,
		snapshot,
		content,
		[]string{"ms-go-student", "ms-go-sandbox"},
	)
	if err != nil {
		t.Fatalf("validateSemanticAnalysis() error = %v", err)
	}
	if len(analysis.Facts) != 0 || len(analysis.RejectedFacts) != 2 ||
		analysis.RejectedFacts[0].Reason != "relation_evidence_does_not_support_relation_type" ||
		analysis.RejectedFacts[1].Reason != "relation_evidence_does_not_support_relation_type" {
		t.Fatalf("analysis = %#v", analysis)
	}
}

type semanticRunnerFake struct {
	result          json.RawMessage
	role            domain.AgentRunRole
	transientOnce   bool
	calls           int
	resumedThreadID string
}

func (r *semanticRunnerFake) Run(
	ctx context.Context,
	request domain.AgentRunRequest,
	callback repository.AgentThreadCallback,
) (domain.AgentRunResponse, error) {
	r.role = request.Role
	r.calls++
	if request.ThreadID != "" {
		r.resumedThreadID = request.ThreadID
	}
	if callback != nil {
		if err := callback(ctx, "semantic-thread"); err != nil {
			return domain.AgentRunResponse{}, err
		}
	}
	if r.transientOnce && r.calls == 1 {
		return domain.AgentRunResponse{ThreadID: "semantic-thread"}, fmt.Errorf("fixture: %w", domain.ErrTransient)
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
