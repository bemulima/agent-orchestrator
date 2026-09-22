package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestMapArchitectureTargetError(t *testing.T) {
	require.ErrorIs(t, mapArchitectureTargetError(pgx.ErrNoRows), domain.ErrNotFound)
	require.Nil(t, mapArchitectureTargetError(nil))
	require.False(t, errors.Is(mapArchitectureTargetError(errors.New("other")), domain.ErrNotFound))
}

func TestArchitectureTargetStatuses(t *testing.T) {
	require.True(t, isArchitectureTargetStatus(domain.ArchitectureTargetStatusChangesRequested))
	require.False(t, isArchitectureTargetStatus("current"))
}
