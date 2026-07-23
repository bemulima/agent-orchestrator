package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type TaskExecutionRepoPG struct {
	Pool *pgxpool.Pool
}

func (r TaskExecutionRepoPG) GetExecutionContext(
	ctx context.Context,
	taskID string,
) (domain.TaskExecutionContext, error) {
	var result domain.TaskExecutionContext
	var err error
	result.Task, err = scanTask(r.Pool.QueryRow(ctx, `SELECT `+taskColumns+` FROM task WHERE id = $1`, taskID))
	if err != nil {
		return domain.TaskExecutionContext{}, mapPlanningError(err)
	}
	result.Project, err = scanProject(r.Pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM project WHERE id = $1`, result.Task.ProjectID))
	if err != nil {
		return domain.TaskExecutionContext{}, mapPlanningError(err)
	}
	err = r.Pool.QueryRow(ctx, `SELECT `+planColumns+` FROM plan WHERE id = $1`, result.Task.PlanID).Scan(planScanTargets(&result.Plan)...)
	if err != nil {
		return domain.TaskExecutionContext{}, mapPlanningError(err)
	}
	result.Command, err = scanCommand(r.Pool.QueryRow(ctx, `SELECT `+commandColumns+` FROM command WHERE id = $1`, result.Plan.CommandID))
	if err != nil {
		return domain.TaskExecutionContext{}, mapPlanningError(err)
	}
	result.Topology, err = (TopologyRepoPG{Pool: r.Pool}).Get(ctx)
	if err != nil {
		return domain.TaskExecutionContext{}, fmt.Errorf("load task execution topology: %w", err)
	}
	result.ConnectedProjects, err = r.listConnectedProjectKnowledge(ctx)
	if err != nil {
		return domain.TaskExecutionContext{}, err
	}
	rows, err := r.Pool.Query(ctx, `
SELECT `+prefixedTaskColumns("prerequisite")+`
FROM task_dependency dependency
JOIN task prerequisite ON prerequisite.id = dependency.depends_on_task_id
WHERE dependency.task_id = $1
ORDER BY prerequisite.id`, taskID)
	if err != nil {
		return domain.TaskExecutionContext{}, fmt.Errorf("list task execution dependencies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		dependencyTask, scanErr := scanTask(rows)
		if scanErr != nil {
			return domain.TaskExecutionContext{}, fmt.Errorf("scan task execution dependency: %w", scanErr)
		}
		dependency := domain.TaskDependencyRef{Task: dependencyTask}
		attempts, listErr := r.ListAttempts(ctx, dependencyTask.ID)
		if listErr != nil {
			return domain.TaskExecutionContext{}, listErr
		}
		for index := len(attempts) - 1; index >= 0; index-- {
			if attempts[index].Status == domain.TaskAttemptStatusCompleted {
				copy := attempts[index]
				dependency.Attempt = &copy
				break
			}
		}
		dependency.Artifacts, listErr = r.ListArtifacts(ctx, dependencyTask.ID)
		if listErr != nil {
			return domain.TaskExecutionContext{}, listErr
		}
		result.Dependencies = append(result.Dependencies, dependency)
	}
	if err := rows.Err(); err != nil {
		return domain.TaskExecutionContext{}, fmt.Errorf("iterate task execution dependencies: %w", err)
	}
	return result, nil
}

func (r TaskExecutionRepoPG) listConnectedProjectKnowledge(
	ctx context.Context,
) ([]domain.ConnectedProjectKnowledge, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT project.id, project.name, project.repository_role,
       COALESCE(snapshot.service_kind, 'unknown'),
       COALESCE(snapshot.language, ''), COALESCE(snapshot.framework, ''),
       COALESCE(snapshot.purpose, ''), COALESCE(snapshot.raw_report, '{}'::jsonb)
FROM project
LEFT JOIN LATERAL (
    SELECT service_kind, language, framework, purpose, raw_report
    FROM service_snapshot
    WHERE project_id = project.id
    ORDER BY version DESC
    LIMIT 1
) snapshot ON true
WHERE project.status = 'analyzed'
ORDER BY project.name, project.id`)
	if err != nil {
		return nil, fmt.Errorf("list connected project knowledge: %w", err)
	}
	defer rows.Close()
	result := make([]domain.ConnectedProjectKnowledge, 0)
	for rows.Next() {
		var value domain.ConnectedProjectKnowledge
		var rawReport []byte
		if err := rows.Scan(
			&value.ProjectID, &value.Name, &value.RepositoryRole, &value.ServiceKind,
			&value.Language, &value.Framework, &value.Purpose, &rawReport,
		); err != nil {
			return nil, fmt.Errorf("scan connected project knowledge: %w", err)
		}
		if isKnowledgeRepositoryRole(value.RepositoryRole) {
			var report domain.DiscoveryReport
			if err := json.Unmarshal(rawReport, &report); err != nil {
				return nil, fmt.Errorf("decode discovery report for %s: %w", value.Name, err)
			}
			value.Evidence = report.Facts
			value.Conflicts = report.Conflicts
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate connected project knowledge: %w", err)
	}
	return result, nil
}

func isKnowledgeRepositoryRole(role domain.RepositoryRole) bool {
	switch role {
	case domain.RepositoryRoleContent, domain.RepositoryRolePolicy,
		domain.RepositoryRoleDocumentation, domain.RepositoryRoleArchive:
		return true
	default:
		return false
	}
}

func (r TaskExecutionRepoPG) BeginAttempt(
	ctx context.Context,
	taskID, workflowID string,
	workspace domain.TaskWorkspace,
	maxAttempts int,
) (domain.TaskAttempt, error) {
	if taskID == "" || strings.TrimSpace(workflowID) == "" || workspace.Path == "" || workspace.BranchName == "" ||
		maxAttempts < 1 || maxAttempts > 3 {
		return domain.TaskAttempt{}, fmt.Errorf("invalid task attempt request: %w", domain.ErrValidation)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.TaskAttempt{}, fmt.Errorf("begin task attempt: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "task-attempt:"+taskID); err != nil {
		return domain.TaskAttempt{}, fmt.Errorf("lock task attempt: %w", err)
	}
	existing, err := scanTaskAttempt(tx.QueryRow(ctx, `
SELECT `+taskAttemptColumns+` FROM task_attempt WHERE workflow_id = $1`, workflowID))
	if err == nil {
		if existing.TaskID != taskID || existing.WorktreePath != workspace.Path || existing.BranchName != workspace.BranchName {
			return domain.TaskAttempt{}, fmt.Errorf("attempt workflow ID belongs to another execution: %w", domain.ErrConflict)
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.TaskAttempt{}, fmt.Errorf("commit reused task attempt: %w", err)
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.TaskAttempt{}, fmt.Errorf("find existing task attempt: %w", err)
	}
	task, err := scanTask(tx.QueryRow(ctx, `SELECT `+taskColumns+` FROM task WHERE id = $1 FOR UPDATE`, taskID))
	if err != nil {
		return domain.TaskAttempt{}, mapPlanningError(err)
	}
	if task.Status == domain.TaskStatusCompleted || task.Status == domain.TaskStatusCancelled {
		return domain.TaskAttempt{}, fmt.Errorf("task is terminal: %w", domain.ErrInvalidStatus)
	}
	var count int
	var previousThreadID *string
	if err := tx.QueryRow(ctx, `
SELECT count(*), (array_agg(agent_thread_id ORDER BY attempt_number DESC) FILTER (WHERE agent_thread_id IS NOT NULL))[1]
FROM task_attempt WHERE task_id = $1`, taskID).Scan(&count, &previousThreadID); err != nil {
		return domain.TaskAttempt{}, fmt.Errorf("count task attempts: %w", err)
	}
	if count >= maxAttempts {
		return domain.TaskAttempt{}, fmt.Errorf("task reached maximum of %d attempts: %w", maxAttempts, domain.ErrInvalidStatus)
	}
	attempt, err := scanTaskAttempt(tx.QueryRow(ctx, `
INSERT INTO task_attempt (
    task_id, attempt_number, agent_thread_id, workflow_id,
    worktree_path, branch_name, status
) VALUES ($1, $2, $3, $4, $5, $6, 'running')
RETURNING `+taskAttemptColumns,
		taskID, count+1, previousThreadID, workflowID, workspace.Path, workspace.BranchName,
	))
	if err != nil {
		return domain.TaskAttempt{}, mapPlanningError(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE task SET status = 'running', started_at = COALESCE(started_at, now()), completed_at = NULL
WHERE id = $1`, taskID); err != nil {
		return domain.TaskAttempt{}, fmt.Errorf("mark task running: %w", err)
	}
	if err := insertResourceAuditTx(ctx, tx, "task", "task.attempt_started", taskID, map[string]any{
		"attempt_id": attempt.ID, "attempt_number": attempt.AttemptNumber,
	}); err != nil {
		return domain.TaskAttempt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.TaskAttempt{}, fmt.Errorf("commit task attempt: %w", err)
	}
	return attempt, nil
}

func (r TaskExecutionRepoPG) AttachAgentThread(
	ctx context.Context,
	attemptID, threadID string,
) (domain.TaskAttempt, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return domain.TaskAttempt{}, fmt.Errorf("agent thread ID is required: %w", domain.ErrValidation)
	}
	attempt, err := scanTaskAttempt(r.Pool.QueryRow(ctx, `
UPDATE task_attempt SET agent_thread_id = $2, heartbeat_at = now(), updated_at = now()
WHERE id = $1 AND (agent_thread_id IS NULL OR agent_thread_id = $2)
RETURNING `+taskAttemptColumns, attemptID, threadID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.TaskAttempt{}, fmt.Errorf("attempt thread cannot be replaced: %w", domain.ErrConflict)
	}
	return attempt, mapPlanningError(err)
}

func (r TaskExecutionRepoPG) HeartbeatAttempt(ctx context.Context, attemptID string) error {
	command, err := r.Pool.Exec(ctx, `
UPDATE task_attempt SET heartbeat_at = now(), updated_at = now()
WHERE id = $1 AND status NOT IN ('completed', 'failed', 'cancelled')`, attemptID)
	if err != nil {
		return fmt.Errorf("heartbeat task attempt: %w", err)
	}
	if command.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r TaskExecutionRepoPG) SetAttemptStatus(
	ctx context.Context,
	attemptID string,
	status domain.TaskAttemptStatus,
) error {
	taskStatus, ok := taskStatusForAttempt(status)
	if !ok {
		return fmt.Errorf("unsupported attempt status %q: %w", status, domain.ErrValidation)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin attempt status: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var taskID string
	if err := tx.QueryRow(ctx, `
UPDATE task_attempt SET status = $2, heartbeat_at = now(), updated_at = now()
WHERE id = $1 AND status NOT IN ('completed', 'failed', 'cancelled')
RETURNING task_id`, attemptID, status).Scan(&taskID); err != nil {
		return mapPlanningError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE task SET status = $2 WHERE id = $1`, taskID, taskStatus); err != nil {
		return fmt.Errorf("update task from attempt: %w", err)
	}
	return tx.Commit(ctx)
}

func (r TaskExecutionRepoPG) CompleteAttempt(
	ctx context.Context,
	attemptID string,
	result domain.AgentResult,
	verification domain.VerificationReport,
	commitSHA string,
) (domain.TaskAttempt, error) {
	structured, err := json.Marshal(result)
	if err != nil {
		return domain.TaskAttempt{}, fmt.Errorf("marshal agent result: %w", err)
	}
	verified, err := json.Marshal(verification)
	if err != nil {
		return domain.TaskAttempt{}, fmt.Errorf("marshal verification report: %w", err)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.TaskAttempt{}, fmt.Errorf("begin completed attempt: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	attempt, err := scanTaskAttempt(tx.QueryRow(ctx, `
UPDATE task_attempt SET status = 'completed', structured_result = $2,
    verification_result = $3, commit_sha = $4, error = NULL,
    heartbeat_at = now(), finished_at = now(), updated_at = now()
WHERE id = $1 AND status NOT IN ('failed', 'cancelled')
RETURNING `+taskAttemptColumns, attemptID, structured, verified, commitSHA))
	if err != nil {
		return domain.TaskAttempt{}, mapPlanningError(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE task SET status = 'completed', completed_at = now() WHERE id = $1`, attempt.TaskID); err != nil {
		return domain.TaskAttempt{}, fmt.Errorf("complete task: %w", err)
	}
	if err := insertResourceAuditTx(ctx, tx, "task", "task.completed", attempt.TaskID, map[string]any{
		"attempt_id": attempt.ID, "commit_sha": commitSHA,
	}); err != nil {
		return domain.TaskAttempt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.TaskAttempt{}, fmt.Errorf("commit completed attempt: %w", err)
	}
	return attempt, nil
}

func (r TaskExecutionRepoPG) FailAttempt(
	ctx context.Context,
	attemptID string,
	status domain.TaskAttemptStatus,
	errorMessage string,
	structured any,
) error {
	if status != domain.TaskAttemptStatusFailed && status != domain.TaskAttemptStatusBlocked &&
		status != domain.TaskAttemptStatusChangesRequested && status != domain.TaskAttemptStatusCancelled {
		return fmt.Errorf("unsupported failed attempt status %q: %w", status, domain.ErrValidation)
	}
	raw := []byte(`{}`)
	if structured != nil {
		var err error
		raw, err = json.Marshal(structured)
		if err != nil {
			return fmt.Errorf("marshal failed attempt result: %w", err)
		}
	}
	taskStatus, _ := taskStatusForAttempt(status)
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin failed attempt: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var taskID string
	if err := tx.QueryRow(ctx, `
UPDATE task_attempt SET status = $2, structured_result = $3, error = NULLIF($4, ''),
    heartbeat_at = now(), finished_at = CASE WHEN $2 IN ('failed', 'blocked', 'cancelled') THEN now() ELSE finished_at END,
    updated_at = now()
WHERE id = $1 RETURNING task_id`, attemptID, status, raw, strings.TrimSpace(errorMessage)).Scan(&taskID); err != nil {
		return mapPlanningError(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE task SET status = $2,
    completed_at = CASE WHEN $2 IN ('failed', 'cancelled') THEN now() ELSE NULL END
WHERE id = $1`, taskID, taskStatus); err != nil {
		return fmt.Errorf("update failed task: %w", err)
	}
	if err := insertResourceAuditTx(ctx, tx, "task", "task."+string(taskStatus), taskID, map[string]any{
		"attempt_id": attemptID, "error_present": strings.TrimSpace(errorMessage) != "",
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r TaskExecutionRepoPG) CreateReview(
	ctx context.Context,
	attemptID string,
	reviewNumber int,
	threadID string,
	result domain.ReviewerResult,
) (domain.TaskReview, error) {
	if reviewNumber < 1 || strings.TrimSpace(threadID) == "" {
		return domain.TaskReview{}, fmt.Errorf("invalid task review identity: %w", domain.ErrValidation)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return domain.TaskReview{}, fmt.Errorf("marshal reviewer result: %w", err)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.TaskReview{}, fmt.Errorf("begin task review: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var coderThreadID *string
	if err := tx.QueryRow(ctx, `SELECT agent_thread_id FROM task_attempt WHERE id = $1 FOR UPDATE`, attemptID).Scan(&coderThreadID); err != nil {
		return domain.TaskReview{}, mapPlanningError(err)
	}
	if coderThreadID != nil && *coderThreadID == threadID {
		return domain.TaskReview{}, fmt.Errorf("reviewer cannot reuse coder thread: %w", domain.ErrConflict)
	}
	review, err := scanTaskReview(tx.QueryRow(ctx, `
INSERT INTO task_review (task_attempt_id, review_number, agent_thread_id, status, structured_result)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (task_attempt_id, review_number) DO UPDATE SET
    status = EXCLUDED.status,
    structured_result = EXCLUDED.structured_result
WHERE task_review.agent_thread_id = EXCLUDED.agent_thread_id
RETURNING `+taskReviewColumns, attemptID, reviewNumber, threadID, result.Status, raw))
	if err != nil {
		return domain.TaskReview{}, mapPlanningError(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE task_attempt SET review_count = GREATEST(review_count, $2), status = 'review', updated_at = now()
WHERE id = $1`, attemptID, reviewNumber); err != nil {
		return domain.TaskReview{}, fmt.Errorf("update task review count: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.TaskReview{}, fmt.Errorf("commit task review: %w", err)
	}
	return review, nil
}

func (r TaskExecutionRepoPG) BeginReview(
	ctx context.Context,
	attemptID string,
	reviewNumber int,
	threadID string,
) (domain.TaskReview, error) {
	if reviewNumber < 1 || strings.TrimSpace(threadID) == "" {
		return domain.TaskReview{}, fmt.Errorf("invalid task review identity: %w", domain.ErrValidation)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.TaskReview{}, fmt.Errorf("begin reviewer thread: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var coderThreadID *string
	if err := tx.QueryRow(ctx, `SELECT agent_thread_id FROM task_attempt WHERE id = $1 FOR UPDATE`, attemptID).Scan(&coderThreadID); err != nil {
		return domain.TaskReview{}, mapPlanningError(err)
	}
	if coderThreadID != nil && *coderThreadID == threadID {
		return domain.TaskReview{}, fmt.Errorf("reviewer cannot reuse coder thread: %w", domain.ErrConflict)
	}
	review, err := scanTaskReview(tx.QueryRow(ctx, `
INSERT INTO task_review (task_attempt_id, review_number, agent_thread_id, status)
VALUES ($1, $2, $3, 'running')
ON CONFLICT (task_attempt_id, review_number) DO UPDATE SET agent_thread_id = EXCLUDED.agent_thread_id
WHERE task_review.status = 'running' AND task_review.agent_thread_id = EXCLUDED.agent_thread_id
RETURNING `+taskReviewColumns, attemptID, reviewNumber, threadID))
	if err != nil {
		return domain.TaskReview{}, mapPlanningError(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE task_attempt SET review_count = GREATEST(review_count, $2), status = 'review', updated_at = now()
WHERE id = $1`, attemptID, reviewNumber); err != nil {
		return domain.TaskReview{}, fmt.Errorf("start task review: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.TaskReview{}, fmt.Errorf("commit reviewer thread: %w", err)
	}
	return review, nil
}

func (r TaskExecutionRepoPG) StoreArtifact(ctx context.Context, artifact domain.Artifact) (domain.Artifact, error) {
	if artifact.TaskID == "" || artifact.Type == "" || artifact.Name == "" || artifact.URI == "" || artifact.Checksum == "" {
		return domain.Artifact{}, fmt.Errorf("artifact identity is incomplete: %w", domain.ErrValidation)
	}
	if len(artifact.Metadata) == 0 {
		artifact.Metadata = json.RawMessage(`{}`)
	}
	stored, err := scanArtifact(r.Pool.QueryRow(ctx, `
INSERT INTO artifact (task_id, type, name, uri, checksum, metadata)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (task_id, type, name, checksum) DO UPDATE SET uri = EXCLUDED.uri
RETURNING `+artifactColumns,
		artifact.TaskID, artifact.Type, artifact.Name, artifact.URI, artifact.Checksum, artifact.Metadata,
	))
	return stored, mapPlanningError(err)
}

func (r TaskExecutionRepoPG) ListAttempts(ctx context.Context, taskID string) ([]domain.TaskAttempt, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT `+taskAttemptColumns+` FROM task_attempt WHERE task_id = $1 ORDER BY attempt_number`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list task attempts: %w", err)
	}
	defer rows.Close()
	result := make([]domain.TaskAttempt, 0)
	for rows.Next() {
		attempt, scanErr := scanTaskAttempt(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan task attempt: %w", scanErr)
		}
		result = append(result, attempt)
	}
	return result, rows.Err()
}

func (r TaskExecutionRepoPG) ListArtifacts(ctx context.Context, taskID string) ([]domain.Artifact, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT `+artifactColumns+` FROM artifact WHERE task_id = $1 ORDER BY created_at, id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list task artifacts: %w", err)
	}
	defer rows.Close()
	result := make([]domain.Artifact, 0)
	for rows.Next() {
		artifact, scanErr := scanArtifact(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan task artifact: %w", scanErr)
		}
		result = append(result, artifact)
	}
	return result, rows.Err()
}

func (r TaskExecutionRepoPG) AddRequiredTasks(
	ctx context.Context,
	parentTaskID string,
	required []domain.RequiredTask,
	maxDepth, maxReplans int,
) (domain.RequiredTaskSchedule, error) {
	if len(required) == 0 || len(required) > 8 || maxDepth < 1 || maxReplans < 0 {
		return domain.RequiredTaskSchedule{}, fmt.Errorf("invalid required-task request: %w", domain.ErrValidation)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.RequiredTaskSchedule{}, fmt.Errorf("begin required tasks: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "required-tasks:"+parentTaskID); err != nil {
		return domain.RequiredTaskSchedule{}, fmt.Errorf("lock required tasks: %w", err)
	}
	parent, err := scanTask(tx.QueryRow(ctx, `SELECT `+taskColumns+` FROM task WHERE id = $1 FOR UPDATE`, parentTaskID))
	if err != nil {
		return domain.RequiredTaskSchedule{}, mapPlanningError(err)
	}
	if parent.Depth >= maxDepth {
		return domain.RequiredTaskSchedule{}, fmt.Errorf("required-task depth limit reached: %w", domain.ErrInvalidStatus)
	}
	var replanCount int
	if err := tx.QueryRow(ctx, `SELECT replan_count FROM plan WHERE id = $1 FOR UPDATE`, parent.PlanID).Scan(&replanCount); err != nil {
		return domain.RequiredTaskSchedule{}, mapPlanningError(err)
	}
	result := domain.RequiredTaskSchedule{}
	newEdge := false
	for index, request := range required {
		projects, projectErr := findProjectsByNameTx(ctx, tx, strings.TrimSpace(request.Service))
		if projectErr != nil {
			return domain.RequiredTaskSchedule{}, projectErr
		}
		if len(projects) != 1 {
			return domain.RequiredTaskSchedule{}, fmt.Errorf("required service %q does not resolve to one project: %w", request.Service, domain.ErrValidation)
		}
		project := projects[0]
		if project.ID == parent.ProjectID {
			return domain.RequiredTaskSchedule{}, fmt.Errorf("required task cannot target the parent project: %w", domain.ErrValidation)
		}
		child, childErr := scanTask(tx.QueryRow(ctx, `
SELECT `+taskColumns+` FROM task WHERE plan_id = $1 AND project_id = $2`, parent.PlanID, project.ID))
		if errors.Is(childErr, pgx.ErrNoRows) {
			criteria, _ := json.Marshal(request.AcceptanceCriteria)
			writeScope := json.RawMessage(`["**"]`)
			commands := json.RawMessage(`["git diff --check"]`)
			role := strings.TrimSpace(request.Role)
			if role == "" {
				role = "implementation"
			}
			child, childErr = scanTask(tx.QueryRow(ctx, `
INSERT INTO task (
    plan_id, project_id, role, title, description, status,
    acceptance_criteria, write_scope, model_profile, priority, idempotency_key,
    planner_key, risk_level, requires_migration, changes_contracts,
    verification_commands, depth
) VALUES ($1, $2, $3, $4, $5, 'ready', $6, $7, 'standard', $8, $9, $10, $11, false, false, $12, $13)
RETURNING `+taskColumns,
				parent.PlanID, project.ID, role, request.Title, request.Description,
				criteria, writeScope, parent.Priority+1,
				fmt.Sprintf("%s:required:%s:%d", parent.PlanID, parent.ID, index),
				fmt.Sprintf("required-%s-%d", compactDatabaseID(parent.ID), index), parent.RiskLevel,
				commands, parent.Depth+1,
			))
		}
		if childErr != nil {
			return domain.RequiredTaskSchedule{}, mapPlanningError(childErr)
		}
		if child.Status == domain.TaskStatusFailed || child.Status == domain.TaskStatusCancelled {
			return domain.RequiredTaskSchedule{}, fmt.Errorf("required task %s is terminal: %w", child.ID, domain.ErrInvalidStatus)
		}
		command, err := tx.Exec(ctx, `
INSERT INTO task_dependency (task_id, depends_on_task_id, dependency_type)
VALUES ($1, $2, 'required_by_agent') ON CONFLICT DO NOTHING`, parent.ID, child.ID)
		if err != nil {
			return domain.RequiredTaskSchedule{}, mapPlanningError(err)
		}
		newEdge = newEdge || command.RowsAffected() > 0
		dependencies, err := taskDependencyIDsTx(ctx, tx, child.ID)
		if err != nil {
			return domain.RequiredTaskSchedule{}, err
		}
		result.Tasks = append(result.Tasks, domain.ScheduledTask{
			TaskID: child.ID, Priority: child.Priority, Dependencies: dependencies,
		})
		result.ParentDependencies = append(result.ParentDependencies, child.ID)
	}
	if newEdge {
		if replanCount >= maxReplans {
			return domain.RequiredTaskSchedule{}, fmt.Errorf("plan reached maximum of %d replans: %w", maxReplans, domain.ErrInvalidStatus)
		}
		if _, err := tx.Exec(ctx, `UPDATE plan SET replan_count = replan_count + 1, updated_at = now() WHERE id = $1`, parent.PlanID); err != nil {
			return domain.RequiredTaskSchedule{}, fmt.Errorf("increment plan replan count: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE task SET status = 'blocked' WHERE id = $1`, parent.ID); err != nil {
		return domain.RequiredTaskSchedule{}, fmt.Errorf("block parent task: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RequiredTaskSchedule{}, fmt.Errorf("commit required tasks: %w", err)
	}
	return result, nil
}

func (r TaskExecutionRepoPG) ResetTaskForRetry(
	ctx context.Context,
	taskID string,
	maxAttempts int,
) (domain.Task, error) {
	if maxAttempts < 1 || maxAttempts > 3 {
		return domain.Task{}, fmt.Errorf("invalid maximum attempts: %w", domain.ErrValidation)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.Task{}, fmt.Errorf("begin task retry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	task, err := scanTask(tx.QueryRow(ctx, `SELECT `+taskColumns+` FROM task WHERE id = $1 FOR UPDATE`, taskID))
	if err != nil {
		return domain.Task{}, mapPlanningError(err)
	}
	if task.Status != domain.TaskStatusFailed && task.Status != domain.TaskStatusBlocked &&
		task.Status != domain.TaskStatusChangesRequested {
		return domain.Task{}, fmt.Errorf("task cannot be retried from %s: %w", task.Status, domain.ErrInvalidStatus)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM task_attempt WHERE task_id = $1`, taskID).Scan(&count); err != nil {
		return domain.Task{}, fmt.Errorf("count retry attempts: %w", err)
	}
	if count >= maxAttempts {
		return domain.Task{}, fmt.Errorf("task reached maximum of %d attempts: %w", maxAttempts, domain.ErrInvalidStatus)
	}
	task, err = scanTask(tx.QueryRow(ctx, `
UPDATE task SET status = 'ready', completed_at = NULL WHERE id = $1 RETURNING `+taskColumns, taskID))
	if err != nil {
		return domain.Task{}, mapPlanningError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Task{}, fmt.Errorf("commit task retry: %w", err)
	}
	return task, nil
}

const taskAttemptColumns = `id, task_id, attempt_number, agent_thread_id, workflow_id,
worktree_path, branch_name, commit_sha, status, structured_result,
verification_result, review_count, error, started_at, heartbeat_at, finished_at, updated_at`

func scanTaskAttempt(row rowScanner) (domain.TaskAttempt, error) {
	var value domain.TaskAttempt
	err := row.Scan(
		&value.ID, &value.TaskID, &value.AttemptNumber, &value.AgentThreadID,
		&value.WorkflowID, &value.WorktreePath, &value.BranchName, &value.CommitSHA,
		&value.Status, &value.StructuredResult, &value.Verification, &value.ReviewCount,
		&value.Error, &value.StartedAt, &value.HeartbeatAt, &value.FinishedAt, &value.UpdatedAt,
	)
	return value, err
}

const taskReviewColumns = `id, task_attempt_id, review_number, agent_thread_id,
status, structured_result, created_at`

func scanTaskReview(row rowScanner) (domain.TaskReview, error) {
	var value domain.TaskReview
	err := row.Scan(
		&value.ID, &value.TaskAttemptID, &value.ReviewNumber, &value.AgentThreadID,
		&value.Status, &value.StructuredResult, &value.CreatedAt,
	)
	return value, err
}

const artifactColumns = `id, task_id, type, name, uri, checksum, metadata, created_at`

func scanArtifact(row rowScanner) (domain.Artifact, error) {
	var value domain.Artifact
	err := row.Scan(
		&value.ID, &value.TaskID, &value.Type, &value.Name,
		&value.URI, &value.Checksum, &value.Metadata, &value.CreatedAt,
	)
	return value, err
}

func taskStatusForAttempt(status domain.TaskAttemptStatus) (domain.TaskStatus, bool) {
	switch status {
	case domain.TaskAttemptStatusRunning:
		return domain.TaskStatusRunning, true
	case domain.TaskAttemptStatusVerification, domain.TaskAttemptStatusReview:
		return domain.TaskStatusVerification, true
	case domain.TaskAttemptStatusChangesRequested:
		return domain.TaskStatusChangesRequested, true
	case domain.TaskAttemptStatusCompleted:
		return domain.TaskStatusCompleted, true
	case domain.TaskAttemptStatusBlocked:
		return domain.TaskStatusBlocked, true
	case domain.TaskAttemptStatusFailed:
		return domain.TaskStatusFailed, true
	case domain.TaskAttemptStatusCancelled:
		return domain.TaskStatusCancelled, true
	default:
		return "", false
	}
}

func prefixedTaskColumns(alias string) string {
	return alias + `.id, ` + alias + `.plan_id, ` + alias + `.project_id,
` + alias + `.role, ` + alias + `.title, ` + alias + `.description, ` + alias + `.status,
` + alias + `.acceptance_criteria, ` + alias + `.write_scope, ` + alias + `.model_profile,
` + alias + `.priority, ` + alias + `.idempotency_key, COALESCE(` + alias + `.planner_key, ''),
` + alias + `.risk_level, ` + alias + `.requires_migration, ` + alias + `.changes_contracts,
` + alias + `.verification_commands, ` + alias + `.depth, ` + alias + `.created_at,
` + alias + `.started_at, ` + alias + `.completed_at`
}

func findProjectsByNameTx(ctx context.Context, tx pgx.Tx, name string) ([]domain.Project, error) {
	rows, err := tx.Query(ctx, `SELECT `+projectColumns+` FROM project WHERE name = $1 ORDER BY id LIMIT 2`, name)
	if err != nil {
		return nil, fmt.Errorf("find required service project: %w", err)
	}
	defer rows.Close()
	var result []domain.Project
	for rows.Next() {
		project, scanErr := scanProject(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan required service project: %w", scanErr)
		}
		result = append(result, project)
	}
	return result, rows.Err()
}

func taskDependencyIDsTx(ctx context.Context, tx pgx.Tx, taskID string) ([]string, error) {
	rows, err := tx.Query(ctx, `
SELECT depends_on_task_id FROM task_dependency WHERE task_id = $1 ORDER BY depends_on_task_id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list required task dependencies: %w", err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func compactDatabaseID(value string) string {
	value = strings.ReplaceAll(value, "-", "")
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

var _ repository.TaskExecutionRepository = TaskExecutionRepoPG{}
