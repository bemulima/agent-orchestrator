package architecturetarget

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestApprovalDecisionsTransitionOnlyTheExactSubmittedProposal(t *testing.T) {
	tests := []struct {
		name   string
		status domain.ArchitectureTargetStatus
		decide func(context.Context, *approvalStoreFake, domain.ArchitectureTargetProposal) (domain.ArchitectureTargetProposal, error)
	}{
		{
			name:   "approve",
			status: domain.ArchitectureTargetStatusApproved,
			decide: func(ctx context.Context, store *approvalStoreFake, proposal domain.ArchitectureTargetProposal) (domain.ArchitectureTargetProposal, error) {
				return (Approve{Store: store}).Handle(ctx, ApproveInput{ProposalID: proposal.ID, ExpectedRevision: proposal.Revision, ExpectedFingerprint: proposal.Fingerprint, Actor: "architecture-owner", Comment: "approved"})
			},
		},
		{
			name:   "reject",
			status: domain.ArchitectureTargetStatusRejected,
			decide: func(ctx context.Context, store *approvalStoreFake, proposal domain.ArchitectureTargetProposal) (domain.ArchitectureTargetProposal, error) {
				return (Reject{Store: store}).Handle(ctx, RejectInput{ProposalID: proposal.ID, ExpectedRevision: proposal.Revision, ExpectedFingerprint: proposal.Fingerprint, Actor: "architecture-owner", Comment: "insufficient evidence"})
			},
		},
		{
			name:   "request changes",
			status: domain.ArchitectureTargetStatusChangesRequested,
			decide: func(ctx context.Context, store *approvalStoreFake, proposal domain.ArchitectureTargetProposal) (domain.ArchitectureTargetProposal, error) {
				return (RequestChanges{Store: store}).Handle(ctx, RequestChangesInput{ProposalID: proposal.ID, ExpectedRevision: proposal.Revision, ExpectedFingerprint: proposal.Fingerprint, Actor: "architecture-owner", Comment: "add contract evidence"})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proposal := approvalSubmittedProposal(t)
			before := proposal
			store := &approvalStoreFake{proposal: proposal}
			result, err := tt.decide(context.Background(), store, proposal)
			if err != nil {
				t.Fatalf("decide: %v", err)
			}
			if result.Status != tt.status || store.transitionStatus != tt.status || store.transitionExpected != before.Revision || store.transitionActor != "architecture-owner" {
				t.Fatalf("unexpected transition: result=%#v store=%#v", result, store)
			}
			if !reflect.DeepEqual(result.Changes, before.Changes) || !reflect.DeepEqual(result.Diff, before.Diff) || !reflect.DeepEqual(result.Impact, before.Impact) || result.CurrentFingerprint != before.CurrentFingerprint || result.Fingerprint != before.Fingerprint {
				t.Fatalf("decision mutated submitted TARGET content: before=%#v after=%#v", before, result)
			}
		})
	}
}

func TestApprovalDecisionsRejectStaleRevisionFingerprintAndStatus(t *testing.T) {
	t.Run("stale revision", func(t *testing.T) {
		proposal := approvalSubmittedProposal(t)
		store := &approvalStoreFake{proposal: proposal}
		_, err := (Approve{Store: store}).Handle(context.Background(), ApproveInput{ProposalID: proposal.ID, ExpectedRevision: proposal.Revision - 1, ExpectedFingerprint: proposal.Fingerprint, Actor: "owner"})
		if !errors.Is(err, domain.ErrConflict) || store.transitionCalls != 0 {
			t.Fatalf("stale revision error = %v, transition calls = %d; want conflict without transition", err, store.transitionCalls)
		}
	})

	t.Run("stale fingerprint", func(t *testing.T) {
		proposal := approvalSubmittedProposal(t)
		store := &approvalStoreFake{proposal: proposal}
		_, err := (Reject{Store: store}).Handle(context.Background(), RejectInput{ProposalID: proposal.ID, ExpectedRevision: proposal.Revision, ExpectedFingerprint: strings.Repeat("b", 64), Actor: "owner"})
		if !errors.Is(err, domain.ErrConflict) || store.transitionCalls != 0 {
			t.Fatalf("stale fingerprint error = %v, transition calls = %d; want conflict without transition", err, store.transitionCalls)
		}
	})

	t.Run("persisted content no longer matches its fingerprint", func(t *testing.T) {
		proposal := approvalSubmittedProposal(t)
		proposal.Changes[0].Rationale.Summary = "tampered after submission"
		store := &approvalStoreFake{proposal: proposal}
		_, err := (Approve{Store: store}).Handle(context.Background(), ApproveInput{ProposalID: proposal.ID, ExpectedRevision: proposal.Revision, ExpectedFingerprint: proposal.Fingerprint, Actor: "owner"})
		if !errors.Is(err, domain.ErrConflict) || store.transitionCalls != 0 {
			t.Fatalf("tampered persisted content error = %v, transition calls = %d; want conflict without transition", err, store.transitionCalls)
		}
	})

	t.Run("not submitted", func(t *testing.T) {
		proposal := approvalSubmittedProposal(t)
		proposal.Status = domain.ArchitectureTargetStatusDraft
		store := &approvalStoreFake{proposal: proposal}
		_, err := (RequestChanges{Store: store}).Handle(context.Background(), RequestChangesInput{ProposalID: proposal.ID, ExpectedRevision: proposal.Revision, ExpectedFingerprint: proposal.Fingerprint, Actor: "owner", Comment: "explain diff"})
		if !errors.Is(err, domain.ErrInvalidStatus) || store.transitionCalls != 0 {
			t.Fatalf("non-submitted error = %v, transition calls = %d; want invalid status without transition", err, store.transitionCalls)
		}
	})
}

func TestApprovalDecisionsRequireNamedActorAndRequestChangesComment(t *testing.T) {
	proposal := approvalSubmittedProposal(t)
	store := &approvalStoreFake{proposal: proposal}
	_, err := (Approve{Store: store}).Handle(context.Background(), ApproveInput{ProposalID: proposal.ID, ExpectedRevision: proposal.Revision, ExpectedFingerprint: proposal.Fingerprint})
	if !errors.Is(err, domain.ErrValidation) || store.transitionCalls != 0 {
		t.Fatalf("missing actor error = %v, transition calls = %d; want validation without transition", err, store.transitionCalls)
	}
	_, err = (RequestChanges{Store: store}).Handle(context.Background(), RequestChangesInput{ProposalID: proposal.ID, ExpectedRevision: proposal.Revision, ExpectedFingerprint: proposal.Fingerprint, Actor: "owner"})
	if !errors.Is(err, domain.ErrValidation) || store.transitionCalls != 0 {
		t.Fatalf("missing request changes comment error = %v, transition calls = %d; want validation without transition", err, store.transitionCalls)
	}
}

func approvalSubmittedProposal(t *testing.T) domain.ArchitectureTargetProposal {
	t.Helper()
	proposal := validTargetProposal(t, targetTestCatalog(), targetOperationChange("approval"))
	proposal.ID = "proposal-approval"
	proposal.Revision = 7
	proposal.Status = domain.ArchitectureTargetStatusSubmitted
	return proposal
}

type approvalStoreFake struct {
	proposal           domain.ArchitectureTargetProposal
	transitionCalls    int
	transitionStatus   domain.ArchitectureTargetStatus
	transitionExpected int
	transitionActor    string
	transitionComment  string
}

func (f *approvalStoreFake) Create(context.Context, domain.ArchitectureTargetProposal) (domain.ArchitectureTargetProposal, error) {
	panic("Create must not be called by approval")
}

func (f *approvalStoreFake) Get(_ context.Context, _ string) (domain.ArchitectureTargetProposal, error) {
	return f.proposal, nil
}

func (f *approvalStoreFake) UpdateDraft(context.Context, domain.ArchitectureTargetProposal, int) (domain.ArchitectureTargetProposal, error) {
	panic("UpdateDraft must not be called by approval")
}

func (f *approvalStoreFake) Transition(_ context.Context, _ string, status domain.ArchitectureTargetStatus, expected int, actor, comment string) (domain.ArchitectureTargetProposal, error) {
	f.transitionCalls++
	f.transitionStatus, f.transitionExpected, f.transitionActor, f.transitionComment = status, expected, actor, comment
	f.proposal.Status = status
	return f.proposal, nil
}
