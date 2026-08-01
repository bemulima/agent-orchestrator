package workitem

import (
	"errors"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestIssuePublicationPreflightRequiresOneCurrentProposalPerTask(t *testing.T) {
	firstTaskID := "task-1"
	secondTaskID := "task-2"
	bundle := domain.PlanBundle{
		Plan:  domain.Plan{Fingerprint: "approved-fingerprint"},
		Tasks: []domain.Task{{ID: firstTaskID}, {ID: secondTaskID}},
		WorkItems: []domain.WorkItem{{
			Kind: domain.WorkItemIssue, TaskID: &firstTaskID, Status: domain.WorkItemProposed,
			AgentRole: domain.AgentRunIssueManager, PlanFingerprint: "approved-fingerprint",
		}},
	}
	if err := validateIssuePublicationBundle(bundle); !errors.Is(err, domain.ErrApprovalNeeded) {
		t.Fatalf("missing proposal error = %v", err)
	}

	bundle.WorkItems = append(bundle.WorkItems, domain.WorkItem{
		Kind: domain.WorkItemIssue, TaskID: &secondTaskID, Status: domain.WorkItemProposed,
		AgentRole: domain.AgentRunIssueManager, PlanFingerprint: "stale-fingerprint",
	})
	if err := validateIssuePublicationBundle(bundle); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale proposal error = %v", err)
	}

	bundle.WorkItems[1].PlanFingerprint = "approved-fingerprint"
	if err := validateIssuePublicationBundle(bundle); err != nil {
		t.Fatalf("complete proposal set rejected: %v", err)
	}
}
