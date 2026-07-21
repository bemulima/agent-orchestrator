package domain

import "time"

const (
	GitLabResourcePlan          = "plan"
	GitLabResourceTask          = "task"
	GitLabResourceOnboardingRun = "onboarding_run"
)

type GitLabProject struct {
	ID        int64  `json:"id"`
	Reference string `json:"reference"`
	WebURL    string `json:"web_url"`
}

type GitLabIssueSpec struct {
	Project        GitLabProject `json:"project"`
	Title          string        `json:"title"`
	Description    string        `json:"description"`
	Labels         []string      `json:"labels"`
	IdempotencyKey string        `json:"idempotency_key"`
	State          string        `json:"state"`
}

type GitLabIssue struct {
	ProjectID int64  `json:"project_id"`
	IID       int64  `json:"iid"`
	Title     string `json:"title"`
	State     string `json:"state"`
	WebURL    string `json:"web_url"`
	DryRun    bool   `json:"dry_run"`
}

type GitLabMergeRequestSpec struct {
	Project        GitLabProject `json:"project"`
	SourceBranch   string        `json:"source_branch"`
	TargetBranch   string        `json:"target_branch"`
	Title          string        `json:"title"`
	Description    string        `json:"description"`
	Labels         []string      `json:"labels"`
	IdempotencyKey string        `json:"idempotency_key"`
}

type GitLabMergeRequest struct {
	ProjectID    int64  `json:"project_id"`
	IID          int64  `json:"iid"`
	State        string `json:"state"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	WebURL       string `json:"web_url"`
	DryRun       bool   `json:"dry_run"`
}

type GitLabSyncItem struct {
	ResourceType string              `json:"resource_type"`
	ResourceID   string              `json:"resource_id"`
	Issue        GitLabIssue         `json:"issue"`
	MergeRequest *GitLabMergeRequest `json:"merge_request,omitempty"`
	Action       string              `json:"action"`
}

type GitLabSyncResult struct {
	PlanID    string           `json:"plan_id"`
	DryRun    bool             `json:"dry_run"`
	PlanIssue GitLabIssue      `json:"plan_issue"`
	Items     []GitLabSyncItem `json:"items"`
	SyncedAt  time.Time        `json:"synced_at"`
}

type GitLabWebhookEvent struct {
	EventUUID       string    `json:"event_uuid"`
	EventType       string    `json:"event_type"`
	ObjectKind      string    `json:"object_kind"`
	GitLabProjectID int64     `json:"gitlab_project_id"`
	ObjectIID       int64     `json:"object_iid"`
	ExternalState   string    `json:"external_state"`
	PipelineStatus  string    `json:"pipeline_status,omitempty"`
	SourceBranch    string    `json:"source_branch,omitempty"`
	PayloadChecksum string    `json:"payload_checksum"`
	ReceivedAt      time.Time `json:"received_at"`
}

type GitLabWebhookResult struct {
	EventUUID string      `json:"event_uuid"`
	Status    string      `json:"status"`
	Duplicate bool        `json:"duplicate"`
	Link      *GitLabLink `json:"link,omitempty"`
}
