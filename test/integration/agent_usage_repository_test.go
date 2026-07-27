//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	pgadapter "github.com/bemulima/agent-orchestrator/internal/adapters/postgres"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestAgentUsageRepositoryRecordsAndAggregates(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := pgadapter.AgentUsageRepoPG{Pool: pool}
	now := time.Now().UTC()
	model := "fixture-economic-model"
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM agent_run_usage WHERE model = $1`, model) }()
	for index, role := range []domain.AgentRunRole{domain.AgentRunCoder, domain.AgentRunReviewer} {
		status := "completed"
		if index == 1 {
			status = "failed"
		}
		err := repo.RecordAgentRun(ctx, domain.AgentRunUsage{
			ID: uuid.NewString(), Role: role, Model: model, ReasoningEffort: "low", RouteReason: "integration fixture", Status: status,
			InputTokens: 100, CachedInputTokens: 20, OutputTokens: 30, ReasoningOutputTokens: 10,
			DurationMilliseconds: 50, StartedAt: now.Add(time.Duration(index) * time.Second), CompletedAt: now.Add(time.Duration(index)*time.Second + 50*time.Millisecond),
		})
		if err != nil {
			t.Fatalf("RecordAgentRun() error = %v", err)
		}
	}
	window, err := repo.AgentUsageWindow(ctx, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("AgentUsageWindow() error = %v", err)
	}
	var found domain.AgentUsageBreakdown
	for _, item := range window.ByModel {
		if item.Key == model {
			found = item
		}
	}
	if found.Runs != 2 || found.FailedRuns != 1 || found.InputTokens != 200 || found.ReasoningOutputTokens != 20 {
		t.Fatalf("model breakdown = %#v", found)
	}
}
