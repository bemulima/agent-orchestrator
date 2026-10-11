package domain

import "context"

// AgentRuntimeContext binds an invocation to the trusted execution activity.
// It is populated by the activity adapter, never by agent output or a prompt.
type AgentRuntimeContext struct {
	WorkflowID      string `json:"workflow_id"`
	WorkflowRunID   string `json:"workflow_run_id"`
	ActivityID      string `json:"activity_id"`
	ActivityAttempt int32  `json:"activity_attempt"`
}

type agentRuntimeContextKey struct{}

// WithAgentRuntimeContext carries a copy of activity-owned execution identity.
func WithAgentRuntimeContext(ctx context.Context, value AgentRuntimeContext) context.Context {
	return context.WithValue(ctx, agentRuntimeContextKey{}, value)
}

func AgentRuntimeContextFromContext(ctx context.Context) (AgentRuntimeContext, bool) {
	value, ok := ctx.Value(agentRuntimeContextKey{}).(AgentRuntimeContext)
	return value, ok
}
