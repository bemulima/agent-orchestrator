package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type OnboardingStatus string

const (
	OnboardingStatusProposalReady    OnboardingStatus = "proposal_ready"
	OnboardingStatusAwaitingApproval OnboardingStatus = "awaiting_approval"
	OnboardingStatusApplying         OnboardingStatus = "applying"
	OnboardingStatusMRCreated        OnboardingStatus = "merge_request_created"
	OnboardingStatusCompleted        OnboardingStatus = "completed"
	OnboardingStatusFailed           OnboardingStatus = "failed"
	OnboardingStatusCancelled        OnboardingStatus = "cancelled"
	OnboardingStatusChangesRequested OnboardingStatus = "changes_requested"
)

type ProposalFileAction string

const (
	ProposalFileCreate    ProposalFileAction = "create"
	ProposalFileUpdate    ProposalFileAction = "update"
	ProposalFileUnchanged ProposalFileAction = "unchanged"
)

type ProposedFile struct {
	Path          string             `json:"path"`
	Content       string             `json:"content"`
	Action        ProposalFileAction `json:"action"`
	Checksum      string             `json:"checksum"`
	Explanation   string             `json:"explanation"`
	EvidencePaths []string           `json:"evidence_paths"`
}

type OnboardingConflict struct {
	Path        string `json:"path"`
	Field       string `json:"field"`
	Existing    string `json:"existing"`
	Discovered  string `json:"discovered"`
	Explanation string `json:"explanation"`
}

type OnboardingProposal struct {
	SchemaVersion int                  `json:"schema_version"`
	Generator     string               `json:"generator"`
	ProjectID     string               `json:"project_id"`
	SnapshotID    string               `json:"snapshot_id"`
	BaseCommit    string               `json:"base_commit"`
	Files         []ProposedFile       `json:"files"`
	Conflicts     []OnboardingConflict `json:"conflicts,omitempty"`
	Checksum      string               `json:"checksum"`
	GeneratedAt   time.Time            `json:"generated_at"`
}

// OnboardingProposalChecksum fingerprints every approval-relevant proposal
// field while deliberately excluding display-only generation time.
func OnboardingProposalChecksum(proposal OnboardingProposal) (string, error) {
	fingerprint := struct {
		SchemaVersion int                  `json:"schema_version"`
		Generator     string               `json:"generator"`
		ProjectID     string               `json:"project_id"`
		SnapshotID    string               `json:"snapshot_id"`
		BaseCommit    string               `json:"base_commit"`
		Files         []ProposedFile       `json:"files"`
		Conflicts     []OnboardingConflict `json:"conflicts"`
	}{proposal.SchemaVersion, proposal.Generator, proposal.ProjectID, proposal.SnapshotID,
		proposal.BaseCommit, proposal.Files, proposal.Conflicts}
	content, err := json.Marshal(fingerprint)
	if err != nil {
		return "", fmt.Errorf("marshal onboarding proposal fingerprint: %w", err)
	}
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:]), nil
}

type OnboardingCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Details string `json:"details,omitempty"`
}

type OnboardingRun struct {
	ID               string             `json:"id"`
	ProjectID        string             `json:"project_id"`
	SnapshotID       string             `json:"snapshot_id"`
	ApprovalID       *string            `json:"approval_id,omitempty"`
	Status           OnboardingStatus   `json:"status"`
	DryRun           bool               `json:"dry_run"`
	BaseCommit       string             `json:"base_commit"`
	BaseBranch       string             `json:"base_branch"`
	ProposalChecksum string             `json:"proposal_checksum"`
	Proposal         OnboardingProposal `json:"proposal"`
	UnifiedDiff      string             `json:"unified_diff"`
	WorktreePath     *string            `json:"worktree_path,omitempty"`
	BranchName       *string            `json:"branch_name,omitempty"`
	CommitSHA        *string            `json:"commit_sha,omitempty"`
	MergeRequestURL  *string            `json:"merge_request_url,omitempty"`
	Checks           []OnboardingCheck  `json:"checks"`
	Error            *string            `json:"error,omitempty"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
	AppliedAt        *time.Time         `json:"applied_at,omitempty"`
}

type ApprovalStatus string

const (
	ApprovalStatusPending   ApprovalStatus = "pending"
	ApprovalStatusApproved  ApprovalStatus = "approved"
	ApprovalStatusRejected  ApprovalStatus = "rejected"
	ApprovalStatusExpired   ApprovalStatus = "expired"
	ApprovalStatusCancelled ApprovalStatus = "cancelled"
)

type OnboardingApplyResult struct {
	WorktreePath string                `json:"worktree_path,omitempty"`
	BranchName   string                `json:"branch_name,omitempty"`
	CommitSHA    string                `json:"commit_sha,omitempty"`
	Checks       []OnboardingCheck     `json:"checks"`
	DryRun       bool                  `json:"dry_run"`
	Publication  OnboardingPublication `json:"publication"`
}

type OnboardingPublication struct {
	Published       bool   `json:"published"`
	GitLabProjectID int64  `json:"gitlab_project_id,omitempty"`
	MergeRequestIID int64  `json:"merge_request_iid,omitempty"`
	MergeRequestURL string `json:"merge_request_url,omitempty"`
	Details         string `json:"details,omitempty"`
}
