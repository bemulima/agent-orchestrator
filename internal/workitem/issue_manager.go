package workitem

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/bemulima/agent-orchestrator/internal/config"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

//go:embed schema/issue-manager-result.schema.json
var issueManagerSchemaJSON []byte

type planGetter interface {
	GetPlan(context.Context, string) (domain.PlanBundle, error)
}

type IssueManager struct {
	Plans     planGetter
	Projects  repository.ProjectRepository
	Items     repository.WorkItemRepository
	Gateway   repository.WorkItemGateway
	Runner    repository.AgentRunner
	Models    map[string]string
	Reasoning map[string]string
}

type issueManagerContext struct {
	Plan         domain.Plan                  `json:"plan"`
	Tasks        []domain.Task                `json:"tasks"`
	Dependencies []domain.TaskDependency      `json:"dependencies"`
	SourceIssues []domain.IssueReference      `json:"source_issues"`
	Projects     []issueManagerProjectContext `json:"projects"`
}

type issueManagerProjectContext struct {
	Project  domain.Project                  `json:"project"`
	Metadata repository.ProjectIssueMetadata `json:"metadata"`
}

func (m IssueManager) Prepare(ctx context.Context, planID string) ([]domain.WorkItem, error) {
	if m.Plans == nil || m.Projects == nil || m.Items == nil || m.Gateway == nil || m.Runner == nil {
		return nil, fmt.Errorf("issue manager is incomplete: %w", domain.ErrInvalidStatus)
	}
	bundle, err := m.Plans.GetPlan(ctx, strings.TrimSpace(planID))
	if err != nil {
		return nil, err
	}
	if bundle.Plan.Status != domain.PlanStatusDiscussion {
		return nil, fmt.Errorf("issues can be prepared only during plan discussion: %w", domain.ErrInvalidStatus)
	}
	if len(bundle.WorkItems) > 0 {
		return bundle.WorkItems, nil
	}
	contextValue, err := m.buildContext(ctx, bundle)
	if err != nil {
		return nil, err
	}
	rawContext, err := json.Marshal(contextValue)
	if err != nil {
		return nil, fmt.Errorf("encode issue-manager context: %w", err)
	}
	profile := managerProfile(bundle.Tasks)
	workingDirectory := ""
	if len(contextValue.Projects) > 0 && contextValue.Projects[0].Project.LocalPath != nil {
		workingDirectory = strings.TrimSpace(*contextValue.Projects[0].Project.LocalPath)
	}
	if workingDirectory == "" {
		return nil, fmt.Errorf("issue manager requires a connected project checkout: %w", domain.ErrInvalidStatus)
	}
	schema, err := issueManagerSchema()
	if err != nil {
		return nil, err
	}
	prompt := `Ты issue-manage-agent. Подготовь по одной полноценной GitHub issue на русском языке для каждой задачи плана.
Ты не изменяешь код, не создаёшь PR и не выполняешь внешние записи. Верни только JSON по схеме.

Для каждой issue:
- выбери тип: question, idea, task, bug или research;
- заголовок и всё объяснение напиши на русском;
- body обязан содержать разделы: ## Контекст, ## Цель, ## Ответственность репозитория,
  ## Объём работ, ## Критерии приёмки, ## Зависимости, ## Риски;
- укажи labels, milestone и хотя бы одного assignee;
- используй существующую source issue только если project_id совпадает с задачей;
- сложность и model_profile должны соответствовать риску задачи: low=fast, medium=standard,
  high/critical=deep;
- не выдумывай выполненные изменения.

Контекст плана:
` + string(rawContext)
	threadID := ""
	response, err := m.Runner.Run(ctx, domain.AgentRunRequest{
		Role: domain.AgentRunIssueManager, WorkingDirectory: workingDirectory, Model: m.Models[profile],
		ReasoningEffort: m.Reasoning[profile],
		Prompt:          prompt, OutputSchema: schema,
	}, func(_ context.Context, value string) error {
		threadID = value
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("run issue-manage-agent: %w", err)
	}
	if response.ThreadID != "" {
		threadID = response.ThreadID
	}
	if threadID == "" || response.ThreadID != threadID {
		return nil, fmt.Errorf("issue-manage-agent thread was not captured: %w", domain.ErrConflict)
	}
	result, err := validateIssueManagerResult(response.Result, bundle, contextValue)
	if err != nil {
		return nil, err
	}
	if err := m.resolveExistingIssues(ctx, contextValue, result.Issues); err != nil {
		return nil, err
	}
	return m.Items.SaveIssueProposals(ctx, bundle, threadID, result.Issues)
}

func (m IssueManager) buildContext(ctx context.Context, bundle domain.PlanBundle) (issueManagerContext, error) {
	result := issueManagerContext{Plan: bundle.Plan, Tasks: bundle.Tasks, Dependencies: bundle.Dependencies}
	var input domain.PlannerInput
	if err := json.Unmarshal(bundle.Plan.PlannerInput, &input); err != nil {
		return issueManagerContext{}, fmt.Errorf("decode source issues: %w", err)
	}
	result.SourceIssues = append([]domain.IssueReference(nil), input.SourceIssues...)
	for _, issue := range result.SourceIssues {
		if issue.Provider != domain.IssueProviderGitHub {
			return issueManagerContext{}, fmt.Errorf("source issue provider %q is not configured for manager-agent publication: %w",
				issue.Provider, domain.ErrValidation)
		}
	}
	projectIDs := make(map[string]struct{}, len(bundle.Tasks))
	for _, task := range bundle.Tasks {
		projectIDs[task.ProjectID] = struct{}{}
	}
	for projectID := range projectIDs {
		project, err := m.Projects.Get(ctx, projectID)
		if err != nil {
			return issueManagerContext{}, err
		}
		metadata, err := m.Gateway.Metadata(ctx, project)
		if err != nil {
			return issueManagerContext{}, err
		}
		result.Projects = append(result.Projects, issueManagerProjectContext{Project: project, Metadata: metadata})
	}
	sort.Slice(result.Projects, func(i, j int) bool { return result.Projects[i].Project.Name < result.Projects[j].Project.Name })
	return result, nil
}

func (m IssueManager) resolveExistingIssues(
	ctx context.Context,
	managerContext issueManagerContext,
	drafts []domain.IssueDraft,
) error {
	projects := make(map[string]domain.Project, len(managerContext.Projects))
	for _, value := range managerContext.Projects {
		projects[value.Project.ID] = value.Project
	}
	allowed := make(map[string]struct{}, len(managerContext.SourceIssues))
	for _, issue := range managerContext.SourceIssues {
		allowed[issueReferenceKey(issue)] = struct{}{}
	}
	for index := range drafts {
		if drafts[index].Existing == nil {
			continue
		}
		if _, ok := allowed[issueReferenceKey(*drafts[index].Existing)]; !ok {
			return fmt.Errorf("issue manager selected an unapproved existing issue: %w", domain.ErrValidation)
		}
		publication, err := m.Gateway.GetIssue(ctx, projects[drafts[index].ProjectID], drafts[index].Existing.Number)
		if err != nil {
			return err
		}
		drafts[index].Existing.URL = publication.URL
	}
	return nil
}

func issueManagerSchema() (map[string]any, error) {
	var schema map[string]any
	if err := json.Unmarshal(issueManagerSchemaJSON, &schema); err != nil {
		return nil, fmt.Errorf("decode embedded issue-manager schema: %w", err)
	}
	return schema, nil
}

func validateIssueManagerResult(
	raw []byte,
	bundle domain.PlanBundle,
	managerContext issueManagerContext,
) (domain.IssueManagerResult, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var result domain.IssueManagerResult
	if err := decoder.Decode(&result); err != nil {
		return domain.IssueManagerResult{}, fmt.Errorf("decode issue-manager result: %w", domain.ErrValidation)
	}
	if !hasCyrillic(result.Summary) || len(result.Issues) != len(bundle.Tasks) {
		return domain.IssueManagerResult{}, fmt.Errorf("issue-manager result must be Russian and cover every task: %w", domain.ErrValidation)
	}
	tasks := make(map[string]domain.Task, len(bundle.Tasks))
	metadata := make(map[string]repository.ProjectIssueMetadata, len(managerContext.Projects))
	for _, task := range bundle.Tasks {
		tasks[task.PlannerKey] = task
	}
	for _, value := range managerContext.Projects {
		metadata[value.Project.ID] = value.Metadata
	}
	seen := make(map[string]struct{}, len(result.Issues))
	for index := range result.Issues {
		draft := &result.Issues[index]
		task, ok := tasks[draft.TaskKey]
		if !ok || task.ProjectID != draft.ProjectID {
			return domain.IssueManagerResult{}, fmt.Errorf("issue does not map to a plan task: %w", domain.ErrValidation)
		}
		if _, duplicate := seen[draft.TaskKey]; duplicate {
			return domain.IssueManagerResult{}, fmt.Errorf("duplicate issue for task: %w", domain.ErrConflict)
		}
		seen[draft.TaskKey] = struct{}{}
		draft.Complexity, draft.ModelProfile = complexityForTask(task), profileForTask(task)
		if !validIssueType(draft.IssueType) || !hasCyrillic(draft.Title) || !hasCyrillic(draft.Body) ||
			len([]rune(strings.TrimSpace(draft.Title))) < 10 || len([]rune(draft.Title)) > 255 ||
			len([]rune(draft.Body)) < 100 || !hasRequiredIssueSections(draft.Body) ||
			len(draft.Labels) == 0 || strings.TrimSpace(draft.Milestone) == "" || len(draft.Assignees) == 0 {
			return domain.IssueManagerResult{}, fmt.Errorf("issue proposal is incomplete or not Russian: %w", domain.ErrValidation)
		}
		availableAssignees := stringSet(metadata[draft.ProjectID].Assignees)
		for _, assignee := range draft.Assignees {
			if _, ok := availableAssignees[assignee]; !ok {
				return domain.IssueManagerResult{}, fmt.Errorf("assignee %q is unavailable in project: %w", assignee, domain.ErrValidation)
			}
		}
	}
	return result, nil
}

func hasRequiredIssueSections(body string) bool {
	for _, section := range []string{
		"## Контекст", "## Цель", "## Ответственность репозитория", "## Объём работ",
		"## Критерии приёмки", "## Зависимости", "## Риски",
	} {
		if !strings.Contains(body, section) {
			return false
		}
	}
	return true
}

func hasCyrillic(value string) bool {
	for _, character := range value {
		if unicode.In(character, unicode.Cyrillic) {
			return true
		}
	}
	return false
}

func validIssueType(value domain.IssueType) bool {
	switch value {
	case domain.IssueTypeQuestion, domain.IssueTypeIdea, domain.IssueTypeTask, domain.IssueTypeBug, domain.IssueTypeResearch:
		return true
	default:
		return false
	}
}

func complexityForTask(task domain.Task) domain.TaskComplexity {
	switch task.RiskLevel {
	case domain.RiskLevelCritical:
		return domain.TaskComplexityCritical
	case domain.RiskLevelHigh:
		return domain.TaskComplexityHigh
	case domain.RiskLevelMedium:
		return domain.TaskComplexityMedium
	default:
		return domain.TaskComplexityLow
	}
}

func profileForTask(task domain.Task) string {
	switch complexityForTask(task) {
	case domain.TaskComplexityCritical, domain.TaskComplexityHigh:
		return config.ModelProfileDeep
	case domain.TaskComplexityMedium:
		return config.ModelProfileStandard
	default:
		return config.ModelProfileFast
	}
}

func managerProfile(tasks []domain.Task) string {
	profile := config.ModelProfileFast
	for _, task := range tasks {
		candidate := profileForTask(task)
		if candidate == config.ModelProfileDeep {
			return candidate
		}
		if candidate == config.ModelProfileStandard {
			profile = candidate
		}
	}
	return profile
}

func issueReferenceKey(value domain.IssueReference) string {
	return string(value.Provider) + "\x00" + value.ProjectID + "\x00" + fmt.Sprint(value.Number)
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[strings.TrimSpace(value)] = struct{}{}
	}
	return result
}
