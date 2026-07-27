package domain

import "time"

type AgentTokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
}

type AgentUsageContext struct {
	ResourceType string
	ResourceID   string
	RouteReason  string
}

type AgentRunUsage struct {
	ID                    string       `json:"id"`
	Role                  AgentRunRole `json:"role"`
	Model                 string       `json:"model"`
	ReasoningEffort       string       `json:"reasoning_effort"`
	ThreadID              string       `json:"thread_id,omitempty"`
	ResourceType          string       `json:"resource_type,omitempty"`
	ResourceID            string       `json:"resource_id,omitempty"`
	RouteReason           string       `json:"route_reason"`
	Status                string       `json:"status"`
	InputTokens           int64        `json:"input_tokens"`
	CachedInputTokens     int64        `json:"cached_input_tokens"`
	OutputTokens          int64        `json:"output_tokens"`
	ReasoningOutputTokens int64        `json:"reasoning_output_tokens"`
	DurationMilliseconds  int64        `json:"duration_ms"`
	StartedAt             time.Time    `json:"started_at"`
	CompletedAt           time.Time    `json:"completed_at"`
}

type AgentUsageBreakdown struct {
	Key                   string `json:"key"`
	Runs                  int64  `json:"runs"`
	FailedRuns            int64  `json:"failed_runs"`
	InputTokens           int64  `json:"input_tokens"`
	CachedInputTokens     int64  `json:"cached_input_tokens"`
	OutputTokens          int64  `json:"output_tokens"`
	ReasoningOutputTokens int64  `json:"reasoning_output_tokens"`
}

type AgentUsageWindow struct {
	Since   time.Time             `json:"since"`
	Runs    int64                 `json:"runs"`
	Failed  int64                 `json:"failed_runs"`
	ByModel []AgentUsageBreakdown `json:"by_model"`
	ByRole  []AgentUsageBreakdown `json:"by_role"`
}

type AgentUsageDashboard struct {
	GeneratedAt time.Time          `json:"generated_at"`
	FiveHours   AgentUsageWindow   `json:"five_hours"`
	SevenDays   AgentUsageWindow   `json:"seven_days"`
	ThirtyDays  AgentUsageWindow   `json:"thirty_days"`
	Budget      AgentBudgetState   `json:"budget"`
	Routing     AgentRoutingPolicy `json:"routing"`
}

type AgentRoutingPolicy struct {
	CoderModel         string `json:"coder_model"`
	RoutineReviewModel string `json:"routine_review_model"`
	FastModel          string `json:"fast_model"`
	StandardModel      string `json:"standard_model"`
	DeepModel          string `json:"deep_model"`
	WorkItemDraftMode  string `json:"work_item_draft_mode"`
}

type AgentBudgetState struct {
	Mode             string `json:"mode"`
	DeepModel        string `json:"deep_model"`
	DeepRunsFiveHour int64  `json:"deep_runs_five_hours"`
	DeepRunLimit     int64  `json:"deep_run_limit"`
	Utilization      int64  `json:"utilization_percent"`
	XHighAllowed     bool   `json:"xhigh_allowed"`
}
