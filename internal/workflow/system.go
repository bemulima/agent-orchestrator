package workflow

import (
	"time"

	"go.temporal.io/sdk/temporal"
	temporalworkflow "go.temporal.io/sdk/workflow"

	"github.com/example/course-dev-orchestrator/internal/activities"
)

// SystemProbeWorkflow exercises durable workflow/activity execution for
// bootstrap verification.
func SystemProbeWorkflow(ctx temporalworkflow.Context, input activities.SystemProbeInput) (activities.SystemProbeOutput, error) {
	ctx = temporalworkflow.WithActivityOptions(ctx, temporalworkflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    5 * time.Second,
			MaximumAttempts:    3,
		},
	})

	var output activities.SystemProbeOutput
	err := temporalworkflow.ExecuteActivity(ctx, "SystemProbe", input).Get(ctx, &output)
	return output, err
}
