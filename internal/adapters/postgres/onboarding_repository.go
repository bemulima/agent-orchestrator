package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type OnboardingRepoPG struct {
	Pool *pgxpool.Pool
}

func (r OnboardingRepoPG) CreateOrGet(ctx context.Context, run domain.OnboardingRun) (domain.OnboardingRun, error) {
	proposal, err := json.Marshal(run.Proposal)
	if err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("marshal onboarding proposal: %w", err)
	}
	checks, err := json.Marshal(run.Checks)
	if err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("marshal onboarding checks: %w", err)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("begin onboarding transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	run, err = scanOnboardingRun(tx.QueryRow(ctx, `
INSERT INTO onboarding_run (
    project_id, snapshot_id, status, dry_run, base_commit, base_branch,
    proposal_checksum, proposal, unified_diff, checks
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (project_id, snapshot_id, proposal_checksum, dry_run)
DO UPDATE SET updated_at = onboarding_run.updated_at
RETURNING `+onboardingColumns,
		run.ProjectID, run.SnapshotID, run.Status, run.DryRun, run.BaseCommit,
		run.BaseBranch, run.ProposalChecksum, proposal, run.UnifiedDiff, checks,
	))
	if err != nil {
		return domain.OnboardingRun{}, mapProjectError(err)
	}
	if !run.DryRun && run.ApprovalID == nil && run.Status != domain.OnboardingStatusCompleted {
		var approvalID string
		err := tx.QueryRow(ctx, `
INSERT INTO approval (resource_type, resource_id, action, status)
VALUES ('onboarding_run', $1, 'apply', 'pending')
ON CONFLICT (resource_type, resource_id, action) WHERE status = 'pending'
DO UPDATE SET requested_at = approval.requested_at
RETURNING id`, run.ID).Scan(&approvalID)
		if err != nil {
			return domain.OnboardingRun{}, fmt.Errorf("create onboarding approval: %w", err)
		}
		run, err = scanOnboardingRun(tx.QueryRow(ctx, `
UPDATE onboarding_run
SET approval_id = $2, status = 'awaiting_approval', updated_at = now()
WHERE id = $1
RETURNING `+onboardingColumns, run.ID, approvalID))
		if err != nil {
			return domain.OnboardingRun{}, fmt.Errorf("attach onboarding approval: %w", err)
		}
	}
	if err := insertResourceAuditTx(ctx, tx, "onboarding_run", "onboarding.prepared", run.ID, map[string]any{
		"project_id":        run.ProjectID,
		"snapshot_id":       run.SnapshotID,
		"dry_run":           run.DryRun,
		"proposal_checksum": run.ProposalChecksum,
	}); err != nil {
		return domain.OnboardingRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("commit onboarding transaction: %w", err)
	}
	return run, nil
}

func (r OnboardingRepoPG) Get(ctx context.Context, id string) (domain.OnboardingRun, error) {
	run, err := scanOnboardingRun(r.Pool.QueryRow(ctx, `SELECT `+onboardingColumns+` FROM onboarding_run WHERE id = $1`, id))
	return run, mapProjectError(err)
}

func (r OnboardingRepoPG) Approve(ctx context.Context, id, actor, comment string) (domain.OnboardingRun, error) {
	return r.decide(ctx, id, actor, comment, domain.ApprovalStatusApproved)
}

func (r OnboardingRepoPG) Reject(ctx context.Context, id, actor, comment string) (domain.OnboardingRun, error) {
	return r.decide(ctx, id, actor, comment, domain.ApprovalStatusRejected)
}

func (r OnboardingRepoPG) decide(
	ctx context.Context,
	id, actor, comment string,
	decision domain.ApprovalStatus,
) (domain.OnboardingRun, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("begin approval transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	run, err := scanOnboardingRun(tx.QueryRow(ctx, `SELECT `+onboardingColumns+` FROM onboarding_run WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return domain.OnboardingRun{}, mapProjectError(err)
	}
	if run.DryRun || run.ApprovalID == nil {
		return domain.OnboardingRun{}, fmt.Errorf("dry-run proposal cannot be approved: %w", domain.ErrInvalidStatus)
	}
	var current domain.ApprovalStatus
	if err := tx.QueryRow(ctx, `SELECT status FROM approval WHERE id = $1 FOR UPDATE`, *run.ApprovalID).Scan(&current); err != nil {
		return domain.OnboardingRun{}, mapProjectError(err)
	}
	if current == decision {
		if err := tx.Commit(ctx); err != nil {
			return domain.OnboardingRun{}, fmt.Errorf("commit repeated approval decision: %w", err)
		}
		return run, nil
	}
	if current != domain.ApprovalStatusPending {
		return domain.OnboardingRun{}, fmt.Errorf("approval is already %s: %w", current, domain.ErrConflict)
	}
	if actor == "" {
		actor = "owner"
	}
	if _, err := tx.Exec(ctx, `
UPDATE approval SET status = $2, decided_at = now(), decided_by = $3, comment = NULLIF($4, '')
WHERE id = $1`, *run.ApprovalID, decision, actor, comment); err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("persist onboarding decision: %w", err)
	}
	if decision == domain.ApprovalStatusRejected {
		run, err = scanOnboardingRun(tx.QueryRow(ctx, `
UPDATE onboarding_run SET status = 'cancelled', updated_at = now()
WHERE id = $1 RETURNING `+onboardingColumns, id))
		if err != nil {
			return domain.OnboardingRun{}, fmt.Errorf("cancel rejected onboarding: %w", err)
		}
	}
	if err := insertResourceAuditTx(ctx, tx, "onboarding_run", "onboarding."+string(decision), run.ID, map[string]any{
		"actor": actor,
	}); err != nil {
		return domain.OnboardingRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("commit onboarding decision: %w", err)
	}
	return run, nil
}

func (r OnboardingRepoPG) BeginApply(ctx context.Context, id string) (domain.OnboardingRun, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("begin onboarding apply transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	run, err := scanOnboardingRun(tx.QueryRow(ctx, `SELECT `+onboardingColumns+` FROM onboarding_run WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return domain.OnboardingRun{}, mapProjectError(err)
	}
	if run.Status == domain.OnboardingStatusCompleted {
		if err := tx.Commit(ctx); err != nil {
			return domain.OnboardingRun{}, fmt.Errorf("commit repeated onboarding apply: %w", err)
		}
		return run, nil
	}
	if run.Status == domain.OnboardingStatusApplying {
		return domain.OnboardingRun{}, fmt.Errorf("onboarding apply is already in progress: %w", domain.ErrConflict)
	}
	if run.DryRun || run.Status != domain.OnboardingStatusAwaitingApproval && run.Status != domain.OnboardingStatusFailed || run.ApprovalID == nil {
		return domain.OnboardingRun{}, fmt.Errorf("onboarding is not ready to apply: %w", domain.ErrInvalidStatus)
	}
	var approvalStatus domain.ApprovalStatus
	if err := tx.QueryRow(ctx, `SELECT status FROM approval WHERE id = $1`, *run.ApprovalID).Scan(&approvalStatus); err != nil {
		return domain.OnboardingRun{}, mapProjectError(err)
	}
	if approvalStatus != domain.ApprovalStatusApproved {
		return domain.OnboardingRun{}, domain.ErrApprovalNeeded
	}
	run, err = scanOnboardingRun(tx.QueryRow(ctx, `
UPDATE onboarding_run SET status = 'applying', error = NULL, updated_at = now()
WHERE id = $1 RETURNING `+onboardingColumns, id))
	if err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("mark onboarding applying: %w", err)
	}
	if err := insertResourceAuditTx(ctx, tx, "onboarding_run", "onboarding.applying", run.ID, map[string]any{}); err != nil {
		return domain.OnboardingRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("commit onboarding apply state: %w", err)
	}
	return run, nil
}

func (r OnboardingRepoPG) RecordPublication(
	ctx context.Context,
	id string,
	publication domain.OnboardingPublication,
) (domain.OnboardingRun, error) {
	if !publication.Published || publication.GitLabProjectID <= 0 || publication.MergeRequestIID <= 0 ||
		strings.TrimSpace(publication.MergeRequestURL) == "" {
		return domain.OnboardingRun{}, fmt.Errorf("incomplete GitLab publication: %w", domain.ErrValidation)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("begin onboarding publication transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	run, err := scanOnboardingRun(tx.QueryRow(ctx, `SELECT `+onboardingColumns+` FROM onboarding_run WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return domain.OnboardingRun{}, mapProjectError(err)
	}
	if run.Status == domain.OnboardingStatusMRCreated && run.MergeRequestURL != nil && *run.MergeRequestURL == publication.MergeRequestURL {
		if err := tx.Commit(ctx); err != nil {
			return domain.OnboardingRun{}, fmt.Errorf("commit repeated onboarding publication: %w", err)
		}
		return run, nil
	}
	if run.Status != domain.OnboardingStatusApplying {
		return domain.OnboardingRun{}, fmt.Errorf("onboarding is not applying: %w", domain.ErrInvalidStatus)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO gitlab_link (
    resource_type, resource_id, gitlab_project_id, merge_request_iid, url
) VALUES ('onboarding_run', $1, $2, $3, $4)
ON CONFLICT (resource_type, resource_id, gitlab_project_id)
DO UPDATE SET merge_request_iid = EXCLUDED.merge_request_iid, url = EXCLUDED.url`,
		id, publication.GitLabProjectID, publication.MergeRequestIID, publication.MergeRequestURL,
	); err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("persist onboarding GitLab link: %w", err)
	}
	run, err = scanOnboardingRun(tx.QueryRow(ctx, `
UPDATE onboarding_run SET status = 'merge_request_created', merge_request_url = $2, updated_at = now()
WHERE id = $1 RETURNING `+onboardingColumns, id, publication.MergeRequestURL))
	if err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("mark onboarding merge request created: %w", err)
	}
	if err := insertResourceAuditTx(ctx, tx, "onboarding_run", "onboarding.merge_request_created", id, map[string]any{
		"gitlab_project_id": publication.GitLabProjectID,
		"merge_request_iid": publication.MergeRequestIID,
		"url":               publication.MergeRequestURL,
	}); err != nil {
		return domain.OnboardingRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("commit onboarding publication: %w", err)
	}
	return run, nil
}

func (r OnboardingRepoPG) CompleteApply(
	ctx context.Context,
	id string,
	result domain.OnboardingApplyResult,
) (domain.OnboardingRun, error) {
	checks, err := json.Marshal(result.Checks)
	if err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("marshal onboarding checks: %w", err)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("begin complete onboarding transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	run, err := scanOnboardingRun(tx.QueryRow(ctx, `
UPDATE onboarding_run SET
    status = 'completed', worktree_path = NULLIF($2, ''), branch_name = NULLIF($3, ''),
    commit_sha = NULLIF($4, ''), checks = $5, error = NULL,
    updated_at = now(), applied_at = now()
WHERE id = $1
RETURNING `+onboardingColumns, id, result.WorktreePath, result.BranchName, result.CommitSHA, checks))
	if err != nil {
		return domain.OnboardingRun{}, mapProjectError(err)
	}
	if err := insertResourceAuditTx(ctx, tx, "onboarding_run", "onboarding.completed", run.ID, map[string]any{
		"commit_sha": result.CommitSHA,
		"dry_run":    result.DryRun,
	}); err != nil {
		return domain.OnboardingRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("commit completed onboarding: %w", err)
	}
	return run, nil
}

func (r OnboardingRepoPG) FailApply(ctx context.Context, id, message string) error {
	message = strings.TrimSpace(message)
	if len(message) > 2000 {
		message = message[:2000]
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin failed onboarding transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
UPDATE onboarding_run SET status = 'failed', error = NULLIF($2, ''), updated_at = now()
WHERE id = $1`, id, message)
	if err != nil {
		return fmt.Errorf("mark onboarding failed: %w", err)
	}
	if command.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	if err := insertResourceAuditTx(ctx, tx, "onboarding_run", "onboarding.failed", id, map[string]any{}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit failed onboarding state: %w", err)
	}
	return nil
}

const onboardingColumns = `id, project_id, snapshot_id, approval_id, status, dry_run,
base_commit, base_branch, proposal_checksum, proposal, unified_diff,
worktree_path, branch_name, commit_sha, merge_request_url, checks, error, created_at, updated_at, applied_at`

func scanOnboardingRun(row rowScanner) (domain.OnboardingRun, error) {
	var run domain.OnboardingRun
	var proposal []byte
	var checks []byte
	err := row.Scan(
		&run.ID, &run.ProjectID, &run.SnapshotID, &run.ApprovalID, &run.Status,
		&run.DryRun, &run.BaseCommit, &run.BaseBranch, &run.ProposalChecksum,
		&proposal, &run.UnifiedDiff, &run.WorktreePath, &run.BranchName,
		&run.CommitSHA, &run.MergeRequestURL, &checks, &run.Error, &run.CreatedAt, &run.UpdatedAt,
		&run.AppliedAt,
	)
	if err != nil {
		return domain.OnboardingRun{}, err
	}
	if err := json.Unmarshal(proposal, &run.Proposal); err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("decode onboarding proposal: %w", err)
	}
	if err := json.Unmarshal(checks, &run.Checks); err != nil {
		return domain.OnboardingRun{}, fmt.Errorf("decode onboarding checks: %w", err)
	}
	if run.Checks == nil {
		run.Checks = []domain.OnboardingCheck{}
	}
	return run, nil
}
