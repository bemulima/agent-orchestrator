package activities

import (
	"context"
	"errors"
	"fmt"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"testing"
)

func TestLifecycleRetryClassification(t *testing.T) {
	for _, message := range []string{"SDK_PRIVATE_STATE_BYTES_EXCEEDED", "PUBLICATION_SCOPE_DENIED", "DOCKER_CONTAINER_STATE_UNKNOWN", "SOURCE_PRECONDITION_CHANGED"} {
		var app *temporal.ApplicationError
		require.True(t, errors.As(lifecycleActivityError(context.Background(), fmt.Errorf("%s", message)), &app))
		require.True(t, app.NonRetryable())
	}
	var app *temporal.ApplicationError
	require.True(t, errors.As(lifecycleActivityError(context.Background(), domain.ErrTransient), &app))
	require.False(t, app.NonRetryable())
	require.True(t, temporal.IsCanceledError(lifecycleActivityError(context.Background(), context.Canceled)))
}
