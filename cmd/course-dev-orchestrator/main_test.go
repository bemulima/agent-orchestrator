package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestReadCommandFileAcceptsBoundedStdin(t *testing.T) {
	text, err := readCommandFile("-", strings.NewReader("  Проверить пути workspace.\n"))

	require.NoError(t, err)
	require.Equal(t, "Проверить пути workspace.", text)
}

func TestReadCommandFileRejectsOversizedStdin(t *testing.T) {
	_, err := readCommandFile("-", strings.NewReader(strings.Repeat("x", (1<<20)+1)))

	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestAgentAssetsCommandsRequireApprovedFingerprintAndPreserveLocalData(t *testing.T) {
	root := initAgentControlCLIGitRepo(t)
	localSkill := filepath.Join(root, ".agents/skills/local-lint/SKILL.md")
	localService := filepath.Join(root, ".ai/service.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(localSkill), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(localService), 0o755))
	require.NoError(t, os.WriteFile(localSkill, []byte("repository-local skill\n"), 0o644))
	require.NoError(t, os.WriteFile(localService, []byte("name: local-service\n"), 0o644))
	commitAgentControlCLIRepo(t, root)

	var planOutput strings.Builder
	require.NoError(t, runAgentAssetsPlan([]string{"--root", root, "--catalog-root", "../.."}, &planOutput))
	var proposal agentcontrol.Proposal
	require.NoError(t, json.Unmarshal([]byte(planOutput.String()), &proposal))
	require.NotEmpty(t, proposal.Fingerprint)
	require.NoFileExists(t, filepath.Join(root, ".agents/manifest.yaml"))

	var rejectedOutput strings.Builder
	err := runAgentAssetsApply([]string{"--root", root, "--catalog-root", "../..", "--approve-fingerprint", "sha256:wrong"}, &rejectedOutput)
	require.ErrorIs(t, err, agentcontrol.ErrApprovalRequired)
	require.NoFileExists(t, filepath.Join(root, ".agents/manifest.yaml"))

	var applyOutput strings.Builder
	require.NoError(t, runAgentAssetsApply([]string{"--root", root, "--catalog-root", "../..", "--approve-fingerprint", proposal.Fingerprint}, &applyOutput))
	require.FileExists(t, filepath.Join(root, ".agents/manifest.yaml"))
	require.FileExists(t, filepath.Join(root, ".agents/skills/task-route/SKILL.md"))
	require.FileExists(t, filepath.Join(root, ".agents/skills/contract-plan/SKILL.md"))
	require.Equal(t, []byte("repository-local skill\n"), readAgentControlCLIFile(t, localSkill))
	require.Equal(t, []byte("name: local-service\n"), readAgentControlCLIFile(t, localService))
}

func TestAgentPolicyPlanIsReadOnlyForMissingAndExistingTargets(t *testing.T) {
	missingHome := canonicalAgentControlCLIPath(t, t.TempDir())
	missingTarget := filepath.Join(missingHome, ".codex/AGENTS.md")
	var missingOutput strings.Builder
	require.NoError(t, runAgentPolicyPlan([]string{"--target", missingTarget, "--catalog-root", "../.."}, &missingOutput))
	var missing agentcontrol.GlobalPolicyProposal
	require.NoError(t, json.Unmarshal([]byte(missingOutput.String()), &missing))
	require.Equal(t, agentcontrol.GlobalPolicyMissing, missing.State)
	require.NoDirExists(t, filepath.Dir(missingTarget))

	existingHome := canonicalAgentControlCLIPath(t, t.TempDir())
	existingTarget := filepath.Join(existingHome, ".codex/AGENTS.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(existingTarget), 0o755))
	require.NoError(t, os.WriteFile(existingTarget, []byte("personal policy\n"), 0o644))
	var existingOutput strings.Builder
	require.NoError(t, runAgentPolicyPlan([]string{"--target", existingTarget, "--catalog-root", "../.."}, &existingOutput))
	var existing agentcontrol.GlobalPolicyProposal
	require.NoError(t, json.Unmarshal([]byte(existingOutput.String()), &existing))
	require.Equal(t, agentcontrol.GlobalPolicyDiffers, existing.State)
	require.Contains(t, existing.Diff, "personal policy")
	require.Equal(t, []byte("personal policy\n"), readAgentControlCLIFile(t, existingTarget))
}

func initAgentControlCLIGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitAgentControlCLI(t, root, "init", "-q")
	gitAgentControlCLI(t, root, "config", "user.name", "Agent Control Plane CLI Test")
	gitAgentControlCLI(t, root, "config", "user.email", "agent-control-cli-test@example.invalid")
	readme := filepath.Join(root, "README.md")
	require.NoError(t, os.WriteFile(readme, []byte("fixture\n"), 0o644))
	commitAgentControlCLIRepo(t, root)
	return root
}

func commitAgentControlCLIRepo(t *testing.T, root string) {
	t.Helper()
	gitAgentControlCLI(t, root, "add", "--all")
	gitAgentControlCLI(t, root, "commit", "-m", "fixture", "--allow-empty")
}

func gitAgentControlCLI(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	return strings.TrimSpace(string(output))
}

func readAgentControlCLIFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return content
}

func canonicalAgentControlCLIPath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	return canonical
}
