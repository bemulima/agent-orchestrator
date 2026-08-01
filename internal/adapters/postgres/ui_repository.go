package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type UIReadRepoPG struct {
	Pool *pgxpool.Pool
}

func (r UIReadRepoPG) Dashboard(ctx context.Context) (domain.Dashboard, error) {
	result := domain.Dashboard{GeneratedAt: time.Now().UTC(), Attention: []domain.AttentionItem{}, ActiveRuns: []domain.RunSummary{}, RecentActivity: []domain.ActivityEvent{}}
	if err := r.Pool.QueryRow(ctx, `
SELECT
  (SELECT count(*) FROM project),
  (SELECT count(*) FROM plan WHERE status IN ('running', 'paused', 'awaiting_approval')),
  (SELECT count(*) FROM task WHERE status IN ('ready', 'running', 'verification')),
  (SELECT count(*) FROM plan WHERE status IN ('paused', 'failed', 'awaiting_approval')) +
  (SELECT count(*) FROM task WHERE status IN ('blocked', 'changes_requested', 'failed')) +
  (SELECT count(*) FROM onboarding_run WHERE status IN ('awaiting_approval', 'changes_requested', 'failed'))
`).Scan(&result.Counts.Projects, &result.Counts.ActivePlans, &result.Counts.ActiveTasks, &result.Counts.AttentionRequired); err != nil {
		return domain.Dashboard{}, fmt.Errorf("query dashboard counts: %w", err)
	}

	rows, err := r.Pool.Query(ctx, `
SELECT resource_type, resource_id, title, status, reason, updated_at FROM (
  SELECT 'plan'::text resource_type, p.id resource_id, p.summary title, p.status::text status,
    CASE p.status WHEN 'awaiting_approval' THEN 'Требуется согласование плана'
      WHEN 'paused' THEN 'Выполнение плана приостановлено'
      ELSE 'План завершился ошибкой' END reason,
    p.updated_at
  FROM plan p WHERE p.status IN ('paused', 'failed', 'awaiting_approval')
  UNION ALL
  SELECT 'task', t.id, t.title, t.status::text,
    CASE t.status WHEN 'blocked' THEN 'Задача заблокирована'
      WHEN 'changes_requested' THEN 'Reviewer запросил изменения'
      ELSE 'Задача завершилась ошибкой' END,
    COALESCE(t.completed_at, t.started_at, t.created_at)
  FROM task t WHERE t.status IN ('blocked', 'changes_requested', 'failed')
  UNION ALL
  SELECT 'onboarding', o.id, pr.name, o.status::text,
    CASE o.status WHEN 'awaiting_approval' THEN 'Требуется согласование onboarding'
      WHEN 'changes_requested' THEN 'Запрошены изменения onboarding'
      ELSE 'Onboarding завершился ошибкой' END,
    o.updated_at
  FROM onboarding_run o JOIN project pr ON pr.id = o.project_id
  WHERE o.status IN ('awaiting_approval', 'changes_requested', 'failed')
) attention
ORDER BY updated_at DESC, resource_id DESC
LIMIT 12`)
	if err != nil {
		return domain.Dashboard{}, fmt.Errorf("query dashboard attention: %w", err)
	}
	for rows.Next() {
		var item domain.AttentionItem
		if err := rows.Scan(&item.ResourceType, &item.ResourceID, &item.Title, &item.Status, &item.Reason, &item.UpdatedAt); err != nil {
			rows.Close()
			return domain.Dashboard{}, fmt.Errorf("scan dashboard attention: %w", err)
		}
		result.Attention = append(result.Attention, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.Dashboard{}, fmt.Errorf("iterate dashboard attention: %w", err)
	}
	rows.Close()

	runs, _, err := r.ListRuns(ctx, domain.PageQuery{Limit: 6, Statuses: []string{"running", "paused"}})
	if err != nil {
		return domain.Dashboard{}, err
	}
	result.ActiveRuns = runs
	activity, _, err := r.ListActivity(ctx, domain.PageQuery{Limit: 10})
	if err != nil {
		return domain.Dashboard{}, err
	}
	result.RecentActivity = activity
	return result, nil
}

func (r UIReadRepoPG) ListPlans(ctx context.Context, query domain.PageQuery) ([]domain.PlanSummary, bool, error) {
	limit := boundedLimit(query.Limit)
	statuses := nullableStrings(query.Statuses)
	beforeAt, beforeID := cursorValues(query.Cursor)
	rows, err := r.Pool.Query(ctx, `
SELECT p.id, p.command_id, p.summary, p.status, p.version, p.risk_level,
       p.source_kind, p.fingerprint, p.approved_fingerprint,
       count(DISTINCT t.id)::int,
       count(DISTINCT t.id) FILTER (WHERE t.status = 'completed')::int,
       count(DISTINCT t.id) FILTER (WHERE t.status IN ('blocked', 'changes_requested', 'failed'))::int,
       count(DISTINCT wi.id) FILTER (WHERE wi.kind = 'issue' AND wi.status <> 'cancelled')::int,
       count(DISTINCT wi.id) FILTER (WHERE wi.kind = 'issue' AND wi.status IN ('published', 'closed'))::int,
       CASE
         WHEN count(DISTINCT wi.id) FILTER (WHERE wi.kind = 'issue' AND wi.status <> 'cancelled') = 0 THEN 'none'
         WHEN count(DISTINCT wi.id) FILTER (WHERE wi.kind = 'issue' AND wi.status IN ('published', 'closed')) = 0 THEN 'draft'
         WHEN bool_and(wi.external_url LIKE 'https://github.example.test/%')
              FILTER (WHERE wi.kind = 'issue' AND wi.status IN ('published', 'closed')) THEN 'simulation'
         ELSE 'external'
       END,
       pr.id, pr.status, pr.error, p.supersedes_plan_id, successor.id, p.updated_at
FROM plan p
LEFT JOIN task t ON t.plan_id = p.id
LEFT JOIN work_item wi ON wi.plan_id = p.id
LEFT JOIN plan_run pr ON pr.plan_id = p.id
LEFT JOIN plan successor ON successor.supersedes_plan_id = p.id
WHERE ($1::text[] IS NULL OR p.status = ANY($1))
  AND ($2::timestamptz IS NULL OR (p.updated_at, p.id) < ($2, $3::uuid))
GROUP BY p.id, pr.id, successor.id
ORDER BY p.updated_at DESC, p.id DESC
LIMIT $4`, statuses, beforeAt, beforeID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list plans: %w", err)
	}
	defer rows.Close()
	items := make([]domain.PlanSummary, 0, limit+1)
	for rows.Next() {
		var item domain.PlanSummary
		if err := rows.Scan(&item.ID, &item.CommandID, &item.Summary, &item.Status, &item.Version, &item.RiskLevel,
			&item.SourceKind, &item.Fingerprint, &item.ApprovedFingerprint, &item.TaskCount, &item.CompletedTasks,
			&item.AttentionTasks, &item.IssueCount, &item.PublishedIssues, &item.IssuePublication,
			&item.RunID, &item.RunStatus, &item.RunError, &item.SupersedesPlanID, &item.SupersededByPlanID,
			&item.UpdatedAt); err != nil {
			return nil, false, fmt.Errorf("scan plan summary: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate plan summaries: %w", err)
	}
	return trimPage(items, limit)
}

func (r UIReadRepoPG) ListRuns(ctx context.Context, query domain.PageQuery) ([]domain.RunSummary, bool, error) {
	limit := boundedLimit(query.Limit)
	statuses := nullableStrings(query.Statuses)
	beforeAt, beforeID := cursorValues(query.Cursor)
	rows, err := r.Pool.Query(ctx, `
SELECT r.id, r.plan_id, p.summary, r.status, r.workflow_id, r.max_parallel_tasks,
       count(t.id)::int,
       count(t.id) FILTER (WHERE t.status = 'completed')::int,
       count(t.id) FILTER (WHERE t.status IN ('ready', 'running', 'verification'))::int,
       r.error, r.created_at, r.started_at, r.completed_at, r.updated_at
FROM plan_run r
JOIN plan p ON p.id = r.plan_id
LEFT JOIN task t ON t.plan_id = p.id
WHERE ($1::text[] IS NULL OR r.status = ANY($1))
  AND ($2::uuid IS NULL OR r.plan_id = $2)
  AND ($3::timestamptz IS NULL OR (r.updated_at, r.id) < ($3, $4::uuid))
GROUP BY r.id, p.id
ORDER BY r.updated_at DESC, r.id DESC
LIMIT $5`, statuses, nullUUID(query.PlanID), beforeAt, beforeID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	items := make([]domain.RunSummary, 0, limit+1)
	for rows.Next() {
		var item domain.RunSummary
		if err := rows.Scan(&item.ID, &item.PlanID, &item.PlanSummary, &item.Status, &item.WorkflowID,
			&item.MaxParallelTasks, &item.TaskCount, &item.CompletedTasks, &item.ActiveTasks, &item.Error,
			&item.CreatedAt, &item.StartedAt, &item.CompletedAt, &item.UpdatedAt); err != nil {
			return nil, false, fmt.Errorf("scan run summary: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate run summaries: %w", err)
	}
	return trimPage(items, limit)
}

func (r UIReadRepoPG) ListTasks(ctx context.Context, query domain.PageQuery) ([]domain.TaskSummary, bool, error) {
	limit := boundedLimit(query.Limit)
	statuses := nullableStrings(query.Statuses)
	beforeAt, beforeID := cursorValues(query.Cursor)
	rows, err := r.Pool.Query(ctx, `
SELECT t.id, t.plan_id, t.project_id, pr.name, p.summary, t.title, t.status,
       t.risk_level, t.priority, t.depth,
       count(ta.id)::int,
       (array_agg(ta.status ORDER BY ta.attempt_number DESC) FILTER (WHERE ta.id IS NOT NULL))[1],
       t.created_at, t.started_at, t.completed_at,
       COALESCE(t.completed_at, t.started_at, t.created_at) updated_at
FROM task t
JOIN project pr ON pr.id = t.project_id
JOIN plan p ON p.id = t.plan_id
LEFT JOIN task_attempt ta ON ta.task_id = t.id
WHERE ($1::text[] IS NULL OR t.status = ANY($1))
  AND ($2::uuid IS NULL OR t.project_id = $2)
  AND ($3::uuid IS NULL OR t.plan_id = $3)
  AND ($4::timestamptz IS NULL OR (COALESCE(t.completed_at, t.started_at, t.created_at), t.id) < ($4, $5::uuid))
GROUP BY t.id, pr.id, p.id
ORDER BY updated_at DESC, t.id DESC
LIMIT $6`, statuses, nullUUID(query.ProjectID), nullUUID(query.PlanID), beforeAt, beforeID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	items := make([]domain.TaskSummary, 0, limit+1)
	for rows.Next() {
		var item domain.TaskSummary
		if err := rows.Scan(&item.ID, &item.PlanID, &item.ProjectID, &item.ProjectName, &item.PlanSummary,
			&item.Title, &item.Status, &item.RiskLevel, &item.Priority, &item.Depth, &item.AttemptCount,
			&item.LastAttempt, &item.CreatedAt, &item.StartedAt, &item.CompletedAt, &item.UpdatedAt); err != nil {
			return nil, false, fmt.Errorf("scan task summary: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate task summaries: %w", err)
	}
	return trimPage(items, limit)
}

func (r UIReadRepoPG) ListApprovals(ctx context.Context, query domain.PageQuery) ([]domain.ApprovalSummary, bool, error) {
	limit := boundedLimit(query.Limit)
	statuses := nullableStrings(query.Statuses)
	beforeAt, beforeID := cursorValues(query.Cursor)
	rows, err := r.Pool.Query(ctx, `
SELECT a.id, a.resource_type, a.resource_id,
       COALESCE(p.summary, pr.name, a.resource_id::text),
       a.action, a.status, COALESCE(p.fingerprint, o.proposal_checksum, ''),
       COALESCE(p.risk_level, ''), a.requested_at, a.decided_at
FROM approval a
LEFT JOIN plan p ON a.resource_type = 'plan' AND p.id = a.resource_id
LEFT JOIN onboarding_run o ON a.resource_type = 'onboarding' AND o.id = a.resource_id
LEFT JOIN project pr ON pr.id = o.project_id
WHERE ($1::text[] IS NULL OR a.status = ANY($1))
  AND ($2::timestamptz IS NULL OR (a.requested_at, a.id) < ($2, $3::uuid))
ORDER BY a.requested_at DESC, a.id DESC
LIMIT $4`, statuses, beforeAt, beforeID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list approvals: %w", err)
	}
	defer rows.Close()
	items := make([]domain.ApprovalSummary, 0, limit+1)
	for rows.Next() {
		var item domain.ApprovalSummary
		if err := rows.Scan(&item.ID, &item.ResourceType, &item.ResourceID, &item.ResourceName,
			&item.Action, &item.Status, &item.Fingerprint, &item.RiskLevel, &item.RequestedAt, &item.DecidedAt); err != nil {
			return nil, false, fmt.Errorf("scan approval summary: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate approval summaries: %w", err)
	}
	return trimPage(items, limit)
}

func (r UIReadRepoPG) ListActivity(ctx context.Context, query domain.PageQuery) ([]domain.ActivityEvent, bool, error) {
	limit := boundedLimit(query.Limit)
	beforeAt, beforeID := cursorValues(query.Cursor)
	rows, err := r.Pool.Query(ctx, `
SELECT id, actor_type, actor_id, action, resource_type, resource_id, payload, created_at
FROM audit_event
WHERE ($1::timestamptz IS NULL OR (created_at, id) < ($1, $2::uuid))
ORDER BY created_at DESC, id DESC
LIMIT $3`, beforeAt, beforeID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list activity: %w", err)
	}
	defer rows.Close()
	items := make([]domain.ActivityEvent, 0, limit+1)
	for rows.Next() {
		var item domain.ActivityEvent
		var payload []byte
		if err := rows.Scan(&item.ID, &item.ActorType, &item.ActorID, &item.Action, &item.ResourceType,
			&item.ResourceID, &payload, &item.CreatedAt); err != nil {
			return nil, false, fmt.Errorf("scan activity: %w", err)
		}
		if err := json.Unmarshal(payload, &item.Payload); err != nil {
			return nil, false, fmt.Errorf("decode activity payload: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate activity: %w", err)
	}
	return trimPage(items, limit)
}

func boundedLimit(value int) int {
	if value <= 0 {
		return 25
	}
	if value > 100 {
		return 100
	}
	return value
}

func nullableStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return values
}

func nullUUID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func cursorValues(cursor *domain.PageCursor) (any, any) {
	if cursor == nil {
		return nil, nil
	}
	return cursor.At, cursor.ID
}

func trimPage[T any](items []T, limit int) ([]T, bool, error) {
	if len(items) <= limit {
		return items, false, nil
	}
	return items[:limit], true, nil
}
