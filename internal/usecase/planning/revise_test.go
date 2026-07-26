package planning

import (
	"context"
	"errors"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestRevisePlanReusesCommandAndCurrentProjects(t *testing.T) {
	bundle := domain.PlanBundle{
		Plan:  domain.Plan{ID: "plan-1", CommandID: "command-1", Status: domain.PlanStatusDiscussion},
		Tasks: []domain.Task{{ProjectID: "project-1"}, {ProjectID: "project-1"}, {ProjectID: "project-2"}},
	}
	creator := &revisionCreatorFake{result: domain.PlanBundle{Plan: domain.Plan{ID: "plan-2", Version: 2}}}
	result, err := (RevisePlan{Plans: revisionSourceFake{bundle: bundle}, Create: creator}).Handle(
		context.Background(), bundle.Plan.ID, domain.PlanRequest{RevisionInstruction: "  Уточнить scope  "},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Plan.ID != "plan-2" || creator.commandID != "command-1" {
		t.Fatalf("unexpected revision result: %#v, command=%q", result.Plan, creator.commandID)
	}
	if len(creator.request.RequestedProjectIDs) != 2 || creator.request.RevisionInstruction != "Уточнить scope" {
		t.Fatalf("unexpected revision request: %#v", creator.request)
	}
}

func TestRevisePlanRejectsApprovedPlan(t *testing.T) {
	_, err := (RevisePlan{
		Plans:  revisionSourceFake{bundle: domain.PlanBundle{Plan: domain.Plan{Status: domain.PlanStatusApproved}}},
		Create: &revisionCreatorFake{},
	}).Handle(context.Background(), "plan-1", domain.PlanRequest{RevisionInstruction: "change"})
	if !errors.Is(err, domain.ErrInvalidStatus) {
		t.Fatalf("expected invalid status, got %v", err)
	}
}

type revisionSourceFake struct{ bundle domain.PlanBundle }

func (f revisionSourceFake) GetPlan(context.Context, string) (domain.PlanBundle, error) {
	return f.bundle, nil
}

type revisionCreatorFake struct {
	commandID string
	request   domain.PlanRequest
	result    domain.PlanBundle
}

func (f *revisionCreatorFake) Handle(_ context.Context, commandID string, request domain.PlanRequest) (domain.PlanBundle, error) {
	f.commandID, f.request = commandID, request
	return f.result, nil
}
