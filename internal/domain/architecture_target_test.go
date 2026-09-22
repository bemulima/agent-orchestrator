package domain

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArchitectureTargetProposalValidAndDeterministicFingerprint(t *testing.T) {
	proposal := validArchitectureTargetProposal()
	first, err := proposal.ComputedFingerprint()
	require.NoError(t, err)

	// Ordering in a UI payload must not change the owner-reviewed content.
	proposal.Changes[0].DesiredResult.SuccessCriteria = []string{"second criterion", "first criterion"}
	proposal.Diff.Entries[0].DesiredResult.SuccessCriteria = []string{"second criterion", "first criterion"}
	second, err := proposal.ComputedFingerprint()
	require.NoError(t, err)
	require.Equal(t, first, second)
	proposal.Fingerprint = second
	require.NoError(t, proposal.Validate())
}

func TestArchitectureTargetProposalRejectsUnboundDiffAndRawYAML(t *testing.T) {
	proposal := validArchitectureTargetProposal()
	proposal.Diff.BaseFingerprint = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	proposal.Fingerprint, _ = proposal.ComputedFingerprint()
	err := proposal.Validate()
	require.ErrorIs(t, err, ErrConflict)

	proposal = validArchitectureTargetProposal()
	proposal.Changes[0].Rationale.Details = "kind: Deployment"
	proposal.Diff.Entries[0].Rationale.Details = "kind: Deployment"
	proposal.Fingerprint, _ = proposal.ComputedFingerprint()
	err = proposal.Validate()
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrValidation))
}

func TestArchitectureTargetProposalRejectsExistingIDForAddedService(t *testing.T) {
	proposal := validArchitectureTargetProposal()
	proposal.Changes[0].AffectedServiceIDs = []string{"ms-new"}
	proposal.Diff.Entries[0].AffectedServiceIDs = []string{"ms-new"}
	proposal.Fingerprint, _ = proposal.ComputedFingerprint()
	require.ErrorIs(t, proposal.Validate(), ErrValidation)
}

func TestCanTransitionArchitectureTarget(t *testing.T) {
	require.True(t, CanTransitionArchitectureTarget(ArchitectureTargetStatusSubmitted, ArchitectureTargetStatusChangesRequested))
	require.True(t, CanTransitionArchitectureTarget(ArchitectureTargetStatusChangesRequested, ArchitectureTargetStatusSuperseded))
	require.False(t, CanTransitionArchitectureTarget(ArchitectureTargetStatusApproved, ArchitectureTargetStatusDraft))
}

func validArchitectureTargetProposal() ArchitectureTargetProposal {
	const current = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	evidence := []ArchitectureEvidence{{SourcePath: "internal/app/service.go", Symbol: "Register", StartLine: 12, EndLine: 21, Checksum: "source-checksum"}}
	change := ArchitectureTargetChange{
		ID:                 "service-add-new",
		Scope:              ArchitectureTargetScopeService,
		Action:             ArchitectureTargetChangeAdd,
		Kind:               ArchitectureTargetChangeKindService,
		AffectedServiceIDs: []string{"proposed:ms-new"},
		Rationale:          ArchitectureTargetRationale{Summary: "Separate a bounded responsibility"},
		DesiredResult:      ArchitectureTargetDesiredResult{Summary: "A proposed service is explicitly catalogued", SuccessCriteria: []string{"first criterion", "second criterion"}},
		Evidence:           evidence,
		Confidence:         0.8,
		UnresolvedAreas:    []ArchitectureTargetUnresolved{{Area: "runtime capacity", Reason: "No checked-in deployment sizing evidence"}},
	}
	diff := ArchitectureTargetDiffEntry{
		ChangeID:           change.ID,
		Scope:              change.Scope,
		Action:             change.Action,
		Kind:               change.Kind,
		AffectedServiceIDs: append([]string(nil), change.AffectedServiceIDs...),
		Summary:            "Add proposed service ms-new",
		Rationale:          change.Rationale,
		DesiredResult:      change.DesiredResult,
		Evidence:           append([]ArchitectureEvidence(nil), evidence...),
		Confidence:         change.Confidence,
		UnresolvedAreas:    append([]ArchitectureTargetUnresolved(nil), change.UnresolvedAreas...),
	}
	proposal := ArchitectureTargetProposal{
		CurrentFingerprint: current,
		Status:             ArchitectureTargetStatusDraft,
		Revision:           1,
		IdempotencyKey:     "target-create-1",
		Changes:            []ArchitectureTargetChange{change},
		Diff:               ArchitectureTargetDiff{BaseFingerprint: current, Entries: []ArchitectureTargetDiffEntry{diff}},
		Impact: ArchitectureTargetImpact{BaseFingerprint: current, Entries: []ArchitectureTargetImpactEntry{{
			ChangeID: change.ID, ServiceID: "proposed:ms-new", Kind: ArchitectureTargetImpactUnresolved,
			Explanation: "No checked-in relation evidence resolves downstream consumers", Evidence: evidence,
		}}},
	}
	proposal.Fingerprint, _ = proposal.ComputedFingerprint()
	return proposal
}
