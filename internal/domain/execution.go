package domain

import (
	"encoding/json"
	"time"
)

type CommandSource string

const (
	CommandSourceCLI      CommandSource = "cli"
	CommandSourceTelegram CommandSource = "telegram"
	CommandSourceAPI      CommandSource = "api"
)

type Command struct {
	ID             string        `json:"id"`
	Source         CommandSource `json:"source"`
	SourceUserID   *string       `json:"source_user_id,omitempty"`
	Text           string        `json:"text"`
	Status         string        `json:"status"`
	IdempotencyKey string        `json:"idempotency_key"`
	CreatedAt      time.Time     `json:"created_at"`
}

type PlanStatus string

const (
	PlanStatusDraft            PlanStatus = "draft"
	PlanStatusPlanned          PlanStatus = "planned"
	PlanStatusAwaitingApproval PlanStatus = "awaiting_approval"
	PlanStatusApproved         PlanStatus = "approved"
	PlanStatusRunning          PlanStatus = "running"
	PlanStatusPaused           PlanStatus = "paused"
	PlanStatusCompleted        PlanStatus = "completed"
	PlanStatusFailed           PlanStatus = "failed"
	PlanStatusCancelled        PlanStatus = "cancelled"
)

type Plan struct {
	ID               string     `json:"id"`
	CommandID        string     `json:"command_id"`
	Status           PlanStatus `json:"status"`
	Version          int        `json:"version"`
	Summary          string     `json:"summary"`
	RiskLevel        string     `json:"risk_level"`
	RequiresApproval bool       `json:"requires_approval"`
	CreatedAt        time.Time  `json:"created_at"`
	ApprovedAt       *time.Time `json:"approved_at,omitempty"`
}

type TaskStatus string

const (
	TaskStatusDraft            TaskStatus = "draft"
	TaskStatusPlanned          TaskStatus = "planned"
	TaskStatusReady            TaskStatus = "ready"
	TaskStatusRunning          TaskStatus = "running"
	TaskStatusBlocked          TaskStatus = "blocked"
	TaskStatusVerification     TaskStatus = "verification"
	TaskStatusChangesRequested TaskStatus = "changes_requested"
	TaskStatusCompleted        TaskStatus = "completed"
	TaskStatusFailed           TaskStatus = "failed"
	TaskStatusCancelled        TaskStatus = "cancelled"
)

type Task struct {
	ID                 string     `json:"id"`
	PlanID             string     `json:"plan_id"`
	ProjectID          string     `json:"project_id"`
	Role               string     `json:"role"`
	Title              string     `json:"title"`
	Description        string     `json:"description"`
	Status             TaskStatus `json:"status"`
	AcceptanceCriteria []string   `json:"acceptance_criteria"`
	WriteScope         []string   `json:"write_scope"`
	ModelProfile       string     `json:"model_profile"`
	Priority           int        `json:"priority"`
	IdempotencyKey     string     `json:"idempotency_key"`
	CreatedAt          time.Time  `json:"created_at"`
	StartedAt          *time.Time `json:"started_at,omitempty"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
}

type TaskDependency struct {
	TaskID          string `json:"task_id"`
	DependsOnTaskID string `json:"depends_on_task_id"`
	DependencyType  string `json:"dependency_type"`
}

type TaskAttempt struct {
	ID               string          `json:"id"`
	TaskID           string          `json:"task_id"`
	AttemptNumber    int             `json:"attempt_number"`
	AgentThreadID    *string         `json:"agent_thread_id,omitempty"`
	WorkflowID       string          `json:"workflow_id"`
	WorktreePath     string          `json:"worktree_path"`
	BranchName       string          `json:"branch_name"`
	CommitSHA        *string         `json:"commit_sha,omitempty"`
	Status           string          `json:"status"`
	StructuredResult json.RawMessage `json:"structured_result"`
	Error            *string         `json:"error,omitempty"`
	StartedAt        time.Time       `json:"started_at"`
	HeartbeatAt      *time.Time      `json:"heartbeat_at,omitempty"`
	FinishedAt       *time.Time      `json:"finished_at,omitempty"`
}

type Artifact struct {
	ID       string          `json:"id"`
	TaskID   string          `json:"task_id"`
	Type     string          `json:"type"`
	Name     string          `json:"name"`
	URI      string          `json:"uri"`
	Checksum string          `json:"checksum"`
	Metadata json.RawMessage `json:"metadata"`
}

type Approval struct {
	ID           string     `json:"id"`
	ResourceType string     `json:"resource_type"`
	ResourceID   string     `json:"resource_id"`
	Action       string     `json:"action"`
	Status       string     `json:"status"`
	RequestedAt  time.Time  `json:"requested_at"`
	DecidedAt    *time.Time `json:"decided_at,omitempty"`
	DecidedBy    *string    `json:"decided_by,omitempty"`
	Comment      *string    `json:"comment,omitempty"`
}

type GitLabLink struct {
	ID              string `json:"id"`
	ResourceType    string `json:"resource_type"`
	ResourceID      string `json:"resource_id"`
	GitLabProjectID int64  `json:"gitlab_project_id"`
	IssueIID        *int64 `json:"issue_iid,omitempty"`
	MergeRequestIID *int64 `json:"merge_request_iid,omitempty"`
	URL             string `json:"url"`
}

type TelegramUser struct {
	ID             string    `json:"id"`
	TelegramUserID int64     `json:"telegram_user_id"`
	TelegramChatID int64     `json:"telegram_chat_id"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
}

type AuditEvent struct {
	ID           string          `json:"id"`
	ActorType    string          `json:"actor_type"`
	ActorID      *string         `json:"actor_id,omitempty"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Payload      json.RawMessage `json:"payload"`
	CreatedAt    time.Time       `json:"created_at"`
}
