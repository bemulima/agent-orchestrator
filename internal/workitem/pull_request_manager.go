package workitem

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

//go:embed schema/pull-request-manager-result.schema.json
var pullRequestManagerSchemaJSON []byte

type planTaskGetter interface {
	GetPlan(context.Context, string) (domain.PlanBundle, error)
	GetTask(context.Context, string) (domain.Task, error)
}

type attemptLister interface {
	ListAttempts(context.Context, string) ([]domain.TaskAttempt, error)
}

type PullRequestManager struct {
	Plans      planTaskGetter
	Projects   repository.ProjectRepository
	Items      repository.WorkItemRepository
	Executions attemptLister
	Gateway    repository.WorkItemGateway
	Runner     repository.AgentRunner
	Models     map[string]string
	Reasoning  map[string]string
}

func (m PullRequestManager) Prepare(ctx context.Context, taskID string) (domain.WorkItem, error) {
	if m.Plans == nil || m.Projects == nil || m.Items == nil || m.Executions == nil ||
		m.Gateway == nil || m.Runner == nil {
		return domain.WorkItem{}, fmt.Errorf("pull-request manager is incomplete: %w", domain.ErrInvalidStatus)
	}
	task, bundle, project, attempt, issue, metadata, err := m.context(ctx, taskID)
	if err != nil {
		return domain.WorkItem{}, err
	}
	rawContext, err := json.Marshal(map[string]any{
		"plan": bundle.Plan, "task": task, "project": project, "issue": issue,
		"attempt": attempt, "metadata": metadata,
	})
	if err != nil {
		return domain.WorkItem{}, err
	}
	schema, err := pullRequestManagerSchema()
	if err != nil {
		return domain.WorkItem{}, err
	}
	profile := profileForTask(task)
	prompt := `Ты pull-request-manage-agent. Подготовь полноценный draft PR на русском языке для уже реализованной и проверенной задачи.
Ты не изменяешь код, не создаёшь issue и не выполняешь merge/deploy. Верни только JSON по схеме.

Body обязан содержать разделы: ## Связанная issue, ## Что сделано, ## Изменённые компоненты,
## Проверки, ## Контракты и миграции, ## Риски и ограничения, ## Проверка результата.
Используй только фактические данные task/attempt. Укажи labels, milestone, assignees и reviewers.
source_branch и target_branch должны точно совпадать с контекстом. Весь объясняющий текст — на русском.

Контекст:
` + string(rawContext)
	threadID := ""
	response, err := m.Runner.Run(ctx, domain.AgentRunRequest{
		Role: domain.AgentRunPullRequestManager, WorkingDirectory: attempt.WorktreePath,
		Model: m.Models[profile], ReasoningEffort: m.Reasoning[profile], Prompt: prompt, OutputSchema: schema,
	}, func(_ context.Context, value string) error {
		threadID = value
		return nil
	})
	if err != nil {
		return domain.WorkItem{}, fmt.Errorf("run pull-request-manage-agent: %w", err)
	}
	if response.ThreadID != "" {
		threadID = response.ThreadID
	}
	if threadID == "" || response.ThreadID != threadID {
		return domain.WorkItem{}, fmt.Errorf("pull-request-manage-agent thread was not captured: %w", domain.ErrConflict)
	}
	result, err := validatePullRequestManagerResult(response.Result, task, project, attempt, metadata)
	if err != nil {
		return domain.WorkItem{}, err
	}
	return m.Items.SavePullRequestProposal(ctx, bundle, task, threadID, result.PullRequest)
}

func (m PullRequestManager) context(
	ctx context.Context,
	taskID string,
) (domain.Task, domain.PlanBundle, domain.Project, domain.TaskAttempt, domain.WorkItem, repository.ProjectIssueMetadata, error) {
	task, err := m.Plans.GetTask(ctx, strings.TrimSpace(taskID))
	if err != nil {
		return domain.Task{}, domain.PlanBundle{}, domain.Project{}, domain.TaskAttempt{}, domain.WorkItem{}, repository.ProjectIssueMetadata{}, err
	}
	if task.Status != domain.TaskStatusCompleted {
		return domain.Task{}, domain.PlanBundle{}, domain.Project{}, domain.TaskAttempt{}, domain.WorkItem{}, repository.ProjectIssueMetadata{},
			fmt.Errorf("PR requires a completed task: %w", domain.ErrInvalidStatus)
	}
	bundle, err := m.Plans.GetPlan(ctx, task.PlanID)
	if err != nil {
		return domain.Task{}, domain.PlanBundle{}, domain.Project{}, domain.TaskAttempt{}, domain.WorkItem{}, repository.ProjectIssueMetadata{}, err
	}
	if bundle.Plan.ApprovedFingerprint == nil || *bundle.Plan.ApprovedFingerprint != bundle.Plan.Fingerprint {
		return domain.Task{}, domain.PlanBundle{}, domain.Project{}, domain.TaskAttempt{}, domain.WorkItem{}, repository.ProjectIssueMetadata{},
			fmt.Errorf("PR requires the approved plan version: %w", domain.ErrApprovalNeeded)
	}
	var issue domain.WorkItem
	for _, item := range bundle.WorkItems {
		if item.TaskID != nil && *item.TaskID == task.ID && item.Kind == domain.WorkItemIssue &&
			(item.Status == domain.WorkItemPublished || item.Status == domain.WorkItemClosed) {
			issue = item
			break
		}
	}
	if issue.ID == "" {
		return domain.Task{}, domain.PlanBundle{}, domain.Project{}, domain.TaskAttempt{}, domain.WorkItem{}, repository.ProjectIssueMetadata{},
			fmt.Errorf("PR requires a published task issue: %w", domain.ErrApprovalNeeded)
	}
	attempts, err := m.Executions.ListAttempts(ctx, task.ID)
	if err != nil {
		return domain.Task{}, domain.PlanBundle{}, domain.Project{}, domain.TaskAttempt{}, domain.WorkItem{}, repository.ProjectIssueMetadata{}, err
	}
	var attempt domain.TaskAttempt
	for _, candidate := range attempts {
		if candidate.Status == domain.TaskAttemptStatusCompleted && candidate.CommitSHA != nil &&
			candidate.WorktreePath != "" && candidate.BranchName != "" {
			attempt = candidate
			break
		}
	}
	if attempt.ID == "" {
		return domain.Task{}, domain.PlanBundle{}, domain.Project{}, domain.TaskAttempt{}, domain.WorkItem{}, repository.ProjectIssueMetadata{},
			fmt.Errorf("PR requires a verified committed attempt: %w", domain.ErrInvalidStatus)
	}
	project, err := m.Projects.Get(ctx, task.ProjectID)
	if err != nil {
		return domain.Task{}, domain.PlanBundle{}, domain.Project{}, domain.TaskAttempt{}, domain.WorkItem{}, repository.ProjectIssueMetadata{}, err
	}
	metadata, err := m.Gateway.Metadata(ctx, project)
	return task, bundle, project, attempt, issue, metadata, err
}

func pullRequestManagerSchema() (map[string]any, error) {
	var schema map[string]any
	if err := json.Unmarshal(pullRequestManagerSchemaJSON, &schema); err != nil {
		return nil, fmt.Errorf("decode embedded PR-manager schema: %w", err)
	}
	return schema, nil
}

func validatePullRequestManagerResult(
	raw []byte,
	task domain.Task,
	project domain.Project,
	attempt domain.TaskAttempt,
	metadata repository.ProjectIssueMetadata,
) (domain.PullRequestManagerResult, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var result domain.PullRequestManagerResult
	if err := decoder.Decode(&result); err != nil {
		return domain.PullRequestManagerResult{}, fmt.Errorf("decode PR-manager result: %w", domain.ErrValidation)
	}
	draft := &result.PullRequest
	targetBranch := strings.TrimSpace(project.DefaultBranch)
	if targetBranch == "" {
		targetBranch = "main"
	}
	draft.Complexity, draft.ModelProfile = complexityForTask(task), profileForTask(task)
	if !hasCyrillic(result.Summary) || draft.TaskID != task.ID || draft.ProjectID != task.ProjectID ||
		draft.SourceBranch != attempt.BranchName || draft.TargetBranch != targetBranch ||
		!hasCyrillic(draft.Title) || !hasCyrillic(draft.Body) || len([]rune(draft.Title)) < 10 ||
		len([]rune(draft.Body)) < 100 || !hasRequiredPullRequestSections(draft.Body) ||
		len(draft.Labels) == 0 || draft.Milestone == "" || len(draft.Assignees) == 0 || len(draft.Reviewers) == 0 {
		return domain.PullRequestManagerResult{}, fmt.Errorf("PR proposal is incomplete or not Russian: %w", domain.ErrValidation)
	}
	if err := requireNames(draft.Assignees, metadata.Assignees, "assignee"); err != nil {
		return domain.PullRequestManagerResult{}, err
	}
	if err := requireNames(draft.Reviewers, metadata.Reviewers, "reviewer"); err != nil {
		return domain.PullRequestManagerResult{}, err
	}
	return result, nil
}

func hasRequiredPullRequestSections(body string) bool {
	for _, section := range []string{
		"## Связанная issue", "## Что сделано", "## Изменённые компоненты", "## Проверки",
		"## Контракты и миграции", "## Риски и ограничения", "## Проверка результата",
	} {
		if !strings.Contains(body, section) {
			return false
		}
	}
	return true
}

func requireNames(requested, available []string, kind string) error {
	known := stringSet(available)
	for _, value := range requested {
		if _, ok := known[value]; !ok {
			return fmt.Errorf("%s %q is unavailable: %w", kind, value, domain.ErrValidation)
		}
	}
	return nil
}

type PullRequestPublisher struct {
	Manager PullRequestManager
}

func (p PullRequestPublisher) Publish(ctx context.Context, workItemID string) (domain.WorkItem, error) {
	item, err := p.Manager.Items.GetWorkItem(ctx, strings.TrimSpace(workItemID))
	if err != nil {
		return domain.WorkItem{}, err
	}
	if item.Kind != domain.WorkItemPullRequest || item.Status != domain.WorkItemProposed || item.TaskID == nil ||
		item.AgentRole != domain.AgentRunPullRequestManager {
		return domain.WorkItem{}, fmt.Errorf("invalid PR proposal: %w", domain.ErrInvalidStatus)
	}
	task, bundle, project, attempt, _, _, err := p.Manager.context(ctx, *item.TaskID)
	if err != nil {
		return domain.WorkItem{}, err
	}
	if item.PlanFingerprint != bundle.Plan.Fingerprint || task.ID != *item.TaskID || item.SourceBranch != attempt.BranchName {
		return domain.WorkItem{}, fmt.Errorf("stale PR proposal: %w", domain.ErrConflict)
	}
	if err := p.Manager.Gateway.PublishBranch(ctx, project, attempt.WorktreePath, attempt.BranchName); err != nil {
		return domain.WorkItem{}, err
	}
	publication, err := p.Manager.Gateway.PublishPullRequest(ctx, project, item)
	if err != nil {
		return domain.WorkItem{}, err
	}
	if p.Manager.Gateway.DryRun() {
		return item, nil
	}
	return p.Manager.Items.MarkWorkItemPublished(ctx, item.ID, publication)
}
