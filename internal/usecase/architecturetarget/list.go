package architecturetarget

import (
	"context"
	"fmt"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// listStore is intentionally read-only. TARGET listing is separate from the
// editable proposal policy and never consults or changes CURRENT.
type listStore interface {
	List(context.Context, []domain.ArchitectureTargetStatus) ([]domain.ArchitectureTargetProposal, error)
}

// List returns deterministic repository order for TARGET proposals. Status
// filters are validated by the persistence boundary so HTTP callers cannot
// reinterpret lifecycle values locally.
type List struct{ Store listStore }

func (uc List) Handle(ctx context.Context, statuses []domain.ArchitectureTargetStatus) ([]domain.ArchitectureTargetProposal, error) {
	if uc.Store == nil {
		return nil, fmt.Errorf("target proposal list store is not configured: %w", domain.ErrValidation)
	}
	return uc.Store.List(ctx, statuses)
}
