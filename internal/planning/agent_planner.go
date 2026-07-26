package planning

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

//go:embed schema/planner-result.schema.json
var plannerResultSchemaJSON []byte

type AgentPlanner struct {
	Base      Planner
	Runner    repository.AgentRunner
	Model     string
	Reasoning string
}

type plannerAgentResult struct {
	Summary      string                     `json:"summary"`
	RiskLevel    domain.RiskLevel           `json:"risk_level"`
	Risks        []string                   `json:"risks"`
	Tasks        []plannerAgentTask         `json:"tasks"`
	Dependencies []domain.PlannedDependency `json:"dependencies"`
}

type plannerAgentTask struct {
	Key                string           `json:"key"`
	Title              string           `json:"title"`
	Description        string           `json:"description"`
	AcceptanceCriteria []string         `json:"acceptance_criteria"`
	RiskLevel          domain.RiskLevel `json:"risk_level"`
	ChangesContracts   bool             `json:"changes_contracts"`
	RequiresMigration  bool             `json:"requires_migration"`
}

type plannerAgentContext struct {
	Request   string                       `json:"request"`
	Baseline  domain.PlannerOutput         `json:"baseline"`
	Projects  []plannerAgentProjectContext `json:"projects"`
	Relations []domain.ServiceRelation     `json:"relations"`
}

type plannerAgentProjectContext struct {
	ProjectID      string                     `json:"project_id"`
	Name           string                     `json:"name"`
	RepositoryRole domain.RepositoryRole      `json:"repository_role"`
	LocalPath      string                     `json:"local_path"`
	ServiceKind    domain.ServiceKind         `json:"service_kind"`
	Purpose        string                     `json:"purpose"`
	Stack          []domain.Evidence          `json:"stack"`
	Capabilities   []domain.ServiceCapability `json:"capabilities"`
	Ownership      []domain.ServiceOwnership  `json:"ownership"`
	Contracts      []plannerContractContext   `json:"contracts"`
}

type plannerContractContext struct {
	Code       string              `json:"code"`
	Type       domain.ContractType `json:"type"`
	Direction  string              `json:"direction"`
	SourcePath string              `json:"source_path"`
}

func (p AgentPlanner) Build(
	ctx context.Context,
	command domain.Command,
	catalog domain.TopologyCatalog,
	request domain.PlanRequest,
) (domain.PlannerInput, domain.PlannerOutput, error) {
	input, baseline, err := p.Base.Build(ctx, command, catalog, request)
	if err != nil {
		return domain.PlannerInput{}, domain.PlannerOutput{}, err
	}
	if p.Runner == nil || strings.TrimSpace(p.Model) == "" {
		return domain.PlannerInput{}, domain.PlannerOutput{}, fmt.Errorf("planner-agent is not configured: %w", domain.ErrInvalidStatus)
	}

	agentContext, workingDirectory, err := buildPlannerAgentContext(input.CommandText, baseline, catalog, request.AvailableProjects)
	if err != nil {
		return domain.PlannerInput{}, domain.PlannerOutput{}, err
	}
	rawContext, err := json.Marshal(agentContext)
	if err != nil {
		return domain.PlannerInput{}, domain.PlannerOutput{}, fmt.Errorf("encode planner-agent context: %w", err)
	}
	schema, err := plannerResultSchema()
	if err != nil {
		return domain.PlannerInput{}, domain.PlannerOutput{}, err
	}
	prompt := `Ты planner-agent мульти-репозиторного оркестратора. Проанализируй запрос владельца,
только перечисленные baseline-задачи, topology relations и read-only checkout каждого проекта.
Ты не изменяешь файлы, не создаёшь issues или PR и не выполняешь внешние записи.

Верни только JSON по схеме. Обязательные правила:
- сохрани ровно по одной задаче на каждый baseline key; не добавляй и не удаляй проекты;
- заголовок, описание, критерии приёмки, summary и риски пиши полностью на русском;
- описание каждой задачи должно касаться только ответственности её репозитория;
- явно отрази все prerequisite-зависимости из запроса владельца и topology;
- task_key — зависимая задача, depends_on_task_key — задача, которая обязана завершиться первой;
- независимые задачи не связывай искусственно;
- security-sensitive реализацию оценивай как high, обычную реализацию как medium,
  документационную или исследовательскую задачу без изменения runtime — как low/medium;
- не ослабляй ограничения baseline и не выдумывай миграции или изменения контрактов;
- публичные API, deploy, merge и внешние публикации остаются вне scope.

Контекст:
` + string(rawContext)
	threadID := ""
	response, err := p.Runner.Run(ctx, domain.AgentRunRequest{
		Role: domain.AgentRunPlanner, WorkingDirectory: workingDirectory,
		Model: p.Model, ReasoningEffort: p.Reasoning,
		Prompt: prompt, OutputSchema: schema,
	}, func(_ context.Context, value string) error {
		threadID = value
		return nil
	})
	if err != nil {
		return domain.PlannerInput{}, domain.PlannerOutput{}, fmt.Errorf("run planner-agent: %w", err)
	}
	if response.ThreadID != "" {
		threadID = response.ThreadID
	}
	if threadID == "" || response.ThreadID != threadID {
		return domain.PlannerInput{}, domain.PlannerOutput{}, fmt.Errorf("planner-agent thread was not captured: %w", domain.ErrConflict)
	}

	result, err := decodePlannerAgentResult(response.Result)
	if err != nil {
		return domain.PlannerInput{}, domain.PlannerOutput{}, err
	}
	refined, err := refinePlannerOutput(baseline, result, p.Base.maxParallel())
	if err != nil {
		return domain.PlannerInput{}, domain.PlannerOutput{}, err
	}
	return input, refined, nil
}

func buildPlannerAgentContext(
	requestText string,
	baseline domain.PlannerOutput,
	catalog domain.TopologyCatalog,
	projects []domain.Project,
) (plannerAgentContext, string, error) {
	selected := make(map[string]struct{}, len(baseline.Tasks))
	for _, task := range baseline.Tasks {
		selected[task.ProjectID] = struct{}{}
	}
	projectByID := make(map[string]domain.Project, len(projects))
	for _, project := range projects {
		projectByID[project.ID] = project
	}
	serviceByID := make(map[string]domain.TopologyService, len(catalog.Services))
	for _, service := range catalog.Services {
		serviceByID[service.ProjectID] = service
	}

	result := plannerAgentContext{Request: requestText, Baseline: baseline}
	workingDirectory := ""
	for _, task := range baseline.Tasks {
		project, ok := projectByID[task.ProjectID]
		if !ok || project.LocalPath == nil || strings.TrimSpace(*project.LocalPath) == "" {
			return plannerAgentContext{}, "", fmt.Errorf("planner-agent requires checkout for project %q: %w", task.ProjectID, domain.ErrInvalidStatus)
		}
		service := serviceByID[task.ProjectID]
		contextValue := plannerAgentProjectContext{
			ProjectID: task.ProjectID, Name: project.Name, RepositoryRole: project.RepositoryRole,
			LocalPath: strings.TrimSpace(*project.LocalPath), ServiceKind: service.ServiceKind,
			Purpose: service.Purpose, Stack: append([]domain.Evidence(nil), service.Stack...),
		}
		if workingDirectory == "" {
			workingDirectory = contextValue.LocalPath
		}
		for _, capability := range catalog.Capabilities {
			if capability.ProjectID == task.ProjectID && len(contextValue.Capabilities) < 50 {
				contextValue.Capabilities = append(contextValue.Capabilities, capability)
			}
		}
		for _, ownership := range catalog.Ownership {
			if ownership.ProjectID == task.ProjectID && len(contextValue.Ownership) < 50 {
				contextValue.Ownership = append(contextValue.Ownership, ownership)
			}
		}
		for _, contract := range catalog.Contracts {
			if contract.ProjectID == task.ProjectID && len(contextValue.Contracts) < 80 {
				contextValue.Contracts = append(contextValue.Contracts, plannerContractContext{
					Code: contract.Code, Type: contract.Type, Direction: contract.Direction, SourcePath: contract.SourcePath,
				})
			}
		}
		result.Projects = append(result.Projects, contextValue)
	}
	for _, relation := range catalog.Relations {
		_, sourceSelected := selected[relation.SourceProjectID]
		_, targetSelected := selected[relation.TargetProjectID]
		if sourceSelected && targetSelected {
			result.Relations = append(result.Relations, relation)
		}
	}
	return result, workingDirectory, nil
}

func plannerResultSchema() (map[string]any, error) {
	var schema map[string]any
	if err := json.Unmarshal(plannerResultSchemaJSON, &schema); err != nil {
		return nil, fmt.Errorf("decode embedded planner-agent schema: %w", err)
	}
	return schema, nil
}

func decodePlannerAgentResult(raw []byte) (plannerAgentResult, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var result plannerAgentResult
	if err := decoder.Decode(&result); err != nil {
		return plannerAgentResult{}, fmt.Errorf("decode planner-agent result: %w", domain.ErrValidation)
	}
	if !containsCyrillic(result.Summary) || !validRisk(result.RiskLevel) {
		return plannerAgentResult{}, fmt.Errorf("planner-agent summary or risk is invalid: %w", domain.ErrValidation)
	}
	for _, risk := range result.Risks {
		if !containsCyrillic(risk) {
			return plannerAgentResult{}, fmt.Errorf("planner-agent risks must be Russian: %w", domain.ErrValidation)
		}
	}
	return result, nil
}

func refinePlannerOutput(
	baseline domain.PlannerOutput,
	result plannerAgentResult,
	maxParallel int,
) (domain.PlannerOutput, error) {
	baseByKey := make(map[string]domain.PlannedTask, len(baseline.Tasks))
	for _, task := range baseline.Tasks {
		baseByKey[task.Key] = task
	}
	if len(result.Tasks) != len(baseByKey) {
		return domain.PlannerOutput{}, fmt.Errorf("planner-agent must cover every baseline task: %w", domain.ErrValidation)
	}
	refinedByKey := make(map[string]domain.PlannedTask, len(result.Tasks))
	for _, candidate := range result.Tasks {
		base, ok := baseByKey[candidate.Key]
		if !ok {
			return domain.PlannerOutput{}, fmt.Errorf("planner-agent introduced task %q: %w", candidate.Key, domain.ErrValidation)
		}
		if _, duplicate := refinedByKey[candidate.Key]; duplicate || !validRisk(candidate.RiskLevel) ||
			!containsCyrillic(candidate.Title) || !containsCyrillic(candidate.Description) ||
			len(candidate.AcceptanceCriteria) == 0 || !allContainCyrillic(candidate.AcceptanceCriteria) {
			return domain.PlannerOutput{}, fmt.Errorf("planner-agent task %q is incomplete or not Russian: %w", candidate.Key, domain.ErrValidation)
		}
		base.Title = strings.TrimSpace(candidate.Title)
		base.Description = strings.TrimSpace(candidate.Description)
		base.AcceptanceCriteria = trimmedValues(candidate.AcceptanceCriteria)
		base.RiskLevel = maxRisk(base.RiskLevel, candidate.RiskLevel)
		base.ModelProfile = modelProfile(base.RiskLevel)
		if candidate.ChangesContracts && !base.ChangesContracts {
			return domain.PlannerOutput{}, fmt.Errorf("planner-agent expanded contract scope for task %q: %w", candidate.Key, domain.ErrValidation)
		}
		if candidate.RequiresMigration && !base.RequiresMigration {
			return domain.PlannerOutput{}, fmt.Errorf("planner-agent expanded migration scope for task %q: %w", candidate.Key, domain.ErrValidation)
		}
		refinedByKey[base.Key] = base
	}

	dependencies := make([]domain.PlannedDependency, 0, len(baseline.Dependencies)+len(result.Dependencies))
	for _, dependency := range baseline.Dependencies {
		if dependency.DependencyType != "parallelism_limit" {
			dependencies = append(dependencies, dependency)
		}
	}
	for _, dependency := range result.Dependencies {
		dependency.TaskKey = strings.TrimSpace(dependency.TaskKey)
		dependency.DependsOnTaskKey = strings.TrimSpace(dependency.DependsOnTaskKey)
		dependency.DependencyType = strings.TrimSpace(dependency.DependencyType)
		if _, ok := refinedByKey[dependency.TaskKey]; !ok {
			return domain.PlannerOutput{}, fmt.Errorf("planner-agent dependency references task %q: %w", dependency.TaskKey, domain.ErrValidation)
		}
		if _, ok := refinedByKey[dependency.DependsOnTaskKey]; !ok || dependency.TaskKey == dependency.DependsOnTaskKey || dependency.DependencyType != "prerequisite" {
			return domain.PlannerOutput{}, fmt.Errorf("planner-agent dependency is invalid: %w", domain.ErrValidation)
		}
		if !hasDependency(dependencies, dependency.TaskKey, dependency.DependsOnTaskKey) {
			dependencies = append(dependencies, dependency)
		}
	}
	projectIDs := make([]string, 0, len(baseline.Tasks))
	for _, task := range baseline.Tasks {
		projectIDs = append(projectIDs, task.Key)
	}
	dependencies = boundParallelism(projectIDs, dependencies, maxParallel)
	depths, err := dependencyDepths(projectIDs, dependencies)
	if err != nil {
		return domain.PlannerOutput{}, err
	}

	tasks := make([]domain.PlannedTask, 0, len(baseline.Tasks))
	planRisk := maxRisk(baseline.RiskLevel, result.RiskLevel)
	for _, base := range baseline.Tasks {
		refined := refinedByKey[base.Key]
		refined.Depth = depths[refined.Key]
		planRisk = maxRisk(planRisk, refined.RiskLevel)
		tasks = append(tasks, refined)
	}
	return domain.PlannerOutput{
		Summary: strings.TrimSpace(result.Summary), RiskLevel: planRisk,
		Risks: uniqueSorted(append(append([]string(nil), baseline.Risks...), result.Risks...)),
		Tasks: tasks, Dependencies: dependencies,
	}, nil
}

func dependencyDepths(keys []string, dependencies []domain.PlannedDependency) (map[string]int, error) {
	depths := make(map[string]int, len(keys))
	resolved := make(map[string]bool, len(keys))
	for len(resolved) < len(keys) {
		progress := false
		for _, key := range keys {
			if resolved[key] {
				continue
			}
			depth := 0
			ready := true
			for _, dependency := range dependencies {
				if dependency.TaskKey != key {
					continue
				}
				if !resolved[dependency.DependsOnTaskKey] {
					ready = false
					break
				}
				if candidate := depths[dependency.DependsOnTaskKey] + 1; candidate > depth {
					depth = candidate
				}
			}
			if ready {
				depths[key] = depth
				resolved[key] = true
				progress = true
			}
		}
		if !progress {
			return nil, fmt.Errorf("planner-agent dependencies contain a cycle: %w", domain.ErrValidation)
		}
	}
	return depths, nil
}

func maxRisk(left, right domain.RiskLevel) domain.RiskLevel {
	order := map[domain.RiskLevel]int{
		domain.RiskLevelLow: 1, domain.RiskLevelMedium: 2,
		domain.RiskLevelHigh: 3, domain.RiskLevelCritical: 4,
	}
	if order[right] > order[left] {
		return right
	}
	return left
}

func containsCyrillic(value string) bool {
	for _, character := range value {
		if unicode.In(character, unicode.Cyrillic) {
			return true
		}
	}
	return false
}

func allContainCyrillic(values []string) bool {
	for _, value := range values {
		if !containsCyrillic(value) {
			return false
		}
	}
	return true
}

func trimmedValues(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

var _ repository.Planner = AgentPlanner{}
