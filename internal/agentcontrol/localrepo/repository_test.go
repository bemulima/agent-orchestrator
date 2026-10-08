package localrepo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/stretchr/testify/require"
)

func TestDistributionRequiresExactApprovalPreservesLocalFilesAndDetectsEdits(t *testing.T) {
	root := initDisposableGitRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".agents/skills/local-only"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".agents/skills/local-only/SKILL.md"), []byte("local skill\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".ai"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".ai/service.yaml"), []byte("name: local-service\n"), 0o644))
	commitDisposableRepo(t, root)

	catalog := fixtureCatalog("shared v1\n")
	service := agentcontrol.DistributionService{Repository: LocalRepository{Root: root}}
	proposal, err := service.Plan(context.Background(), catalog)
	require.NoError(t, err)
	require.NotEmpty(t, proposal.Fingerprint)

	_, err = service.Apply(context.Background(), catalog, "sha256:wrong")
	require.ErrorIs(t, err, agentcontrol.ErrApprovalRequired)
	require.NoFileExists(t, filepath.Join(root, ".agents/skills/task-route/SKILL.md"))

	result, err := service.Apply(context.Background(), catalog, proposal.Fingerprint)
	require.NoError(t, err)
	require.NotEmpty(t, result.Written)
	require.Equal(t, []byte("local skill\n"), readTestFile(t, filepath.Join(root, ".agents/skills/local-only/SKILL.md")))
	require.Equal(t, []byte("name: local-service\n"), readTestFile(t, filepath.Join(root, ".ai/service.yaml")))
	require.FileExists(t, filepath.Join(root, ".agents/manifest.yaml"))

	current, err := service.Plan(context.Background(), catalog)
	require.NoError(t, err)
	require.Equal(t, agentcontrol.AssetStateCurrent, current.Change("skill/task-route").State)

	managedPath := filepath.Join(root, ".agents/skills/task-route/SKILL.md")
	require.NoError(t, os.WriteFile(managedPath, []byte("independent edit\n"), 0o644))
	modified, err := service.Plan(context.Background(), catalog)
	require.NoError(t, err)
	require.Equal(t, agentcontrol.AssetStateLocallyModified, modified.Change("skill/task-route").State)
	snapshot, err := InspectRepository(root, catalog)
	require.NoError(t, err)
	_, err = (LocalRepository{Root: root}).Apply(context.Background(), snapshot, modified, modified.Fingerprint)
	require.ErrorIs(t, err, agentcontrol.ErrApplyConflict)
	_, err = service.Apply(context.Background(), catalog, modified.Fingerprint)
	require.ErrorIs(t, err, agentcontrol.ErrApplyConflict)
	require.Equal(t, []byte("independent edit\n"), readTestFile(t, managedPath))
}

func TestDistributionRefreshesPreviouslyManagedAssetsAndRevalidatesBase(t *testing.T) {
	root := initDisposableGitRepo(t)
	service := agentcontrol.DistributionService{Repository: LocalRepository{Root: root}}
	firstCatalog := fixtureCatalog("shared v1\n")
	firstProposal, err := service.Plan(context.Background(), firstCatalog)
	require.NoError(t, err)
	_, err = service.Apply(context.Background(), firstCatalog, firstProposal.Fingerprint)
	require.NoError(t, err)

	updatedCatalog := fixtureCatalog("shared v2\n")
	updatedProposal, err := service.Plan(context.Background(), updatedCatalog)
	require.NoError(t, err)
	require.Equal(t, agentcontrol.AssetStateStale, updatedProposal.Change("skill/task-route").State)
	result, err := service.Apply(context.Background(), updatedCatalog, updatedProposal.Fingerprint)
	require.NoError(t, err)
	require.NotEmpty(t, result.Written)
	require.Equal(t, []byte("shared v2\n"), readTestFile(t, filepath.Join(root, ".agents/skills/task-route/SKILL.md")))

	staleBase, err := service.Plan(context.Background(), fixtureCatalog("shared v3\n"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "later.txt"), []byte("advance base\n"), 0o644))
	commitDisposableRepo(t, root)
	_, err = service.Apply(context.Background(), fixtureCatalog("shared v3\n"), staleBase.Fingerprint)
	require.ErrorIs(t, err, agentcontrol.ErrApprovalRequired)
	require.ErrorContains(t, err, "proposal changed")
	require.Equal(t, []byte("shared v2\n"), readTestFile(t, filepath.Join(root, ".agents/skills/task-route/SKILL.md")))
}

func TestUnsafeSymlinkTargetBlocksApply(t *testing.T) {
	root := initDisposableGitRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".agents/skills"), 0o755))
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(root, ".agents/skills/task-route")))
	service := agentcontrol.DistributionService{Repository: LocalRepository{Root: root}}
	proposal, err := service.Plan(context.Background(), fixtureCatalog("shared v1\n"))
	require.NoError(t, err)
	require.Equal(t, agentcontrol.AssetStateConflicting, proposal.Change("skill/task-route").State)
	_, err = service.Apply(context.Background(), fixtureCatalog("shared v1\n"), proposal.Fingerprint)
	require.ErrorIs(t, err, agentcontrol.ErrApplyConflict)
}

func TestGlobalPolicyTargetInspectionIsExplicitAndReadOnly(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	target := filepath.Join(home, ".codex", "AGENTS.md")
	resolved, content, exists, err := ReadGlobalPolicyTarget(target)
	require.NoError(t, err)
	require.Equal(t, target, resolved)
	require.False(t, exists)
	require.Nil(t, content)
	require.NoDirExists(t, filepath.Join(home, ".codex"))

	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("local global policy\n"), 0o644))
	resolved, content, exists, err = ReadGlobalPolicyTarget(target)
	require.NoError(t, err)
	require.Equal(t, target, resolved)
	require.True(t, exists)
	require.Equal(t, []byte("local global policy\n"), content)
	require.Equal(t, []byte("local global policy\n"), readTestFile(t, target))
	_, _, _, err = ReadGlobalPolicyTarget(filepath.Join(home, "AGENTS.md"))
	require.Error(t, err)
}

func TestApplyRejectsProvenanceChangedAfterInspection(t *testing.T) {
	root := initDisposableGitRepo(t)
	catalog := fixtureCatalog("shared v1\n")
	snapshot, err := InspectRepository(root, catalog)
	require.NoError(t, err)
	proposal, err := agentcontrol.BuildDistributionProposal(catalog, snapshot)
	require.NoError(t, err)

	manifestPath := filepath.Join(root, ".agents/manifest.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(manifestPath), 0o755))
	changedProvenance, err := agentcontrol.MarshalDistributionManifest(agentcontrol.DistributionManifest{
		SchemaVersion: 1, Ownership: agentcontrol.OwnershipGenerated, Generator: "test",
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, changedProvenance, 0o644))
	_, err = applyProposalFiles(context.Background(), snapshot, proposal, proposal.Fingerprint)
	require.ErrorContains(t, err, "target provenance changed")
	require.NoFileExists(t, filepath.Join(root, ".agents/skills/task-route/SKILL.md"))
}

func fixtureCatalog(skillContent string) agentcontrol.Catalog {
	skillFiles := map[string][]byte{"SKILL.md": []byte(skillContent)}
	profileFiles := map[string][]byte{"go-canonical.yaml": []byte("profile\n")}
	policyFiles := map[string][]byte{"AGENTS.md": []byte("global policy\n")}
	assets := []agentcontrol.ResolvedAsset{
		{Asset: agentcontrol.Asset{ID: "skill/task-route", Kind: agentcontrol.AssetKindSkill, SourcePath: "agent-system/skills/task-route", Ownership: agentcontrol.OwnershipManaged, Version: "1.0.0", TargetPath: ".agents/skills/task-route"}, Files: skillFiles, Checksum: agentcontrol.ChecksumFiles(skillFiles)},
		{Asset: agentcontrol.Asset{ID: "profile/go.canonical", Kind: agentcontrol.AssetKindProfile, SourcePath: "agent-system/profiles/go-canonical.yaml", Ownership: agentcontrol.OwnershipManaged, Version: "1.0.0", TargetPath: ".agents/profiles/go-canonical.yaml"}, Files: profileFiles, Checksum: agentcontrol.ChecksumFiles(profileFiles)},
		{Asset: agentcontrol.Asset{ID: "global-policy", Kind: agentcontrol.AssetKindGlobalPolicy, SourcePath: "agent-system/global/AGENTS.md", Ownership: agentcontrol.OwnershipManaged, Version: "1.0.0", TargetPath: "~/.codex/AGENTS.md"}, Files: policyFiles, Checksum: agentcontrol.ChecksumFiles(policyFiles)},
	}
	catalog := agentcontrol.Catalog{Assets: assets}
	catalog.Digest = agentcontrol.ChecksumFiles(map[string][]byte{"catalog": []byte(assets[0].Checksum + assets[1].Checksum + assets[2].Checksum)})
	return catalog
}

func initDisposableGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitTestCommand(t, root, "init", "-q")
	gitTestCommand(t, root, "config", "user.name", "Agent Control Plane Test")
	gitTestCommand(t, root, "config", "user.email", "agent-control-test@example.invalid")
	require.NoError(t, os.WriteFile(filepath.Join(root, "README.md"), []byte("fixture\n"), 0o644))
	commitDisposableRepo(t, root)
	return root
}

func commitDisposableRepo(t *testing.T, root string) {
	t.Helper()
	gitTestCommand(t, root, "add", "--all")
	gitTestCommand(t, root, "commit", "-m", "fixture", "--allow-empty")
}

func gitTestCommand(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	return strings.TrimSpace(string(output))
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return content
}
