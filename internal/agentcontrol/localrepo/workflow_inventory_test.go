package localrepo

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/stretchr/testify/require"
)

func TestWorkflowInventoryDistributionRequiresConsentAndPreservesFacts(t *testing.T) {
	root := initDisposableGitRepo(t)
	catalog := fixtureCatalog("fixture skill\n")
	catalog.Manifest.WorkflowCapabilities = []agentcontrol.WorkflowCapability{{ID: "fixture-worker", Status: agentcontrol.CapabilityPartiallyImplemented, Scope: "fixture only", Evidence: []string{"internal/fixture.go"}, ExecutionRequirements: []string{"owner choice"}}}
	service := agentcontrol.DistributionService{Repository: LocalRepository{Root: root}}
	proposal, err := service.Plan(context.Background(), catalog)
	require.NoError(t, err)
	_, err = service.Apply(context.Background(), catalog, "")
	require.ErrorIs(t, err, agentcontrol.ErrApprovalRequired)
	require.NoFileExists(t, filepath.Join(root, ".agents/manifest.yaml"))
	changed := catalog
	changed.Manifest.WorkflowCapabilities = append([]agentcontrol.WorkflowCapability(nil), catalog.Manifest.WorkflowCapabilities...)
	changed.Manifest.WorkflowCapabilities[0].Status = agentcontrol.CapabilityBlocked
	_, err = service.Apply(context.Background(), changed, proposal.Fingerprint)
	require.ErrorIs(t, err, agentcontrol.ErrApprovalRequired)
	require.NoFileExists(t, filepath.Join(root, ".agents/manifest.yaml"))
	_, err = service.Apply(context.Background(), catalog, proposal.Fingerprint)
	require.NoError(t, err)
	snapshot, err := InspectRepository(root, catalog)
	require.NoError(t, err)
	require.Equal(t, catalog.Manifest.WorkflowCapabilities, snapshot.Provenance.WorkflowCapabilities)
	// Applying central evidence does not mark any task's local facts as confirmed.
	decision := agentcontrol.EvaluateFutureTask(agentcontrol.FutureTaskRequest{Repository: root, TaskFingerprint: "new", Requirements: []agentcontrol.FutureTaskRequirement{{ID: "fixture-worker", Status: agentcontrol.CapabilityReadyVerified}}}, nil)
	require.False(t, decision.MainTaskAllowed)
	require.False(t, decision.AgentSetupAllowed)
}
