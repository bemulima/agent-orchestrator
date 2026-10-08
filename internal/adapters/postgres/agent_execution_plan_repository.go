package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type AgentExecutionPlanRepoPG struct {
	Pool *pgxpool.Pool
}

func (r AgentExecutionPlanRepoPG) SaveArchitecturalShards(ctx context.Context, planID, taskID string, shards []domain.ArchitecturalShard) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin architectural shard save: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "architectural-shards:"+planID+":"+taskID); err != nil {
		return fmt.Errorf("lock architectural shard plan: %w", err)
	}
	// Lock existing shard keys before checking history. A concurrent attempt insert
	// must acquire its FK key lock and cannot race deletion of an obsolete shard.
	rows, err := tx.Query(ctx, `SELECT id FROM architectural_shard WHERE plan_id=$1 AND task_id=$2 FOR UPDATE`, planID, taskID)
	if err != nil {
		return mapPlanningError(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return mapPlanningError(err)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return mapPlanningError(err)
	}
	ids := shardIDs(shards)
	var hasObsoleteHistory bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
    SELECT 1 FROM architectural_shard shard JOIN shard_attempt attempt ON attempt.shard_id=shard.id
    WHERE shard.plan_id=$1 AND shard.task_id=$2 AND NOT (shard.id=ANY($3::uuid[]))
)`, planID, taskID, ids).Scan(&hasObsoleteHistory); err != nil {
		return mapPlanningError(err)
	}
	if hasObsoleteHistory {
		return fmt.Errorf("cannot remove architectural shards with attempt history: %w", domain.ErrConflict)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM architectural_shard WHERE plan_id=$1 AND task_id=$2 AND NOT (id=ANY($3::uuid[]))`, planID, taskID, ids); err != nil {
		return mapPlanningError(err)
	}
	for _, shard := range shards {
		if shard.PlanID != planID || shard.TaskID != taskID {
			return fmt.Errorf("architectural shard scope changed: %w", domain.ErrValidation)
		}
		payload, marshalErr := json.Marshal(shard)
		if marshalErr != nil {
			return fmt.Errorf("marshal architectural shard: %w", marshalErr)
		}
		tag, err := tx.Exec(ctx, `
INSERT INTO architectural_shard (
    id, plan_id, task_id, repository_project_id, profile_id, profile_fingerprint,
    route_id, status, payload, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (id) DO UPDATE SET
    profile_id=EXCLUDED.profile_id, profile_fingerprint=EXCLUDED.profile_fingerprint,
    status=EXCLUDED.status, payload=EXCLUDED.payload
WHERE architectural_shard.plan_id=EXCLUDED.plan_id
  AND architectural_shard.task_id=EXCLUDED.task_id
  AND architectural_shard.repository_project_id=EXCLUDED.repository_project_id
  AND architectural_shard.route_id=EXCLUDED.route_id`,
			shard.ID, planID, taskID, shard.RepositoryProjectID, shard.ProfileID,
			shard.ProfileFingerprint, shard.RouteID, shard.Status, payload, shard.CreatedAt)
		if err != nil {
			return mapPlanningError(err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("architectural shard identity changed: %w", domain.ErrConflict)
		}
	}
	if len(shards) > 0 {
		if err := insertResourceAuditTx(ctx, tx, "plan", "plan.architectural_shards_prepared", planID, map[string]any{
			"task_id": taskID, "shard_ids": shardIDs(shards),
		}); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit architectural shards: %w", err)
	}
	return nil
}

func (r AgentExecutionPlanRepoPG) ListArchitecturalShards(ctx context.Context, planID string) ([]domain.ArchitecturalShard, error) {
	rows, err := r.Pool.Query(ctx, `SELECT payload FROM architectural_shard WHERE plan_id = $1 ORDER BY repository_project_id, task_id, route_id`, planID)
	if err != nil {
		return nil, mapPlanningError(err)
	}
	defer rows.Close()
	var result []domain.ArchitecturalShard
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan architectural shard: %w", err)
		}
		var shard domain.ArchitecturalShard
		if err := json.Unmarshal(payload, &shard); err != nil {
			return nil, fmt.Errorf("decode architectural shard: %w", err)
		}
		result = append(result, shard)
	}
	return result, rows.Err()
}

func (r AgentExecutionPlanRepoPG) SaveContractBaseline(ctx context.Context, baseline domain.ContractBaseline) error {
	payload, err := json.Marshal(baseline)
	if err != nil {
		return fmt.Errorf("marshal contract baseline: %w", err)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin contract baseline save: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
INSERT INTO contract_baseline (
    id, plan_id, task_id, repository_project_id, profile_id,
    planning_contract_state, execution_state, payload, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (id) DO UPDATE SET
    profile_id = EXCLUDED.profile_id,
    planning_contract_state = EXCLUDED.planning_contract_state,
    execution_state = EXCLUDED.execution_state,
    payload = EXCLUDED.payload,
    updated_at = EXCLUDED.updated_at
WHERE contract_baseline.execution_state <> 'INVALIDATED'
  AND contract_baseline.plan_id = EXCLUDED.plan_id
  AND contract_baseline.task_id = EXCLUDED.task_id
  AND contract_baseline.repository_project_id = EXCLUDED.repository_project_id`, baseline.ID, baseline.PlanID, baseline.TaskID,
		baseline.RepositoryProjectID, baseline.ProfileID, baseline.PlanningContractState,
		baseline.ExecutionState, payload, baseline.CreatedAt, baseline.UpdatedAt)
	if err != nil {
		return mapPlanningError(err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("contract baseline is retired or identity changed: %w", domain.ErrInvalidStatus)
	}
	if err := insertResourceAuditTx(ctx, tx, "plan", "plan.contract_baseline_"+string(baseline.ExecutionState), baseline.PlanID, map[string]any{
		"task_id": baseline.TaskID, "repository_project_id": baseline.RepositoryProjectID,
		"baseline_id": baseline.ID, "state": baseline.ExecutionState,
		"contract_plan_fingerprint": baseline.ContractPlanFingerprint,
		"profile_fingerprint":       baseline.ProfileFingerprint,
		"validation_passed":         baseline.Validation.Passed,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit contract baseline: %w", err)
	}
	return nil
}

func (r AgentExecutionPlanRepoPG) ListContractBaselines(ctx context.Context, planID string) ([]domain.ContractBaseline, error) {
	rows, err := r.Pool.Query(ctx, `SELECT payload FROM (
    SELECT DISTINCT ON (task_id) task_id, repository_project_id, payload
    FROM contract_baseline WHERE plan_id = $1
    ORDER BY task_id, generation DESC
) latest ORDER BY repository_project_id, task_id`, planID)
	if err != nil {
		return nil, mapPlanningError(err)
	}
	defer rows.Close()
	var result []domain.ContractBaseline
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan contract baseline: %w", err)
		}
		var baseline domain.ContractBaseline
		if err := json.Unmarshal(payload, &baseline); err != nil {
			return nil, fmt.Errorf("decode contract baseline: %w", err)
		}
		result = append(result, baseline)
	}
	return result, rows.Err()
}

// GetContractBaseline retrieves an exact historical identity, including retired versions.
func (r AgentExecutionPlanRepoPG) GetContractBaseline(ctx context.Context, baselineID string) (domain.ContractBaseline, error) {
	var payload []byte
	if err := r.Pool.QueryRow(ctx, `SELECT payload FROM contract_baseline WHERE id = $1`, baselineID).Scan(&payload); err != nil {
		return domain.ContractBaseline{}, mapPlanningError(err)
	}
	var baseline domain.ContractBaseline
	if err := json.Unmarshal(payload, &baseline); err != nil {
		return domain.ContractBaseline{}, fmt.Errorf("decode contract baseline: %w", err)
	}
	return baseline, nil
}

func (r AgentExecutionPlanRepoPG) SaveFanoutReadiness(ctx context.Context, evidence domain.FanoutReadinessEvidence) error {
	payload, err := json.Marshal(evidence)
	if err != nil {
		return fmt.Errorf("marshal fan-out readiness evidence: %w", err)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin readiness save: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
INSERT INTO fanout_readiness (plan_id, state, parallelism_scope, payload, recorded_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (plan_id) DO UPDATE SET state = EXCLUDED.state,
    parallelism_scope = EXCLUDED.parallelism_scope,
    payload = EXCLUDED.payload, recorded_at = EXCLUDED.recorded_at`,
		evidence.PlanID, evidence.State, evidence.ParallelismScope, payload, evidence.RecordedAt); err != nil {
		return mapPlanningError(err)
	}
	if err := insertResourceAuditTx(ctx, tx, "plan", "plan.fanout_readiness_"+string(evidence.State), evidence.PlanID, map[string]any{
		"state": evidence.State, "parallelism_scope": evidence.ParallelismScope, "reasons": evidence.Reasons,
		"task_ids": evidence.TaskIDs, "shard_ids": evidence.ShardIDs,
		"baseline_ids": evidence.BaselineIDs,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit fan-out readiness: %w", err)
	}
	return nil
}

func (r AgentExecutionPlanRepoPG) GetFanoutReadiness(ctx context.Context, planID string) (domain.FanoutReadinessEvidence, error) {
	var payload []byte
	err := r.Pool.QueryRow(ctx, `SELECT payload FROM fanout_readiness WHERE plan_id = $1`, planID).Scan(&payload)
	if err != nil {
		return domain.FanoutReadinessEvidence{}, mapPlanningError(err)
	}
	var evidence domain.FanoutReadinessEvidence
	if err := json.Unmarshal(payload, &evidence); err != nil {
		return domain.FanoutReadinessEvidence{}, fmt.Errorf("decode fan-out readiness evidence: %w", err)
	}
	return evidence, nil
}

func shardIDs(shards []domain.ArchitecturalShard) []string {
	result := make([]string, 0, len(shards))
	for _, shard := range shards {
		result = append(result, shard.ID)
	}
	return result
}
