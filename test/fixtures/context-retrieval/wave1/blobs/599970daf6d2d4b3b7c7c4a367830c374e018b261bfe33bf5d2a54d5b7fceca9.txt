// Package events defines internal compatibility envelopes; it does not publish them.
package events

import "time"

// UserEvent is a compatibility envelope for user lifecycle data, with no active publisher.
type UserEvent struct {
	Event      string    `json:"event"`
	UserID     string    `json:"user_id"`
	Email      string    `json:"email,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
	TraceID    string    `json:"trace_id"`
}

// NewUserEvent creates a timestamped internal user event envelope.
func NewUserEvent(event, userID, email, traceID string) UserEvent {
	return UserEvent{
		Event:      event,
		UserID:     userID,
		Email:      email,
		OccurredAt: time.Now().UTC(),
		TraceID:    traceID,
	}
}
