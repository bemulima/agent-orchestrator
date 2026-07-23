package domain

import "time"

type PlanSourceKind string

const (
	PlanSourceDiscussion PlanSourceKind = "discussion"
	PlanSourceIssue      PlanSourceKind = "issue"
)

type IssueProvider string

const (
	IssueProviderGitHub IssueProvider = "github"
	IssueProviderGitLab IssueProvider = "gitlab"
)

type IssueReference struct {
	Provider  IssueProvider `json:"provider"`
	ProjectID string        `json:"project_id"`
	Number    int64         `json:"number"`
	URL       string        `json:"url,omitempty"`
}

type WorkItemKind string

const (
	WorkItemIssue       WorkItemKind = "issue"
	WorkItemPullRequest WorkItemKind = "pull_request"
)

type WorkItemStatus string

const (
	WorkItemProposed  WorkItemStatus = "proposed"
	WorkItemPublished WorkItemStatus = "published"
	WorkItemClosed    WorkItemStatus = "closed"
	WorkItemCancelled WorkItemStatus = "cancelled"
)

type IssueType string

const (
	IssueTypeQuestion IssueType = "question"
	IssueTypeIdea     IssueType = "idea"
	IssueTypeTask     IssueType = "task"
	IssueTypeBug      IssueType = "bug"
	IssueTypeResearch IssueType = "research"
)

type TaskComplexity string

const (
	TaskComplexityLow      TaskComplexity = "low"
	TaskComplexityMedium   TaskComplexity = "medium"
	TaskComplexityHigh     TaskComplexity = "high"
	TaskComplexityCritical TaskComplexity = "critical"
)

// WorkItem is a persisted, owner-reviewable issue or pull-request proposal.
// External publication is impossible while it remains proposed.
type WorkItem struct {
	ID              string         `json:"id"`
	PlanID          string         `json:"plan_id"`
	TaskID          *string        `json:"task_id,omitempty"`
	ProjectID       string         `json:"project_id"`
	Kind            WorkItemKind   `json:"kind"`
	Provider        IssueProvider  `json:"provider"`
	IssueType       *IssueType     `json:"issue_type,omitempty"`
	Status          WorkItemStatus `json:"status"`
	Title           string         `json:"title"`
	Body            string         `json:"body"`
	Labels          []string       `json:"labels"`
	Milestone       string         `json:"milestone"`
	Assignees       []string       `json:"assignees"`
	Reviewers       []string       `json:"reviewers"`
	SourceBranch    string         `json:"source_branch,omitempty"`
	TargetBranch    string         `json:"target_branch,omitempty"`
	ExternalNumber  *int64         `json:"external_number,omitempty"`
	ExternalURL     string         `json:"external_url,omitempty"`
	AgentRole       AgentRunRole   `json:"agent_role"`
	AgentThreadID   string         `json:"agent_thread_id"`
	Complexity      TaskComplexity `json:"complexity"`
	ModelProfile    string         `json:"model_profile"`
	PlanFingerprint string         `json:"plan_fingerprint"`
	IdempotencyKey  string         `json:"idempotency_key"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	PublishedAt     *time.Time     `json:"published_at,omitempty"`
}

type IssueDraft struct {
	TaskKey      string          `json:"task_key"`
	ProjectID    string          `json:"project_id"`
	IssueType    IssueType       `json:"issue_type"`
	Title        string          `json:"title"`
	Body         string          `json:"body"`
	Labels       []string        `json:"labels"`
	Milestone    string          `json:"milestone"`
	Assignees    []string        `json:"assignees"`
	Existing     *IssueReference `json:"existing_issue,omitempty"`
	Complexity   TaskComplexity  `json:"complexity"`
	ModelProfile string          `json:"model_profile"`
}

type IssueManagerResult struct {
	Summary string       `json:"summary"`
	Issues  []IssueDraft `json:"issues"`
}

type PullRequestDraft struct {
	TaskID       string         `json:"task_id"`
	ProjectID    string         `json:"project_id"`
	Title        string         `json:"title"`
	Body         string         `json:"body"`
	Labels       []string       `json:"labels"`
	Milestone    string         `json:"milestone"`
	Assignees    []string       `json:"assignees"`
	Reviewers    []string       `json:"reviewers"`
	SourceBranch string         `json:"source_branch"`
	TargetBranch string         `json:"target_branch"`
	Complexity   TaskComplexity `json:"complexity"`
	ModelProfile string         `json:"model_profile"`
}

type PullRequestManagerResult struct {
	Summary     string           `json:"summary"`
	PullRequest PullRequestDraft `json:"pull_request"`
}

type PlanComment struct {
	ID        string    `json:"id"`
	PlanID    string    `json:"plan_id"`
	Revision  int       `json:"revision"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type WorkItemPublication struct {
	Number int64  `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"`
}
