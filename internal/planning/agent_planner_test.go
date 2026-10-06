package planning

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
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
		ControlPlane: plannerControlPlane(t),
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
	if !strings.Contains(runner.request.Prompt, "# Shared agent policy") ||
		!strings.Contains(runner.request.Prompt, "# Task route") ||
		!strings.Contains(runner.request.Prompt, "# Contract plan") ||
		!strings.Contains(runner.request.Prompt, "CONTEXT.canonical_assets") {
		t.Fatal("planner prompt did not consume the canonical route and contract-plan skills")
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
	if err := (Validator{MaxParallelTasks: 3, MaxRequiredTaskDepth: 3, ControlPlane: planner.ControlPlane}).Validate(context.Background(), output); err != nil {
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
		ControlPlane: plannerControlPlane(t),
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
	if err := (Validator{MaxParallelTasks: 3, MaxRequiredTaskDepth: 3, ControlPlane: planner.ControlPlane}).Validate(context.Background(), output); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestAgentPlannerIncludesCanonicalProfileAndBoundedRepositoryFacts(t *testing.T) {
	topology, request := plannerAgentFixture(t)
	var gitRoot string
	for index := range request.AvailableProjects {
		if request.AvailableProjects[index].ID == "git" {
			gitRoot = *request.AvailableProjects[index].LocalPath
		}
	}
	writeFixtureFiles(t, gitRoot, canonicalGoFixtureFiles(map[string]string{
		"go.mod":                       "module example.test/git-validator\n\ngo 1.24\n",
		"AGENTS.md":                    "local-routing-fact: backend.usecase owns request validation",
		".ai/service.yaml":             "name: validator\nowner_route: backend.usecase\n",
		"internal/usecase/checkout.go": "package usecase\n\ntype Checkout struct{}\n",
	}))
	result := plannerAgentResult{
		Summary:   "План сохраняет отдельные репозитории и существующий порядок работ.",
		RiskLevel: domain.RiskLevelHigh,
		Risks:     []string{"Изменение должно сохранить границы каждого репозитория."},
		Tasks: []plannerAgentTask{
			plannerAgentTaskFixture("policy", "Зафиксировать правило проверки", domain.RiskLevelMedium),
			plannerAgentTaskFixture("git", "Исправить сценарий валидации", domain.RiskLevelHigh),
			plannerAgentTaskFixture("http", "Сохранить поведение HTTP runtime", domain.RiskLevelHigh),
			plannerAgentTaskFixture("browser", "Сохранить поведение браузерного runtime", domain.RiskLevelHigh),
		},
		Dependencies: []domain.PlannedDependency{},
	}
	planner := AgentPlanner{
		Base: Planner{MaxParallelTasks: 3}, Runner: &plannerRunnerFake{result: result},
		Model: config.DefaultCodexModelDeep, Reasoning: config.DefaultCodexReasoningDeep,
		ControlPlane: plannerControlPlane(t),
	}
	_, output, err := planner.Build(context.Background(), domain.Command{
		ID: "command", Text: "Добавить бизнес-процесс проверки запроса.",
	}, topology, request)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	runner := planner.Runner.(*plannerRunnerFake)
	if !strings.Contains(runner.request.Prompt, "profile/go.canonical") ||
		!strings.Contains(runner.request.Prompt, "local-routing-fact: backend.usecase owns request validation") ||
		!strings.Contains(runner.request.Prompt, "local-routing-fact") {
		t.Fatal("planner prompt omitted canonical profile or bounded local repository facts")
	}
	resolved := false
	for _, profile := range output.Routing.Profiles {
		if profile.ProjectID == "git" && profile.ProfileID == "go.canonical" && profile.Status == domain.ProfileResolutionResolved {
			resolved = true
		}
	}
	if !resolved {
		t.Fatalf("output did not persist resolved Go profile: %#v", output.Routing.Profiles)
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
		ControlPlane: plannerControlPlane(t),
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
		ControlPlane: plannerControlPlane(t),
	}).Build(context.Background(), domain.Command{
		ID: "command", Text: "Исправить обработку путей рабочего пространства.",
	}, catalog, request)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("Build() error = %v, want validation", err)
	}
}

func TestAgentPlannerRejectsDuplicateArchitecturalRoutes(t *testing.T) {
	catalog, request := plannerAgentFixture(t)
	result := plannerAgentResult{
		Summary: "План сохраняет ограниченный состав задач.", RiskLevel: domain.RiskLevelMedium,
		Risks: []string{"Повтор маршрута не должен расширять или искажать границы ответственности."},
		Tasks: []plannerAgentTask{
			plannerAgentTaskFixture("policy", "Зафиксировать общее правило проверки", domain.RiskLevelMedium),
			plannerAgentTaskFixture("git", "Обновить выбранную проверку пути", domain.RiskLevelMedium),
			plannerAgentTaskFixture("http", "Сохранить проверку HTTP-границы", domain.RiskLevelMedium),
			plannerAgentTaskFixture("browser", "Сохранить проверку runtime-границы", domain.RiskLevelMedium),
		},
		Dependencies: []domain.PlannedDependency{},
	}
	result.Tasks[1].ArchitecturalRoutes = []string{"backend.usecase", "backend.usecase"}
	_, _, err := (AgentPlanner{
		Base: Planner{MaxParallelTasks: 3}, Runner: &plannerRunnerFake{result: result},
		Model: config.DefaultCodexModelDeep, Reasoning: config.DefaultCodexReasoningDeep,
		ControlPlane: plannerControlPlane(t),
	}).Build(context.Background(), domain.Command{
		ID: "command", Text: "Исправить сценарий проверки бизнес-процесса в выбранных репозиториях.",
	}, catalog, request)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("Build() error = %v, want validation for duplicate architectural routes", err)
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

func plannerControlPlane(t *testing.T) agentcontrol.Catalog {
	t.Helper()
	catalog, err := agentcontrol.LoadCatalog(os.DirFS(filepath.Join("..", "..")))
	if err != nil {
		t.Fatalf("load canonical planner catalog: %v", err)
	}
	return catalog
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
