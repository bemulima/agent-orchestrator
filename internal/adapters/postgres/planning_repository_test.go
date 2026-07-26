package postgres

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestNextPlanRetryWorkflowID(t *testing.T) {
	plan := domain.Plan{ID: "plan-id", Version: 4}

	first, err := nextPlanRetryWorkflowID(plan, "plan-plan-id-v4")
	require.NoError(t, err)
	require.Equal(t, "plan-plan-id-v4-retry-2", first)

	second, err := nextPlanRetryWorkflowID(plan, first)
	require.NoError(t, err)
	require.Equal(t, "plan-plan-id-v4-retry-3", second)

	_, err = nextPlanRetryWorkflowID(plan, "another-workflow")
	require.ErrorIs(t, err, domain.ErrConflict)
	require.True(t, errors.Is(err, domain.ErrConflict))
}
