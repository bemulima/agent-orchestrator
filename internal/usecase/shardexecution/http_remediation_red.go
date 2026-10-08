package shardexecution

import (
	"fmt"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// HTTP remediation must reproduce the approved delegation bug, rather than an
// unrelated semantic assertion or a missing constructor/interface.
func validateHTTPRemediationDelegationRED(prepared PreparedShard, result domain.WorkspaceCheckResult) error {
	if prepared.Remediation == nil || prepared.WorkPackage.Route != "backend.transport.http" {
		return nil
	}
	if result.ExitCode == 0 || !strings.Contains(result.Output, "semantic RED: non-zero offset is currently delegated") {
		return fmt.Errorf("HTTP remediation lacks the actual non-zero-offset delegation RED: %w", domain.ErrValidation)
	}
	return nil
}
