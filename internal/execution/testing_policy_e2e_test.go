package execution

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	gitadapter "github.com/bemulima/agent-orchestrator/internal/adapters/git"
	"github.com/bemulima/agent-orchestrator/internal/agent"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

// This test deliberately runs the pinned policy bundle against a disposable Git
// repository and then takes the result through Service.Execute. CI must provide
// Node 20 (or POLICY_NODE20 pointing at a Node 20 binary); this policy runner
// rejects other major versions.
func TestBundleTestingPolicyDoDControlsTaskCompletionWithRealArtifacts(t *testing.T) {
	node20 := testingPolicyNode20(t)

	for _, test := range []struct {
		name                 string
		changedFile          string
		businessEvidenceMode string
		wantMatrixBA         string
		wantResolution       string
		wantDodBA            string
		wantDoD              string
		wantTask             domain.TaskStatus
		wantDone             bool
		wantBlocker          string
		wantBlockerMessage   string
	}{
		{
			name:           "test-only change can complete",
			changedFile:    "internal/dod_fixture_test.go",
			wantMatrixBA:   "NOT_REQUIRED",
			wantResolution: "NOT_REQUIRED",
			wantDodBA:      "NOT_REQUIRED",
			wantDoD:        "DONE",
			wantTask:       domain.TaskStatusCompleted,
			wantDone:       true,
		},
		{
			name:           "unmapped functional change stays blocked",
			changedFile:    "internal/dod_fixture.go",
			wantMatrixBA:   "UNKNOWN",
			wantResolution: "UNKNOWN",
			wantDodBA:      "PENDING",
			wantDoD:        "BLOCKED",
			wantTask:       domain.TaskStatusBlocked,
			wantDone:       false,
		},
		{
			name:                 "mapped required capability with valid immutable evidence can complete",
			changedFile:          "internal/domain/course_outline.go",
			businessEvidenceMode: "valid",
			wantMatrixBA:         "REQUIRED",
			wantResolution:       "MAPPED",
			wantDodBA:            "EVIDENCE_PRESENT",
			wantDoD:              "DONE",
			wantTask:             domain.TaskStatusCompleted,
			wantDone:             true,
		},
		{
			name:           "mapped required capability without evidence remains pending",
			changedFile:    "internal/domain/course_outline.go",
			wantMatrixBA:   "REQUIRED",
			wantResolution: "MAPPED",
			wantDodBA:      "PENDING",
			wantDoD:        "BUSINESS_ACCEPTANCE_PENDING",
			wantTask:       domain.TaskStatusBlocked,
			wantBlocker:    "DOD_BUSINESS_ACCEPTANCE_PENDING",
		},
		{
			name:                 "mapped required capability with invalid evidence remains pending",
			changedFile:          "internal/domain/course_outline.go",
			businessEvidenceMode: "invalid",
			wantMatrixBA:         "REQUIRED",
			wantResolution:       "MAPPED",
			wantDodBA:            "PENDING",
			wantDoD:              "BUSINESS_ACCEPTANCE_PENDING",
			wantTask:             domain.TaskStatusBlocked,
			wantBlocker:          "DOD_BUSINESS_ACCEPTANCE_INVALID",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := prepareTestingPolicyFixture(t, test.changedFile, test.businessEvidenceMode, node20)
			outcome, err := fixture.service.Execute(context.Background(), fixture.taskID, "workflow-e2e")
			require.NoError(t, err)
			fixture.headSHA = runFixtureGit(t, fixture.workspace, "rev-parse", "HEAD")
			require.Equal(t, test.wantTask, outcome.Result.Status, "unexpected lifecycle status: outcome=%#v attempt=%#v", outcome, fixture.repo.failedStructured)
			require.Equal(t, test.wantDone, fixture.repo.completed)
			require.Equal(t, map[bool]domain.TaskAttemptStatus{true: domain.TaskAttemptStatusCompleted, false: domain.TaskAttemptStatusBlocked}[test.wantDone], fixture.repo.attempt.Status)

			aggregatePath := filepath.Join(fixture.workspace, "test-results", "attempt-1", "verify", "pr", "test-result.v1.json")
			aggregateBytes, err := os.ReadFile(aggregatePath)
			require.NoError(t, err)
			var aggregate testingPolicyE2EAggregate
			require.NoError(t, json.Unmarshal(aggregateBytes, &aggregate))
			require.Equal(t, "required-test-matrix.v1", aggregate.Selection.Selector)
			require.Equal(t, fixture.headSHA, aggregate.Repository.CommitSHA)
			require.NotEmpty(t, aggregate.Selection.SelectedCommands)

			matrixDescriptor := testingPolicyE2EMatrixDescriptor(t, aggregate.Artifacts)
			matrixBytes, err := os.ReadFile(filepath.Join(fixture.workspace, filepath.FromSlash(matrixDescriptor.URI)))
			require.NoError(t, err)
			matrixHash := sha256.Sum256(matrixBytes)
			require.Equal(t, "sha256:"+hex.EncodeToString(matrixHash[:]), matrixDescriptor.Checksum)
			var matrix testingPolicyE2EMatrix
			require.NoError(t, json.Unmarshal(matrixBytes, &matrix))
			require.Equal(t, fixture.baseSHA, matrix.BaseSHA)
			require.Equal(t, fixture.headSHA, matrix.HeadSHA)
			require.Equal(t, test.wantMatrixBA, matrix.BusinessAcceptanceRequired)
			require.Equal(t, test.wantResolution, matrix.BusinessAcceptanceResolution.Disposition)
			require.Equal(t, []string{test.changedFile}, testingPolicyE2EChangedPathNames(matrix.ChangedPaths))
			if test.wantMatrixBA == "NOT_REQUIRED" {
				require.Equal(t, "unit-test", matrix.ChangedPaths[0].Classification)
				require.Equal(t, []string{"impact-unit-test-v1"}, matrix.ChangedPaths[0].RuleIDs)
			} else if test.wantMatrixBA == "UNKNOWN" {
				require.Equal(t, "source-code-unmapped", matrix.ChangedPaths[0].Classification)
				require.Equal(t, []string{"impact-source-unmapped-v1"}, matrix.ChangedPaths[0].RuleIDs)
				require.Contains(t, matrix.Uncertainty, "UNKNOWN_OR_AMBIGUOUS_PATH:"+test.changedFile)
			} else {
				require.Equal(t, "domain-usecase-or-state-transition", matrix.ChangedPaths[0].Classification)
				require.Equal(t, []string{"impact-domain-usecase-v1"}, matrix.ChangedPaths[0].RuleIDs)
				require.Equal(t, []string{"learning_content_discovery"}, matrix.BusinessAcceptanceResolution.RequiredCapabilityIDs)
				require.Empty(t, matrix.BusinessAcceptanceResolution.UnmappedPaths)
			}

			for _, command := range aggregate.Selection.SelectedCommands {
				leafPath := filepath.Join(fixture.workspace, "test-results", "attempt-1", filepath.FromSlash(strings.ReplaceAll(command, ":", "/")), "test-result.v1.json")
				leafBytes, readErr := os.ReadFile(leafPath)
				require.NoError(t, readErr, "missing real leaf result for %s", command)
				var leaf struct {
					Command string `json:"command"`
				}
				require.NoError(t, json.Unmarshal(leafBytes, &leaf))
				require.Equal(t, command, leaf.Command)
			}

			dodPath := filepath.Join(fixture.workspace, "test-results", "attempt-1", "agent-dod.v1.json")
			dodBytes, err := os.ReadFile(dodPath)
			require.NoError(t, err)
			var dod testingPolicyDodReport
			require.NoError(t, json.Unmarshal(dodBytes, &dod))
			require.NoError(t, validateTestingPolicyDodReport(dod, fixture.baseSHA, fixture.headSHA))
			require.Equal(t, test.wantDoD, dod.LifecycleState)
			require.Equal(t, test.wantDodBA, *dod.Dispositions.BusinessAcceptance)
			if test.wantBlocker != "" {
				blocker := testingPolicyE2EBlockerByCode(t, dod.Blockers, test.wantBlocker)
				require.Equal(t, "PENDING", blocker.Severity)
				if test.wantBlockerMessage != "" {
					require.Contains(t, blocker.Message, test.wantBlockerMessage)
				}
			} else if matrix.BusinessAcceptanceRequired != "UNKNOWN" {
				require.Empty(t, dod.Blockers)
			}

			if matrix.BusinessAcceptanceRequired == "UNKNOWN" {
				scopeBlocker := testingPolicyE2EBlockerByCode(t, dod.Blockers, "DOD_BUSINESS_SCOPE_UNKNOWN")
				require.Equal(t, "PENDING", scopeBlocker.Severity)
				require.Contains(t, scopeBlocker.Message, "cannot prove whether central business acceptance applies")
				require.Contains(t, testingPolicyE2EBlockerCodes(dod.Blockers), "DOD_IMPACT_UNRESOLVED")
				require.Equal(t, domain.TaskAttemptStatusBlocked, fixture.repo.failedStatus)
			} else if test.wantBlocker == "" {
				require.Equal(t, domain.TaskAttemptStatusCompleted, fixture.repo.attempt.Status)
			} else {
				require.Equal(t, domain.TaskAttemptStatusBlocked, fixture.repo.failedStatus)
			}
			if test.businessEvidenceMode == "missing" || test.businessEvidenceMode == "" {
				require.NotContains(t, strings.Join(fixture.worktrees.commands, "\n"), "--business-acceptance-evidence")
			} else {
				require.Contains(t, strings.Join(fixture.worktrees.commands, "\n"), "--business-acceptance-evidence "+businessAcceptanceEvidencePath)
			}
			if test.businessEvidenceMode == "valid" || test.businessEvidenceMode == "invalid" {
				storedEvidence := false
				for _, artifact := range fixture.repo.artifacts {
					if artifact.TaskID == fixture.taskID && artifact.Type == businessAcceptanceEvidenceType &&
						artifact.Name == businessAcceptanceEvidenceName && strings.HasSuffix(artifact.URI, "/"+businessAcceptanceEvidencePath) {
						storedEvidence = true
						require.Len(t, artifact.Checksum, 64)
					}
				}
				require.True(t, storedEvidence, "business evidence artifact should be stored for audit")
			}
		})
	}
}

type testingPolicyE2EFixture struct {
	service   Service
	repo      *fakeExecutionRepository
	worktrees *policyNode20Worktree
	baseSHA   string
	headSHA   string
	taskID    string
	workspace string
}

func prepareTestingPolicyFixture(t *testing.T, changedFile, businessEvidenceMode, node20 string) testingPolicyE2EFixture {
	t.Helper()
	root := t.TempDir()
	repositoryName := "course-dev-orchestrator"
	if changedFile == "internal/domain/course_outline.go" {
		repositoryName = "ms-go-course"
	}
	repositoryRoot := filepath.Join(root, repositoryName)
	require.NoError(t, os.MkdirAll(repositoryRoot, 0o750))
	copyCommittedTestingPolicyFixture(t, repositoryRoot, repositoryName)

	const taskID = "e2e-task-1"
	const projectName = "policy-e2e"
	rewriteTestingPolicyFixtureManifest(t, repositoryRoot)

	binPath := filepath.Join(root, "node20-bin")
	require.NoError(t, os.MkdirAll(binPath, 0o750))
	require.NoError(t, os.Symlink(node20, filepath.Join(binPath, "node")))

	runFixtureGit(t, repositoryRoot, "init", "-b", "main")
	runFixtureGit(t, repositoryRoot, "add", "-A")
	runFixtureGit(t, repositoryRoot, "-c", "user.name=Policy fixture", "-c", "user.email=policy-fixture@example.test", "commit", "-m", "policy fixture base")
	baseSHA := runFixtureGit(t, repositoryRoot, "rev-parse", "HEAD")

	validator, err := agent.NewValidator()
	require.NoError(t, err)
	repo := newFakeExecutionRepository()
	repo.executionContext.Task.ID = taskID
	repo.executionContext.Task.Title = "Testing Policy DoD fixture"
	repo.executionContext.Task.VerificationCommands = []string{"git diff --check"}
	repo.attempt.TaskID = taskID
	projectPath := repositoryRoot
	repo.executionContext.Project.Name = projectName
	repo.executionContext.Project.LocalPath = &projectPath
	repo.executionContext.Project.HeadCommit = baseSHA

	worktreeStore := filepath.Join(root, "task-worktrees")
	worktrees := &policyNode20Worktree{
		TaskWorktree:         gitadapter.TaskWorktree{StoragePath: worktreeStore},
		node20:               node20,
		path:                 binPath + string(os.PathListSeparator) + os.Getenv("PATH"),
		businessEvidenceMode: businessEvidenceMode,
	}
	runner := &testingPolicyE2EAgentRunner{changedFile: changedFile}
	service := Service{
		Repository: repo, Worktrees: worktrees, Runner: runner, Validator: validator,
		Verifier: Verifier{Worktrees: worktrees}, TestingPolicy: BundleTestingPolicyGate{Worktrees: worktrees},
		Models: map[string]string{"standard": "fixture-model"}, Reasoning: map[string]string{"standard": "medium"},
		ReviewModel: "fixture-review", ReviewReasoning: "high", MaxTaskAttempts: 3, MaxReviewAttempts: 2,
	}
	workspacePath := filepath.Join(worktreeStore, projectName+"-task-e2etask1", repositoryName)
	require.Equal(t, filepath.Base(repositoryRoot), filepath.Base(workspacePath))

	return testingPolicyE2EFixture{
		service: service, repo: repo, worktrees: worktrees, baseSHA: baseSHA, taskID: taskID, workspace: workspacePath,
	}
}

// The mapped cases exercise an orchestrated GO_SERVICE target, using immutable
// metadata from the published Course repository. These are protocol fixtures;
// their pass commands and hosted evidence identifiers are synthetic.
//
//go:embed testdata/course-target-context.tar.gz
var testingPolicyCourseContextArchive []byte

//go:embed testdata/course-target-context.source.json
var testingPolicyCourseContextSource []byte

func copyTestingPolicyCourseContext(t *testing.T, destination string) {
	t.Helper()
	var source struct {
		Repository    string `json:"repository"`
		SourceSHA     string `json:"source_sha"`
		ArchiveSHA256 string `json:"archive_sha256"`
		Files         []struct {
			Path    string `json:"path"`
			SHA256  string `json:"sha256"`
			BlobSHA string `json:"blob_sha"`
		} `json:"files"`
	}
	require.NoError(t, json.Unmarshal(testingPolicyCourseContextSource, &source))
	require.Equal(t, "bemulima/ms-go-course", source.Repository)
	require.Equal(t, "1b740849e42f7b3f1616b715f252665a1ebaab19", source.SourceSHA)
	archiveHash := sha256.Sum256(testingPolicyCourseContextArchive)
	require.Equal(t, source.ArchiveSHA256, hex.EncodeToString(archiveHash[:]))
	reader, err := gzip.NewReader(bytes.NewReader(testingPolicyCourseContextArchive))
	require.NoError(t, err)
	contents, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.NoError(t, extractTestingPolicyArchive(destination, contents))
	require.NotEmpty(t, source.Files)
	for _, file := range source.Files {
		require.True(t, testingPolicyE2EFullGitSHA(file.BlobSHA), "immutable source blob for %s", file.Path)
		content, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(file.Path)))
		require.NoError(t, err)
		digest := sha256.Sum256(content)
		require.Equal(t, file.SHA256, hex.EncodeToString(digest[:]), "published target bytes for %s", file.Path)
	}
}

func copyCommittedTestingPolicyFixture(t *testing.T, destination, repositoryName string) {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	cdoRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	if repositoryName == "ms-go-course" {
		copyTestingPolicyCourseContext(t, destination)
	} else {
		archiveCommand := exec.Command("git", "archive", "--format=tar", "HEAD")
		archiveCommand.Dir = cdoRoot
		archiveBytes, err := archiveCommand.Output()
		require.NoError(t, err)
		require.NoError(t, extractTestingPolicyArchive(destination, archiveBytes))
	}

	for _, relative := range []string{".ai/testing/policy/policy-lock.json", ".ai/testing/policy/policy-runner.cjs"} {
		content, readErr := os.ReadFile(filepath.Join(cdoRoot, filepath.FromSlash(relative)))
		require.NoError(t, readErr, "read current pinned bundle file %s", relative)
		target := filepath.Join(destination, filepath.FromSlash(relative))
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o750))
		require.NoError(t, os.WriteFile(target, content, 0o640))
	}

	passScript := filepath.Join(destination, "scripts", "policy-e2e-pass.mjs")
	require.NoError(t, os.MkdirAll(filepath.Dir(passScript), 0o750))
	require.NoError(t, os.WriteFile(passScript, []byte("process.stdout.write('fixture PASS\\n');\n"), 0o640))
}

func extractTestingPolicyArchive(destination string, contents []byte) error {
	reader := tar.NewReader(bytes.NewReader(contents))
	root, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			return nil
		}
		if nextErr != nil {
			return nextErr
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			if header.Name != "pax_global_header" {
				return fmt.Errorf("unexpected PAX global header name %q", header.Name)
			}
			for key, value := range header.PAXRecords {
				if key != "comment" || !testingPolicyE2EFullGitSHA(value) {
					return fmt.Errorf("unsupported PAX global metadata %q=%q", key, value)
				}
			}
			continue
		}
		name := filepath.Clean(filepath.FromSlash(header.Name))
		if filepath.IsAbs(name) || name == "." || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe path in Git archive: %q", header.Name)
		}
		target := filepath.Join(root, name)
		relative, relErr := filepath.Rel(root, target)
		if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("Git archive path escaped fixture root: %q", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			file, createErr := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode)&0o750)
			if createErr != nil {
				return createErr
			}
			_, copyErr := io.Copy(file, reader)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsupported Git archive entry type %d for %q", header.Typeflag, header.Name)
		}
	}
}

func testingPolicyE2EFullGitSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func rewriteTestingPolicyFixtureManifest(t *testing.T, repositoryRoot string) {
	t.Helper()
	manifestPath := filepath.Join(repositoryRoot, ".ai", "testing", "test-manifest.yaml")
	content, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	var document yaml.Node
	require.NoError(t, yaml.Unmarshal(content, &document))
	require.NotEmpty(t, document.Content)
	root := document.Content[0]
	commands := testingPolicyYAMLMapValue(root, "commands")
	require.NotNil(t, commands)
	for index := 0; index+1 < len(commands.Content); index += 2 {
		command := commands.Content[index+1]
		availability := testingPolicyYAMLMapValue(command, "availability")
		if availability == nil || availability.Value != "AVAILABLE" {
			continue
		}
		testingPolicyYAMLSetSequence(command, "invoke", "node", "scripts/policy-e2e-pass.mjs")
		testingPolicyYAMLSetSequence(command, "requires")
		if testingPolicyYAMLMapValue(command, "env") != nil {
			testingPolicyYAMLSetSequence(command, "env")
		}
		if testingPolicyYAMLMapValue(command, "artifacts") != nil {
			testingPolicyYAMLSetSequence(command, "artifacts")
		}
	}

	updated, err := yaml.Marshal(&document)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, updated, 0o640))
}

func testingPolicyYAMLMapValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func testingPolicyYAMLSetScalar(mapping *yaml.Node, key, value string) {
	node := testingPolicyYAMLMapValue(mapping, key)
	if node == nil {
		node = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str"}
		mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, node)
	}
	node.Kind = yaml.ScalarNode
	node.Tag = "!!str"
	node.Value = value
}

func testingPolicyYAMLSetSequence(mapping *yaml.Node, key string, values ...string) {
	node := testingPolicyYAMLMapValue(mapping, key)
	if node == nil {
		node = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, node)
	}
	node.Kind = yaml.SequenceNode
	node.Tag = "!!seq"
	node.Content = nil
	for _, value := range values {
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
	}
}

func testingPolicyNode20(t *testing.T) string {
	t.Helper()
	configured := strings.TrimSpace(os.Getenv("POLICY_NODE20"))
	candidates := make([]string, 0, 3)
	if configured != "" {
		candidates = append(candidates, configured)
	} else {
		for _, name := range []string{"node20", "node"} {
			if path, err := exec.LookPath(name); err == nil {
				candidates = append(candidates, path)
			}
		}
	}
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate)
		if err != nil {
			if configured != "" {
				t.Fatalf("POLICY_NODE20 does not resolve to an executable: %v", err)
			}
			continue
		}
		output, versionErr := exec.Command(path, "--version").CombinedOutput()
		if versionErr == nil && strings.HasPrefix(strings.TrimSpace(string(output)), "v20.") {
			return path
		}
		if configured != "" {
			t.Fatalf("POLICY_NODE20 must be Node 20, got %q (%v)", strings.TrimSpace(string(output)), versionErr)
		}
	}
	t.Skip("requires Node 20; set POLICY_NODE20 or run under a Node 20 PATH")
	return ""
}

type policyNode20Worktree struct {
	gitadapter.TaskWorktree
	node20               string
	path                 string
	businessEvidenceMode string
	commands             []string
}

func (w *policyNode20Worktree) RunCheck(ctx context.Context, workspace domain.TaskWorkspace, requested string) (domain.WorkspaceCheckResult, error) {
	w.commands = append(w.commands, requested)
	if !strings.HasPrefix(requested, "node20 .ai/testing/policy/policy-runner.cjs ") {
		return w.TaskWorktree.RunCheck(ctx, workspace, requested)
	}
	parts := strings.Fields(requested)
	if len(parts) < 3 || parts[0] != "node20" || parts[1] != ".ai/testing/policy/policy-runner.cjs" {
		return domain.WorkspaceCheckResult{}, fmt.Errorf("invalid fixture Testing Policy command %q", requested)
	}
	command := exec.CommandContext(ctx, w.node20, parts[1:]...)
	command.Dir = workspace.Path
	command.Env = testingPolicyE2EPathEnvironment(os.Environ(), w.path)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	exitCode := 0
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			return domain.WorkspaceCheckResult{}, err
		}
		exitCode = exitError.ExitCode()
	}
	if parts[2] == "verify" && w.businessEvidenceMode != "" {
		if err := writeTestingPolicyE2EBusinessEvidence(workspace.Path, w.businessEvidenceMode); err != nil {
			return domain.WorkspaceCheckResult{}, err
		}
	}
	return domain.WorkspaceCheckResult{Command: requested, ExitCode: exitCode, Output: output.String()}, nil
}

func writeTestingPolicyE2EBusinessEvidence(repositoryRoot, mode string) error {
	// The hosted run and artifact identifiers below are synthetic test inputs.
	// This fixture exercises path forwarding and parser behavior; it does not
	// establish GitHub Actions run or artifact API authenticity.
	if mode == "invalid" {
		path := filepath.Join(repositoryRoot, filepath.FromSlash(businessAcceptanceEvidencePath))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("{invalid structured evidence"), 0o640)
	}
	if mode != "valid" {
		return fmt.Errorf("unsupported business evidence fixture mode %q", mode)
	}
	matrixPaths, err := filepath.Glob(filepath.Join(repositoryRoot, "test-results", "required-test-matrix", "*.json"))
	if err != nil {
		return err
	}
	if len(matrixPaths) != 1 {
		return fmt.Errorf("expected one generated Required Test Matrix, got %d", len(matrixPaths))
	}
	matrixBytes, err := os.ReadFile(matrixPaths[0])
	if err != nil {
		return err
	}
	var matrix testingPolicyE2EMatrix
	if err := json.Unmarshal(matrixBytes, &matrix); err != nil {
		return err
	}
	if matrix.BusinessAcceptanceRequired != "REQUIRED" || matrix.BusinessAcceptanceResolution.Disposition != "MAPPED" ||
		len(matrix.BusinessAcceptanceResolution.RequiredCapabilityIDs) != 1 || matrix.BusinessAcceptanceResolution.RequiredCapabilityIDs[0] != "learning_content_discovery" {
		return fmt.Errorf("fixture matrix did not resolve the expected mapped business capability")
	}
	const verificationRunID = 1
	const verificationRunAttempt = 1
	const verificationRepository = "bemulima/learning-platform-verification"
	verificationCommit := matrix.Policy.Identity.SourceCommit
	if !fullSHA(verificationCommit) {
		return fmt.Errorf("fixture matrix policy identity has no exact Verification source commit")
	}
	runURL := fmt.Sprintf("https://github.com/%s/actions/runs/%d", verificationRepository, verificationRunID)
	resultPath := "test-results/business-acceptance/course-outline-test-result.json"
	result := map[string]any{
		"schema_version":          "test-result.v1",
		"repository":              map[string]any{"id": "learning-platform-verification", "commit_sha": verificationCommit},
		"manifest_schema_version": "test-manifest.v1",
		"manifest_version":        matrix.Manifest.Version,
		"policy_version":          "testing-policy.v1",
		"tier":                    matrix.Tier,
		"command":                 "test:unit",
		"categories":              []string{"unit.domain"},
		"started_at":              "2026-01-01T00:00:00.000Z",
		"completed_at":            "2026-01-01T00:00:00.000Z",
		"duration_ms":             0,
		"native_exit_code":        0,
		"exit_code":               0,
		"status":                  "PASS",
		"status_counts": map[string]int{
			"PASS": 1, "FAIL": 0, "SKIP_EXPECTED": 0, "BLOCKED": 0, "NOT_APPLICABLE": 0,
		},
		"selection": map[string]any{"selector": "required-test-matrix.v1", "selected_commands": []string{"test:unit"}},
		"checks": []any{map[string]any{
			"logical_command": "test:unit", "categories": []string{"unit.domain"}, "status": "PASS", "duration_ms": 0, "native_exit_code": 0,
		}},
		"artifacts": []any{},
		"ci":        map[string]any{"provider": "github-actions", "run_id": fmt.Sprint(verificationRunID), "run_attempt": verificationRunAttempt, "run_url": runURL},
	}
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return err
	}
	matrixHash := sha256.Sum256(matrixBytes)
	archivePath := "test-results/business-acceptance/acceptance-results.zip"
	var archiveBytes bytes.Buffer
	archiveWriter := zip.NewWriter(&archiveBytes)
	archiveEntry, err := archiveWriter.Create(resultPath)
	if err != nil {
		return err
	}
	if _, err := archiveEntry.Write(resultBytes); err != nil {
		return err
	}
	if err := archiveWriter.Close(); err != nil {
		return err
	}
	archiveHash := sha256.Sum256(archiveBytes.Bytes())
	resultHash := sha256.Sum256(resultBytes)
	evidence := map[string]any{
		"schema_version": "business-acceptance-evidence.v1",
		"subject": map[string]any{
			"repository_id": matrix.Repository.ID, "base_sha": matrix.BaseSHA, "head_sha": matrix.HeadSHA,
			"matrix_sha256":           hex.EncodeToString(matrixHash[:]),
			"required_capability_ids": matrix.BusinessAcceptanceResolution.RequiredCapabilityIDs,
		},
		"policy": map[string]any{
			"source_repository": matrix.Policy.Identity.SourceRepository,
			"source_commit_sha": verificationCommit,
			"semantics_sha256":  matrix.Policy.Identity.SemanticsSHA,
			"bundle_sha256":     matrix.Policy.Identity.BundleSHA256,
		},
		"verification": map[string]any{"repository_id": verificationRepository, "commit_sha": verificationCommit},
		"run": map[string]any{
			"provider": "github-actions", "repository": verificationRepository, "run_id": verificationRunID,
			"run_attempt": verificationRunAttempt, "run_url": runURL,
		},
		"artifact": map[string]any{
			"name": "business-acceptance-e2e-fixture", "artifact_id": 1,
			"archive_sha256": hex.EncodeToString(archiveHash[:]), "archive_path": archivePath,
		},
		"results": []any{map[string]any{
			"capability_id": "learning_content_discovery", "feature_id": "course_public_outline", "scenario_id": "course_outline_visible",
			"evidence_type": "test-result.v1", "status": "PASS", "result_path": resultPath, "result_sha256": hex.EncodeToString(resultHash[:]),
		}},
		"status": "PASS",
	}
	evidenceBytes, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	evidenceFile := filepath.Join(repositoryRoot, filepath.FromSlash(resultPath))
	if err := os.MkdirAll(filepath.Dir(evidenceFile), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(evidenceFile, resultBytes, 0o640); err != nil {
		return err
	}
	archiveFile := filepath.Join(repositoryRoot, filepath.FromSlash(archivePath))
	if err := os.MkdirAll(filepath.Dir(archiveFile), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(archiveFile, archiveBytes.Bytes(), 0o640); err != nil {
		return err
	}
	evidencePath := filepath.Join(repositoryRoot, filepath.FromSlash(businessAcceptanceEvidencePath))
	if err := os.MkdirAll(filepath.Dir(evidencePath), 0o750); err != nil {
		return err
	}
	return os.WriteFile(evidencePath, evidenceBytes, 0o640)
}

func testingPolicyE2EPathEnvironment(source []string, path string) []string {
	result := make([]string, 0, len(source)+1)
	for _, pair := range source {
		if strings.HasPrefix(pair, "PATH=") {
			continue
		}
		result = append(result, pair)
	}
	return append(result, "PATH="+path)
}

type testingPolicyE2EAgentRunner struct {
	changedFile string
}

func (r *testingPolicyE2EAgentRunner) Run(ctx context.Context, request domain.AgentRunRequest, callback repository.AgentThreadCallback) (domain.AgentRunResponse, error) {
	threadID := "policy-e2e-coder"
	var result []byte
	if request.Role == domain.AgentRunCoder {
		path := filepath.Join(request.WorkingDirectory, filepath.FromSlash(r.changedFile))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return domain.AgentRunResponse{}, err
		}
		if err := os.WriteFile(path, []byte("package fixture\n\nconst value = \"changed\"\n"), 0o640); err != nil {
			return domain.AgentRunResponse{}, err
		}
		payload := map[string]any{
			"status": "completed", "summary": "changed the disposable fixture", "files_changed": []string{r.changedFile},
			"checks":    []map[string]string{{"name": "git diff --check", "status": "passed", "details": "clean"}},
			"artifacts": []any{}, "blockers": []any{}, "required_tasks": []any{}, "risks": []any{}, "notes_for_reviewer": []any{},
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return domain.AgentRunResponse{}, err
		}
		result = encoded
	} else {
		threadID = "policy-e2e-reviewer"
		result = approvedReviewResult()
	}
	if err := callback(ctx, threadID); err != nil {
		return domain.AgentRunResponse{}, err
	}
	return domain.AgentRunResponse{ThreadID: threadID, Result: result}, nil
}

type testingPolicyE2EAggregate struct {
	Repository struct {
		CommitSHA string `json:"commit_sha"`
	} `json:"repository"`
	Selection struct {
		Selector         string   `json:"selector"`
		SelectedCommands []string `json:"selected_commands"`
	} `json:"selection"`
	Artifacts []struct {
		URI      string `json:"uri"`
		Checksum string `json:"checksum"`
		Category string `json:"category"`
	} `json:"artifacts"`
}

type testingPolicyE2EMatrix struct {
	BaseSHA                    string `json:"base_sha"`
	HeadSHA                    string `json:"head_sha"`
	BusinessAcceptanceRequired string `json:"business_acceptance_required"`
	Repository                 struct {
		ID string `json:"id"`
	} `json:"repository"`
	Manifest struct {
		Version string `json:"version"`
	} `json:"manifest"`
	Policy struct {
		Identity struct {
			SourceRepository string `json:"source_repository"`
			SourceCommit     string `json:"source_commit"`
			SemanticsSHA     string `json:"semantics_sha"`
			BundleSHA256     string `json:"bundle_sha256"`
		} `json:"identity"`
	} `json:"policy"`
	Tier                         string `json:"tier"`
	BusinessAcceptanceResolution struct {
		Disposition           string   `json:"disposition"`
		RequiredCapabilityIDs []string `json:"required_capability_ids"`
		UnmappedPaths         []string `json:"unmapped_paths"`
	} `json:"business_acceptance_resolution"`
	Uncertainty  []string                      `json:"uncertainty"`
	ChangedPaths []testingPolicyE2EChangedPath `json:"changed_paths"`
}

type testingPolicyE2EChangedPath = struct {
	Path           string   `json:"path"`
	Classification string   `json:"classification"`
	RuleIDs        []string `json:"rule_ids"`
}

type testingPolicyE2EBlocker = struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Subject  string `json:"subject"`
	Message  string `json:"message"`
}

func testingPolicyE2EMatrixDescriptor(t *testing.T, artifacts []struct {
	URI      string `json:"uri"`
	Checksum string `json:"checksum"`
	Category string `json:"category"`
}) struct {
	URI      string `json:"uri"`
	Checksum string `json:"checksum"`
	Category string `json:"category"`
} {
	t.Helper()
	for _, artifact := range artifacts {
		if artifact.Category == "diagnostic" && strings.HasPrefix(artifact.URI, "test-results/required-test-matrix/") {
			return artifact
		}
	}
	t.Fatal("aggregate result did not include the required-test-matrix artifact descriptor")
	return struct {
		URI      string `json:"uri"`
		Checksum string `json:"checksum"`
		Category string `json:"category"`
	}{}
}

func testingPolicyE2EChangedPathNames(paths []testingPolicyE2EChangedPath) []string {
	result := make([]string, len(paths))
	for index, path := range paths {
		result[index] = path.Path
	}
	return result
}

func testingPolicyE2EBlockerCodes(blockers []testingPolicyE2EBlocker) []string {
	result := make([]string, len(blockers))
	for index, blocker := range blockers {
		result[index] = blocker.Code
	}
	return result
}

func testingPolicyE2EBlockerByCode(t *testing.T, blockers []testingPolicyE2EBlocker, code string) testingPolicyE2EBlocker {
	t.Helper()
	for _, blocker := range blockers {
		if blocker.Code == code {
			return blocker
		}
	}
	t.Fatalf("missing DoD blocker %s", code)
	return testingPolicyE2EBlocker{}
}
