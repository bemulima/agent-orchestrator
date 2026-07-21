package planning

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/bemulima/agent-orchestrator/internal/config"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type Planner struct {
	MaxParallelTasks int
}

func (p Planner) Build(
	ctx context.Context,
	command domain.Command,
	catalog domain.TopologyCatalog,
	request domain.PlanRequest,
) (domain.PlannerInput, domain.PlannerOutput, error) {
	if err := ctx.Err(); err != nil {
		return domain.PlannerInput{}, domain.PlannerOutput{}, err
	}
	text := strings.TrimSpace(command.Text)
	if command.ID == "" || text == "" || catalog.Revision.ID == "" {
		return domain.PlannerInput{}, domain.PlannerOutput{}, fmt.Errorf("command and materialized topology are required: %w", domain.ErrValidation)
	}
	input := domain.PlannerInput{
		CommandID: command.ID, CommandText: text, TopologyRevisionID: catalog.Revision.ID,
		RequestedProjectIDs: uniqueSorted(request.RequestedProjectIDs),
	}
	services := make(map[string]domain.TopologyService, len(catalog.Services))
	for _, service := range catalog.Services {
		services[service.ProjectID] = service
	}
	selected, err := selectProjects(text, input.RequestedProjectIDs, services, catalog)
	if err != nil {
		return domain.PlannerInput{}, domain.PlannerOutput{}, err
	}
	selected = includeRelatedProjects(selected, services, catalog.Relations)

	changesContracts := containsAny(text, "api", "contract", "контракт", "event", "событ", "endpoint", "эндпоинт")
	requiresMigration := containsAny(text, "migration", "миграц", "database", "баз", "schema", "схем", "table", "таблиц")
	risk, risks := planRisk(selected, catalog.Drifts, changesContracts, requiresMigration)
	projectIDs := make([]string, 0, len(selected))
	for projectID := range selected {
		projectIDs = append(projectIDs, projectID)
	}
	sort.Slice(projectIDs, func(i, j int) bool {
		left, right := services[projectIDs[i]], services[projectIDs[j]]
		return left.Name+left.ProjectID < right.Name+right.ProjectID
	})

	tasks := make([]domain.PlannedTask, 0, len(projectIDs))
	for index, projectID := range projectIDs {
		service := services[projectID]
		taskRisk := risk
		if taskRisk == domain.RiskLevelCritical {
			taskRisk = domain.RiskLevelHigh
		}
		tasks = append(tasks, domain.PlannedTask{
			Key: projectID, ProjectID: projectID, Role: taskRole(service),
			Title:       bounded("Implement requested change in "+service.Name, 255),
			Description: text,
			AcceptanceCriteria: []string{
				"Implement the requested behavior in " + service.Name + ".",
				"Keep changes inside the approved write scope.",
				"Pass all listed verification commands and report contract or migration changes explicitly.",
			},
			WriteScope: writeScope(service), ModelProfile: modelProfile(taskRisk),
			Priority: len(projectIDs) - index, RiskLevel: taskRisk,
			RequiresMigration: requiresMigration && isBackend(service),
			ChangesContracts:  changesContracts, VerificationCommands: verificationCommands(service),
			Depth: 0,
		})
	}
	dependencies := planDependencies(projectIDs, selected, catalog.Relations)
	dependencies = boundParallelism(projectIDs, dependencies, p.maxParallel())
	output := domain.PlannerOutput{
		Summary:   "Implement requested change across " + strings.Join(serviceNames(projectIDs, services), ", "),
		RiskLevel: risk, Risks: risks, Tasks: tasks, Dependencies: dependencies,
	}
	return input, output, nil
}

func (p Planner) maxParallel() int {
	if p.MaxParallelTasks < 1 {
		return 1
	}
	if p.MaxParallelTasks > 3 {
		return 3
	}
	return p.MaxParallelTasks
}

func selectProjects(
	text string,
	requested []string,
	services map[string]domain.TopologyService,
	catalog domain.TopologyCatalog,
) (map[string]struct{}, error) {
	selected := make(map[string]struct{})
	if len(requested) > 0 {
		for _, projectID := range requested {
			if _, exists := services[projectID]; !exists {
				return nil, fmt.Errorf("project %q is not in the current topology: %w", projectID, domain.ErrValidation)
			}
			selected[projectID] = struct{}{}
		}
		return selected, nil
	}
	tokens := commandTokens(text)
	for projectID, service := range services {
		searchable := service.Name + " " + service.Purpose
		for _, capability := range catalog.Capabilities {
			if capability.ProjectID == projectID {
				searchable += " " + capability.Code + " " + capability.Name + " " + capability.Description
			}
		}
		for _, ownership := range catalog.Ownership {
			if ownership.ProjectID == projectID {
				searchable += " " + ownership.ResourceType + " " + ownership.ResourceName
			}
		}
		for _, contract := range catalog.Contracts {
			if contract.ProjectID == projectID {
				searchable += " " + contract.Code + " " + contract.SourcePath
			}
		}
		searchable = strings.ToLower(searchable)
		score := 0
		if strings.Contains(strings.ToLower(text), strings.ToLower(service.Name)) {
			score += 5
		}
		for _, token := range tokens {
			if strings.Contains(searchable, token) {
				score++
			}
		}
		if score > 0 {
			selected[projectID] = struct{}{}
		}
	}
	if len(selected) == 0 && len(services) == 1 {
		for projectID := range services {
			selected[projectID] = struct{}{}
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("command does not identify an affected topology project: %w", domain.ErrValidation)
	}
	return selected, nil
}

func includeRelatedProjects(
	selected map[string]struct{},
	services map[string]domain.TopologyService,
	relations []domain.ServiceRelation,
) map[string]struct{} {
	result := make(map[string]struct{}, len(selected))
	for projectID := range selected {
		result[projectID] = struct{}{}
	}
	for _, relation := range relations {
		_, sourceSelected := selected[relation.SourceProjectID]
		_, targetSelected := selected[relation.TargetProjectID]
		if sourceSelected {
			if _, exists := services[relation.TargetProjectID]; exists {
				result[relation.TargetProjectID] = struct{}{}
			}
		}
		if targetSelected {
			if _, exists := services[relation.SourceProjectID]; exists {
				result[relation.SourceProjectID] = struct{}{}
			}
		}
	}
	return result
}

func planDependencies(
	projectIDs []string,
	selected map[string]struct{},
	relations []domain.ServiceRelation,
) []domain.PlannedDependency {
	candidates := make([]domain.PlannedDependency, 0)
	for _, relation := range relations {
		if _, sourceExists := selected[relation.SourceProjectID]; !sourceExists {
			continue
		}
		if _, targetExists := selected[relation.TargetProjectID]; !targetExists {
			continue
		}
		switch relation.RelationType {
		case domain.RelationDependsOn, domain.RelationConsumes, domain.RelationSubscribes, domain.RelationRoutesTo:
			candidates = append(candidates, domain.PlannedDependency{
				TaskKey: relation.SourceProjectID, DependsOnTaskKey: relation.TargetProjectID,
				DependencyType: string(relation.RelationType),
			})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return dependencyKey(candidates[i]) < dependencyKey(candidates[j])
	})
	result := make([]domain.PlannedDependency, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.TaskKey == candidate.DependsOnTaskKey || hasDependency(result, candidate.TaskKey, candidate.DependsOnTaskKey) {
			continue
		}
		if pathExists(result, candidate.DependsOnTaskKey, candidate.TaskKey) {
			continue
		}
		result = append(result, candidate)
	}
	return result
}

func boundParallelism(projectIDs []string, dependencies []domain.PlannedDependency, limit int) []domain.PlannedDependency {
	result := append([]domain.PlannedDependency(nil), dependencies...)
	for index := limit; index < len(projectIDs); index++ {
		taskKey, dependsOn := projectIDs[index], projectIDs[index-limit]
		if hasDependency(result, taskKey, dependsOn) || pathExists(result, dependsOn, taskKey) {
			continue
		}
		result = append(result, domain.PlannedDependency{
			TaskKey: taskKey, DependsOnTaskKey: dependsOn, DependencyType: "parallelism_limit",
		})
	}
	sort.Slice(result, func(i, j int) bool { return dependencyKey(result[i]) < dependencyKey(result[j]) })
	return result
}

func pathExists(dependencies []domain.PlannedDependency, from, to string) bool {
	adjacency := make(map[string][]string)
	for _, dependency := range dependencies {
		adjacency[dependency.TaskKey] = append(adjacency[dependency.TaskKey], dependency.DependsOnTaskKey)
	}
	visited := make(map[string]struct{})
	queue := []string{from}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == to {
			return true
		}
		if _, seen := visited[current]; seen {
			continue
		}
		visited[current] = struct{}{}
		queue = append(queue, adjacency[current]...)
	}
	return false
}

func hasDependency(dependencies []domain.PlannedDependency, taskKey, dependsOn string) bool {
	for _, dependency := range dependencies {
		if dependency.TaskKey == taskKey && dependency.DependsOnTaskKey == dependsOn {
			return true
		}
	}
	return false
}

func dependencyKey(value domain.PlannedDependency) string {
	return value.TaskKey + "\x00" + value.DependsOnTaskKey + "\x00" + value.DependencyType
}

func planRisk(
	selected map[string]struct{},
	drifts []domain.ContractDrift,
	changesContracts, requiresMigration bool,
) (domain.RiskLevel, []string) {
	risk := domain.RiskLevelLow
	risks := make([]string, 0)
	if changesContracts {
		risk = domain.RiskLevelMedium
		risks = append(risks, "The request may change public HTTP or event contracts.")
	}
	if requiresMigration {
		risk = domain.RiskLevelHigh
		risks = append(risks, "The request may require a database migration and compatibility review.")
	}
	for _, drift := range drifts {
		producer, consumer := "", ""
		if drift.ProducerProjectID != nil {
			producer = *drift.ProducerProjectID
		}
		if drift.ConsumerProjectID != nil {
			consumer = *drift.ConsumerProjectID
		}
		if _, producerSelected := selected[producer]; !producerSelected {
			if _, consumerSelected := selected[consumer]; !consumerSelected {
				continue
			}
		}
		risks = append(risks, "Existing "+string(drift.Severity)+" contract drift: "+drift.ContractCode)
		if drift.Severity == domain.DriftSeverityCritical {
			risk = domain.RiskLevelCritical
		} else if drift.Severity == domain.DriftSeverityError && risk != domain.RiskLevelCritical {
			risk = domain.RiskLevelHigh
		}
	}
	return risk, uniqueSorted(risks)
}

func taskRole(service domain.TopologyService) string {
	switch {
	case service.RepositoryRole == domain.RepositoryRoleFrontend || service.ServiceKind == domain.ServiceKindFrontendApplication:
		return "frontend-coder"
	case service.ServiceKind == domain.ServiceKindGateway:
		return "gateway-coder"
	case service.RepositoryRole == domain.RepositoryRoleInfrastructure || service.ServiceKind == domain.ServiceKindInfrastructure:
		return "infrastructure-coder"
	default:
		return "backend-coder"
	}
}

func writeScope(service domain.TopologyService) []string {
	switch taskRole(service) {
	case "frontend-coder":
		return []string{"src/**", "app/**", "pages/**", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock"}
	case "gateway-coder":
		return []string{"nginx.conf", "conf.d/**", "config/**", "Dockerfile"}
	case "infrastructure-coder":
		return []string{"docker-compose*.yml", "docker-compose*.yaml", "docker/**", "scripts/**"}
	default:
		return []string{"cmd/**", "internal/**", "db/migrations/**", "openapi/**", "proto/**", "go.mod", "go.sum"}
	}
}

func verificationCommands(service domain.TopologyService) []string {
	commands := []string{"git diff --check"}
	for _, evidence := range service.Stack {
		value := strings.ToLower(evidence.Value)
		if value == "go" {
			commands = append(commands, "go test ./...", "go vet ./...")
		}
		if value == "node" || value == "nextjs" || value == "typescript" {
			commands = append(commands, "npm test", "npm run lint")
		}
	}
	return uniqueSorted(commands)
}

func modelProfile(risk domain.RiskLevel) string {
	if risk == domain.RiskLevelHigh || risk == domain.RiskLevelCritical {
		return config.ModelProfileDeep
	}
	return config.ModelProfileStandard
}

func isBackend(service domain.TopologyService) bool {
	return taskRole(service) == "backend-coder"
}

func serviceNames(ids []string, services map[string]domain.TopologyService) []string {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		result = append(result, services[id].Name)
	}
	return result
}

func commandTokens(value string) []string {
	stopWords := map[string]struct{}{
		"add": {}, "change": {}, "create": {}, "implement": {}, "the": {}, "and": {}, "for": {}, "with": {},
		"добавь": {}, "добавить": {}, "измени": {}, "изменить": {}, "создай": {}, "создать": {},
		"возможность": {}, "только": {}, "которые": {}, "чтобы": {}, "для": {}, "или": {}, "это": {}, "как": {},
	}
	parts := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_'
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if len([]rune(part)) < 3 {
			continue
		}
		if _, excluded := stopWords[part]; excluded {
			continue
		}
		result = append(result, part)
	}
	return uniqueSorted(result)
}

func containsAny(value string, signals ...string) bool {
	value = strings.ToLower(value)
	for _, signal := range signals {
		if strings.Contains(value, signal) {
			return true
		}
	}
	return false
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func bounded(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return string(runes)
}

var _ repository.Planner = Planner{}
