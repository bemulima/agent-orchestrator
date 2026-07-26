package planning

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/bemulima/agent-orchestrator/internal/config"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
	"gopkg.in/yaml.v3"
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
	sourceIssues, err := normalizedIssueReferences(request.SourceIssues)
	if err != nil {
		return domain.PlannerInput{}, domain.PlannerOutput{}, err
	}
	input := domain.PlannerInput{
		CommandID: command.ID, CommandText: text, TopologyRevisionID: catalog.Revision.ID,
		RequestedProjectIDs: uniqueSorted(request.RequestedProjectIDs),
		SourceIssues:        sourceIssues,
	}
	services := make(map[string]domain.TopologyService, len(catalog.Services))
	projects := make(map[string]domain.Project, len(request.AvailableProjects))
	for _, project := range request.AvailableProjects {
		projects[project.ID] = project
	}
	for _, service := range catalog.Services {
		services[service.ProjectID] = service
	}
	for _, project := range request.AvailableProjects {
		if _, exists := services[project.ID]; exists {
			continue
		}
		services[project.ID] = domain.TopologyService{
			ProjectID: project.ID, Name: project.Name,
			RepositoryRole: project.RepositoryRole, ServiceKind: serviceKindForRole(project.RepositoryRole),
			Purpose: "Repository " + project.Name,
		}
	}
	selectionSeeds := input.RequestedProjectIDs
	if len(selectionSeeds) == 0 && len(input.SourceIssues) > 0 {
		for _, issue := range input.SourceIssues {
			selectionSeeds = append(selectionSeeds, issue.ProjectID)
		}
		selectionSeeds = uniqueSorted(selectionSeeds)
	}
	selected, err := selectProjects(text, selectionSeeds, services, catalog)
	if err != nil {
		return domain.PlannerInput{}, domain.PlannerOutput{}, err
	}
	if len(input.RequestedProjectIDs) == 0 {
		selected = includeRelatedProjects(selected, services, catalog.Relations)
	}

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
		checks, err := verificationCommands(projects[projectID])
		if err != nil {
			return domain.PlannerInput{}, domain.PlannerOutput{}, err
		}
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
			ChangesContracts:  changesContracts, VerificationCommands: checks,
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

func normalizedIssueReferences(values []domain.IssueReference) ([]domain.IssueReference, error) {
	seen := make(map[string]domain.IssueReference, len(values))
	for _, value := range values {
		value.ProjectID = strings.TrimSpace(value.ProjectID)
		value.URL = strings.TrimSpace(value.URL)
		if value.ProjectID == "" || value.Number < 1 ||
			value.Provider != domain.IssueProviderGitHub && value.Provider != domain.IssueProviderGitLab {
			return nil, fmt.Errorf("invalid source issue reference: %w", domain.ErrValidation)
		}
		key := string(value.Provider) + "\x00" + value.ProjectID + "\x00" + fmt.Sprint(value.Number)
		seen[key] = value
	}
	result := make([]domain.IssueReference, 0, len(seen))
	for _, value := range seen {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		return string(result[i].Provider)+result[i].ProjectID+fmt.Sprint(result[i].Number) <
			string(result[j].Provider)+result[j].ProjectID+fmt.Sprint(result[j].Number)
	})
	return result, nil
}

func serviceKindForRole(role domain.RepositoryRole) domain.ServiceKind {
	switch role {
	case domain.RepositoryRoleFrontend:
		return domain.ServiceKindFrontendApplication
	case domain.RepositoryRoleInfrastructure:
		return domain.ServiceKindInfrastructure
	default:
		return domain.ServiceKindUnknown
	}
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
		risks = append(risks, "Запрос может изменить публичные HTTP- или event-контракты.")
	}
	if requiresMigration {
		risk = domain.RiskLevelHigh
		risks = append(risks, "Запрос может потребовать миграцию базы данных и проверку совместимости.")
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
		risks = append(risks, "Существующий contract drift уровня "+string(drift.Severity)+": "+drift.ContractCode)
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
	case service.RepositoryRole == domain.RepositoryRoleContent ||
		service.RepositoryRole == domain.RepositoryRolePolicy ||
		service.RepositoryRole == domain.RepositoryRoleDocumentation ||
		service.RepositoryRole == domain.RepositoryRoleArchive:
		return "knowledge-coder"
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
	case "knowledge-coder":
		return []string{"**/*.md", "docs/**", ".ai/**", "AGENTS.md", "prompts/**", "wiki/**", "journal/**"}
	}
	if stackContains(service, "node", "nextjs", "typescript", "javascript") {
		return []string{
			"src/**", "lib/**", "app/**", "test/**", "tests/**", "__tests__/**",
			"package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock",
			"tsconfig*.json", ".ai/**", "AGENTS.md",
		}
	}
	if stackContains(service, "python") {
		return []string{
			"src/**", "app/**", "tests/**", "pyproject.toml", "poetry.lock", "requirements*.txt",
			".ai/**", "AGENTS.md",
		}
	}
	if stackContains(service, "php") {
		return []string{
			"src/**", "app/**", "tests/**", "composer.json", "composer.lock", ".ai/**", "AGENTS.md",
		}
	}
	return []string{
		"cmd/**", "internal/**", "pkg/**", "tests/**", "db/migrations/**", "openapi/**", "proto/**",
		"go.mod", "go.sum", ".ai/**", "AGENTS.md",
	}
}

func verificationCommands(project domain.Project) ([]string, error) {
	manifestCommands, found, err := approvedManifestVerificationCommands(project)
	if err != nil {
		return nil, err
	}
	if found {
		return uniqueSorted(append([]string{"git diff --check"}, manifestCommands...)), nil
	}
	return []string{"git diff --check"}, nil
}

type commandManifest struct {
	SchemaVersion int                    `yaml:"schema_version"`
	Commands      []commandManifestEntry `yaml:"commands"`
}

type commandManifestEntry struct {
	Name             string `yaml:"name"`
	Run              string `yaml:"run"`
	RequiresApproval bool   `yaml:"requires_approval"`
	Risk             string `yaml:"risk"`
}

func approvedManifestVerificationCommands(project domain.Project) ([]string, bool, error) {
	if project.LocalPath == nil || strings.TrimSpace(*project.LocalPath) == "" {
		return nil, false, nil
	}
	root := filepath.Clean(strings.TrimSpace(*project.LocalPath))
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, false, fmt.Errorf("resolve %s checkout: %w", project.Name, err)
	}
	aiPath := filepath.Join(resolvedRoot, ".ai")
	aiInfo, err := os.Lstat(aiPath)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect %s command directory: %w", project.Name, err)
	}
	if !aiInfo.IsDir() || aiInfo.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("unsafe %s command directory: %w", project.Name, domain.ErrValidation)
	}
	path := filepath.Join(aiPath, "commands.yaml")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect %s command manifest: %w", project.Name, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 256<<10 {
		return nil, false, fmt.Errorf("unsafe or oversized %s command manifest: %w", project.Name, domain.ErrValidation)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, false, fmt.Errorf("resolve %s command manifest: %w", project.Name, err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, false, fmt.Errorf("command manifest escapes %s checkout: %w", project.Name, domain.ErrValidation)
	}
	file, err := os.Open(resolvedPath)
	if err != nil {
		return nil, false, fmt.Errorf("read %s command manifest: %w", project.Name, err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || openedInfo.Size() > 256<<10 || !os.SameFile(info, openedInfo) {
		return nil, false, fmt.Errorf("command manifest changed while opening %s checkout: %w", project.Name, domain.ErrConflict)
	}
	content, err := io.ReadAll(io.LimitReader(file, (256<<10)+1))
	if err != nil || len(content) > 256<<10 {
		return nil, false, fmt.Errorf("read bounded %s command manifest: %w", project.Name, domain.ErrValidation)
	}
	var manifest commandManifest
	if err := yaml.Unmarshal(content, &manifest); err != nil || manifest.SchemaVersion != 1 {
		return nil, false, fmt.Errorf("decode %s command manifest: %w", project.Name, domain.ErrValidation)
	}
	commands := make([]string, 0, len(manifest.Commands))
	for _, entry := range manifest.Commands {
		if entry.RequiresApproval || !manifestEntryIsVerification(entry) {
			continue
		}
		command := canonicalVerificationCommand(entry.Run)
		if command != "" {
			commands = append(commands, command)
		}
	}
	return uniqueSorted(commands), true, nil
}

func manifestEntryIsVerification(entry commandManifestEntry) bool {
	if strings.EqualFold(strings.TrimSpace(entry.Risk), "verification") {
		return true
	}
	name := strings.ToLower(strings.TrimSpace(entry.Name))
	for _, marker := range []string{"test", "lint", "check", "verify", "build"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func canonicalVerificationCommand(value string) string {
	switch strings.Join(strings.Fields(strings.TrimSpace(value)), " ") {
	case "go test ./...":
		return "go test ./..."
	case "go test ./... -count=1":
		return "go test ./... -count=1"
	case "npm test", "npm run test":
		return "npm test"
	case "npm run build":
		return "npm run build"
	case "npm run lint":
		return "npm run lint"
	default:
		return ""
	}
}

func stackContains(service domain.TopologyService, values ...string) bool {
	for _, evidence := range service.Stack {
		value := strings.ToLower(strings.TrimSpace(evidence.Value))
		for _, candidate := range values {
			if value == candidate || strings.Contains(value, candidate) {
				return true
			}
		}
	}
	return false
}

func modelProfile(risk domain.RiskLevel) string {
	if risk == domain.RiskLevelHigh || risk == domain.RiskLevelCritical {
		return config.ModelProfileDeep
	}
	if risk == domain.RiskLevelMedium {
		return config.ModelProfileStandard
	}
	return config.ModelProfileFast
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
