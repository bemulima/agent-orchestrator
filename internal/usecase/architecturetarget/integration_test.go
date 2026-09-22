package architecturetarget

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestIntegrateApprovedTargetCreatesSingleIssuePlanAndProposedDraft(t *testing.T) {
	proposal := approvedIntegrationProposal(t, integrationSingleCatalog(), targetOperationChange("single-change"))
	commands := &integrationCommandFake{command: domain.Command{ID: "command-single"}}
	taskID := "task-single"
	plans := &integrationPlanFake{bundle: domain.PlanBundle{Plan: domain.Plan{ID: "plan-single"}, Tasks: []domain.Task{{ID: taskID, ProjectID: "service-a", PlannerKey: "service-a-change"}}}}
	issues := &integrationIssuesFake{drafts: []domain.WorkItem{{Kind: domain.WorkItemIssue, Status: domain.WorkItemProposed, TaskID: &taskID, ProjectID: "service-a"}}}

	result, err := (IntegrateApprovedTarget{Commands: commands, Plans: plans, Issues: issues}).Handle(context.Background(), IntegrationInput{Proposal: proposal, ExpectedFingerprint: proposal.Fingerprint})
	if err != nil {
		t.Fatalf("integrate approved target: %v", err)
	}
	if result.Classification != IntegrationClassificationIssue || !reflect.DeepEqual(result.AffectedProjectIDs, []string{"service-a"}) {
		t.Fatalf("unexpected integration result: %#v", result)
	}
	if !reflect.DeepEqual(plans.request.RequestedProjectIDs, []string{"service-a"}) || issues.planID != "plan-single" {
		t.Fatalf("pipeline inputs = request %#v / issue plan %q", plans.request, issues.planID)
	}
	for _, required := range []string{"## Objective", "## Approved diff", "## Affected services", "## Affected operations", "## Contracts and dependencies", "## Implementation order", "## Acceptance criteria", "## Tests", "## Evidence", "## Verification", proposal.Fingerprint, "service-a", "operation-a"} {
		if !strings.Contains(commands.request.Text, required) {
			t.Fatalf("command is missing %q:\n%s", required, commands.request.Text)
		}
	}
	if commands.request.Source != domain.CommandSourceAPI || commands.request.IdempotencyKey == "" {
		t.Fatalf("command request did not bind default source/idempotency: %#v", commands.request)
	}
}

func TestIntegrateApprovedTargetCreatesCrossServiceProjectPlanDAG(t *testing.T) {
	catalog := targetTestCatalog()
	second := targetOperationChange("second-change")
	second.AffectedServiceIDs, second.AffectedOperationIDs = []string{"service-b"}, []string{"operation-b"}
	proposal := approvedIntegrationProposal(t, catalog, targetOperationChange("first-change"), second)
	commands := &integrationCommandFake{command: domain.Command{ID: "command-multi"}}
	taskA, taskB := "task-a", "task-b"
	plans := &integrationPlanFake{bundle: domain.PlanBundle{Plan: domain.Plan{ID: "plan-multi"}, Tasks: []domain.Task{{ID: taskA, ProjectID: "service-a"}, {ID: taskB, ProjectID: "service-b"}}, Dependencies: []domain.TaskDependency{{TaskID: taskB, DependsOnTaskID: taskA, DependencyType: "blocks"}}}}
	issues := &integrationIssuesFake{drafts: []domain.WorkItem{{Kind: domain.WorkItemIssue, Status: domain.WorkItemProposed, TaskID: &taskA, ProjectID: "service-a"}, {Kind: domain.WorkItemIssue, Status: domain.WorkItemProposed, TaskID: &taskB, ProjectID: "service-b"}}}

	result, err := (IntegrateApprovedTarget{Commands: commands, Plans: plans, Issues: issues}).Handle(context.Background(), IntegrationInput{Proposal: proposal, ExpectedFingerprint: proposal.Fingerprint, Source: domain.CommandSourceCLI})
	if err != nil {
		t.Fatalf("integrate cross-service target: %v", err)
	}
	if result.Classification != IntegrationClassificationProjectPlan || !reflect.DeepEqual(result.AffectedProjectIDs, []string{"service-a", "service-b"}) {
		t.Fatalf("unexpected integration result: %#v", result)
	}
	if !reflect.DeepEqual(plans.request.RequestedProjectIDs, []string{"service-a", "service-b"}) || commands.request.Source != domain.CommandSourceCLI {
		t.Fatalf("cross-service pipeline inputs are wrong: request=%#v command=%#v", plans.request, commands.request)
	}
}

func TestIntegrateApprovedTargetRejectsAnythingNotImplementationReadyBeforePipeline(t *testing.T) {
	base := approvedIntegrationProposal(t, targetTestCatalog(), targetOperationChange("rejection-change"))
	tests := []struct {
		name   string
		mutate func(*domain.ArchitectureTargetProposal)
	}{
		{name: "not approved", mutate: func(value *domain.ArchitectureTargetProposal) {
			value.Status = domain.ArchitectureTargetStatusSubmitted
		}},
		{name: "unresolved impact", mutate: func(value *domain.ArchitectureTargetProposal) {
			value.Impact.Entries[0].Kind = domain.ArchitectureTargetImpactUnresolved
			recomputeIntegrationFingerprint(t, value)
		}},
		{name: "proposed operation", mutate: func(value *domain.ArchitectureTargetProposal) {
			value.Changes[0].Action = domain.ArchitectureTargetChangeAdd
			value.Changes[0].AffectedOperationIDs = []string{"proposed:operation-new"}
			value.Diff.Entries[0].Action = domain.ArchitectureTargetChangeAdd
			value.Diff.Entries[0].AffectedOperationIDs = []string{"proposed:operation-new"}
			recomputeIntegrationFingerprint(t, value)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proposal := base
			proposal.Changes = append([]domain.ArchitectureTargetChange(nil), base.Changes...)
			proposal.Diff.Entries = append([]domain.ArchitectureTargetDiffEntry(nil), base.Diff.Entries...)
			proposal.Impact.Entries = append([]domain.ArchitectureTargetImpactEntry(nil), base.Impact.Entries...)
			tt.mutate(&proposal)
			commands := &integrationCommandFake{command: domain.Command{ID: "must-not-create"}}
			plans, issues := &integrationPlanFake{}, &integrationIssuesFake{}
			_, err := (IntegrateApprovedTarget{Commands: commands, Plans: plans, Issues: issues}).Handle(context.Background(), IntegrationInput{Proposal: proposal, ExpectedFingerprint: proposal.Fingerprint})
			if !errors.Is(err, domain.ErrInvalidStatus) && !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("error = %v, want invalid status or conflict", err)
			}
			if commands.calls != 0 || plans.calls != 0 || issues.calls != 0 {
				t.Fatalf("non-ready target touched pipeline: command=%d plan=%d issues=%d", commands.calls, plans.calls, issues.calls)
			}
		})
	}
}

func TestIntegrateApprovedTargetRejectsPlanCycleAndPublishedDraft(t *testing.T) {
	proposal := approvedIntegrationProposal(t, integrationSingleCatalog(), targetOperationChange("plan-validation"))
	taskID := "task-a"
	tests := []struct {
		name      string
		bundle    domain.PlanBundle
		drafts    []domain.WorkItem
		wantCalls int
	}{
		{name: "single task dependency", bundle: domain.PlanBundle{Plan: domain.Plan{ID: "plan"}, Tasks: []domain.Task{{ID: taskID, ProjectID: "service-a"}}, Dependencies: []domain.TaskDependency{{TaskID: taskID, DependsOnTaskID: taskID}}}, wantCalls: 0},
		{name: "published draft", bundle: domain.PlanBundle{Plan: domain.Plan{ID: "plan"}, Tasks: []domain.Task{{ID: taskID, ProjectID: "service-a"}}}, drafts: []domain.WorkItem{{Kind: domain.WorkItemIssue, Status: domain.WorkItemPublished, TaskID: &taskID, ProjectID: "service-a"}}, wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commands := &integrationCommandFake{command: domain.Command{ID: "command"}}
			plans := &integrationPlanFake{bundle: tt.bundle}
			issues := &integrationIssuesFake{drafts: tt.drafts}
			_, err := (IntegrateApprovedTarget{Commands: commands, Plans: plans, Issues: issues}).Handle(context.Background(), IntegrationInput{Proposal: proposal, ExpectedFingerprint: proposal.Fingerprint})
			if !errors.Is(err, domain.ErrConflict) || issues.calls != tt.wantCalls {
				t.Fatalf("error=%v issue calls=%d, want conflict / %d", err, issues.calls, tt.wantCalls)
			}
		})
	}
}

func approvedIntegrationProposal(t *testing.T, catalog domain.ArchitectureCatalog, changes ...domain.ArchitectureTargetChange) domain.ArchitectureTargetProposal {
	t.Helper()
	proposal, err := newProposal(catalog, "architecture-target-integration", changes)
	if err != nil {
		t.Fatalf("build proposal: %v", err)
	}
	proposal.ID = "target-integration"
	proposal.Status = domain.ArchitectureTargetStatusApproved
	proposal.DecidedBy = "architecture-owner"
	return proposal
}

func integrationSingleCatalog() domain.ArchitectureCatalog {
	catalog := targetTestCatalog()
	catalog.Platform.Relations = nil
	return catalog
}

func recomputeIntegrationFingerprint(t *testing.T, proposal *domain.ArchitectureTargetProposal) {
	t.Helper()
	fingerprint, err := proposal.ComputedFingerprint()
	if err != nil {
		t.Fatalf("recompute proposal fingerprint: %v", err)
	}
	proposal.Fingerprint = fingerprint
}

type integrationCommandFake struct {
	command domain.Command
	request CommandRequest
	calls   int
}

func (f *integrationCommandFake) CreateCommand(_ context.Context, request CommandRequest) (domain.Command, error) {
	f.calls++
	f.request = request
	return f.command, nil
}

type integrationPlanFake struct {
	bundle  domain.PlanBundle
	request domain.PlanRequest
	command string
	calls   int
}

func (f *integrationPlanFake) CreatePlan(_ context.Context, commandID string, request domain.PlanRequest) (domain.PlanBundle, error) {
	f.calls++
	f.command, f.request = commandID, request
	return f.bundle, nil
}

type integrationIssuesFake struct {
	drafts []domain.WorkItem
	planID string
	calls  int
}

func (f *integrationIssuesFake) PrepareIssueDrafts(_ context.Context, planID string) ([]domain.WorkItem, error) {
	f.calls++
	f.planID = planID
	return f.drafts, nil
}
