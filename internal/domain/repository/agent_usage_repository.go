package repository

import (
	"context"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type AgentUsageRepository interface {
	RecordAgentRun(context.Context, domain.AgentRunUsage) error
	AgentUsageWindow(context.Context, time.Time) (domain.AgentUsageWindow, error)
}
