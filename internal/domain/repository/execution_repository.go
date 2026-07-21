package repository

import (
	"context"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type TaskExecutionRepository interface {
	GetExecutionContext(context.Context, string) (domain.TaskExecutionContext, error)
	BeginAttempt(context.Context, string, string, domain.TaskWorkspace, int) (domain.TaskAttempt, error)
	AttachAgentThread(context.Context, string, string) (domain.TaskAttempt, error)
	HeartbeatAttempt(context.Context, string) error
	SetAttemptStatus(context.Context, string, domain.TaskAttemptStatus) error
	CompleteAttempt(context.Context, string, domain.AgentResult, domain.VerificationReport, string) (domain.TaskAttempt, error)
	FailAttempt(context.Context, string, domain.TaskAttemptStatus, string, any) error
	BeginReview(context.Context, string, int, string) (domain.TaskReview, error)
	CreateReview(context.Context, string, int, string, domain.ReviewerResult) (domain.TaskReview, error)
	StoreArtifact(context.Context, domain.Artifact) (domain.Artifact, error)
	ListAttempts(context.Context, string) ([]domain.TaskAttempt, error)
	ListArtifacts(context.Context, string) ([]domain.Artifact, error)
	AddRequiredTasks(context.Context, string, []domain.RequiredTask, int, int) (domain.RequiredTaskSchedule, error)
	ResetTaskForRetry(context.Context, string, int) (domain.Task, error)
}

type TaskWorktree interface {
	Prepare(context.Context, domain.Project, domain.Task) (domain.TaskWorkspace, error)
	Inspect(context.Context, domain.Project, domain.TaskWorkspace) (domain.WorkspaceState, error)
	RunCheck(context.Context, domain.TaskWorkspace, string) (domain.WorkspaceCheckResult, error)
	ReadArtifact(context.Context, domain.TaskWorkspace, string, int64) ([]byte, error)
	Commit(context.Context, domain.Project, domain.Task, domain.TaskWorkspace, []string) (string, error)
}

type AgentThreadCallback func(context.Context, string) error

type AgentRunner interface {
	Run(context.Context, domain.AgentRunRequest, AgentThreadCallback) (domain.AgentRunResponse, error)
}

type AgentResultValidator interface {
	AgentSchema() map[string]any
	ReviewerSchema() map[string]any
	ValidateAgentResult([]byte) (domain.AgentResult, error)
	ValidateReviewerResult([]byte) (domain.ReviewerResult, error)
}
