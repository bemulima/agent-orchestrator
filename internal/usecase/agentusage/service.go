package agentusage

import (
	"context"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type Service struct {
	Usage        repository.AgentUsageRepository
	Now          func() time.Time
	BudgetMode   string
	DeepModel    string
	DeepRunLimit int64
	XHighAllowed bool
	Routing      domain.AgentRoutingPolicy
}

func (s Service) Dashboard(ctx context.Context) (domain.AgentUsageDashboard, error) {
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	windows := []time.Duration{5 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour}
	values := make([]domain.AgentUsageWindow, len(windows))
	for index, window := range windows {
		value, err := s.Usage.AgentUsageWindow(ctx, now.Add(-window))
		if err != nil {
			return domain.AgentUsageDashboard{}, err
		}
		values[index] = value
	}
	deepRuns := runsFor(values[0].ByModel, s.DeepModel)
	utilization := int64(0)
	if s.DeepRunLimit > 0 {
		utilization = deepRuns * 100 / s.DeepRunLimit
	}
	return domain.AgentUsageDashboard{
		GeneratedAt: now, FiveHours: values[0], SevenDays: values[1], ThirtyDays: values[2],
		Budget: domain.AgentBudgetState{Mode: s.BudgetMode, DeepModel: s.DeepModel, DeepRunsFiveHour: deepRuns,
			DeepRunLimit: s.DeepRunLimit, Utilization: utilization, XHighAllowed: s.XHighAllowed},
		Routing: s.Routing,
	}, nil
}

func runsFor(items []domain.AgentUsageBreakdown, key string) int64 {
	for _, item := range items {
		if item.Key == key {
			return item.Runs
		}
	}
	return 0
}
