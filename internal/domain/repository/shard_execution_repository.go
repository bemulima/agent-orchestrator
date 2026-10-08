package repository

import (
	"context"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type ShardExecutionRepository interface {
	GetShardFanout(context.Context, string) (domain.ShardFanoutExecution, error)
	SaveShardAttempt(context.Context, domain.ShardAttempt) error
	ListShardAttempts(context.Context, string) ([]domain.ShardAttempt, error)
	SaveShardFanout(context.Context, domain.ShardFanoutExecution) error
}
