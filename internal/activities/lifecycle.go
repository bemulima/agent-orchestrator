package activities

import (
	"context"
	"errors"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"go.temporal.io/sdk/temporal"
)

// Only explicitly transient infrastructure is eligible for automatic retry.
// Unknown runtime state must reconcile first, never retry into a late success.
func lifecycleActivityError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return temporal.NewCanceledError("CANCELLED")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return temporal.NewNonRetryableApplicationError("TIMED_OUT", "TIMED_OUT", err)
	}
	message := strings.ToUpper(err.Error())
	if strings.Contains(message, "TIMEOUT") {
		return temporal.NewNonRetryableApplicationError("TIMED_OUT", "TIMED_OUT", err)
	}
	kind := "NON_RETRYABLE_POLICY"
	if strings.Contains(message, "UNKNOWN") || strings.Contains(message, "CLEANUP") {
		kind = "UNKNOWN_RUNTIME"
	} else if strings.Contains(message, "LIMIT") || strings.Contains(message, "EXCEEDED") || strings.Contains(message, "DENIED") || strings.Contains(message, "MISMATCH") {
		kind = "SECURITY_FAILURE"
	} else if errors.Is(err, domain.ErrTransient) {
		return temporal.NewApplicationErrorWithCause("RETRYABLE_INFRASTRUCTURE", "RETRYABLE_INFRASTRUCTURE", err)
	}
	return temporal.NewNonRetryableApplicationError(kind, kind, err)
}
