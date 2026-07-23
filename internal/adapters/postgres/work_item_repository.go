package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type WorkItemRepoPG struct {
	Pool *pgxpool.Pool
}

func (r WorkItemRepoPG) SaveIssueProposals(
	ctx context.Context,
	bundle domain.PlanBundle,
	threadID string,
	drafts []domain.IssueDraft,
) ([]domain.WorkItem, error) {
	if bundle.Plan.ID == "" || bundle.Plan.Fingerprint == "" || strings.TrimSpace(threadID) == "" ||
		bundle.Plan.Status != domain.PlanStatusDiscussion {
		return nil, fmt.Errorf("issue proposals require a discussion plan and agent thread: %w", domain.ErrInvalidStatus)
	}
	tasks := make(map[string]domain.Task, len(bundle.Tasks))
	for _, task := range bundle.Tasks {
		tasks[task.PlannerKey] = task
	}
	if len(drafts) != len(tasks) {
		return nil, fmt.Errorf("every plan task requires exactly one issue proposal: %w", domain.ErrValidation)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin issue proposal transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentFingerprint string
	var currentStatus domain.PlanStatus
	if err := tx.QueryRow(ctx, `SELECT fingerprint, status FROM plan WHERE id = $1 FOR UPDATE`, bundle.Plan.ID).
		Scan(&currentFingerprint, &currentStatus); err != nil {
		return nil, mapPlanningError(err)
	}
	if currentStatus != domain.PlanStatusDiscussion || currentFingerprint != bundle.Plan.Fingerprint {
		return nil, fmt.Errorf("issue proposals target a stale plan version: %w", domain.ErrConflict)
	}
	var existingCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM work_item WHERE plan_id = $1`, bundle.Plan.ID).Scan(&existingCount); err != nil {
		return nil, err
	}
	if existingCount != 0 {
		return nil, fmt.Errorf("plan issue proposals are immutable; create a new plan version: %w", domain.ErrConflict)
	}
	canonicalDrafts := append([]domain.IssueDraft(nil), drafts...)
	for index := range canonicalDrafts {
		canonicalDrafts[index].Title = strings.TrimSpace(canonicalDrafts[index].Title)
		canonicalDrafts[index].Body = strings.TrimSpace(canonicalDrafts[index].Body)
		canonicalDrafts[index].Milestone = strings.TrimSpace(canonicalDrafts[index].Milestone)
		canonicalDrafts[index].Labels = normalizedStrings(canonicalDrafts[index].Labels)
		canonicalDrafts[index].Assignees = normalizedStrings(canonicalDrafts[index].Assignees)
	}
	sort.Slice(canonicalDrafts, func(i, j int) bool { return canonicalDrafts[i].TaskKey < canonicalDrafts[j].TaskKey })
	canonicalJSON, err := json.Marshal(canonicalDrafts)
	if err != nil {
		return nil, fmt.Errorf("encode canonical issue proposals: %w", err)
	}
	approvalFingerprint := planningChecksum(append(append([]byte(bundle.Plan.Fingerprint), 0), canonicalJSON...))
	if _, err := tx.Exec(ctx, `
UPDATE plan SET fingerprint = $2, discussion_revision = discussion_revision + 1, updated_at = now()
WHERE id = $1`, bundle.Plan.ID, approvalFingerprint); err != nil {
		return nil, fmt.Errorf("bind issue proposals to plan fingerprint: %w", err)
	}
	bundle.Plan.Fingerprint = approvalFingerprint
	bundle.Plan.DiscussionRevision++
	for _, draft := range canonicalDrafts {
		task, ok := tasks[draft.TaskKey]
		if !ok || task.ProjectID != draft.ProjectID {
			return nil, fmt.Errorf("issue proposal does not match a plan task: %w", domain.ErrValidation)
		}
		labels, _ := json.Marshal(draft.Labels)
		assignees, _ := json.Marshal(draft.Assignees)
		status := domain.WorkItemProposed
		var number *int64
		url := ""
		provider := domain.IssueProviderGitHub
		if draft.Existing != nil {
			status, number, url, provider = domain.WorkItemPublished, &draft.Existing.Number,
				strings.TrimSpace(draft.Existing.URL), draft.Existing.Provider
			if url == "" {
				return nil, fmt.Errorf("existing issue URL is required: %w", domain.ErrValidation)
			}
		}
		key := bundle.Plan.ID + ":issue:" + task.ID
		_, err = tx.Exec(ctx, `
INSERT INTO work_item (
    plan_id, task_id, project_id, kind, provider, issue_type, status,
    title, body, labels, milestone, assignees, agent_role, agent_thread_id,
    complexity, model_profile, plan_fingerprint, idempotency_key,
    external_number, external_url, published_at
) VALUES (
    $1, $2, $3, 'issue', $4, $5, $6, $7, $8, $9, $10, $11,
    'issue-manager', $12, $13, $14, $15, $16, $17, $18,
    CASE WHEN $6::varchar = 'published' THEN now() ELSE NULL END
)
ON CONFLICT (idempotency_key) DO NOTHING`, bundle.Plan.ID, task.ID, task.ProjectID,
			provider, draft.IssueType, status, strings.TrimSpace(draft.Title), strings.TrimSpace(draft.Body),
			labels, strings.TrimSpace(draft.Milestone), assignees, threadID, draft.Complexity,
			draft.ModelProfile, bundle.Plan.Fingerprint, key, number, url)
		if err != nil {
			return nil, mapPlanningError(err)
		}
		delete(tasks, draft.TaskKey)
	}
	if len(tasks) != 0 {
		return nil, fmt.Errorf("one or more plan tasks have no issue proposal: %w", domain.ErrValidation)
	}
	if err := insertResourceAuditTx(ctx, tx, "plan", "plan.issues_proposed", bundle.Plan.ID, map[string]any{
		"count": len(drafts), "agent_thread_id": threadID,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit issue proposals: %w", err)
	}
	return r.ListPlanWorkItems(ctx, bundle.Plan.ID)
}

func normalizedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (r WorkItemRepoPG) SavePullRequestProposal(
	ctx context.Context,
	bundle domain.PlanBundle,
	task domain.Task,
	threadID string,
	draft domain.PullRequestDraft,
) (domain.WorkItem, error) {
	if bundle.Plan.ID == "" || task.PlanID != bundle.Plan.ID || task.Status != domain.TaskStatusCompleted ||
		draft.TaskID != task.ID || draft.ProjectID != task.ProjectID || strings.TrimSpace(threadID) == "" {
		return domain.WorkItem{}, fmt.Errorf("pull-request proposal requires one completed plan task: %w", domain.ErrInvalidStatus)
	}
	labels, _ := json.Marshal(draft.Labels)
	assignees, _ := json.Marshal(draft.Assignees)
	reviewers, _ := json.Marshal(draft.Reviewers)
	key := bundle.Plan.ID + ":pull-request:" + task.ID
	row := r.Pool.QueryRow(ctx, `
INSERT INTO work_item (
    plan_id, task_id, project_id, kind, provider, status, title, body,
    labels, milestone, assignees, reviewers, source_branch, target_branch,
    agent_role, agent_thread_id, complexity, model_profile, plan_fingerprint,
    idempotency_key
) VALUES (
    $1, $2, $3, 'pull_request', 'github', 'proposed', $4, $5,
    $6, $7, $8, $9, $10, $11, 'pull-request-manager', $12, $13, $14, $15, $16
)
ON CONFLICT (idempotency_key) DO UPDATE SET updated_at = work_item.updated_at
RETURNING `+workItemColumns, bundle.Plan.ID, task.ID, task.ProjectID,
		strings.TrimSpace(draft.Title), strings.TrimSpace(draft.Body), labels,
		strings.TrimSpace(draft.Milestone), assignees, reviewers,
		strings.TrimSpace(draft.SourceBranch), strings.TrimSpace(draft.TargetBranch),
		threadID, draft.Complexity, draft.ModelProfile, bundle.Plan.Fingerprint, key)
	item, err := scanWorkItem(row)
	return item, mapPlanningError(err)
}

func (r WorkItemRepoPG) ListPlanWorkItems(ctx context.Context, planID string) ([]domain.WorkItem, error) {
	rows, err := r.Pool.Query(ctx, `SELECT `+workItemColumns+` FROM work_item
WHERE plan_id = $1 ORDER BY kind, task_id, id`, planID)
	if err != nil {
		return nil, fmt.Errorf("list plan work items: %w", err)
	}
	defer rows.Close()
	items := make([]domain.WorkItem, 0)
	for rows.Next() {
		item, scanErr := scanWorkItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r WorkItemRepoPG) GetWorkItem(ctx context.Context, id string) (domain.WorkItem, error) {
	item, err := scanWorkItem(r.Pool.QueryRow(ctx, `SELECT `+workItemColumns+` FROM work_item WHERE id = $1`, id))
	return item, mapPlanningError(err)
}

func (r WorkItemRepoPG) MarkWorkItemPublished(
	ctx context.Context,
	id string,
	publication domain.WorkItemPublication,
) (domain.WorkItem, error) {
	if publication.Number < 1 || strings.TrimSpace(publication.URL) == "" {
		return domain.WorkItem{}, fmt.Errorf("external work-item identity is required: %w", domain.ErrValidation)
	}
	item, err := scanWorkItem(r.Pool.QueryRow(ctx, `
UPDATE work_item SET status = 'published', external_number = $2,
    external_url = $3, published_at = COALESCE(published_at, now()), updated_at = now()
WHERE id = $1 AND status IN ('proposed', 'published')
RETURNING `+workItemColumns, id, publication.Number, strings.TrimSpace(publication.URL)))
	return item, mapPlanningError(err)
}

const workItemColumns = `id, plan_id, task_id, project_id, kind, provider,
issue_type, status, title, body, labels, milestone, assignees, reviewers,
source_branch, target_branch, external_number, external_url, agent_role,
agent_thread_id, complexity, model_profile, plan_fingerprint, idempotency_key,
created_at, updated_at, published_at`

func scanWorkItem(row rowScanner) (domain.WorkItem, error) {
	var value domain.WorkItem
	var labels, assignees, reviewers []byte
	err := row.Scan(
		&value.ID, &value.PlanID, &value.TaskID, &value.ProjectID, &value.Kind,
		&value.Provider, &value.IssueType, &value.Status, &value.Title, &value.Body,
		&labels, &value.Milestone, &assignees, &reviewers, &value.SourceBranch,
		&value.TargetBranch, &value.ExternalNumber, &value.ExternalURL,
		&value.AgentRole, &value.AgentThreadID, &value.Complexity, &value.ModelProfile,
		&value.PlanFingerprint, &value.IdempotencyKey, &value.CreatedAt,
		&value.UpdatedAt, &value.PublishedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.WorkItem{}, domain.ErrNotFound
		}
		return domain.WorkItem{}, err
	}
	if err := json.Unmarshal(labels, &value.Labels); err != nil {
		return domain.WorkItem{}, fmt.Errorf("decode work-item labels: %w", err)
	}
	if err := json.Unmarshal(assignees, &value.Assignees); err != nil {
		return domain.WorkItem{}, fmt.Errorf("decode work-item assignees: %w", err)
	}
	if err := json.Unmarshal(reviewers, &value.Reviewers); err != nil {
		return domain.WorkItem{}, fmt.Errorf("decode work-item reviewers: %w", err)
	}
	return value, nil
}
