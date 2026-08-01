package domain

import "time"

// ResourceAction describes an operation the backend currently permits for a
// resource. The UI must not reconstruct state-machine rules on its own.
type ResourceAction struct {
	Action               string `json:"action"`
	RequiresConfirmation bool   `json:"requires_confirmation"`
	RequiresFingerprint  bool   `json:"requires_fingerprint"`
}

type PageCursor struct {
	At time.Time
	ID string
}

type PageQuery struct {
	Limit     int
	Cursor    *PageCursor
	Statuses  []string
	ProjectID string
	PlanID    string
}

type PageInfo struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

type DashboardCounts struct {
	Projects          int `json:"projects"`
	ActivePlans       int `json:"active_plans"`
	ActiveTasks       int `json:"active_tasks"`
	AttentionRequired int `json:"attention_required"`
}

type AttentionItem struct {
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	Title        string    `json:"title"`
	Status       string    `json:"status"`
	Reason       string    `json:"reason"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Dashboard struct {
	GeneratedAt    time.Time       `json:"generated_at"`
	Counts         DashboardCounts `json:"counts"`
	Attention      []AttentionItem `json:"attention"`
	ActiveRuns     []RunSummary    `json:"active_runs"`
	RecentActivity []ActivityEvent `json:"recent_activity"`
}

type IssuePublication string

const (
	IssuePublicationNone       IssuePublication = "none"
	IssuePublicationDraft      IssuePublication = "draft"
	IssuePublicationSimulation IssuePublication = "simulation"
	IssuePublicationExternal   IssuePublication = "external"
)

type PlanSummary struct {
	ID                  string           `json:"id"`
	CommandID           string           `json:"command_id"`
	Summary             string           `json:"summary"`
	Status              PlanStatus       `json:"status"`
	Version             int              `json:"version"`
	RiskLevel           RiskLevel        `json:"risk_level"`
	SourceKind          PlanSourceKind   `json:"source_kind"`
	Fingerprint         string           `json:"fingerprint"`
	ApprovedFingerprint *string          `json:"approved_fingerprint,omitempty"`
	TaskCount           int              `json:"task_count"`
	CompletedTasks      int              `json:"completed_tasks"`
	AttentionTasks      int              `json:"attention_tasks"`
	IssueCount          int              `json:"issue_count"`
	PublishedIssues     int              `json:"published_issues"`
	IssuePublication    IssuePublication `json:"issue_publication"`
	RunID               *string          `json:"run_id,omitempty"`
	RunStatus           *PlanRunStatus   `json:"run_status,omitempty"`
	RunError            *string          `json:"run_error,omitempty"`
	SupersedesPlanID    *string          `json:"supersedes_plan_id,omitempty"`
	SupersededByPlanID  *string          `json:"superseded_by_plan_id,omitempty"`
	UpdatedAt           time.Time        `json:"updated_at"`
	AllowedActions      []ResourceAction `json:"allowed_actions"`
}

type PlanSummaryPage struct {
	Items                 []PlanSummary `json:"items"`
	WorkItemGateway       string        `json:"work_item_gateway"`
	ExternalWritesEnabled bool          `json:"external_writes_enabled"`
	PageInfo
}

type RunSummary struct {
	ID               string           `json:"id"`
	PlanID           string           `json:"plan_id"`
	PlanSummary      string           `json:"plan_summary"`
	Status           PlanRunStatus    `json:"status"`
	WorkflowID       string           `json:"workflow_id"`
	MaxParallelTasks int              `json:"max_parallel_tasks"`
	TaskCount        int              `json:"task_count"`
	CompletedTasks   int              `json:"completed_tasks"`
	ActiveTasks      int              `json:"active_tasks"`
	Error            *string          `json:"error,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
	StartedAt        *time.Time       `json:"started_at,omitempty"`
	CompletedAt      *time.Time       `json:"completed_at,omitempty"`
	UpdatedAt        time.Time        `json:"updated_at"`
	AllowedActions   []ResourceAction `json:"allowed_actions"`
}

type RunSummaryPage struct {
	Items []RunSummary `json:"items"`
	PageInfo
}

type TaskSummary struct {
	ID             string             `json:"id"`
	PlanID         string             `json:"plan_id"`
	ProjectID      string             `json:"project_id"`
	ProjectName    string             `json:"project_name"`
	PlanSummary    string             `json:"plan_summary"`
	Title          string             `json:"title"`
	Status         TaskStatus         `json:"status"`
	RiskLevel      RiskLevel          `json:"risk_level"`
	Priority       int                `json:"priority"`
	Depth          int                `json:"depth"`
	AttemptCount   int                `json:"attempt_count"`
	LastAttempt    *TaskAttemptStatus `json:"last_attempt_status,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
	StartedAt      *time.Time         `json:"started_at,omitempty"`
	CompletedAt    *time.Time         `json:"completed_at,omitempty"`
	UpdatedAt      time.Time          `json:"updated_at"`
	AllowedActions []ResourceAction   `json:"allowed_actions"`
}

type TaskSummaryPage struct {
	Items []TaskSummary `json:"items"`
	PageInfo
}

type ApprovalSummary struct {
	ID           string         `json:"id"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	ResourceName string         `json:"resource_name"`
	Action       string         `json:"action"`
	Status       ApprovalStatus `json:"status"`
	Fingerprint  string         `json:"fingerprint,omitempty"`
	RiskLevel    string         `json:"risk_level,omitempty"`
	RequestedAt  time.Time      `json:"requested_at"`
	DecidedAt    *time.Time     `json:"decided_at,omitempty"`
}

type ApprovalSummaryPage struct {
	Items []ApprovalSummary `json:"items"`
	PageInfo
}

type ActivityEvent struct {
	ID           string         `json:"id"`
	ActorType    string         `json:"actor_type"`
	ActorID      *string        `json:"actor_id,omitempty"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Payload      map[string]any `json:"payload"`
	CreatedAt    time.Time      `json:"created_at"`
}

type ActivityPage struct {
	Items []ActivityEvent `json:"items"`
	PageInfo
}
