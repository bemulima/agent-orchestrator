package architecturetarget

import (
	"errors"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestVerifyApprovedTargetReturnsPendingForIncompleteCurrent(t *testing.T) {
	proposal := approvedVerificationProposal(t, targetOperationChange("pending"))
	current := targetTestCatalog()
	current.Fingerprint = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	current.Platform.Completeness.OperationsMissingManifests = 1

	report, err := (VerifyApprovedTarget{}).Handle(VerificationInput{Proposal: proposal, ExpectedFingerprint: proposal.Fingerprint, Current: current})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Status != VerificationStatusPending || len(report.Unknowns) != 1 {
		t.Fatalf("report = %#v, want pending with CURRENT unknown", report)
	}
}

func TestVerifyApprovedTargetTreatsUnchangedCurrentAsNotImplemented(t *testing.T) {
	proposal := approvedVerificationProposal(t, targetOperationChange("unchanged"))
	current := targetTestCatalog()

	report, err := (VerifyApprovedTarget{}).Handle(VerificationInput{Proposal: proposal, ExpectedFingerprint: proposal.Fingerprint, Current: current})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Status != VerificationStatusNotImplemented || len(report.Changes) != 1 || report.Changes[0].Status != VerificationStatusNotImplemented {
		t.Fatalf("report = %#v, want unchanged approved target to be not implemented", report)
	}
}

func TestVerifyApprovedTargetMatchesProvableRemovedOperation(t *testing.T) {
	change := targetOperationChange("remove-operation")
	change.Action = domain.ArchitectureTargetChangeRemove
	proposal := approvedVerificationProposal(t, change)
	current := targetTestCatalog()
	current.Fingerprint = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	current.Platform.Services[0].Ungrouped = nil

	report, err := (VerifyApprovedTarget{}).Handle(VerificationInput{Proposal: proposal, ExpectedFingerprint: proposal.Fingerprint, Current: current})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Status != VerificationStatusMatched || report.Changes[0].Status != VerificationStatusMatched || report.Confidence != 1 {
		t.Fatalf("report = %#v, want matched removed operation", report)
	}
}

func TestVerifyApprovedTargetReportsPartialWhenOnlyStructuralEffectIsProvable(t *testing.T) {
	remove := targetOperationChange("a-remove")
	remove.Action = domain.ArchitectureTargetChangeRemove
	semantic := targetOperationChange("z-semantic")
	semantic.Kind = domain.ArchitectureTargetChangeKindBusinessProcess
	proposal := approvedVerificationProposal(t, remove, semantic)
	current := targetTestCatalog()
	current.Fingerprint = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	current.Platform.Services[0].Ungrouped = nil

	report, err := (VerifyApprovedTarget{}).Handle(VerificationInput{Proposal: proposal, ExpectedFingerprint: proposal.Fingerprint, Current: current})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Status != VerificationStatusPartiallyImplemented || report.Changes[0].Status != VerificationStatusMatched || report.Changes[1].Status != VerificationStatusDrift || len(report.Changes[1].Unknowns) == 0 {
		t.Fatalf("report = %#v, want partial structural proof plus semantic unknown", report)
	}
}

func TestVerifyApprovedTargetReportsDriftForUnprovableSemanticTarget(t *testing.T) {
	change := targetOperationChange("semantic")
	change.Kind = domain.ArchitectureTargetChangeKindBusinessProcess
	proposal := approvedVerificationProposal(t, change)
	current := targetTestCatalog()
	current.Fingerprint = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	report, err := (VerifyApprovedTarget{}).Handle(VerificationInput{Proposal: proposal, ExpectedFingerprint: proposal.Fingerprint, Current: current})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Status != VerificationStatusDrift || len(report.Unknowns) == 0 || report.Changes[0].Status != VerificationStatusDrift {
		t.Fatalf("report = %#v, want drift with semantic unknown", report)
	}
}

func TestVerifyApprovedTargetRejectsUnapprovedOrTamperedProposal(t *testing.T) {
	proposal := approvedVerificationProposal(t, targetOperationChange("invalid"))
	proposal.Status = domain.ArchitectureTargetStatusSubmitted
	_, err := (VerifyApprovedTarget{}).Handle(VerificationInput{Proposal: proposal, ExpectedFingerprint: proposal.Fingerprint, Current: targetTestCatalog()})
	if !errors.Is(err, domain.ErrInvalidStatus) {
		t.Fatalf("unapproved error = %v, want invalid status", err)
	}

	proposal = approvedVerificationProposal(t, targetOperationChange("tampered"))
	proposal.Changes[0].Rationale.Summary = "tampered"
	_, err = (VerifyApprovedTarget{}).Handle(VerificationInput{Proposal: proposal, ExpectedFingerprint: proposal.Fingerprint, Current: targetTestCatalog()})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("tampered error = %v, want conflict", err)
	}
}

func approvedVerificationProposal(t *testing.T, changes ...domain.ArchitectureTargetChange) domain.ArchitectureTargetProposal {
	t.Helper()
	proposal, err := newProposal(targetTestCatalog(), "verify-approved-target", changes)
	if err != nil {
		t.Fatalf("build proposal: %v", err)
	}
	proposal.ID = "approved-target"
	proposal.Status = domain.ArchitectureTargetStatusApproved
	return proposal
}
