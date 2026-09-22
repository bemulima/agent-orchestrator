package architecturetarget

import (
	"context"
	"fmt"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// ApproveInput binds an approval to the exact submitted TARGET version the
// reviewer inspected. ExpectedFingerprint and ExpectedRevision are both
// required so neither a revised proposal nor changed persisted content can be
// decided accidentally.
type ApproveInput struct {
	ProposalID          string
	ExpectedRevision    int
	ExpectedFingerprint string
	Actor               string
	Comment             string
}

// RejectInput binds a rejection to the exact submitted TARGET version the
// reviewer inspected. A comment is optional, but the deciding actor is not.
type RejectInput struct {
	ProposalID          string
	ExpectedRevision    int
	ExpectedFingerprint string
	Actor               string
	Comment             string
}

// RequestChangesInput records actionable requested changes against one exact
// submitted TARGET version. Comment is mandatory so this transition cannot be
// used as an unexplained rejection.
type RequestChangesInput struct {
	ProposalID          string
	ExpectedRevision    int
	ExpectedFingerprint string
	Actor               string
	Comment             string
}

// Approve decides a submitted TARGET proposal without modifying its reviewed
// structured content. Authorization of the named actor belongs to the HTTP
// adapter/auth policy; this use case requires the actor identity explicitly.
type Approve struct{ Store Store }

func (uc Approve) Handle(ctx context.Context, input ApproveInput) (domain.ArchitectureTargetProposal, error) {
	return decide(ctx, uc.Store, decisionInput{proposalID: input.ProposalID, expectedRevision: input.ExpectedRevision, expectedFingerprint: input.ExpectedFingerprint, actor: input.Actor, comment: input.Comment}, domain.ArchitectureTargetStatusApproved, false)
}

// Reject decides a submitted TARGET proposal without modifying its reviewed
// structured content.
type Reject struct{ Store Store }

func (uc Reject) Handle(ctx context.Context, input RejectInput) (domain.ArchitectureTargetProposal, error) {
	return decide(ctx, uc.Store, decisionInput{proposalID: input.ProposalID, expectedRevision: input.ExpectedRevision, expectedFingerprint: input.ExpectedFingerprint, actor: input.Actor, comment: input.Comment}, domain.ArchitectureTargetStatusRejected, false)
}

// RequestChanges records a review request for the submitted TARGET proposal.
// It does not reopen or rewrite the submitted content; a follow-up proposal
// remains a separate S5/S6 lifecycle action.
type RequestChanges struct{ Store Store }

func (uc RequestChanges) Handle(ctx context.Context, input RequestChangesInput) (domain.ArchitectureTargetProposal, error) {
	return decide(ctx, uc.Store, decisionInput{proposalID: input.ProposalID, expectedRevision: input.ExpectedRevision, expectedFingerprint: input.ExpectedFingerprint, actor: input.Actor, comment: input.Comment}, domain.ArchitectureTargetStatusChangesRequested, true)
}

type decisionInput struct {
	proposalID          string
	expectedRevision    int
	expectedFingerprint string
	actor               string
	comment             string
}

func decide(ctx context.Context, store Store, input decisionInput, status domain.ArchitectureTargetStatus, commentRequired bool) (domain.ArchitectureTargetProposal, error) {
	if store == nil {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target proposal store is not configured: %w", domain.ErrValidation)
	}
	input.proposalID = strings.TrimSpace(input.proposalID)
	input.expectedFingerprint = strings.TrimSpace(input.expectedFingerprint)
	input.actor = strings.TrimSpace(input.actor)
	input.comment = strings.TrimSpace(input.comment)
	if input.proposalID == "" || input.expectedRevision < 1 || input.expectedFingerprint == "" || input.actor == "" {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target proposal ID, positive expected revision, expected fingerprint, and actor are required: %w", domain.ErrValidation)
	}
	if commentRequired && input.comment == "" {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("requested changes require a comment: %w", domain.ErrValidation)
	}

	// Always use a fresh persisted copy.  In particular, never trust a proposal
	// previously supplied by an adapter as the basis for a lifecycle decision.
	proposal, err := store.Get(ctx, input.proposalID)
	if err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	if proposal.Status != domain.ArchitectureTargetStatusSubmitted {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("only a submitted target proposal can be decided: %w", domain.ErrInvalidStatus)
	}
	if proposal.Revision != input.expectedRevision {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target proposal revision is stale: %w", domain.ErrConflict)
	}
	// Valid rechecks every typed change, diff and impact.  Then explicitly
	// recompute the checksum and compare it to both stored and caller-bound
	// fingerprints. This preserves approval semantics even with a faulty store.
	if err := proposal.Valid(); err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	computed, err := proposal.ComputedFingerprint()
	if err != nil {
		return domain.ArchitectureTargetProposal{}, err
	}
	if proposal.Fingerprint == "" || proposal.Fingerprint != computed || input.expectedFingerprint != computed {
		return domain.ArchitectureTargetProposal{}, fmt.Errorf("target proposal fingerprint is stale or invalid: %w", domain.ErrConflict)
	}

	return store.Transition(ctx, proposal.ID, status, input.expectedRevision, input.actor, input.comment)
}
