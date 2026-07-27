package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type AgentUsageRepoPG struct{ Pool *pgxpool.Pool }

func (r AgentUsageRepoPG) RecordAgentRun(ctx context.Context, value domain.AgentRunUsage) error {
	var resourceType any
	var resourceID any
	var completedAt any
	if value.ResourceType != "" && value.ResourceID != "" {
		resourceType = value.ResourceType
		resourceID = value.ResourceID
	}
	if !value.CompletedAt.IsZero() {
		completedAt = value.CompletedAt
	}
	_, err := r.Pool.Exec(ctx, `
INSERT INTO agent_run_usage (
    id, role, model, reasoning_effort, thread_id, resource_type, resource_id,
    route_reason, status, input_tokens, cached_input_tokens, output_tokens,
    reasoning_output_tokens, duration_ms, started_at, completed_at
) VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
ON CONFLICT (id) DO UPDATE SET
    thread_id = EXCLUDED.thread_id, status = EXCLUDED.status,
    input_tokens = EXCLUDED.input_tokens, cached_input_tokens = EXCLUDED.cached_input_tokens,
    output_tokens = EXCLUDED.output_tokens, reasoning_output_tokens = EXCLUDED.reasoning_output_tokens,
    duration_ms = EXCLUDED.duration_ms, completed_at = EXCLUDED.completed_at`,
		value.ID, value.Role, value.Model, value.ReasoningEffort, value.ThreadID, resourceType, resourceID,
		value.RouteReason, value.Status, value.InputTokens, value.CachedInputTokens,
		value.OutputTokens, value.ReasoningOutputTokens, value.DurationMilliseconds,
		value.StartedAt, completedAt)
	return err
}

func (r AgentUsageRepoPG) AgentUsageWindow(ctx context.Context, since time.Time) (domain.AgentUsageWindow, error) {
	result := domain.AgentUsageWindow{Since: since, ByModel: []domain.AgentUsageBreakdown{}, ByRole: []domain.AgentUsageBreakdown{}}
	if err := r.Pool.QueryRow(ctx, `
SELECT count(*), count(*) FILTER (WHERE status = 'failed')
FROM agent_run_usage WHERE started_at >= $1 AND status <> 'denied'`, since).Scan(&result.Runs, &result.Failed); err != nil {
		return domain.AgentUsageWindow{}, err
	}
	byModel, err := r.breakdown(ctx, since, "model")
	if err != nil {
		return domain.AgentUsageWindow{}, err
	}
	byRole, err := r.breakdown(ctx, since, "role")
	if err != nil {
		return domain.AgentUsageWindow{}, err
	}
	result.ByModel, result.ByRole = byModel, byRole
	return result, nil
}

func (r AgentUsageRepoPG) breakdown(ctx context.Context, since time.Time, column string) ([]domain.AgentUsageBreakdown, error) {
	if column != "model" && column != "role" {
		panic("unsupported agent usage breakdown")
	}
	rows, err := r.Pool.Query(ctx, `
SELECT `+column+`, count(*), count(*) FILTER (WHERE status = 'failed'),
       coalesce(sum(input_tokens),0), coalesce(sum(cached_input_tokens),0),
       coalesce(sum(output_tokens),0), coalesce(sum(reasoning_output_tokens),0)
FROM agent_run_usage WHERE started_at >= $1 AND status <> 'denied'
GROUP BY `+column+` ORDER BY count(*) DESC, `+column, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.AgentUsageBreakdown{}
	for rows.Next() {
		var item domain.AgentUsageBreakdown
		if err := rows.Scan(&item.Key, &item.Runs, &item.FailedRuns, &item.InputTokens,
			&item.CachedInputTokens, &item.OutputTokens, &item.ReasoningOutputTokens); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
