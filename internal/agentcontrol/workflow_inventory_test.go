package agentcontrol

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkflowInventoryIsOptionalValidatedAndBoundToDistribution(t *testing.T) {
	catalog := testCatalog("fixture\n")
	legacy := workflowCatalogDigest(catalog.Assets, nil)
	require.Equal(t, catalog.Digest, legacy)
	cap := WorkflowCapability{ID: "fixture-worker", Status: CapabilityPartiallyImplemented, Scope: "fixture only", Evidence: []string{"internal/fixture.go"}, ExecutionRequirements: []string{"explicit owner decision"}}
	catalog.Manifest.WorkflowCapabilities = []WorkflowCapability{cap}
	catalog.Digest = workflowCatalogDigest(catalog.Assets, catalog.Manifest.WorkflowCapabilities)
	require.NotEqual(t, legacy, catalog.Digest)
	snapshot := RepositorySnapshot{Root: "/fixture", Revision: "fixture-revision", Assets: map[string]TargetAsset{}}
	proposal, err := BuildDistributionProposal(catalog, snapshot)
	require.NoError(t, err)
	require.Equal(t, catalog.Manifest.WorkflowCapabilities, proposal.NextManifest.WorkflowCapabilities)
	require.Contains(t, proposal.ManifestDiff, "PARTIALLY_IMPLEMENTED")
	firstDigest, firstFingerprint := catalog.Digest, proposal.Fingerprint
	catalog.Manifest.WorkflowCapabilities[0].Status = CapabilityBlocked
	catalog.Digest = workflowCatalogDigest(catalog.Assets, catalog.Manifest.WorkflowCapabilities)
	changed, err := BuildDistributionProposal(catalog, snapshot)
	require.NoError(t, err)
	require.NotEqual(t, firstDigest, catalog.Digest)
	require.NotEqual(t, firstFingerprint, changed.Fingerprint)
	catalog.Manifest.WorkflowCapabilities[0].Status = "INVENTED"
	_, err = BuildDistributionProposal(catalog, snapshot)
	require.Error(t, err)
	require.Error(t, ValidateDistributionManifest(DistributionManifest{SchemaVersion: 1, Ownership: OwnershipGenerated, Generator: "fixture", WorkflowCapabilities: catalog.Manifest.WorkflowCapabilities}, true))
}

func TestCanonicalWorkflowInventoryEvidenceExistsAndDoesNotGrantTargetReadiness(t *testing.T) {
	catalog, err := LoadCatalog(os.DirFS("../.."))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(catalog.Manifest.WorkflowCapabilities), 16)
	for _, capability := range catalog.Manifest.WorkflowCapabilities {
		for _, evidence := range capability.Evidence {
			_, err := os.Stat("../../" + evidence)
			require.NoError(t, err, "%s evidence %s", capability.ID, evidence)
		}
		request := FutureTaskRequest{Repository: "uninspected-target", TaskFingerprint: "new-task", Requirements: []FutureTaskRequirement{{ID: capability.ID, Status: capability.Status}}}
		decision := EvaluateFutureTask(request, nil)
		require.False(t, decision.MainTaskAllowed)
		require.False(t, decision.AgentSetupAllowed)
	}
}

func TestWorkflowInventoryDigestBindsScopeAndRequirementsAndIgnoresItemOrder(t *testing.T) {
	catalog := testCatalog("fixture\n")
	a := WorkflowCapability{ID: "a", Status: CapabilityCanaryVerified, Scope: "availability only", Evidence: []string{"a.go"}, ExecutionRequirements: []string{"fixture"}}
	b := a
	b.ID = "b"
	first := workflowCatalogDigest(catalog.Assets, []WorkflowCapability{a, b})
	require.Equal(t, first, workflowCatalogDigest(catalog.Assets, []WorkflowCapability{b, a}))
	a.Scope = "generalized"
	require.NotEqual(t, first, workflowCatalogDigest(catalog.Assets, []WorkflowCapability{a, b}))
	a.Scope = "availability only"
	a.ExecutionRequirements = []string{"different permission"}
	require.NotEqual(t, first, workflowCatalogDigest(catalog.Assets, []WorkflowCapability{a, b}))
	// Preserve nil metadata compatibility for existing provenance consumers.
	content, err := MarshalDistributionManifest(DistributionManifest{SchemaVersion: 1, Ownership: OwnershipGenerated, Generator: "fixture"})
	require.NoError(t, err)
	require.False(t, strings.Contains(string(content), "workflow_capabilities"))
}

func TestManagedSkillsHaveValidFrontmatterAndReachableDecisionProcedure(t *testing.T) {
	for _, name := range []string{"task-route", "contract-plan"} {
		raw, err := os.ReadFile("../../agent-system/skills/" + name + "/SKILL.md")
		require.NoError(t, err)
		parts := strings.SplitN(string(raw), "---", 3)
		require.Len(t, parts, 3)
		var meta struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}
		require.NoError(t, DecodeStrictYAML([]byte(parts[1]), &meta))
		require.Equal(t, name, meta.Name)
		require.NotEmpty(t, meta.Description)
	}
	_, err := os.Stat("../../agent-system/skills/task-route/references/future-task-decision.md")
	require.NoError(t, err)
}
