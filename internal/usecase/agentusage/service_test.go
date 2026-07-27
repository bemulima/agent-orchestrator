package agentusage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type usageRepoFake struct {
	windows []domain.AgentUsageWindow
	records []domain.AgentRunUsage
}

func (r *usageRepoFake) RecordAgentRun(_ context.Context, value domain.AgentRunUsage) error {
	for index := range r.records {
		if r.records[index].ID == value.ID {
			r.records[index] = value
			return nil
		}
	}
	r.records = append(r.records, value)
	return nil
}
func (r *usageRepoFake) AgentUsageWindow(_ context.Context, _ time.Time) (domain.AgentUsageWindow, error) {
	if len(r.windows) == 0 {
		return domain.AgentUsageWindow{}, nil
	}
	value := r.windows[0]
	r.windows = r.windows[1:]
	return value, nil
}

type runnerFake struct{ calls int }

func (r *runnerFake) Run(_ context.Context, request domain.AgentRunRequest, callback repository.AgentThreadCallback) (domain.AgentRunResponse, error) {
	r.calls++
	if callback != nil {
		_ = callback(context.Background(), "thread")
	}
	return domain.AgentRunResponse{ThreadID: "thread", Usage: domain.AgentTokenUsage{InputTokens: 10, OutputTokens: 3}}, nil
}

func TestDashboardCalculatesDeepModelBudget(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	repo := &usageRepoFake{windows: []domain.AgentUsageWindow{{ByModel: []domain.AgentUsageBreakdown{{Key: "sol", Runs: 5}}}, {}, {}}}
	result, err := (Service{Usage: repo, Now: func() time.Time { return now }, BudgetMode: "enforce", DeepModel: "sol", DeepRunLimit: 20}).Dashboard(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 25, result.Budget.Utilization)
}

func TestTrackedRunnerRecordsUsageWithoutPrompts(t *testing.T) {
	repo, base := &usageRepoFake{}, &runnerFake{}
	runner := TrackedRunner{Base: base, Usage: repo, DeepModel: "sol", DeepRunLimit: 20}
	_, err := runner.Run(context.Background(), domain.AgentRunRequest{Role: domain.AgentRunCoder, Model: "spark", ReasoningEffort: "low", Prompt: "secret prompt", UsageContext: &domain.AgentUsageContext{ResourceType: "task", ResourceID: "00000000-0000-0000-0000-000000000001", RouteReason: "cheap"}}, nil)
	require.NoError(t, err)
	require.Len(t, repo.records, 1)
	require.EqualValues(t, 10, repo.records[0].InputTokens)
	require.NotContains(t, repo.records[0].RouteReason, "secret prompt")
}

func TestTrackedRunnerBlocksXHighAndExhaustedDeepBudget(t *testing.T) {
	repo, base := &usageRepoFake{}, &runnerFake{}
	runner := TrackedRunner{Base: base, Usage: repo, BudgetMode: "enforce", DeepModel: "sol", DeepRunLimit: 1}
	_, err := runner.Run(context.Background(), domain.AgentRunRequest{Role: domain.AgentRunPlanner, Model: "sol", ReasoningEffort: "xhigh"}, nil)
	require.True(t, errors.Is(err, domain.ErrApprovalNeeded))
	require.Zero(t, base.calls)
	// The reserved running row is included in the database window, so two runs
	// means the configured limit of one has already been exceeded.
	repo.windows = []domain.AgentUsageWindow{{ByModel: []domain.AgentUsageBreakdown{{Key: "sol", Runs: 2}}}}
	_, err = runner.Run(context.Background(), domain.AgentRunRequest{Role: domain.AgentRunPlanner, Model: "sol", ReasoningEffort: "high"}, nil)
	require.True(t, errors.Is(err, domain.ErrApprovalNeeded))
	require.Zero(t, base.calls)
}
