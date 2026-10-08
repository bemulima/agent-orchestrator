package repository

import (
	"context"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type ArchitecturalShardRepository interface {
	SaveArchitecturalShards(context.Context, string, string, []domain.ArchitecturalShard) error
	ListArchitecturalShards(context.Context, string) ([]domain.ArchitecturalShard, error)
}

type ContractBaselineRepository interface {
	SaveContractBaseline(context.Context, domain.ContractBaseline) error
	// ListContractBaselines returns only the latest version per Task, including a
	// retired latest version until its replacement has been persisted.
	ListContractBaselines(context.Context, string) ([]domain.ContractBaseline, error)
}

type FanoutReadinessRepository interface {
	SaveFanoutReadiness(context.Context, domain.FanoutReadinessEvidence) error
	GetFanoutReadiness(context.Context, string) (domain.FanoutReadinessEvidence, error)
}
