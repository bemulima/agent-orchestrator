package planning

import (
	"context"
	"errors"
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
