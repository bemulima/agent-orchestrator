package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type PlanningRepoPG struct {
	Pool *pgxpool.Pool
}

func (r PlanningRepoPG) CreateCommand(ctx context.Context, command domain.Command) (domain.Command, error) {
	command.Text = strings.TrimSpace(command.Text)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	if command.Text == "" || command.IdempotencyKey == "" || !validCommandSource(command.Source) {
		return domain.Command{}, fmt.Errorf("command text, source, and idempotency key are required: %w", domain.ErrValidation)
	}
	if command.Status == "" {
		command.Status = domain.CommandStatusReceived
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.Command{}, fmt.Errorf("begin command transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "command:"+command.IdempotencyKey); err != nil {
		return domain.Command{}, fmt.Errorf("lock command: %w", err)
	}
	existing, err := scanCommand(tx.QueryRow(ctx, `SELECT `+commandColumns+` FROM command WHERE idempotency_key = $1`, command.IdempotencyKey))
	if err == nil {
		if existing.Text != command.Text || existing.Source != command.Source {
			return domain.Command{}, fmt.Errorf("idempotency key belongs to a different command: %w", domain.ErrConflict)
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.Command{}, fmt.Errorf("commit reused command: %w", err)
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Command{}, fmt.Errorf("find command by idempotency key: %w", err)
	}
	command, err = scanCommand(tx.QueryRow(ctx, `
INSERT INTO command (source, source_user_id, text, status, idempotency_key)
VALUES ($1, $2, $3, $4, $5)
RETURNING `+commandColumns, command.Source, command.SourceUserID, command.Text, command.Status, command.IdempotencyKey))
	if err != nil {
		return domain.Command{}, mapPlanningError(err)
	}
	if err := insertResourceAuditTx(ctx, tx, "command", "command.received", command.ID, map[string]any{
		"source": command.Source,
	}); err != nil {
		return domain.Command{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Command{}, fmt.Errorf("commit command: %w", err)
	}
	return command, nil
}

func (r PlanningRepoPG) GetCommand(ctx context.Context, id string) (domain.Command, error) {
	command, err := scanCommand(r.Pool.QueryRow(ctx, `SELECT `+commandColumns+` FROM command WHERE id = $1`, id))
	return command, mapPlanningError(err)
}

func (r PlanningRepoPG) CreatePlan(
	ctx context.Context,
	command domain.Command,
	input domain.PlannerInput,
	output domain.PlannerOutput,
) (domain.PlanBundle, error) {
	rawInput, err := json.Marshal(input)
	if err != nil {
		return domain.PlanBundle{}, fmt.Errorf("marshal planner input: %w", err)
	}
	rawOutput, err := json.Marshal(output)
	if err != nil {
		return domain.PlanBundle{}, fmt.Errorf("marshal planner output: %w", err)
	}
	fingerprintInput := make([]byte, 0, len(rawInput)+len(rawOutput)+1)
	fingerprintInput = append(fingerprintInput, rawInput...)
	fingerprintInput = append(fingerprintInput, 0)
	fingerprintInput = append(fingerprintInput, rawOutput...)
	fingerprint := planningChecksum(fingerprintInput)
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.PlanBundle{}, fmt.Errorf("begin plan transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "plan:"+command.ID); err != nil {
		return domain.PlanBundle{}, fmt.Errorf("lock plan creation: %w", err)
	}
	var existingID string
	err = tx.QueryRow(ctx, `SELECT id FROM plan WHERE command_id = $1 AND fingerprint = $2`, command.ID, fingerprint).Scan(&existingID)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return domain.PlanBundle{}, fmt.Errorf("commit reused plan: %w", err)
		}
		return r.GetPlan(ctx, existingID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.PlanBundle{}, fmt.Errorf("find matching plan: %w", err)
	}
	var currentStatus domain.CommandStatus
	if err := tx.QueryRow(ctx, `SELECT status FROM command WHERE id = $1 FOR UPDATE`, command.ID).Scan(&currentStatus); err != nil {
		return domain.PlanBundle{}, mapPlanningError(err)
	}
	if currentStatus == domain.CommandStatusCancelled || currentStatus == domain.CommandStatusCompleted {
		return domain.PlanBundle{}, fmt.Errorf("command cannot be planned from status %s: %w", currentStatus, domain.ErrInvalidStatus)
	}
	var plan domain.Plan
	err = tx.QueryRow(ctx, `
INSERT INTO plan (
    command_id, status, version, summary, risk_level, requires_approval,
    topology_revision_id, fingerprint, planner_input, planner_output
) VALUES (
    $1, 'awaiting_approval',
    (SELECT COALESCE(MAX(version), 0) + 1 FROM plan WHERE command_id = $1),
    $2, $3, true, $4, $5, $6, $7
)
RETURNING `+planColumns, command.ID, output.Summary, output.RiskLevel,
		input.TopologyRevisionID, fingerprint, rawInput, rawOutput).Scan(planScanTargets(&plan)...)
	if err != nil {
		return domain.PlanBundle{}, mapPlanningError(err)
	}
	taskIDs := make(map[string]string, len(output.Tasks))
	for _, draft := range output.Tasks {
		criteria, _ := json.Marshal(draft.AcceptanceCriteria)
		writeScope, _ := json.Marshal(draft.WriteScope)
		commands, _ := json.Marshal(draft.VerificationCommands)
		var taskID string
		err := tx.QueryRow(ctx, `
INSERT INTO task (
    plan_id, project_id, role, title, description, status,
    acceptance_criteria, write_scope, model_profile, priority, idempotency_key,
    planner_key, risk_level, requires_migration, changes_contracts,
    verification_commands, depth
) VALUES ($1, $2, $3, $4, $5, 'planned', $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
RETURNING id`, plan.ID, draft.ProjectID, draft.Role, draft.Title, draft.Description,
			criteria, writeScope, draft.ModelProfile, draft.Priority, plan.ID+":"+draft.Key,
			draft.Key, draft.RiskLevel, draft.RequiresMigration, draft.ChangesContracts, commands, draft.Depth).Scan(&taskID)
		if err != nil {
			return domain.PlanBundle{}, mapPlanningError(err)
		}
		taskIDs[draft.Key] = taskID
	}
	for _, dependency := range output.Dependencies {
		taskID, taskExists := taskIDs[dependency.TaskKey]
		dependsOnID, dependencyExists := taskIDs[dependency.DependsOnTaskKey]
		if !taskExists || !dependencyExists {
			return domain.PlanBundle{}, fmt.Errorf("planner dependency does not resolve to persisted tasks: %w", domain.ErrValidation)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO task_dependency (task_id, depends_on_task_id, dependency_type)
VALUES ($1, $2, $3)`, taskID, dependsOnID, dependency.DependencyType); err != nil {
			return domain.PlanBundle{}, mapPlanningError(err)
		}
	}
	var approvalID string
	if err := tx.QueryRow(ctx, `
INSERT INTO approval (resource_type, resource_id, action, status)
VALUES ('plan', $1, 'run', 'pending')
RETURNING id`, plan.ID).Scan(&approvalID); err != nil {
		return domain.PlanBundle{}, mapPlanningError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE plan SET approval_id = $2, updated_at = now() WHERE id = $1`, plan.ID, approvalID); err != nil {
		return domain.PlanBundle{}, fmt.Errorf("attach plan approval: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE command SET status = 'planned' WHERE id = $1`, command.ID); err != nil {
		return domain.PlanBundle{}, fmt.Errorf("mark command planned: %w", err)
	}
	if err := insertResourceAuditTx(ctx, tx, "plan", "plan.created", plan.ID, map[string]any{
		"command_id": command.ID, "version": plan.Version, "fingerprint": fingerprint,
		"task_count": len(output.Tasks), "risk_level": output.RiskLevel,
	}); err != nil {
		return domain.PlanBundle{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.PlanBundle{}, fmt.Errorf("commit plan: %w", err)
	}
	return r.GetPlan(ctx, plan.ID)
}

func (r PlanningRepoPG) GetPlan(ctx context.Context, id string) (domain.PlanBundle, error) {
	var bundle domain.PlanBundle
	err := r.Pool.QueryRow(ctx, `SELECT `+planColumns+` FROM plan WHERE id = $1`, id).Scan(planScanTargets(&bundle.Plan)...)
	if err != nil {
		return domain.PlanBundle{}, mapPlanningError(err)
	}
	if bundle.Tasks, err = r.listTasks(ctx, id); err != nil {
		return domain.PlanBundle{}, err
	}
	if bundle.Dependencies, err = r.listDependencies(ctx, id); err != nil {
		return domain.PlanBundle{}, err
	}
	if bundle.Plan.ApprovalID != nil {
		approval, approvalErr := scanApproval(r.Pool.QueryRow(ctx, `SELECT `+approvalColumns+` FROM approval WHERE id = $1`, *bundle.Plan.ApprovalID))
		if approvalErr != nil {
			return domain.PlanBundle{}, mapPlanningError(approvalErr)
		}
		bundle.Approval = &approval
	}
	run, runErr := scanPlanRun(r.Pool.QueryRow(ctx, `SELECT `+planRunColumns+` FROM plan_run WHERE plan_id = $1`, id))
	if runErr == nil {
		bundle.Run = &run
	} else if !errors.Is(runErr, pgx.ErrNoRows) {
		return domain.PlanBundle{}, fmt.Errorf("get plan run: %w", runErr)
	}
	return bundle, nil
}

func (r PlanningRepoPG) ApprovePlan(ctx context.Context, id, actor, comment string) (domain.PlanBundle, error) {
	return r.decidePlan(ctx, id, actor, comment, true)
}

func (r PlanningRepoPG) RejectPlan(ctx context.Context, id, actor, comment string) (domain.PlanBundle, error) {
	return r.decidePlan(ctx, id, actor, comment, false)
}

func (r PlanningRepoPG) decidePlan(ctx context.Context, id, actor, comment string, approve bool) (domain.PlanBundle, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.PlanBundle{}, fmt.Errorf("begin plan decision: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var plan domain.Plan
	if err := tx.QueryRow(ctx, `SELECT `+planColumns+` FROM plan WHERE id = $1 FOR UPDATE`, id).Scan(planScanTargets(&plan)...); err != nil {
		return domain.PlanBundle{}, mapPlanningError(err)
	}
	target := domain.PlanStatusCancelled
	approvalStatus := domain.ApprovalStatusRejected
	action := "plan.rejected"
	commandStatus := domain.CommandStatusCancelled
	if approve {
		target, approvalStatus, action = domain.PlanStatusApproved, domain.ApprovalStatusApproved, "plan.approved"
		commandStatus = domain.CommandStatusApproved
	}
	if plan.Status == target {
		if err := tx.Commit(ctx); err != nil {
			return domain.PlanBundle{}, fmt.Errorf("commit repeated plan decision: %w", err)
		}
		return r.GetPlan(ctx, id)
	}
	if plan.Status != domain.PlanStatusAwaitingApproval || plan.ApprovalID == nil {
		return domain.PlanBundle{}, fmt.Errorf("plan is not awaiting approval: %w", domain.ErrInvalidStatus)
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "owner"
	}
	command, err := tx.Exec(ctx, `
UPDATE approval SET status = $2, decided_at = now(), decided_by = $3, comment = NULLIF($4, '')
WHERE id = $1 AND status = 'pending'`, *plan.ApprovalID, approvalStatus, actor, strings.TrimSpace(comment))
	if err != nil {
		return domain.PlanBundle{}, fmt.Errorf("persist plan approval: %w", err)
	}
	if command.RowsAffected() != 1 {
		return domain.PlanBundle{}, fmt.Errorf("plan approval is no longer pending: %w", domain.ErrConflict)
	}
	if _, err := tx.Exec(ctx, `
UPDATE plan SET status = $2::varchar, approved_at = CASE WHEN $2::varchar = 'approved' THEN now() ELSE approved_at END, updated_at = now()
WHERE id = $1`, id, target); err != nil {
		return domain.PlanBundle{}, fmt.Errorf("update plan decision: %w", err)
	}
	if !approve {
		if _, err := tx.Exec(ctx, `UPDATE task SET status = 'cancelled', completed_at = now() WHERE plan_id = $1`, id); err != nil {
			return domain.PlanBundle{}, fmt.Errorf("cancel rejected plan tasks: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE command SET status = $2 WHERE id = $1`, plan.CommandID, commandStatus); err != nil {
		return domain.PlanBundle{}, fmt.Errorf("update command decision: %w", err)
	}
	if err := insertResourceAuditTx(ctx, tx, "plan", action, id, map[string]any{"actor": actor}); err != nil {
		return domain.PlanBundle{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.PlanBundle{}, fmt.Errorf("commit plan decision: %w", err)
	}
	return r.GetPlan(ctx, id)
}

func (r PlanningRepoPG) PrepareRun(ctx context.Context, planID string, maxParallel int) (domain.PlanRun, domain.PlanBundle, error) {
	if maxParallel < 1 || maxParallel > 3 {
		return domain.PlanRun{}, domain.PlanBundle{}, fmt.Errorf("max parallel tasks must be between 1 and 3: %w", domain.ErrValidation)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.PlanRun{}, domain.PlanBundle{}, fmt.Errorf("begin plan run: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var plan domain.Plan
	if err := tx.QueryRow(ctx, `SELECT `+planColumns+` FROM plan WHERE id = $1 FOR UPDATE`, planID).Scan(planScanTargets(&plan)...); err != nil {
		return domain.PlanRun{}, domain.PlanBundle{}, mapPlanningError(err)
	}
	existing, err := scanPlanRun(tx.QueryRow(ctx, `SELECT `+planRunColumns+` FROM plan_run WHERE plan_id = $1`, planID))
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return domain.PlanRun{}, domain.PlanBundle{}, fmt.Errorf("commit reused plan run: %w", err)
		}
		bundle, getErr := r.GetPlan(ctx, planID)
		return existing, bundle, getErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.PlanRun{}, domain.PlanBundle{}, fmt.Errorf("find existing plan run: %w", err)
	}
	if plan.Status != domain.PlanStatusApproved {
		return domain.PlanRun{}, domain.PlanBundle{}, fmt.Errorf("plan must be approved before run: %w", domain.ErrApprovalNeeded)
	}
	workflowID := fmt.Sprintf("plan-%s-v%d", plan.ID, plan.Version)
	run, err := scanPlanRun(tx.QueryRow(ctx, `
INSERT INTO plan_run (plan_id, status, workflow_id, idempotency_key, max_parallel_tasks)
VALUES ($1, 'pending', $2, $2, $3)
RETURNING `+planRunColumns, plan.ID, workflowID, maxParallel))
	if err != nil {
		return domain.PlanRun{}, domain.PlanBundle{}, mapPlanningError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE plan SET status = 'running', updated_at = now() WHERE id = $1`, plan.ID); err != nil {
		return domain.PlanRun{}, domain.PlanBundle{}, fmt.Errorf("mark plan starting: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE command SET status = 'running' WHERE id = $1`, plan.CommandID); err != nil {
		return domain.PlanRun{}, domain.PlanBundle{}, fmt.Errorf("mark command running: %w", err)
	}
	if err := insertResourceAuditTx(ctx, tx, "plan_run", "plan_run.created", run.ID, map[string]any{
		"plan_id": plan.ID, "workflow_id": workflowID, "max_parallel_tasks": maxParallel,
	}); err != nil {
		return domain.PlanRun{}, domain.PlanBundle{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.PlanRun{}, domain.PlanBundle{}, fmt.Errorf("commit plan run: %w", err)
	}
	bundle, err := r.GetPlan(ctx, planID)
	return run, bundle, err
}

func (r PlanningRepoPG) AttachTemporalRun(ctx context.Context, runID, temporalRunID string) (domain.PlanRun, error) {
	run, err := scanPlanRun(r.Pool.QueryRow(ctx, `
UPDATE plan_run SET temporal_run_id = COALESCE(temporal_run_id, NULLIF($2, '')), updated_at = now()
WHERE id = $1
RETURNING `+planRunColumns, runID, temporalRunID))
	return run, mapPlanningError(err)
}

func (r PlanningRepoPG) GetRun(ctx context.Context, id string) (domain.PlanRun, error) {
	run, err := scanPlanRun(r.Pool.QueryRow(ctx, `SELECT `+planRunColumns+` FROM plan_run WHERE id = $1`, id))
	return run, mapPlanningError(err)
}

func (r PlanningRepoPG) UpdateRunStatus(
	ctx context.Context,
	runID string,
	status domain.PlanRunStatus,
	errorMessage string,
) (domain.PlanRun, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.PlanRun{}, fmt.Errorf("begin run status transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	run, err := scanPlanRun(tx.QueryRow(ctx, `SELECT `+planRunColumns+` FROM plan_run WHERE id = $1 FOR UPDATE`, runID))
	if err != nil {
		return domain.PlanRun{}, mapPlanningError(err)
	}
	if run.Status == status {
		if err := tx.Commit(ctx); err != nil {
			return domain.PlanRun{}, fmt.Errorf("commit repeated run status: %w", err)
		}
		return run, nil
	}
	if !validRunTransition(run.Status, status) {
		return domain.PlanRun{}, fmt.Errorf("invalid run transition %s -> %s: %w", run.Status, status, domain.ErrInvalidStatus)
	}
	run, err = scanPlanRun(tx.QueryRow(ctx, `
UPDATE plan_run SET
    status = $2::varchar,
    error = NULLIF($3, ''),
    started_at = CASE WHEN $2::varchar = 'running' THEN COALESCE(started_at, now()) ELSE started_at END,
    paused_at = CASE WHEN $2::varchar = 'paused' THEN now() WHEN $2::varchar = 'running' THEN NULL ELSE paused_at END,
    completed_at = CASE WHEN $2::varchar IN ('completed', 'failed', 'cancelled') THEN now() ELSE completed_at END,
    updated_at = now()
WHERE id = $1
RETURNING `+planRunColumns, runID, status, strings.TrimSpace(errorMessage)))
	if err != nil {
		return domain.PlanRun{}, mapPlanningError(err)
	}
	planStatus, commandStatus := statusForRun(status)
	if _, err := tx.Exec(ctx, `UPDATE plan SET status = $2, updated_at = now() WHERE id = $1`, run.PlanID, planStatus); err != nil {
		return domain.PlanRun{}, fmt.Errorf("update plan from run: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE command SET status = $2
WHERE id = (SELECT command_id FROM plan WHERE id = $1)`, run.PlanID, commandStatus); err != nil {
		return domain.PlanRun{}, fmt.Errorf("update command from run: %w", err)
	}
	if status == domain.PlanRunStatusCancelled {
		if _, err := tx.Exec(ctx, `
UPDATE task SET status = 'cancelled', completed_at = now()
WHERE plan_id = $1 AND status NOT IN ('completed', 'failed', 'cancelled')`, run.PlanID); err != nil {
			return domain.PlanRun{}, fmt.Errorf("cancel plan tasks: %w", err)
		}
	}
	if status == domain.PlanRunStatusFailed {
		if _, err := tx.Exec(ctx, `
UPDATE task SET status = 'failed', completed_at = now()
WHERE plan_id = $1 AND status IN ('planned', 'ready', 'running')`, run.PlanID); err != nil {
			return domain.PlanRun{}, fmt.Errorf("fail unfinished plan tasks: %w", err)
		}
	}
	if err := insertResourceAuditTx(ctx, tx, "plan_run", "plan_run."+string(status), run.ID, map[string]any{
		"plan_id": run.PlanID,
	}); err != nil {
		return domain.PlanRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.PlanRun{}, fmt.Errorf("commit run status: %w", err)
	}
	return run, nil
}

func (r PlanningRepoPG) MarkTaskReady(ctx context.Context, runID, taskID string) (domain.Task, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.Task{}, fmt.Errorf("begin task dispatch: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var runPlanID string
	var runStatus domain.PlanRunStatus
	if err := tx.QueryRow(ctx, `SELECT plan_id, status FROM plan_run WHERE id = $1`, runID).Scan(&runPlanID, &runStatus); err != nil {
		return domain.Task{}, mapPlanningError(err)
	}
	if runStatus != domain.PlanRunStatusRunning {
		return domain.Task{}, fmt.Errorf("run is not running: %w", domain.ErrInvalidStatus)
	}
	task, err := scanTask(tx.QueryRow(ctx, `SELECT `+taskColumns+` FROM task WHERE id = $1 FOR UPDATE`, taskID))
	if err != nil {
		return domain.Task{}, mapPlanningError(err)
	}
	if task.PlanID != runPlanID {
		return domain.Task{}, fmt.Errorf("task does not belong to run plan: %w", domain.ErrConflict)
	}
	if task.Status == domain.TaskStatusReady || task.Status == domain.TaskStatusRunning {
		if err := tx.Commit(ctx); err != nil {
			return domain.Task{}, fmt.Errorf("commit reused task dispatch: %w", err)
		}
		return task, nil
	}
	if task.Status != domain.TaskStatusPlanned {
		return domain.Task{}, fmt.Errorf("task cannot become ready from %s: %w", task.Status, domain.ErrInvalidStatus)
	}
	task, err = scanTask(tx.QueryRow(ctx, `
UPDATE task SET status = 'ready' WHERE id = $1 RETURNING `+taskColumns, taskID))
	if err != nil {
		return domain.Task{}, mapPlanningError(err)
	}
	if err := insertResourceAuditTx(ctx, tx, "task", "task.ready", task.ID, map[string]any{"run_id": runID}); err != nil {
		return domain.Task{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Task{}, fmt.Errorf("commit task dispatch: %w", err)
	}
	return task, nil
}

func (r PlanningRepoPG) RecordTaskResult(ctx context.Context, runID string, result domain.TaskResult) (domain.Task, error) {
	if result.Status != domain.TaskStatusCompleted && result.Status != domain.TaskStatusFailed &&
		result.Status != domain.TaskStatusBlocked && result.Status != domain.TaskStatusCancelled {
		return domain.Task{}, fmt.Errorf("unsupported task result status %q: %w", result.Status, domain.ErrValidation)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.Task{}, fmt.Errorf("begin task result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var runPlanID string
	if err := tx.QueryRow(ctx, `SELECT plan_id FROM plan_run WHERE id = $1`, runID).Scan(&runPlanID); err != nil {
		return domain.Task{}, mapPlanningError(err)
	}
	task, err := scanTask(tx.QueryRow(ctx, `SELECT `+taskColumns+` FROM task WHERE id = $1 FOR UPDATE`, result.TaskID))
	if err != nil {
		return domain.Task{}, mapPlanningError(err)
	}
	if task.PlanID != runPlanID {
		return domain.Task{}, fmt.Errorf("task does not belong to run plan: %w", domain.ErrConflict)
	}
	if task.Status == result.Status {
		if err := tx.Commit(ctx); err != nil {
			return domain.Task{}, fmt.Errorf("commit reused task result: %w", err)
		}
		return task, nil
	}
	if task.Status == domain.TaskStatusCompleted || task.Status == domain.TaskStatusCancelled {
		return domain.Task{}, fmt.Errorf("task is already terminal: %w", domain.ErrInvalidStatus)
	}
	task, err = scanTask(tx.QueryRow(ctx, `
UPDATE task SET status = $2::varchar,
    completed_at = CASE WHEN $2::varchar IN ('completed', 'failed', 'cancelled') THEN now() ELSE completed_at END
WHERE id = $1 RETURNING `+taskColumns, result.TaskID, result.Status))
	if err != nil {
		return domain.Task{}, mapPlanningError(err)
	}
	if err := insertResourceAuditTx(ctx, tx, "task", "task."+string(result.Status), task.ID, map[string]any{
		"run_id": runID, "error_present": strings.TrimSpace(result.Error) != "",
	}); err != nil {
		return domain.Task{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Task{}, fmt.Errorf("commit task result: %w", err)
	}
	return task, nil
}

func (r PlanningRepoPG) GetTask(ctx context.Context, id string) (domain.Task, error) {
	task, err := scanTask(r.Pool.QueryRow(ctx, `SELECT `+taskColumns+` FROM task WHERE id = $1`, id))
	return task, mapPlanningError(err)
}

func (r PlanningRepoPG) CancelTask(ctx context.Context, id string) (domain.Task, error) {
	task, err := scanTask(r.Pool.QueryRow(ctx, `
UPDATE task SET status = 'cancelled', completed_at = now()
WHERE id = $1 AND status NOT IN ('completed', 'cancelled')
RETURNING `+taskColumns, id))
	return task, mapPlanningError(err)
}

func (r PlanningRepoPG) listTasks(ctx context.Context, planID string) ([]domain.Task, error) {
	rows, err := r.Pool.Query(ctx, `SELECT `+taskColumns+` FROM task WHERE plan_id = $1 ORDER BY priority DESC, id`, planID)
	if err != nil {
		return nil, fmt.Errorf("list plan tasks: %w", err)
	}
	defer rows.Close()
	result := make([]domain.Task, 0)
	for rows.Next() {
		task, scanErr := scanTask(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan plan task: %w", scanErr)
		}
		result = append(result, task)
	}
	return result, rows.Err()
}

func (r PlanningRepoPG) listDependencies(ctx context.Context, planID string) ([]domain.TaskDependency, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT dependency.task_id, dependency.depends_on_task_id, dependency.dependency_type
FROM task_dependency dependency
JOIN task ON task.id = dependency.task_id
WHERE task.plan_id = $1
ORDER BY dependency.task_id, dependency.depends_on_task_id`, planID)
	if err != nil {
		return nil, fmt.Errorf("list plan dependencies: %w", err)
	}
	defer rows.Close()
	result := make([]domain.TaskDependency, 0)
	for rows.Next() {
		var dependency domain.TaskDependency
		if err := rows.Scan(&dependency.TaskID, &dependency.DependsOnTaskID, &dependency.DependencyType); err != nil {
			return nil, fmt.Errorf("scan plan dependency: %w", err)
		}
		result = append(result, dependency)
	}
	return result, rows.Err()
}

func validCommandSource(source domain.CommandSource) bool {
	return source == domain.CommandSourceAPI || source == domain.CommandSourceCLI || source == domain.CommandSourceTelegram
}

func validRunTransition(from, to domain.PlanRunStatus) bool {
	switch from {
	case domain.PlanRunStatusPending:
		return to == domain.PlanRunStatusRunning || to == domain.PlanRunStatusFailed || to == domain.PlanRunStatusCancelled
	case domain.PlanRunStatusRunning:
		return to == domain.PlanRunStatusPaused || to == domain.PlanRunStatusCompleted || to == domain.PlanRunStatusFailed || to == domain.PlanRunStatusCancelled
	case domain.PlanRunStatusPaused:
		return to == domain.PlanRunStatusRunning || to == domain.PlanRunStatusFailed || to == domain.PlanRunStatusCancelled
	default:
		return false
	}
}

func statusForRun(status domain.PlanRunStatus) (domain.PlanStatus, domain.CommandStatus) {
	switch status {
	case domain.PlanRunStatusPaused:
		return domain.PlanStatusPaused, domain.CommandStatusRunning
	case domain.PlanRunStatusCompleted:
		return domain.PlanStatusCompleted, domain.CommandStatusCompleted
	case domain.PlanRunStatusFailed:
		return domain.PlanStatusFailed, domain.CommandStatusFailed
	case domain.PlanRunStatusCancelled:
		return domain.PlanStatusCancelled, domain.CommandStatusCancelled
	default:
		return domain.PlanStatusRunning, domain.CommandStatusRunning
	}
}

func planningChecksum(value []byte) string {
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}

func mapPlanningError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return fmt.Errorf("planning resource already exists: %w", domain.ErrConflict)
	}
	return err
}

const commandColumns = `id, source, source_user_id, text, status, idempotency_key, created_at`

func scanCommand(row rowScanner) (domain.Command, error) {
	var value domain.Command
	err := row.Scan(&value.ID, &value.Source, &value.SourceUserID, &value.Text, &value.Status, &value.IdempotencyKey, &value.CreatedAt)
	return value, err
}

const planColumns = `id, command_id, approval_id, topology_revision_id, status, version,
summary, risk_level, requires_approval, COALESCE(fingerprint, ''), planner_input, planner_output,
replan_count, created_at, updated_at, approved_at`

func planScanTargets(value *domain.Plan) []any {
	return []any{
		&value.ID, &value.CommandID, &value.ApprovalID, &value.TopologyRevisionID,
		&value.Status, &value.Version, &value.Summary, &value.RiskLevel,
		&value.RequiresApproval, &value.Fingerprint, &value.PlannerInput, &value.PlannerOutput,
		&value.ReplanCount, &value.CreatedAt, &value.UpdatedAt, &value.ApprovedAt,
	}
}

const taskColumns = `id, plan_id, project_id, role, title, description, status,
acceptance_criteria, write_scope, model_profile, priority, idempotency_key,
COALESCE(planner_key, ''), risk_level, requires_migration, changes_contracts,
verification_commands, depth, created_at, started_at, completed_at`

func scanTask(row rowScanner) (domain.Task, error) {
	var value domain.Task
	var criteria, writeScope, commands []byte
	err := row.Scan(
		&value.ID, &value.PlanID, &value.ProjectID, &value.Role, &value.Title,
		&value.Description, &value.Status, &criteria, &writeScope, &value.ModelProfile,
		&value.Priority, &value.IdempotencyKey, &value.PlannerKey, &value.RiskLevel,
		&value.RequiresMigration, &value.ChangesContracts, &commands, &value.Depth,
		&value.CreatedAt, &value.StartedAt, &value.CompletedAt,
	)
	if err != nil {
		return domain.Task{}, err
	}
	if err := json.Unmarshal(criteria, &value.AcceptanceCriteria); err != nil {
		return domain.Task{}, fmt.Errorf("decode task acceptance criteria: %w", err)
	}
	if err := json.Unmarshal(writeScope, &value.WriteScope); err != nil {
		return domain.Task{}, fmt.Errorf("decode task write scope: %w", err)
	}
	if err := json.Unmarshal(commands, &value.VerificationCommands); err != nil {
		return domain.Task{}, fmt.Errorf("decode task verification commands: %w", err)
	}
	return value, nil
}

const planRunColumns = `id, plan_id, status, workflow_id, temporal_run_id,
idempotency_key, max_parallel_tasks, error, created_at, started_at, paused_at,
completed_at, updated_at`

func scanPlanRun(row rowScanner) (domain.PlanRun, error) {
	var value domain.PlanRun
	err := row.Scan(
		&value.ID, &value.PlanID, &value.Status, &value.WorkflowID, &value.TemporalRunID,
		&value.IdempotencyKey, &value.MaxParallelTasks, &value.Error, &value.CreatedAt,
		&value.StartedAt, &value.PausedAt, &value.CompletedAt, &value.UpdatedAt,
	)
	return value, err
}

const approvalColumns = `id, resource_type, resource_id, action, status,
requested_at, decided_at, decided_by, comment`

func scanApproval(row rowScanner) (domain.Approval, error) {
	var value domain.Approval
	err := row.Scan(
		&value.ID, &value.ResourceType, &value.ResourceID, &value.Action, &value.Status,
		&value.RequestedAt, &value.DecidedAt, &value.DecidedBy, &value.Comment,
	)
	return value, err
}
