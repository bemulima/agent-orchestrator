package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func (r AgentExecutionPlanRepoPG) SaveShardAttempt(ctx context.Context, attempt domain.ShardAttempt) error {
	if attempt.ID == "" || attempt.PlanID == "" || attempt.TaskID == "" || attempt.ShardID == "" ||
		attempt.AttemptNumber < 1 || attempt.BaselineCommit == "" || attempt.WorkerStartedAt.IsZero() {
		return fmt.Errorf("incomplete shard attempt identity or start evidence: %w", domain.ErrValidation)
	}
	payload, err := json.Marshal(attempt)
	if err != nil {
		return fmt.Errorf("marshal shard attempt: %w", err)
	}
	var finished any
	if attempt.FinishedAt != nil {
		finished = *attempt.FinishedAt
	}
	command, err := r.Pool.Exec(ctx, `
INSERT INTO shard_attempt (
    id, plan_id, task_id, shard_id, attempt_number, baseline_commit,
    worker_thread_id, model, reasoning_effort, status, worker_started_at,
    worker_finished_at, payload, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
ON CONFLICT (shard_id, attempt_number) DO UPDATE SET
    worker_thread_id = EXCLUDED.worker_thread_id,
    model = EXCLUDED.model,
    reasoning_effort = EXCLUDED.reasoning_effort,
    status = EXCLUDED.status,
    worker_finished_at = EXCLUDED.worker_finished_at,
    payload = EXCLUDED.payload,
    updated_at = EXCLUDED.updated_at
WHERE shard_attempt.id = EXCLUDED.id
  AND shard_attempt.baseline_commit = EXCLUDED.baseline_commit
  AND shard_attempt.payload->'work_package' = EXCLUDED.payload->'work_package'
  AND shard_attempt.worker_started_at = EXCLUDED.worker_started_at`,
		attempt.ID, attempt.PlanID, attempt.TaskID, attempt.ShardID, attempt.AttemptNumber,
		attempt.BaselineCommit, attempt.WorkerThreadID, attempt.Model, attempt.ReasoningEffort,
		attempt.Status, attempt.WorkerStartedAt, finished, payload, attempt.CreatedAt, attempt.UpdatedAt)
	if err != nil {
		return mapPlanningError(err)
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("shard attempt identity, package, or baseline changed on retry: %w", domain.ErrConflict)
	}
	if err := recordShardAudit(ctx, r.Pool, "plan.shard_attempt_"+strings.ToLower(string(attempt.Status)), attempt.PlanID, map[string]any{
		"task_id": attempt.TaskID, "shard_id": attempt.ShardID, "attempt_id": attempt.ID,
		"baseline_commit": attempt.BaselineCommit, "status": attempt.Status,
	}); err != nil {
		return err
	}
	return nil
}

func (r AgentExecutionPlanRepoPG) ListShardAttempts(ctx context.Context, planID string) ([]domain.ShardAttempt, error) {
	rows, err := r.Pool.Query(ctx, `SELECT payload FROM shard_attempt WHERE plan_id=$1 ORDER BY shard_id,attempt_number`, planID)
	if err != nil {
		return nil, mapPlanningError(err)
	}
	defer rows.Close()
	result := []domain.ShardAttempt{}
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan shard attempt: %w", err)
		}
		var attempt domain.ShardAttempt
		if err := json.Unmarshal(payload, &attempt); err != nil {
			return nil, fmt.Errorf("decode shard attempt: %w", err)
		}
		result = append(result, attempt)
	}
	return result, rows.Err()
}

func (r AgentExecutionPlanRepoPG) SaveShardFanout(ctx context.Context, execution domain.ShardFanoutExecution) error {
	if execution.PlanID == "" || execution.ContractBaselineID == "" || len(execution.BaselineCommit) != 40 {
		return fmt.Errorf("incomplete shard fan-out identity: %w", domain.ErrValidation)
	}
	payload, err := json.Marshal(execution)
	if err != nil {
		return fmt.Errorf("marshal shard fan-out execution: %w", err)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin shard fanout save: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "shard-fanout:"+execution.PlanID); err != nil {
		return fmt.Errorf("lock shard fanout: %w", err)
	}
	// Keep the exact prior projection in durable audit before superseding it.
	var priorPayload []byte
	var priorBaselineID string
	rows, err := tx.Query(ctx, `SELECT contract_baseline_id::text, payload FROM shard_fanout_execution WHERE plan_id=$1 FOR UPDATE`, execution.PlanID)
	if err != nil {
		return mapPlanningError(err)
	}
	if rows.Next() {
		if err := rows.Scan(&priorBaselineID, &priorPayload); err != nil {
			rows.Close()
			return mapPlanningError(err)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return mapPlanningError(err)
	}
	if priorBaselineID != "" && (priorBaselineID != execution.ContractBaselineID || (execution.State == "FANOUT_RUNNING" && len(priorPayload) > 0)) {
		if err := insertResourceAuditTx(ctx, tx, "plan", "plan.shard_fanout_superseded", execution.PlanID, map[string]any{
			"prior_execution": json.RawMessage(priorPayload), "replacement_baseline_id": execution.ContractBaselineID,
		}); err != nil {
			return err
		}
	}
	command, err := tx.Exec(ctx, `
INSERT INTO shard_fanout_execution (
    plan_id, contract_baseline_id, baseline_commit, state, barrier_state,
    composition_state, payload, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (plan_id) DO UPDATE SET
    contract_baseline_id=EXCLUDED.contract_baseline_id,
    baseline_commit=EXCLUDED.baseline_commit,
    state=EXCLUDED.state,
    barrier_state=EXCLUDED.barrier_state,
    composition_state=EXCLUDED.composition_state,
    payload=EXCLUDED.payload,
    updated_at=EXCLUDED.updated_at
WHERE (shard_fanout_execution.contract_baseline_id=EXCLUDED.contract_baseline_id
  AND shard_fanout_execution.baseline_commit=EXCLUDED.baseline_commit)
OR (
    shard_fanout_execution.state IN ('BARRIER_BLOCKED', 'COMPOSITION_BLOCKED',
        'INTEGRATION_CONFLICT', 'INTEGRATION_VERIFICATION_FAILED', 'INTEGRATION_REJECTED',
        'REPLAN_REQUIRED', 'INTEGRATION_VERIFIED')
    AND EXISTS (
        SELECT 1 FROM contract_baseline prior JOIN contract_baseline replacement
          ON replacement.plan_id=prior.plan_id AND replacement.task_id=prior.task_id
          AND replacement.repository_project_id=prior.repository_project_id
        WHERE prior.id=shard_fanout_execution.contract_baseline_id
          AND prior.execution_state='INVALIDATED'
          AND replacement.id=EXCLUDED.contract_baseline_id
          AND replacement.plan_id=EXCLUDED.plan_id AND replacement.execution_state='FROZEN'
          AND replacement.payload->>'contract_baseline_commit'=EXCLUDED.baseline_commit
    )
    AND NOT EXISTS (
        SELECT 1 FROM shard_attempt WHERE plan_id=EXCLUDED.plan_id
          AND (status='RUNNING' OR worker_finished_at IS NULL)
    )
)`,
		execution.PlanID, execution.ContractBaselineID, execution.BaselineCommit, execution.State,
		execution.Barrier, execution.Composition, payload, execution.CreatedAt, execution.UpdatedAt)
	if err != nil {
		return mapPlanningError(err)
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("fan-out baseline identity changed: %w", domain.ErrConflict)
	}
	if err := insertResourceAuditTx(ctx, tx, "plan", "plan.shard_fanout_"+strings.ToLower(execution.State), execution.PlanID, map[string]any{
		"baseline_id": execution.ContractBaselineID, "baseline_commit": execution.BaselineCommit,
		"barrier": execution.Barrier, "composition": execution.Composition,
		"reasons": execution.BarrierReasons,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit shard fanout: %w", err)
	}
	return nil
}

func (r AgentExecutionPlanRepoPG) GetShardFanout(ctx context.Context, planID string) (domain.ShardFanoutExecution, error) {
	var payload []byte
	err := r.Pool.QueryRow(ctx, `SELECT payload FROM shard_fanout_execution WHERE plan_id=$1`, planID).Scan(&payload)
	if err != nil {
		return domain.ShardFanoutExecution{}, mapPlanningError(err)
	}
	var result domain.ShardFanoutExecution
	if err := json.Unmarshal(payload, &result); err != nil {
		return domain.ShardFanoutExecution{}, fmt.Errorf("decode shard fan-out execution: %w", err)
	}
	history, err := r.ListShardAttempts(ctx, planID)
	if err != nil {
		return domain.ShardFanoutExecution{}, err
	}
	result.Attempts, err = currentFanoutAttempts(history, result.ContractBaselineID, result.BaselineCommit)
	return result, err
}

// Current execution views use one attempt per shard. Full history remains
// available through ListShardAttempts for audit and monotonic retry numbering.
func currentFanoutAttempts(history []domain.ShardAttempt, baselineID, commit string) ([]domain.ShardAttempt, error) {
	latest := map[string]domain.ShardAttempt{}
	for _, attempt := range history {
		if attempt.BaselineCommit != commit {
			if attempt.Status == domain.ShardAttemptRunning || attempt.FinishedAt == nil {
				return nil, fmt.Errorf("unfinished historical shard attempt has another baseline: %w", domain.ErrConflict)
			}
			continue
		}
		if previous, ok := latest[attempt.ShardID]; !ok || attempt.AttemptNumber > previous.AttemptNumber {
			latest[attempt.ShardID] = attempt
		}
	}
	result := make([]domain.ShardAttempt, 0, len(latest))
	for _, attempt := range latest {
		base := attempt.WorkPackage.ExecutionBase
		if base.Revision != commit || base.ContractBaselineID != baselineID || base.Kind != domain.ShardBaseContractBaseline {
			return nil, fmt.Errorf("current shard attempt work package has another baseline: %w", domain.ErrConflict)
		}
		result = append(result, attempt)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ShardID < result[j].ShardID })
	return result, nil
}

func recordShardAudit(ctx context.Context, pool *pgxpool.Pool, action, resourceID string, payload map[string]any) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin shard execution audit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := insertResourceAuditTx(ctx, tx, "plan", action, resourceID, payload); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit shard execution audit: %w", err)
	}
	return nil
}
