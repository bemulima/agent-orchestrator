package conversation

import (
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestValidateProposalsRequiresBackendAllowedAction(t *testing.T) {
	values := []operatorProposal{{Action: "pause", ResourceType: "run", ResourceID: "run-1", Title: "Pause", Description: "Inspect", RiskLevel: domain.RiskLevelMedium}}
	result, err := validateProposals(values, map[string]struct{}{"run:run-1:pause": {}}, map[string]string{})
	if err != nil || len(result) != 1 || result[0].Action != "pause" {
		t.Fatalf("validateProposals() = %#v, %v", result, err)
	}
	if _, err := validateProposals(values, map[string]struct{}{}, map[string]string{}); err == nil {
		t.Fatal("expected disallowed action error")
	}
}

func TestValidateProposalsBindsApprovalFingerprint(t *testing.T) {
	values := []operatorProposal{{Action: "approve", ResourceType: "plan", ResourceID: "plan-1", Title: "Approve", Description: "Exact version", RiskLevel: domain.RiskLevelHigh}}
	result, err := validateProposals(values, map[string]struct{}{"plan:plan-1:approve": {}}, map[string]string{"plan:plan-1": "fingerprint"})
	if err != nil || result[0].Fingerprint == nil || *result[0].Fingerprint != "fingerprint" {
		t.Fatalf("approval proposal = %#v, %v", result, err)
	}
}
