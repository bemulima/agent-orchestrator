package shardexecution

import (
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestHTTPRemediationRequiresActualDelegationRED(t *testing.T) {
	p := PreparedShard{Remediation: &ShardRemediation{RequestID: "owner-decision"}, WorkPackage: domain.WorkPackage{Route: "backend.transport.http"}}
	for _, tc := range []struct {
		output string
		exit   int
		pass   bool
	}{
		{"semantic RED: non-zero offset is currently delegated: calls=1 want=0", 1, true},
		{"semantic RED: constructor missing", 1, false},
		{"semantic RED: wrong intervals", 1, false},
		{"semantic RED: non-zero offset is currently delegated", 0, false},
	} {
		if err := validateHTTPRemediationDelegationRED(p, domain.WorkspaceCheckResult{ExitCode: tc.exit, Output: tc.output}); (err == nil) != tc.pass {
			t.Fatalf("unexpected gate for %+v: %v", tc, err)
		}
	}
	if err := validateHTTPRemediationDelegationRED(PreparedShard{}, domain.WorkspaceCheckResult{}); err != nil {
		t.Fatal(err)
	}
}
