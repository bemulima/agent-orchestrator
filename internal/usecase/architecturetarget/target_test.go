package architecturetarget

import (
	"context"
	"errors"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestCreateBindsVerifiedCurrentAndBuildsEvidencePreservingDiffAndImpact(t *testing.T) {
	catalog := targetTestCatalog()
	store := &targetStoreFake{}
	change := targetOperationChange("change-operation")
	proposal, err := (Create{Store: store}).Handle(context.Background(), CreateInput{Current: catalog, CurrentFingerprint: catalog.Fingerprint, IdempotencyKey: "create-operation", Changes: []domain.ArchitectureTargetChange{change}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if proposal.CurrentFingerprint != catalog.Fingerprint || proposal.Status != domain.ArchitectureTargetStatusDraft || proposal.Revision != 1 || proposal.Fingerprint == "" {
		t.Fatalf("unexpected proposal binding: %#v", proposal)
	}
	if len(proposal.Diff.Entries) != 1 || proposal.Diff.Entries[0].Kind != change.Kind || proposal.Diff.Entries[0].Evidence[0] != change.Evidence[0] {
		t.Fatalf("diff did not preserve change evidence: %#v", proposal.Diff)
	}
	if !hasImpact(proposal.Impact.Entries, "service-a", "operation-a", domain.ArchitectureTargetImpactDirect) {
		t.Fatalf("direct operation impact is missing: %#v", proposal.Impact.Entries)
	}
	if !hasImpact(proposal.Impact.Entries, "service-b", "", domain.ArchitectureTargetImpactDownstream) {
		t.Fatalf("relation-derived downstream impact is missing: %#v", proposal.Impact.Entries)
	}
	if store.created.Fingerprint != proposal.Fingerprint {
		t.Fatalf("store did not receive deterministic proposal")
	}
}

func TestCreateRejectsUnverifiedCurrentAndGuessedTargets(t *testing.T) {
	catalog := targetTestCatalog()
	change := targetOperationChange("unknown-operation")
	change.AffectedOperationIDs = []string{"not-in-current"}
	_, err := (Create{Store: &targetStoreFake{}}).Handle(context.Background(), CreateInput{Current: catalog, CurrentFingerprint: catalog.Fingerprint, IdempotencyKey: "unknown", Changes: []domain.ArchitectureTargetChange{change}})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("unknown operation error = %v, want validation", err)
	}

	change = targetOperationChange("stale-current")
	catalog.Platform.Completeness.OperationsMissingManifests = 1
	_, err = (Create{Store: &targetStoreFake{}}).Handle(context.Background(), CreateInput{Current: catalog, CurrentFingerprint: catalog.Fingerprint, IdempotencyKey: "stale", Changes: []domain.ArchitectureTargetChange{change}})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("incomplete current error = %v, want conflict", err)
	}

	catalog = targetTestCatalog()
	change = targetOperationChange("not-proposed")
	change.Action = domain.ArchitectureTargetChangeAdd
	change.AffectedOperationIDs = []string{"operation-new"}
	_, err = (Create{Store: &targetStoreFake{}}).Handle(context.Background(), CreateInput{Current: catalog, CurrentFingerprint: catalog.Fingerprint, IdempotencyKey: "not-proposed", Changes: []domain.ArchitectureTargetChange{change}})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("non-proposed add error = %v, want validation", err)
	}
}

func TestCreateAllowsStableDirtyCurrent(t *testing.T) {
	catalog := targetTestCatalog()
	catalog.Platform.Services[0].Source.IsDirty = true
	proposal, err := (Create{Store: &targetStoreFake{}}).Handle(context.Background(), CreateInput{
		Current: catalog, CurrentFingerprint: catalog.Fingerprint, IdempotencyKey: "stable-dirty", Changes: []domain.ArchitectureTargetChange{targetOperationChange("stable-dirty")},
	})
	if err != nil {
		t.Fatalf("Create stable dirty CURRENT: %v", err)
	}
	if proposal.CurrentFingerprint != catalog.Fingerprint {
		t.Fatalf("proposal binding = %q, want %q", proposal.CurrentFingerprint, catalog.Fingerprint)
	}
}

func TestReviseAllowsOnlyCurrentBoundDraftWithExpectedRevision(t *testing.T) {
	catalog := targetTestCatalog()
	store := &targetStoreFake{proposal: validTargetProposal(t, catalog, targetOperationChange("original"))}
	store.proposal.ID, store.proposal.Revision = "proposal-1", 2
	nextChange := targetOperationChange("next")
	updated, err := (Revise{Store: store}).Handle(context.Background(), ReviseInput{ProposalID: "proposal-1", ExpectedRevision: 2, Current: catalog, CurrentFingerprint: catalog.Fingerprint, Changes: []domain.ArchitectureTargetChange{nextChange}})
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	if updated.Revision != 3 || updated.CurrentFingerprint != catalog.Fingerprint || updated.Changes[0].ID != "next" {
		t.Fatalf("unexpected draft revision: %#v", updated)
	}
	if store.updateExpected != 2 {
		t.Fatalf("expected revision not passed to store: %d", store.updateExpected)
	}

	store.proposal.Status = domain.ArchitectureTargetStatusSubmitted
	_, err = (Revise{Store: store}).Handle(context.Background(), ReviseInput{ProposalID: "proposal-1", ExpectedRevision: 2, Current: catalog, CurrentFingerprint: catalog.Fingerprint, Changes: []domain.ArchitectureTargetChange{nextChange}})
	if !errors.Is(err, domain.ErrInvalidStatus) {
		t.Fatalf("submitted revision error = %v, want invalid status", err)
	}
}

func TestSubmitFreezesOnlyValidDraftAtExpectedRevision(t *testing.T) {
	catalog := targetTestCatalog()
	proposal := validTargetProposal(t, catalog, targetOperationChange("submit"))
	proposal.ID, proposal.Revision = "proposal-1", 4
	store := &targetStoreFake{proposal: proposal}
	result, err := (Submit{Store: store}).Handle(context.Background(), SubmitInput{ProposalID: proposal.ID, ExpectedRevision: 4, Actor: "owner", Comment: "ready"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if result.Status != domain.ArchitectureTargetStatusSubmitted || store.transitionExpected != 4 || store.transitionActor != "owner" {
		t.Fatalf("unexpected submit result: %#v / %#v", result, store)
	}
	_, err = (Submit{Store: store}).Handle(context.Background(), SubmitInput{ProposalID: proposal.ID, ExpectedRevision: 4, Actor: "owner"})
	if !errors.Is(err, domain.ErrInvalidStatus) {
		t.Fatalf("resubmit error = %v, want invalid status", err)
	}
}

func targetTestCatalog() domain.ArchitectureCatalog {
	evidence := domain.ArchitectureEvidence{SourcePath: "internal/handler.go", Symbol: "Handle", StartLine: 10, EndLine: 20}
	service := func(id, operation string) domain.ArchitectureCatalogService {
		manifest := domain.ArchitectureServiceManifest{ID: id + "-manifest"}
		return domain.ArchitectureCatalogService{Source: domain.ArchitectureCatalogSourceStatus{ProjectID: id, SourceCurrent: true}, Covered: true, Manifest: &manifest, Ungrouped: []domain.ArchitectureCatalogOperation{{Manifest: domain.ArchitectureOperationManifest{ID: operation}}}}
	}
	return domain.ArchitectureCatalog{Mode: domain.ArchitectureCatalogModeCurrent, Fingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Platform: domain.ArchitectureCatalogPlatform{Services: []domain.ArchitectureCatalogService{service("service-a", "operation-a"), service("service-b", "operation-b")}, Relations: []domain.ArchitectureCatalogRelation{{ID: "a-to-b", SourceProjectID: "service-a", TargetProjectID: "service-b", Evidence: []domain.ArchitectureEvidence{evidence}}}}}
}

func targetOperationChange(id string) domain.ArchitectureTargetChange {
	return domain.ArchitectureTargetChange{ID: id, Scope: domain.ArchitectureTargetScopeOperation, Action: domain.ArchitectureTargetChangeChange, Kind: domain.ArchitectureTargetChangeKindOperation, AffectedServiceIDs: []string{"service-a"}, AffectedOperationIDs: []string{"operation-a"}, Rationale: domain.ArchitectureTargetRationale{Summary: "Adjust operation contract"}, DesiredResult: domain.ArchitectureTargetDesiredResult{Summary: "Operation remains documented", SuccessCriteria: []string{"Current evidence remains linked"}}, Evidence: []domain.ArchitectureEvidence{{SourcePath: "internal/handler.go", Symbol: "Handle", StartLine: 10, EndLine: 20}}, Confidence: .8}
}

func validTargetProposal(t *testing.T, catalog domain.ArchitectureCatalog, change domain.ArchitectureTargetChange) domain.ArchitectureTargetProposal {
	t.Helper()
	proposal, err := newProposal(catalog, "target-proposal", []domain.ArchitectureTargetChange{change})
	if err != nil {
		t.Fatalf("new proposal: %v", err)
	}
	return proposal
}

func hasImpact(values []domain.ArchitectureTargetImpactEntry, serviceID, operationID string, kind domain.ArchitectureTargetImpactKind) bool {
	for _, value := range values {
		if value.ServiceID == serviceID && value.OperationID == operationID && value.Kind == kind {
			return true
		}
	}
	return false
}

type targetStoreFake struct {
	proposal           domain.ArchitectureTargetProposal
	created            domain.ArchitectureTargetProposal
	updateExpected     int
	transitionExpected int
	transitionActor    string
}

func (f *targetStoreFake) Create(_ context.Context, proposal domain.ArchitectureTargetProposal) (domain.ArchitectureTargetProposal, error) {
	f.created, f.proposal = proposal, proposal
	return proposal, nil
}
func (f *targetStoreFake) Get(_ context.Context, _ string) (domain.ArchitectureTargetProposal, error) {
	return f.proposal, nil
}
func (f *targetStoreFake) UpdateDraft(_ context.Context, proposal domain.ArchitectureTargetProposal, expected int) (domain.ArchitectureTargetProposal, error) {
	f.updateExpected, f.proposal = expected, proposal
	return proposal, nil
}
func (f *targetStoreFake) Transition(_ context.Context, _ string, status domain.ArchitectureTargetStatus, expected int, actor, _ string) (domain.ArchitectureTargetProposal, error) {
	f.transitionExpected, f.transitionActor, f.proposal.Status = expected, actor, status
	return f.proposal, nil
}
