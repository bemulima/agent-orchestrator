package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// ArchitectureTargetRepoPG persists the editable TARGET side of the
// Architecture Control Center. It never alters architecture CURRENT rows.
type ArchitectureTargetRepoPG struct {
	Pool *pgxpool.Pool
}

func (r ArchitectureTargetRepoPG) Create(ctx context.Context, proposal domain.ArchitectureTargetProposal) (domain.ArchitectureTargetProposal, error) {
	proposal.IdempotencyKey = strings.TrimSpace(proposal.IdempotencyKey)
	proposal.SupersedesProposalID = strings.TrimSpace(proposal.SupersedesProposalID)
	if proposal.Status == "" {
		proposal.Status = domain.ArchitectureTargetStatusDraft
	}
	if proposal.Revision == 0 {
		proposal.Revision = 1
	}
	if proposal.Status != domain.ArchitectureTargetStatusDraft || proposal.Revision != 1 || proposal.ID != "" {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("only a new revision-one draft may be created: %w", domain.ErrValidation)
	}
	fingerprint, err := proposal.ComputedFingerprint()
	if err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	proposal.Fingerprint = fingerprint
	if err := proposal.Validate(); err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}

	changes, diff, impact, err := marshalArchitectureTargetContent(proposal)
	if err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("begin architecture target create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "architecture-target:"+proposal.IdempotencyKey); err != nil {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("lock architecture target create: %w", err)
	}
	existing, err := scanArchitectureTargetProposal(tx.QueryRow(ctx, `SELECT `+architectureTargetColumns+` FROM architecture_target_proposal WHERE idempotency_key = $1`, proposal.IdempotencyKey))
	if err == nil {
		if existing.CurrentFingerprint != proposal.CurrentFingerprint || existing.Fingerprint != proposal.Fingerprint {
			return domain.ArchitectureTargetProposal{}, fmt.Errorf("idempotency key belongs to a different target proposal: %w", domain.ErrConflict)
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.ArchitectureTargetProposal{}, fmt.Errorf("commit reused architecture target: %w", err)
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("find target proposal by idempotency key: %w", err)
	}

	if proposal.SupersedesProposalID != "" {
		var parentFingerprint string
		var parentStatus domain.ArchitectureTargetStatus
		if err := tx.QueryRow(ctx, `
SELECT current_fingerprint, status
FROM architecture_target_proposal
WHERE id = $1
FOR UPDATE`, proposal.SupersedesProposalID).Scan(&parentFingerprint, &parentStatus); err != nil {
			return domain.ArchitectureTargetProposal{}, mapArchitectureTargetError(err)
		}
		if parentFingerprint != proposal.CurrentFingerprint || parentStatus != domain.ArchitectureTargetStatusChangesRequested {
			return domain.ArchitectureTargetProposal{}, fmt.Errorf("successor draft must replace a changes-requested proposal at the same CURRENT fingerprint: %w", domain.ErrConflict)
		}
		if _, err := tx.Exec(ctx, `
UPDATE architecture_target_proposal
SET status = 'superseded', revision = revision + 1, superseded_at = now(), updated_at = now()
WHERE id = $1 AND status = 'changes_requested'`, proposal.SupersedesProposalID); err != nil {
			return domain.ArchitectureTargetProposal{}, fmt.Errorf("supersede changes-requested target: %w", err)
		}
		if err := insertResourceAuditTx(ctx, tx, "architecture_target_proposal", "architecture_target.superseded", proposal.SupersedesProposalID, map[string]any{
			"reason": "successor_draft_created",
		}); err != nil {
			return domain.ArchitectureTargetProposal{}, err
		}
	}

	proposal, err = scanArchitectureTargetProposal(tx.QueryRow(ctx, `
INSERT INTO architecture_target_proposal (
    current_fingerprint, fingerprint, status, revision, idempotency_key,
    supersedes_proposal_id, changes, diff, impact
) VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::uuid, $7, $8, $9)
RETURNING `+architectureTargetColumns,
		proposal.CurrentFingerprint, proposal.Fingerprint, proposal.Status, proposal.Revision,
		proposal.IdempotencyKey, proposal.SupersedesProposalID, changes, diff, impact))
	if err != nil {
		return domain.ArchitectureTargetProposal{}, mapArchitectureTargetError(err)
	}
	if err := insertResourceAuditTx(ctx, tx, "architecture_target_proposal", "architecture_target.created", proposal.ID, map[string]any{
		"current_fingerprint": proposal.CurrentFingerprint,
		"fingerprint":         proposal.Fingerprint,
		"change_count":        len(proposal.Changes),
		"supersedes_id":       proposal.SupersedesProposalID,
	}); err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("commit architecture target create: %w", err)
	}
	return proposal, nil
}

func (r ArchitectureTargetRepoPG) Get(ctx context.Context, id string) (domain.ArchitectureTargetProposal, error) {
	proposal, err := scanArchitectureTargetProposal(r.Pool.QueryRow(ctx, `
SELECT `+architectureTargetColumns+`
FROM architecture_target_proposal
WHERE id = $1`, strings.TrimSpace(id)))
	return proposal, mapArchitectureTargetError(err)
}

func (r ArchitectureTargetRepoPG) List(ctx context.Context, statuses []domain.ArchitectureTargetStatus) ([]domain.ArchitectureTargetProposal, error) {
	values := make([]string, 0, len(statuses))
	for _, status := range statuses {
		if !isArchitectureTargetStatus(status) {
			return nil, fmt.Errorf("target list status %q: %w", status, domain.ErrValidation)
		}
		values = append(values, string(status))
	}
	rows, err := r.Pool.Query(ctx, `
SELECT `+architectureTargetColumns+`
FROM architecture_target_proposal
WHERE cardinality($1::varchar[]) = 0 OR status = ANY($1::varchar[])
ORDER BY updated_at DESC, id DESC`, values)
	if err != nil {
		return nil, fmt.Errorf("list architecture targets: %w", err)
	}
	defer rows.Close()
	result := make([]domain.ArchitectureTargetProposal, 0)
	for rows.Next() {
		proposal, scanErr := scanArchitectureTargetProposal(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, proposal)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate architecture targets: %w", err)
	}
	return result, nil
}

func (r ArchitectureTargetRepoPG) UpdateDraft(ctx context.Context, proposal domain.ArchitectureTargetProposal, expectedRevision int) (domain.ArchitectureTargetProposal, error) {
	if strings.TrimSpace(proposal.ID) == "" || expectedRevision < 1 || proposal.Revision != expectedRevision {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target draft id and expected revision are required: %w", domain.ErrValidation)
	}
	proposal.Status = domain.ArchitectureTargetStatusDraft
	proposal.IdempotencyKey = strings.TrimSpace(proposal.IdempotencyKey)
	fingerprint, err := proposal.ComputedFingerprint()
	if err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	proposal.Fingerprint = fingerprint
	if err := proposal.Validate(); err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	changes, diff, impact, err := marshalArchitectureTargetContent(proposal)
	if err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("begin target draft update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanArchitectureTargetProposal(tx.QueryRow(ctx, `
SELECT `+architectureTargetColumns+`
FROM architecture_target_proposal
WHERE id = $1
FOR UPDATE`, proposal.ID))
	if err != nil {
		return domain.ArchitectureTargetProposal{}, mapArchitectureTargetError(err)
	}
	if current.Status != domain.ArchitectureTargetStatusDraft || current.Revision != expectedRevision {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target draft is stale or no longer editable: %w", domain.ErrConflict)
	}
	if current.CurrentFingerprint != proposal.CurrentFingerprint || current.IdempotencyKey != proposal.IdempotencyKey || current.SupersedesProposalID != proposal.SupersedesProposalID {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target CURRENT binding and creation identity are immutable: %w", domain.ErrConflict)
	}
	updated, err := scanArchitectureTargetProposal(tx.QueryRow(ctx, `
UPDATE architecture_target_proposal
SET fingerprint = $2, changes = $3, diff = $4, impact = $5,
    revision = revision + 1, updated_at = now()
WHERE id = $1 AND status = 'draft' AND revision = $6
RETURNING `+architectureTargetColumns,
		proposal.ID, proposal.Fingerprint, changes, diff, impact, expectedRevision))
	if err != nil {
		return domain.ArchitectureTargetProposal{}, mapArchitectureTargetError(err)
	}
	if err := insertResourceAuditTx(ctx, tx, "architecture_target_proposal", "architecture_target.draft_updated", updated.ID, map[string]any{
		"revision": updated.Revision, "fingerprint": updated.Fingerprint, "change_count": len(updated.Changes),
	}); err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("commit target draft update: %w", err)
	}
	return updated, nil
}

func (r ArchitectureTargetRepoPG) Transition(ctx context.Context, id string, target domain.ArchitectureTargetStatus, expectedRevision int, actor, comment string) (domain.ArchitectureTargetProposal, error) {
	id, actor, comment = strings.TrimSpace(id), strings.TrimSpace(actor), strings.TrimSpace(comment)
	if id == "" || expectedRevision < 1 || !isArchitectureTargetStatus(target) || target == domain.ArchitectureTargetStatusDraft {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target transition identity, revision, and status are required: %w", domain.ErrValidation)
	}
	if (target == domain.ArchitectureTargetStatusApproved || target == domain.ArchitectureTargetStatusRejected || target == domain.ArchitectureTargetStatusChangesRequested) && actor == "" {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target decision actor is required: %w", domain.ErrValidation)
	}
	if target == domain.ArchitectureTargetStatusChangesRequested && comment == "" {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("requested changes require an audit comment: %w", domain.ErrValidation)
	}
	if err := validateArchitectureTargetTransitionComment(comment); err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("begin target transition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanArchitectureTargetProposal(tx.QueryRow(ctx, `
SELECT `+architectureTargetColumns+`
FROM architecture_target_proposal
WHERE id = $1
FOR UPDATE`, id))
	if err != nil {
		return domain.ArchitectureTargetProposal{}, mapArchitectureTargetError(err)
	}
	if current.Revision != expectedRevision || !domain.CanTransitionArchitectureTarget(current.Status, target) {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target transition is stale or invalid: %w", domain.ErrConflict)
	}
	updated, err := scanArchitectureTargetProposal(tx.QueryRow(ctx, `
UPDATE architecture_target_proposal
SET status = $2::varchar,
    revision = revision + 1,
    updated_at = now(),
    submitted_at = CASE WHEN $2::varchar = 'submitted' THEN now() ELSE submitted_at END,
    decided_at = CASE WHEN $2::varchar IN ('approved', 'rejected') THEN now() ELSE decided_at END,
    decided_by = CASE WHEN $2::varchar IN ('approved', 'rejected') THEN $3 ELSE decided_by END,
    decision_comment = CASE WHEN $2::varchar IN ('approved', 'rejected') THEN $4 ELSE decision_comment END,
    changes_requested_at = CASE WHEN $2::varchar = 'changes_requested' THEN now() ELSE changes_requested_at END,
    changes_requested_by = CASE WHEN $2::varchar = 'changes_requested' THEN $3 ELSE changes_requested_by END,
    changes_request_comment = CASE WHEN $2::varchar = 'changes_requested' THEN $4 ELSE changes_request_comment END,
    superseded_at = CASE WHEN $2::varchar = 'superseded' THEN now() ELSE superseded_at END
WHERE id = $1 AND revision = $5
RETURNING `+architectureTargetColumns, id, target, actor, comment, expectedRevision))
	if err != nil {
		return domain.ArchitectureTargetProposal{}, mapArchitectureTargetError(err)
	}
	if err := insertResourceAuditTx(ctx, tx, "architecture_target_proposal", "architecture_target."+string(target), updated.ID, map[string]any{
		"from_status": current.Status, "to_status": target, "revision": updated.Revision, "actor": actor,
	}); err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("commit target transition: %w", err)
	}
	return updated, nil
}

const architectureTargetColumns = `id, current_fingerprint, fingerprint, status, revision, idempotency_key,
COALESCE(supersedes_proposal_id::text, ''), changes, diff, impact, submitted_at, decided_at, superseded_at,
COALESCE(decided_by, ''), decision_comment, COALESCE(changes_requested_by, ''), changes_requested_at,
changes_request_comment, created_at, updated_at`

func scanArchitectureTargetProposal(row rowScanner) (domain.ArchitectureTargetProposal, error) {
	var proposal domain.ArchitectureTargetProposal
	var changes, diff, impact []byte
	err := row.Scan(
		&proposal.ID, &proposal.CurrentFingerprint, &proposal.Fingerprint, &proposal.Status, &proposal.Revision, &proposal.IdempotencyKey,
		&proposal.SupersedesProposalID, &changes, &diff, &impact, &proposal.SubmittedAt, &proposal.DecidedAt, &proposal.SupersededAt,
		&proposal.DecidedBy, &proposal.DecisionComment, &proposal.ChangesRequestedBy, &proposal.ChangesRequestedAt,
		&proposal.ChangesRequestComment, &proposal.CreatedAt, &proposal.UpdatedAt,
	)
	if err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	if err := json.Unmarshal(changes, &proposal.Changes); err != nil {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("decode target changes: %w", err)
	}
	if err := json.Unmarshal(diff, &proposal.Diff); err != nil {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("decode target diff: %w", err)
	}
	if err := json.Unmarshal(impact, &proposal.Impact); err != nil {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("decode target impact: %w", err)
	}
	if err := proposal.Validate(); err != nil {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("stored target proposal is invalid: %w", err)
	}
	return proposal, nil
}

func marshalArchitectureTargetContent(proposal domain.ArchitectureTargetProposal) ([]byte, []byte, []byte, error) {
	changes, err := json.Marshal(proposal.Changes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshal target changes: %w", err)
	}
	diff, err := json.Marshal(proposal.Diff)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshal target diff: %w", err)
	}
	impact, err := json.Marshal(proposal.Impact)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshal target impact: %w", err)
	}
	return changes, diff, impact, nil
}

func isArchitectureTargetStatus(status domain.ArchitectureTargetStatus) bool {
	switch status {
	case domain.ArchitectureTargetStatusDraft, domain.ArchitectureTargetStatusSubmitted, domain.ArchitectureTargetStatusApproved, domain.ArchitectureTargetStatusRejected, domain.ArchitectureTargetStatusChangesRequested, domain.ArchitectureTargetStatusSuperseded:
		return true
	default:
		return false
	}
}

func validateArchitectureTargetTransitionComment(comment string) error {
	if len(comment) > 4000 || strings.Contains(comment, "```") || targetRepositorySecretPattern.MatchString(comment) {
		return fmt.Errorf("target transition comment is invalid: %w", domain.ErrValidation)
	}
	return nil
}

var targetRepositorySecretPattern = regexp.MustCompile(`(?i)(password|secret|token|api[_-]?key|authorization)\s*[:=]\s*[^\s]+`)

func mapArchitectureTargetError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return fmt.Errorf("architecture target already exists: %w", domain.ErrConflict)
	}
	return err
}
