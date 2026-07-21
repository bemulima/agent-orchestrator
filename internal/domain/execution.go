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

type CommandStatus string

const (
	CommandStatusReceived  CommandStatus = "received"
	CommandStatusPlanning  CommandStatus = "planning"
	CommandStatusPlanned   CommandStatus = "planned"
	CommandStatusApproved  CommandStatus = "approved"
	CommandStatusRunning   CommandStatus = "running"
	CommandStatusCompleted CommandStatus = "completed"
	CommandStatusFailed    CommandStatus = "failed"
	CommandStatusCancelled CommandStatus = "cancelled"
)

type Command struct {
	ID             string        `json:"id"`
	Source         CommandSource `json:"source"`
	SourceUserID   *string       `json:"source_user_id,omitempty"`
	Text           string        `json:"text"`
	Status         CommandStatus `json:"status"`
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
	ID                 string          `json:"id"`
	CommandID          string          `json:"command_id"`
	ApprovalID         *string         `json:"approval_id,omitempty"`
	TopologyRevisionID *string         `json:"topology_revision_id,omitempty"`
	Status             PlanStatus      `json:"status"`
	Version            int             `json:"version"`
	Summary            string          `json:"summary"`
	RiskLevel          string          `json:"risk_level"`
	RequiresApproval   bool            `json:"requires_approval"`
	Fingerprint        string          `json:"fingerprint"`
	PlannerInput       json.RawMessage `json:"planner_input"`
	PlannerOutput      json.RawMessage `json:"planner_output"`
	ReplanCount        int             `json:"replan_count"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	ApprovedAt         *time.Time      `json:"approved_at,omitempty"`
}

type RiskLevel string

const (
	RiskLevelLow      RiskLevel = "low"
	RiskLevelMedium   RiskLevel = "medium"
	RiskLevelHigh     RiskLevel = "high"
	RiskLevelCritical RiskLevel = "critical"
)

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
	ID                   string     `json:"id"`
	PlanID               string     `json:"plan_id"`
	ProjectID            string     `json:"project_id"`
	Role                 string     `json:"role"`
	Title                string     `json:"title"`
	Description          string     `json:"description"`
	Status               TaskStatus `json:"status"`
	AcceptanceCriteria   []string   `json:"acceptance_criteria"`
	WriteScope           []string   `json:"write_scope"`
	ModelProfile         string     `json:"model_profile"`
	Priority             int        `json:"priority"`
	IdempotencyKey       string     `json:"idempotency_key"`
	PlannerKey           string     `json:"planner_key"`
	RiskLevel            RiskLevel  `json:"risk_level"`
	RequiresMigration    bool       `json:"requires_migration"`
	ChangesContracts     bool       `json:"changes_contracts"`
	VerificationCommands []string   `json:"verification_commands"`
	Depth                int        `json:"depth"`
	CreatedAt            time.Time  `json:"created_at"`
	StartedAt            *time.Time `json:"started_at,omitempty"`
	CompletedAt          *time.Time `json:"completed_at,omitempty"`
}

type TaskDependency struct {
	TaskID          string `json:"task_id"`
	DependsOnTaskID string `json:"depends_on_task_id"`
	DependencyType  string `json:"dependency_type"`
}

type PlanRequest struct {
	RequestedProjectIDs []string `json:"project_ids,omitempty"`
}

type PlannerInput struct {
	CommandID           string   `json:"command_id"`
	CommandText         string   `json:"command_text"`
	TopologyRevisionID  string   `json:"topology_revision_id"`
	RequestedProjectIDs []string `json:"requested_project_ids,omitempty"`
}

type PlannedTask struct {
	Key                  string    `json:"key"`
	ProjectID            string    `json:"project_id"`
	Role                 string    `json:"role"`
	Title                string    `json:"title"`
	Description          string    `json:"description"`
	AcceptanceCriteria   []string  `json:"acceptance_criteria"`
	WriteScope           []string  `json:"write_scope"`
	ModelProfile         string    `json:"model_profile"`
	Priority             int       `json:"priority"`
	RiskLevel            RiskLevel `json:"risk_level"`
	RequiresMigration    bool      `json:"requires_migration"`
	ChangesContracts     bool      `json:"changes_contracts"`
	VerificationCommands []string  `json:"verification_commands"`
	Depth                int       `json:"depth"`
}

type PlannedDependency struct {
	TaskKey          string `json:"task_key"`
	DependsOnTaskKey string `json:"depends_on_task_key"`
	DependencyType   string `json:"dependency_type"`
}

type PlannerOutput struct {
	Summary      string              `json:"summary"`
	RiskLevel    RiskLevel           `json:"risk_level"`
	Risks        []string            `json:"risks"`
	Tasks        []PlannedTask       `json:"tasks"`
	Dependencies []PlannedDependency `json:"dependencies"`
}

type PlanBundle struct {
	Plan         Plan             `json:"plan"`
	Tasks        []Task           `json:"tasks"`
	Dependencies []TaskDependency `json:"dependencies"`
	Approval     *Approval        `json:"approval,omitempty"`
	Run          *PlanRun         `json:"run,omitempty"`
}

type PlanRunStatus string

const (
	PlanRunStatusPending   PlanRunStatus = "pending"
	PlanRunStatusRunning   PlanRunStatus = "running"
	PlanRunStatusPaused    PlanRunStatus = "paused"
	PlanRunStatusCompleted PlanRunStatus = "completed"
	PlanRunStatusFailed    PlanRunStatus = "failed"
	PlanRunStatusCancelled PlanRunStatus = "cancelled"
)

type PlanRun struct {
	ID               string        `json:"id"`
	PlanID           string        `json:"plan_id"`
	Status           PlanRunStatus `json:"status"`
	WorkflowID       string        `json:"workflow_id"`
	TemporalRunID    *string       `json:"temporal_run_id,omitempty"`
	IdempotencyKey   string        `json:"idempotency_key"`
	MaxParallelTasks int           `json:"max_parallel_tasks"`
	Error            *string       `json:"error,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
	StartedAt        *time.Time    `json:"started_at,omitempty"`
	PausedAt         *time.Time    `json:"paused_at,omitempty"`
	CompletedAt      *time.Time    `json:"completed_at,omitempty"`
	UpdatedAt        time.Time     `json:"updated_at"`
}

type ScheduledTask struct {
	TaskID       string   `json:"task_id"`
	Priority     int      `json:"priority"`
	Dependencies []string `json:"dependencies"`
}

type PlanSchedule struct {
	RunID               string          `json:"run_id"`
	PlanID              string          `json:"plan_id"`
	MaxParallelTasks    int             `json:"max_parallel_tasks"`
	MaxActivityAttempts int             `json:"max_activity_attempts"`
	Tasks               []ScheduledTask `json:"tasks"`
}

type RunControlAction string

const (
	RunControlPause  RunControlAction = "pause"
	RunControlResume RunControlAction = "resume"
	RunControlCancel RunControlAction = "cancel"
)

type TaskResult struct {
	TaskID string     `json:"task_id"`
	Status TaskStatus `json:"status"`
	Error  string     `json:"error,omitempty"`
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
