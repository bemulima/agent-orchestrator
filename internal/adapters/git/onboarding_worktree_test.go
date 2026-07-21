package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestOnboardingWorktreeDryRunAndApplyKeepSourceUnchanged(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source")
	worktreeStorage := filepath.Join(root, "worktrees")
	if err := os.Mkdir(sourcePath, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "AGENTS.md"), []byte("# Existing rules\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	runGit(t, sourcePath, "init", "-b", "main")
	runGit(t, sourcePath, "add", "AGENTS.md")
	runGit(t, sourcePath, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "initial")
	baseCommit := runGit(t, sourcePath, "rev-parse", "HEAD")
	localPath := sourcePath
	project := domain.Project{ID: "project-id", Name: "orders", LocalPath: &localPath}
	agentsContent := "# Existing rules\n\n<!-- agent-orchestrator:start -->\nManaged rules\n<!-- agent-orchestrator:end -->\n"
	serviceContent := "schema_version: 1\nname: orders\n"
	proposal := domain.OnboardingProposal{
		Files: []domain.ProposedFile{
			{Path: "AGENTS.md", Content: agentsContent, Checksum: contentChecksum(agentsContent), Action: domain.ProposalFileUpdate},
			{Path: ".ai/service.yaml", Content: serviceContent, Checksum: contentChecksum(serviceContent), Action: domain.ProposalFileCreate},
		},
	}
	proposal.Checksum, _ = domain.OnboardingProposalChecksum(proposal)
	run := domain.OnboardingRun{
		ID: uuid.NewString(), ProjectID: project.ID, BaseCommit: baseCommit,
		ProposalChecksum: proposal.Checksum, Proposal: proposal,
	}
	worktrees := OnboardingWorktree{StoragePath: worktreeStorage}

	dryRun, err := worktrees.DryRun(context.Background(), project, run)
	if err != nil {
		t.Fatalf("DryRun() error = %v", err)
	}
	if !dryRun.DryRun || dryRun.WorktreePath != "" {
		t.Fatalf("DryRun() result = %#v", dryRun)
	}
	if _, err := os.Stat(worktreeStorage); !os.IsNotExist(err) {
		t.Fatalf("DryRun() created worktree storage: %v", err)
	}
	assertSourceUnchanged(t, sourcePath, baseCommit)
	tampered := run
	tampered.Proposal.Files = append([]domain.ProposedFile(nil), run.Proposal.Files...)
	tampered.Proposal.Files[0].Explanation = "changed after approval"
	if _, err := worktrees.DryRun(context.Background(), project, tampered); err == nil {
		t.Fatal("DryRun() accepted a proposal whose approval fingerprint changed")
	}

	result, err := worktrees.Apply(context.Background(), project, run)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.WorktreePath == "" || !strings.HasPrefix(result.BranchName, "ai/onboard-orders-") || result.CommitSHA == "" {
		t.Fatalf("Apply() result = %#v", result)
	}
	if got := readFile(t, filepath.Join(result.WorktreePath, "AGENTS.md")); got != agentsContent {
		t.Fatalf("worktree AGENTS.md = %q", got)
	}
	if got := readFile(t, filepath.Join(result.WorktreePath, ".ai", "service.yaml")); got != serviceContent {
		t.Fatalf("worktree service.yaml = %q", got)
	}
	changed := strings.Fields(runGit(t, result.WorktreePath, "diff", "--name-only", baseCommit, result.CommitSHA))
	if strings.Join(changed, ",") != ".ai/service.yaml,AGENTS.md" {
		t.Fatalf("committed paths = %v", changed)
	}
	assertSourceUnchanged(t, sourcePath, baseCommit)

	repeated, err := worktrees.Apply(context.Background(), project, run)
	if err != nil {
		t.Fatalf("repeated Apply() error = %v", err)
	}
	if repeated.CommitSHA != result.CommitSHA || repeated.WorktreePath != result.WorktreePath {
		t.Fatalf("repeated Apply() = %#v, want same commit/worktree", repeated)
	}
	assertSourceUnchanged(t, sourcePath, baseCommit)

	if err := os.WriteFile(filepath.Join(sourcePath, "dirty.txt"), []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := worktrees.DryRun(context.Background(), project, run); err == nil {
		t.Fatal("DryRun() accepted a dirty source checkout")
	}
}

func TestValidateNULPathsRejectsUnapprovedFileWithinAI(t *testing.T) {
	approved := map[string]struct{}{".ai/service.yaml": {}}
	if err := validateNULPaths(".ai/service.yaml\x00.ai/not-approved.yaml\x00", approved); err == nil {
		t.Fatal("validateNULPaths() accepted an unapproved .ai file")
	}
}

func TestWriteProposedFileRejectsSymlinkedDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".ai")); err != nil {
		t.Fatal(err)
	}
	content := "schema_version: 1\n"
	err := writeProposedFile(root, domain.ProposedFile{
		Path: ".ai/service.yaml", Content: content, Checksum: contentChecksum(content),
	})
	if err == nil {
		t.Fatal("writeProposedFile() followed a symlinked .ai directory")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "service.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("write escaped through symlink: %v", statErr)
	}
}

func assertSourceUnchanged(t *testing.T, sourcePath, baseCommit string) {
	t.Helper()
	if head := runGit(t, sourcePath, "rev-parse", "HEAD"); head != baseCommit {
		t.Fatalf("source HEAD = %s, want %s", head, baseCommit)
	}
	if status := runGit(t, sourcePath, "status", "--porcelain=v1", "--untracked-files=all"); status != "" {
		t.Fatalf("source checkout changed: %q", status)
	}
	if _, err := os.Stat(filepath.Join(sourcePath, ".ai")); !os.IsNotExist(err) {
		t.Fatalf("source .ai was created: %v", err)
	}
}

func runGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
