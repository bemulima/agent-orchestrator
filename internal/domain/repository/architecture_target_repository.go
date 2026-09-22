package repository

import (
	"context"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// ArchitectureTargetRepository persists editable TARGET proposals separately
// from the immutable CURRENT catalog. expectedRevision is mandatory for every
// mutation and protects an owner decision from overwriting a newer draft.
type ArchitectureTargetRepository interface {
	Create(context.Context, domain.ArchitectureTargetProposal) (domain.ArchitectureTargetProposal, error)
	Get(context.Context, string) (domain.ArchitectureTargetProposal, error)
	List(context.Context, []domain.ArchitectureTargetStatus) ([]domain.ArchitectureTargetProposal, error)
	UpdateDraft(context.Context, domain.ArchitectureTargetProposal, int) (domain.ArchitectureTargetProposal, error)
	Transition(
		context.Context,
		string,
		domain.ArchitectureTargetStatus,
		int,
		string,
		string,
	) (domain.ArchitectureTargetProposal, error)
}
