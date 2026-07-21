package domain

import (
	"encoding/json"
	"time"
)

type TaskAttemptStatus string

const (
	TaskAttemptStatusRunning          TaskAttemptStatus = "running"
	TaskAttemptStatusVerification     TaskAttemptStatus = "verification"
	TaskAttemptStatusReview           TaskAttemptStatus = "review"
	TaskAttemptStatusChangesRequested TaskAttemptStatus = "changes_requested"
	TaskAttemptStatusCompleted        TaskAttemptStatus = "completed"
	TaskAttemptStatusBlocked          TaskAttemptStatus = "blocked"
	TaskAttemptStatusFailed           TaskAttemptStatus = "failed"
	TaskAttemptStatusCancelled        TaskAttemptStatus = "cancelled"
)

type AgentResultStatus string

const (
	AgentResultCompleted       AgentResultStatus = "completed"
	AgentResultBlocked         AgentResultStatus = "blocked"
	AgentResultFailed          AgentResultStatus = "failed"
	AgentResultChangesRequired AgentResultStatus = "changes_required"
)

type AgentCheckStatus string

const (
	AgentCheckPassed  AgentCheckStatus = "passed"
	AgentCheckFailed  AgentCheckStatus = "failed"
	AgentCheckSkipped AgentCheckStatus = "skipped"
)

type AgentCheck struct {
	Name    string           `json:"name"`
	Status  AgentCheckStatus `json:"status"`
	Details string           `json:"details"`
}

type AgentArtifactClaim struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type RequiredTask struct {
	Service            string   `json:"service"`
	Role               string   `json:"role"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Reason             string   `json:"reason"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
}

type AgentResult struct {
	Status           AgentResultStatus    `json:"status"`
	Summary          string               `json:"summary"`
	FilesChanged     []string             `json:"files_changed"`
	Checks           []AgentCheck         `json:"checks"`
	Artifacts        []AgentArtifactClaim `json:"artifacts"`
	Blockers         []string             `json:"blockers"`
	RequiredTasks    []RequiredTask       `json:"required_tasks"`
	Risks            []string             `json:"risks"`
	NotesForReviewer []string             `json:"notes_for_reviewer"`
}

type ReviewStatus string

const (
	ReviewRunning          ReviewStatus = "running"
	ReviewApproved         ReviewStatus = "approved"
	ReviewChangesRequested ReviewStatus = "changes_requested"
)

type ReviewIssue struct {
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
}

type ReviewerResult struct {
	Status            ReviewStatus  `json:"status"`
	Summary           string        `json:"summary"`
	BlockingIssues    []ReviewIssue `json:"blocking_issues"`
	NonBlockingIssues []ReviewIssue `json:"non_blocking_issues"`
	Risks             []string      `json:"risks"`
	SuggestedChecks   []string      `json:"suggested_checks"`
}

type VerificationCheck struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Details  string `json:"details,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
}

type VerificationReport struct {
	Status       string              `json:"status"`
	ChangedFiles []string            `json:"changed_files"`
	Checks       []VerificationCheck `json:"checks"`
	VerifiedAt   time.Time           `json:"verified_at"`
}

type TaskReview struct {
	ID               string          `json:"id"`
	TaskAttemptID    string          `json:"task_attempt_id"`
	ReviewNumber     int             `json:"review_number"`
	AgentThreadID    string          `json:"agent_thread_id"`
	Status           ReviewStatus    `json:"status"`
	StructuredResult json.RawMessage `json:"structured_result"`
	CreatedAt        time.Time       `json:"created_at"`
}

type TaskExecutionContext struct {
	Task         Task                `json:"task"`
	Project      Project             `json:"project"`
	Plan         Plan                `json:"plan"`
	Command      Command             `json:"command"`
	Dependencies []TaskDependencyRef `json:"dependencies"`
}

type TaskDependencyRef struct {
	Task      Task         `json:"task"`
	Attempt   *TaskAttempt `json:"attempt,omitempty"`
	Artifacts []Artifact   `json:"artifacts"`
}

type TaskWorkspace struct {
	Path       string `json:"path"`
	BranchName string `json:"branch_name"`
	BaseCommit string `json:"base_commit"`
}

type WorkspaceState struct {
	ChangedFiles []string `json:"changed_files"`
	Diff         string   `json:"diff"`
	HeadCommit   string   `json:"head_commit"`
}

type WorkspaceCheckResult struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

type AgentRunRole string

const (
	AgentRunCoder    AgentRunRole = "coder"
	AgentRunReviewer AgentRunRole = "reviewer"
)

type AgentRunRequest struct {
	Role             AgentRunRole   `json:"role"`
	ThreadID         string         `json:"thread_id,omitempty"`
	WorkingDirectory string         `json:"working_directory"`
	Model            string         `json:"model,omitempty"`
	Prompt           string         `json:"prompt"`
	OutputSchema     map[string]any `json:"output_schema"`
}

type AgentRunResponse struct {
	ThreadID string          `json:"thread_id"`
	Result   json.RawMessage `json:"result"`
}

type RequiredTaskSchedule struct {
	Tasks              []ScheduledTask `json:"tasks"`
	ParentDependencies []string        `json:"parent_dependencies"`
}

type TaskExecutionOutcome struct {
	Result           TaskResult            `json:"result"`
	RequiredSchedule *RequiredTaskSchedule `json:"required_schedule,omitempty"`
}
