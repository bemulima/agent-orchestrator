package activities

import (
	"context"
	"time"
)

type SystemProbeInput struct {
	RequestID string `json:"request_id"`
}

type SystemProbeOutput struct {
	RequestID  string    `json:"request_id"`
	ObservedAt time.Time `json:"observed_at"`
	Status     string    `json:"status"`
}

// SystemActivities contains side-effecting operations used by system
// workflows. Business activities are added in later implementation stages.
type SystemActivities struct{}

// SystemProbe confirms that a Temporal worker can execute an activity.
func (SystemActivities) SystemProbe(_ context.Context, input SystemProbeInput) (SystemProbeOutput, error) {
	return SystemProbeOutput{
		RequestID:  input.RequestID,
		ObservedAt: time.Now().UTC(),
		Status:     "ok",
	}, nil
}
