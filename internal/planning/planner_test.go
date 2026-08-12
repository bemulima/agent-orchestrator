package planning

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/config"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestPlannerBuildsDeterministicMultiRepositoryDAG(t *testing.T) {
	catalog := domain.TopologyCatalog{
		Revision: domain.TopologyRevision{ID: "revision-id"},
		Services: []domain.TopologyService{
			{ProjectID: "course", Name: "ms-go-course", Purpose: "Владеет уроками и публикацией уроков", ServiceKind: domain.ServiceKindBackendService,
				Stack: []domain.Evidence{{Name: "language", Value: "go"}}},
			{ProjectID: "admin", Name: "admin-nextjs", RepositoryRole: domain.RepositoryRoleFrontend, ServiceKind: domain.ServiceKindFrontendApplication,
				Stack: []domain.Evidence{{Name: "runtime", Value: "node"}}},
			{ProjectID: "gateway", Name: "gateway", ServiceKind: domain.ServiceKindGateway},
		},
		Capabilities: []domain.ServiceCapability{{ProjectID: "course", Name: "publish lessons", Code: "lessons.publish"}},
		Relations: []domain.ServiceRelation{
			{SourceProjectID: "admin", TargetProjectID: "course", RelationType: domain.RelationConsumes},
			{SourceProjectID: "gateway", TargetProjectID: "course", RelationType: domain.RelationRoutesTo},
		},
	}
	command := domain.Command{ID: "command-id", Text: "Добавь API для публикации только проверенных уроков"}
	planner := Planner{MaxParallelTasks: 2}
	input, output, err := planner.Build(context.Background(), command, catalog, domain.PlanRequest{})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if input.TopologyRevisionID != catalog.Revision.ID || len(output.Tasks) != 3 || len(output.Dependencies) != 2 {
		t.Fatalf("plan = %#v / %#v", input, output)
	}
	if output.RiskLevel != domain.RiskLevelMedium {
		t.Fatalf("risk = %q, want medium", output.RiskLevel)
	}
	for _, task := range output.Tasks {
		if len(task.AcceptanceCriteria) == 0 || len(task.WriteScope) == 0 || len(task.VerificationCommands) == 0 {
			t.Fatalf("incomplete task = %#v", task)
		}
	}
	if err := (Validator{MaxParallelTasks: 2, MaxRequiredTaskDepth: 3}).Validate(context.Background(), output); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	reversed := catalog
	reversed.Services = []domain.TopologyService{catalog.Services[2], catalog.Services[1], catalog.Services[0]}
	_, rebuilt, err := planner.Build(context.Background(), command, reversed, domain.PlanRequest{})
	if err != nil {
		t.Fatalf("reordered Build() error = %v", err)
	}
	if output.Summary != rebuilt.Summary || dependencyKey(output.Dependencies[0]) != dependencyKey(rebuilt.Dependencies[0]) {
		t.Fatalf("planner output is not deterministic: %#v != %#v", output, rebuilt)
	}
}

func TestBoundParallelismUsesRunnableWaves(t *testing.T) {
	projectIDs := []string{"http", "browser", "sandbox", "node"}
	dependencies := []domain.PlannedDependency{
		{TaskKey: "http", DependsOnTaskKey: "sandbox", DependencyType: "prerequisite"},
		{TaskKey: "browser", DependsOnTaskKey: "sandbox", DependencyType: "prerequisite"},
		{TaskKey: "node", DependsOnTaskKey: "sandbox", DependencyType: "prerequisite"},
	}

	bounded := boundParallelism(projectIDs, dependencies, 3)
	if len(bounded) != len(dependencies) {
		t.Fatalf("dependencies = %#v, want only explicit prerequisite edges", bounded)
	}
	for _, dependency := range bounded {
		if dependency.DependencyType == "parallelism_limit" {
			t.Fatalf("unexpected parallelism edge = %#v", dependency)
		}
	}
}

func TestBoundParallelismLimitsActuallyConcurrentTasks(t *testing.T) {
	bounded := boundParallelism([]string{"one", "two", "three", "four"}, nil, 3)
	if len(bounded) != 1 || bounded[0].TaskKey != "four" || bounded[0].DependsOnTaskKey != "one" ||
		bounded[0].DependencyType != "parallelism_limit" {
		t.Fatalf("dependencies = %#v", bounded)
	}
}

func TestPlannerDoesNotTreatImmutableAsDatabaseMigration(t *testing.T) {
	catalog := domain.TopologyCatalog{
		Revision: domain.TopologyRevision{ID: "revision-id"},
		Services: []domain.TopologyService{{
			ProjectID: "sandbox", Name: "sandbox", ServiceKind: domain.ServiceKindBackendService,
		}},
	}

	_, output, err := (Planner{MaxParallelTasks: 3}).Build(context.Background(), domain.Command{
		ID: "command", Text: "Создать immutable workspace snapshot с безопасным lifecycle",
	}, catalog, domain.PlanRequest{RequestedProjectIDs: []string{"sandbox"}})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(output.Tasks) != 1 || output.Tasks[0].RequiresMigration {
		t.Fatalf("tasks = %#v, immutable must not imply a database migration", output.Tasks)
	}
}

func TestPlannerRecognizesExplicitDatabaseMigration(t *testing.T) {
	catalog := domain.TopologyCatalog{
		Revision: domain.TopologyRevision{ID: "revision-id"},
		Services: []domain.TopologyService{{
			ProjectID: "service", Name: "service", ServiceKind: domain.ServiceKindBackendService,
		}},
	}

	_, output, err := (Planner{MaxParallelTasks: 3}).Build(context.Background(), domain.Command{
		ID: "command", Text: "Подготовить миграцию схемы базы данных",
	}, catalog, domain.PlanRequest{RequestedProjectIDs: []string{"service"}})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(output.Tasks) != 1 || !output.Tasks[0].RequiresMigration {
		t.Fatalf("tasks = %#v, explicit migration must be preserved", output.Tasks)
	}
}

func TestPlannerRejectsCatalogProjectMissingFromActiveProjects(t *testing.T) {
	catalog := domain.TopologyCatalog{
		Revision: domain.TopologyRevision{ID: "revision-id"},
		Services: []domain.TopologyService{{
			ProjectID: "archived-service", Name: "archived-service", ServiceKind: domain.ServiceKindBackendService,
		}},
	}

	_, _, err := (Planner{MaxParallelTasks: 3}).Build(context.Background(), domain.Command{
		ID: "command", Text: "Исправить архивный сервис",
	}, catalog, domain.PlanRequest{RequestedProjectIDs: []string{"archived-service"}, AvailableProjects: []domain.Project{}})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("Build() error = %v, want validation", err)
	}
}

func TestPlannerUsesOnlyApprovedManifestVerificationCommands(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".ai"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `schema_version: 1
commands:
  - name: build
    run: npm run build
    risk: verification
  - name: lint
    run: npm run lint
    requires_approval: true
    risk: verification
  - name: test
    run: npm run test
    risk: verification
  - name: dev
    run: npm run dev
    risk: lifecycle
`
	if err := os.WriteFile(filepath.Join(root, ".ai", "commands.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "browser", Name: "browser", LocalPath: &root}
	catalog := domain.TopologyCatalog{
		Revision: domain.TopologyRevision{ID: "revision-id"},
		Services: []domain.TopologyService{{
			ProjectID: "browser", Name: "browser", Stack: []domain.Evidence{{Name: "language", Value: "typescript"}},
		}},
	}
	_, output, err := (Planner{MaxParallelTasks: 3}).Build(context.Background(), domain.Command{
		ID: "command", Text: "Исправить browser validator",
	}, catalog, domain.PlanRequest{RequestedProjectIDs: []string{"browser"}, AvailableProjects: []domain.Project{project}})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []string{"git diff --check", "npm run build", "npm test"}
	if len(output.Tasks) != 1 || len(output.Tasks[0].VerificationCommands) != len(want) {
		t.Fatalf("verification commands = %#v", output.Tasks)
	}
	for _, command := range want {
		if !containsValue(output.Tasks[0].VerificationCommands, command) {
			t.Fatalf("verification commands = %#v, missing %q", output.Tasks[0].VerificationCommands, command)
		}
	}
	if containsValue(output.Tasks[0].VerificationCommands, "npm run lint") {
		t.Fatalf("approval-gated command leaked into plan: %#v", output.Tasks[0].VerificationCommands)
	}
}

func TestPlannerRejectsSymlinkedCommandManifestDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "commands.yaml"), []byte("schema_version: 1\ncommands: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".ai")); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "service", Name: "service", LocalPath: &root}
	_, _, err := (Planner{MaxParallelTasks: 3}).Build(context.Background(), domain.Command{
		ID: "command", Text: "Исправить сервис",
	}, domain.TopologyCatalog{
		Revision: domain.TopologyRevision{ID: "revision"},
		Services: []domain.TopologyService{{ProjectID: "service", Name: "service"}},
	}, domain.PlanRequest{RequestedProjectIDs: []string{"service"}, AvailableProjects: []domain.Project{project}})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("Build() error = %v, want validation", err)
	}
}

func TestPlannerDoesNotInventVerificationCommandsWithoutManifest(t *testing.T) {
	root := t.TempDir()
	project := domain.Project{ID: "service", Name: "service", LocalPath: &root}
	_, output, err := (Planner{MaxParallelTasks: 3}).Build(context.Background(), domain.Command{
		ID: "command", Text: "Исправить сервис",
	}, domain.TopologyCatalog{
		Revision: domain.TopologyRevision{ID: "revision"},
		Services: []domain.TopologyService{{
			ProjectID: "service", Name: "service", Stack: []domain.Evidence{{Name: "language", Value: "go"}},
		}},
	}, domain.PlanRequest{RequestedProjectIDs: []string{"service"}, AvailableProjects: []domain.Project{project}})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(output.Tasks) != 1 || len(output.Tasks[0].VerificationCommands) != 1 ||
		output.Tasks[0].VerificationCommands[0] != "git diff --check" {
		t.Fatalf("verification commands = %#v", output.Tasks)
	}
}

func TestPlannerRequiresKnownAffectedProject(t *testing.T) {
	catalog := domain.TopologyCatalog{
		Revision: domain.TopologyRevision{ID: "revision-id"},
		Services: []domain.TopologyService{{ProjectID: "one", Name: "orders"}, {ProjectID: "two", Name: "courses"}},
	}
	_, _, err := (Planner{MaxParallelTasks: 2}).Build(context.Background(), domain.Command{
		ID: "command", Text: "Сделай неизвестное изменение",
	}, catalog, domain.PlanRequest{})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("Build() error = %v, want validation", err)
	}
}

func TestPlannerKeepsExplicitProjectSelectionExact(t *testing.T) {
	catalog := domain.TopologyCatalog{
		Revision: domain.TopologyRevision{ID: "revision-id"},
		Services: []domain.TopologyService{
			{ProjectID: "validator", Name: "validator", Stack: []domain.Evidence{{Name: "language", Value: "go"}}},
			{ProjectID: "consumer", Name: "consumer", Stack: []domain.Evidence{{Name: "language", Value: "go"}}},
		},
		Relations: []domain.ServiceRelation{{
			SourceProjectID: "consumer", TargetProjectID: "validator", RelationType: domain.RelationConsumes,
		}},
	}

	_, output, err := (Planner{MaxParallelTasks: 3}).Build(context.Background(), domain.Command{
		ID: "command", Text: "Add focused health handler tests",
	}, catalog, domain.PlanRequest{RequestedProjectIDs: []string{"validator"}})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(output.Tasks) != 1 || output.Tasks[0].ProjectID != "validator" || len(output.Dependencies) != 0 {
		t.Fatalf("explicit selection expanded outside its scope: %#v", output)
	}
}

func TestValidatorRejectsCyclesIncompleteTasksAndWideWaves(t *testing.T) {
	validator := Validator{MaxParallelTasks: 2, MaxRequiredTaskDepth: 3}
	validTask := func(key string) domain.PlannedTask {
		return domain.PlannedTask{
			Key: key, ProjectID: key, Role: "backend-coder", Title: key, Description: "fixture",
			AcceptanceCriteria: []string{"passes"}, WriteScope: []string{"internal/**"},
			VerificationCommands: []string{"go test ./..."}, ModelProfile: config.ModelProfileStandard,
			RiskLevel: domain.RiskLevelLow,
		}
	}
	tests := []struct {
		name   string
		output domain.PlannerOutput
	}{
		{name: "cycle", output: domain.PlannerOutput{
			Summary: "cycle", RiskLevel: domain.RiskLevelLow,
			Tasks: []domain.PlannedTask{validTask("a"), validTask("b")},
			Dependencies: []domain.PlannedDependency{
				{TaskKey: "a", DependsOnTaskKey: "b", DependencyType: "depends_on"},
				{TaskKey: "b", DependsOnTaskKey: "a", DependencyType: "depends_on"},
			},
		}},
		{name: "incomplete", output: domain.PlannerOutput{
			Summary: "incomplete", RiskLevel: domain.RiskLevelLow, Tasks: []domain.PlannedTask{{Key: "a", ProjectID: "a"}},
		}},
		{name: "wide", output: domain.PlannerOutput{
			Summary: "wide", RiskLevel: domain.RiskLevelLow,
			Tasks: []domain.PlannedTask{validTask("a"), validTask("b"), validTask("c")},
		}},
		{name: "duplicate project", output: func() domain.PlannerOutput {
			first, second := validTask("a"), validTask("b")
			second.ProjectID = first.ProjectID
			return domain.PlannerOutput{Summary: "duplicate", RiskLevel: domain.RiskLevelLow,
				Tasks: []domain.PlannedTask{first, second}}
		}()},
		{name: "unsafe scope", output: func() domain.PlannerOutput {
			task := validTask("a")
			task.WriteScope = []string{"../other-repository/**"}
			return domain.PlannerOutput{Summary: "unsafe", RiskLevel: domain.RiskLevelLow,
				Tasks: []domain.PlannedTask{task}}
		}()},
		{name: "blank check", output: func() domain.PlannerOutput {
			task := validTask("a")
			task.VerificationCommands = []string{" "}
			return domain.PlannerOutput{Summary: "blank", RiskLevel: domain.RiskLevelLow,
				Tasks: []domain.PlannedTask{task}}
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validator.Validate(context.Background(), test.output); !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("Validate() error = %v, want validation", err)
			}
		})
	}
}
