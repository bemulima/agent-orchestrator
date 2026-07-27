package planning

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/config"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

func TestAgentPlannerBuildsRussianScopedDependencyDAG(t *testing.T) {
	catalog, request := plannerAgentFixture(t)
	result := plannerAgentResult{
		Summary:   "Сначала зафиксировать общее правило containment, затем параллельно изменить три валидатора.",
		RiskLevel: domain.RiskLevelHigh,
		Risks:     []string{"Ошибочная проверка пути может оставить возможность выхода из рабочего каталога."},
		Tasks: []plannerAgentTask{
			plannerAgentTaskFixture("policy", "Зафиксировать правило containment путей", domain.RiskLevelMedium),
			plannerAgentTaskFixture("git", "Защитить пути Git-валидатора", domain.RiskLevelHigh),
			plannerAgentTaskFixture("http", "Защитить пути HTTP-runtime-валидатора", domain.RiskLevelHigh),
			plannerAgentTaskFixture("browser", "Защитить пути browser-runtime-валидатора", domain.RiskLevelHigh),
		},
		Dependencies: []domain.PlannedDependency{
			{TaskKey: "git", DependsOnTaskKey: "policy", DependencyType: "prerequisite"},
			{TaskKey: "http", DependsOnTaskKey: "policy", DependencyType: "prerequisite"},
			{TaskKey: "browser", DependsOnTaskKey: "policy", DependencyType: "prerequisite"},
		},
	}
	runner := &plannerRunnerFake{result: result}
	planner := AgentPlanner{
		Base: Planner{MaxParallelTasks: 3}, Runner: runner,
		Model: config.DefaultCodexModelDeep, Reasoning: config.DefaultCodexReasoningDeep,
	}
	_, output, err := planner.Build(context.Background(), domain.Command{
		ID: "command", Text: "Сначала определить правило, затем параллельно исправить три валидатора.",
	}, catalog, request)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if runner.request.Role != domain.AgentRunPlanner || runner.request.Model != config.DefaultCodexModelDeep {
		t.Fatalf("planner request = %#v", runner.request)
	}
	if len(output.Tasks) != 4 || len(output.Dependencies) != 3 || output.RiskLevel != domain.RiskLevelHigh {
		t.Fatalf("output = %#v", output)
	}
	for _, task := range output.Tasks {
		if task.Key == "policy" {
			if task.Depth != 0 || task.ModelProfile != config.ModelProfileStandard {
				t.Fatalf("policy task = %#v", task)
			}
			continue
		}
		if task.Depth != 1 || task.ModelProfile != config.ModelProfileDeep {
			t.Fatalf("implementation task = %#v", task)
		}
		if task.Key == "browser" && (!containsValue(task.WriteScope, "src/**") || containsValue(task.WriteScope, "cmd/**")) {
			t.Fatalf("browser write scope = %#v", task.WriteScope)
		}
	}
	if err := (Validator{MaxParallelTasks: 3, MaxRequiredTaskDepth: 3}).Validate(context.Background(), output); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestAgentPlannerOwnerPrerequisiteOverridesReverseRuntimeTopology(t *testing.T) {
	catalog, request := plannerAgentFixture(t)
	catalog.Relations = []domain.ServiceRelation{
		{SourceProjectID: "policy", TargetProjectID: "git", RelationType: domain.RelationConsumes},
		{SourceProjectID: "policy", TargetProjectID: "http", RelationType: domain.RelationConsumes},
		{SourceProjectID: "policy", TargetProjectID: "browser", RelationType: domain.RelationDependsOn},
	}
	result := plannerAgentResult{
		Summary:   "Сначала реализовать безопасный handoff, затем параллельно изменить три валидатора.",
		RiskLevel: domain.RiskLevelHigh,
		Risks:     []string{"Runtime-связи направлены противоположно порядку разработки контракта."},
		Tasks: []plannerAgentTask{
			plannerAgentTaskFixture("policy", "Реализовать безопасный handoff рабочего пространства", domain.RiskLevelHigh),
			plannerAgentTaskFixture("git", "Применить handoff в первом валидаторе", domain.RiskLevelHigh),
			plannerAgentTaskFixture("http", "Применить handoff в HTTP-валидаторе", domain.RiskLevelHigh),
			plannerAgentTaskFixture("browser", "Применить handoff в browser-валидаторе", domain.RiskLevelHigh),
		},
		Dependencies: []domain.PlannedDependency{
			{TaskKey: "git", DependsOnTaskKey: "policy", DependencyType: "prerequisite"},
			{TaskKey: "http", DependsOnTaskKey: "policy", DependencyType: "prerequisite"},
			{TaskKey: "browser", DependsOnTaskKey: "policy", DependencyType: "prerequisite"},
		},
	}
	planner := AgentPlanner{
		Base: Planner{MaxParallelTasks: 3}, Runner: &plannerRunnerFake{result: result},
		Model: config.DefaultCodexModelDeep, Reasoning: config.DefaultCodexReasoningDeep,
	}

	_, output, err := planner.Build(context.Background(), domain.Command{
		ID: "command", Text: "Сначала создать handoff, затем изменить его runtime-потребителей.",
	}, catalog, request)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(output.Dependencies) != 3 {
		t.Fatalf("dependencies = %#v", output.Dependencies)
	}
	for _, dependency := range output.Dependencies {
		if dependency.DependsOnTaskKey != "policy" || dependency.TaskKey == "policy" ||
			dependency.DependencyType != "prerequisite" {
			t.Fatalf("dependency = %#v", dependency)
		}
	}
	if err := (Validator{MaxParallelTasks: 3, MaxRequiredTaskDepth: 3}).Validate(context.Background(), output); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestAgentPlannerRejectsMissingOrForeignTasks(t *testing.T) {
	catalog, request := plannerAgentFixture(t)
	result := plannerAgentResult{
		Summary: "Неполный план изменения валидаторов.", RiskLevel: domain.RiskLevelMedium,
		Risks:        []string{"План не покрывает все выбранные репозитории."},
		Tasks:        []plannerAgentTask{plannerAgentTaskFixture("policy", "Зафиксировать правило containment путей", domain.RiskLevelMedium)},
		Dependencies: []domain.PlannedDependency{},
	}
	_, _, err := (AgentPlanner{
		Base: Planner{MaxParallelTasks: 3}, Runner: &plannerRunnerFake{result: result},
		Model: config.DefaultCodexModelDeep, Reasoning: config.DefaultCodexReasoningDeep,
	}).Build(context.Background(), domain.Command{
		ID: "command", Text: "Исправить выбранные валидаторы после определения общего правила.",
	}, catalog, request)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("Build() error = %v, want validation", err)
	}
}

func TestAgentPlannerRejectsUnverifiedScopeExpansion(t *testing.T) {
	catalog, request := plannerAgentFixture(t)
	result := plannerAgentResult{
		Summary: "План с недопустимым расширением области миграций.", RiskLevel: domain.RiskLevelHigh,
		Risks: []string{"Агент не может самостоятельно добавлять миграции в область работ."},
		Tasks: []plannerAgentTask{
			plannerAgentTaskFixture("policy", "Зафиксировать правило containment путей", domain.RiskLevelMedium),
			plannerAgentTaskFixture("git", "Защитить пути Git-валидатора", domain.RiskLevelHigh),
			plannerAgentTaskFixture("http", "Защитить пути HTTP-runtime-валидатора", domain.RiskLevelHigh),
			plannerAgentTaskFixture("browser", "Защитить пути browser-runtime-валидатора", domain.RiskLevelHigh),
		},
		Dependencies: []domain.PlannedDependency{},
	}
	result.Tasks[1].RequiresMigration = true
	_, _, err := (AgentPlanner{
		Base: Planner{MaxParallelTasks: 3}, Runner: &plannerRunnerFake{result: result},
		Model: config.DefaultCodexModelDeep, Reasoning: config.DefaultCodexReasoningDeep,
	}).Build(context.Background(), domain.Command{
		ID: "command", Text: "Исправить обработку путей рабочего пространства.",
	}, catalog, request)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("Build() error = %v, want validation", err)
	}
}

func plannerAgentFixture(t *testing.T) (domain.TopologyCatalog, domain.PlanRequest) {
	t.Helper()
	root := t.TempDir()
	services := []domain.TopologyService{
		{ProjectID: "policy", Name: "ms-course-promts", RepositoryRole: domain.RepositoryRolePolicy},
		{ProjectID: "git", Name: "ms-go-git-validator", ServiceKind: domain.ServiceKindBackendService,
			Stack: []domain.Evidence{{Name: "language", Value: "go"}}},
		{ProjectID: "http", Name: "ms-go-http-runtime-validator", ServiceKind: domain.ServiceKindBackendService,
			Stack: []domain.Evidence{{Name: "language", Value: "go"}}},
		{ProjectID: "browser", Name: "ms-ts-browser-runtime-validator", ServiceKind: domain.ServiceKindBackendService,
			Stack: []domain.Evidence{{Name: "language", Value: "typescript"}, {Name: "runtime", Value: "node"}}},
	}
	projects := make([]domain.Project, 0, len(services))
	requested := make([]string, 0, len(services))
	for _, service := range services {
		path := filepath.Join(root, service.Name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		projects = append(projects, domain.Project{
			ID: service.ProjectID, Name: service.Name, RepositoryRole: service.RepositoryRole, LocalPath: &path,
		})
		requested = append(requested, service.ProjectID)
	}
	return domain.TopologyCatalog{
		Revision: domain.TopologyRevision{ID: "revision"}, Services: services,
	}, domain.PlanRequest{RequestedProjectIDs: requested, AvailableProjects: projects}
}

func plannerAgentTaskFixture(key, title string, risk domain.RiskLevel) plannerAgentTask {
	return plannerAgentTask{
		Key: key, Title: title,
		Description: "Выполнить только относящиеся к этому репозиторию изменения и сохранить существующие публичные контракты.",
		AcceptanceCriteria: []string{
			"Добавлены сфокусированные проверки требуемого поведения.",
			"Все разрешённые проверки репозитория завершаются успешно.",
		},
		RiskLevel: risk,
	}
}

type plannerRunnerFake struct {
	result  plannerAgentResult
	request domain.AgentRunRequest
}

func (r *plannerRunnerFake) Run(
	ctx context.Context,
	request domain.AgentRunRequest,
	onThread repository.AgentThreadCallback,
) (domain.AgentRunResponse, error) {
	r.request = request
	threadID := "planner-thread"
	if err := onThread(ctx, threadID); err != nil {
		return domain.AgentRunResponse{}, err
	}
	raw, err := json.Marshal(r.result)
	if err != nil {
		return domain.AgentRunResponse{}, err
	}
	return domain.AgentRunResponse{ThreadID: threadID, Result: raw}, nil
}

func containsValue(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
