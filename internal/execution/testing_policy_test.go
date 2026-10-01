package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestDefaultTestingPolicyPinMatchesBundledArtifact(t *testing.T) {
	lockBytes, err := os.ReadFile(filepath.Join("..", "..", ".ai", "testing", "policy", "policy-lock.json"))
	require.NoError(t, err)
	var lock testingPolicyLock
	require.NoError(t, json.Unmarshal(lockBytes, &lock))

	bundle, err := os.ReadFile(filepath.Join("..", "..", ".ai", "testing", "policy", "policy-runner.cjs"))
	require.NoError(t, err)
	digest := sha256.Sum256(bundle)

	require.Equal(t, lock.SourceCommit, TestingPolicySourceCommit)
	require.Equal(t, lock.SemanticsSHA, TestingPolicySemanticsSHA)
	require.Equal(t, lock.BundleSHA256, TestingPolicyBundleSHA256)
	require.Equal(t, lock.BundleSHA256, hex.EncodeToString(digest[:]))
}

func TestBundleTestingPolicyGateRejectsUntrustedBundleBeforeExecution(t *testing.T) {
	worktrees := &testingPolicyWorktreeFixture{bundle: []byte("runner")}
	gate := BundleTestingPolicyGate{
		Worktrees:            worktrees,
		ExpectedSourceCommit: strings.Repeat("a", 40),
		ExpectedSemanticsSHA: strings.Repeat("b", 40),
		ExpectedBundleSHA256: strings.Repeat("c", 64),
	}

	_, err := gate.VerifyTask(context.Background(), domain.TaskWorkspace{}, "run-1", strings.Repeat("d", 40), strings.Repeat("e", 40))
	require.Error(t, err)
	require.Empty(t, worktrees.commands)
}

func TestBundleTestingPolicyGateRequiresDoneReportAndExactEvidenceCommands(t *testing.T) {
	bundle := []byte("trusted runner bundle")
	digest := sha256.Sum256(bundle)
	bundleSHA := hex.EncodeToString(digest[:])
	sourceSHA := strings.Repeat("a", 40)
	semanticsSHA := strings.Repeat("b", 40)
	baseSHA, headSHA := strings.Repeat("d", 40), strings.Repeat("e", 40)
	lock, err := json.Marshal(testingPolicyLock{
		LockSchemaVersion: "testing-policy-bundle.v2",
		PolicyVersion:     "testing-policy.v1",
		MatrixSchema:      "required-test-matrix.v1",
		SourceRepository:  "bemulima/learning-platform-verification",
		SourceCommit:      sourceSHA,
		SemanticsSHA:      semanticsSHA,
		BundleSHA256:      bundleSHA,
	})
	require.NoError(t, err)
	report := []byte(fmt.Sprintf(`{"schema_version":"agent-dod.v1","lifecycle_state":"DONE","identity":{"base_sha":%q,"head_sha":%q},"dispositions":{"business_acceptance":"NOT_REQUIRED"},"blockers":[]}`, baseSHA, headSHA))
	worktrees := &testingPolicyWorktreeFixture{bundle: bundle, lock: lock, report: report}
	gate := BundleTestingPolicyGate{
		Worktrees:            worktrees,
		ExpectedSourceCommit: sourceSHA,
		ExpectedSemanticsSHA: semanticsSHA,
		ExpectedBundleSHA256: bundleSHA,
	}
	outcome, err := gate.VerifyTask(context.Background(), domain.TaskWorkspace{}, "run-1", baseSHA, headSHA)
	require.NoError(t, err)
	require.Equal(t, "DONE", outcome.LifecycleState)
	require.Equal(t, report, outcome.ReportBytes)
	require.Len(t, outcome.Checks, 3)
	require.Equal(t, []string{
		"node20 .ai/testing/policy/policy-runner.cjs verify-lock --lock .ai/testing/policy/policy-lock.json",
		fmt.Sprintf("node20 .ai/testing/policy/policy-runner.cjs verify --repo . --command verify:pr --output-dir . --run-id run-1 --base %s --head %s", baseSHA, headSHA),
		fmt.Sprintf("node20 .ai/testing/policy/policy-runner.cjs agent-dod-from-run --repo . --aggregate test-results/run-1/verify/pr/test-result.v1.json --base %s --head %s --output test-results/run-1/agent-dod.v1.json", baseSHA, headSHA),
	}, worktrees.commands)
}

func TestBundleTestingPolicyGateRejectsMissingInvalidAndImpossibleBusinessAcceptance(t *testing.T) {
	bundle := []byte("trusted runner bundle")
	digest := sha256.Sum256(bundle)
	bundleSHA := hex.EncodeToString(digest[:])
	sourceSHA, semanticsSHA := strings.Repeat("a", 40), strings.Repeat("b", 40)
	baseSHA, headSHA := strings.Repeat("d", 40), strings.Repeat("e", 40)
	lock, err := json.Marshal(testingPolicyLock{
		LockSchemaVersion: "testing-policy-bundle.v2",
		PolicyVersion:     "testing-policy.v1",
		MatrixSchema:      "required-test-matrix.v1",
		SourceRepository:  "bemulima/learning-platform-verification",
		SourceCommit:      sourceSHA,
		SemanticsSHA:      semanticsSHA,
		BundleSHA256:      bundleSHA,
	})
	require.NoError(t, err)
	gate := BundleTestingPolicyGate{
		ExpectedSourceCommit: sourceSHA,
		ExpectedSemanticsSHA: semanticsSHA,
		ExpectedBundleSHA256: bundleSHA,
	}
	for _, report := range []string{
		fmt.Sprintf(`{"schema_version":"agent-dod.v1","lifecycle_state":"DONE","identity":{"base_sha":%q,"head_sha":%q},"dispositions":{},"blockers":[]}`, baseSHA, headSHA),
		fmt.Sprintf(`{"schema_version":"agent-dod.v1","lifecycle_state":"DONE","identity":{"base_sha":%q,"head_sha":%q},"dispositions":{"business_acceptance":"UNKNOWN"},"blockers":[]}`, baseSHA, headSHA),
		fmt.Sprintf(`{"schema_version":"agent-dod.v1","lifecycle_state":"DONE","identity":{"base_sha":%q,"head_sha":%q},"dispositions":{"business_acceptance":"PENDING"},"blockers":[]}`, baseSHA, headSHA),
		fmt.Sprintf(`{"schema_version":"agent-dod.v1","lifecycle_state":"BUSINESS_ACCEPTANCE_PENDING","identity":{"base_sha":%q,"head_sha":%q},"dispositions":{"business_acceptance":"NOT_REQUIRED"},"blockers":[]}`, baseSHA, headSHA),
		fmt.Sprintf(`{"schema_version":"agent-dod.v1","lifecycle_state":"BUSINESS_ACCEPTANCE_PENDING","identity":{"base_sha":%q,"head_sha":%q},"dispositions":{"business_acceptance":"PENDING"},"blockers":[]}`, baseSHA, headSHA),
	} {
		worktrees := &testingPolicyWorktreeFixture{bundle: bundle, lock: lock, report: []byte(report)}
		gate.Worktrees = worktrees
		_, err := gate.VerifyTask(context.Background(), domain.TaskWorkspace{}, "run-1", baseSHA, headSHA)
		require.Error(t, err)
	}
}

func TestValidateTestingPolicyDodReportAcceptsConsistentBusinessAcceptancePending(t *testing.T) {
	baseSHA, headSHA := strings.Repeat("d", 40), strings.Repeat("e", 40)
	bytes := []byte(fmt.Sprintf(`{"schema_version":"agent-dod.v1","lifecycle_state":"BUSINESS_ACCEPTANCE_PENDING","identity":{"base_sha":%q,"head_sha":%q},"dispositions":{"business_acceptance":"PENDING"},"blockers":[{"code":"DOD_BUSINESS_ACCEPTANCE_PENDING","severity":"PENDING","subject":"business_acceptance","message":"evidence pending"}]}`, baseSHA, headSHA))
	var report testingPolicyDodReport
	require.NoError(t, json.Unmarshal(bytes, &report))
	require.NoError(t, validateTestingPolicyDodReport(report, baseSHA, headSHA))
}

func TestBundleTestingPolicyGateRejectsDoneWithMismatchedIdentityOrBlockers(t *testing.T) {
	bundle := []byte("trusted runner bundle")
	digest := sha256.Sum256(bundle)
	bundleSHA := hex.EncodeToString(digest[:])
	sourceSHA, semanticsSHA := strings.Repeat("a", 40), strings.Repeat("b", 40)
	baseSHA, headSHA := strings.Repeat("d", 40), strings.Repeat("e", 40)
	lock, err := json.Marshal(testingPolicyLock{
		LockSchemaVersion: "testing-policy-bundle.v2",
		PolicyVersion:     "testing-policy.v1",
		MatrixSchema:      "required-test-matrix.v1",
		SourceRepository:  "bemulima/learning-platform-verification",
		SourceCommit:      sourceSHA,
		SemanticsSHA:      semanticsSHA,
		BundleSHA256:      bundleSHA,
	})
	require.NoError(t, err)
	gate := BundleTestingPolicyGate{
		Worktrees:            nil,
		ExpectedSourceCommit: sourceSHA,
		ExpectedSemanticsSHA: semanticsSHA,
		ExpectedBundleSHA256: bundleSHA,
	}
	for _, test := range []struct {
		report  string
		wantErr bool
	}{
		{report: fmt.Sprintf(`{"schema_version":"agent-dod.v1","lifecycle_state":"DONE","identity":{"base_sha":%q,"head_sha":%q},"dispositions":{"business_acceptance":"NOT_REQUIRED"},"blockers":[{"code":"pending","severity":"PENDING"}]}`, baseSHA, headSHA)},
		{report: fmt.Sprintf(`{"schema_version":"agent-dod.v1","lifecycle_state":"DONE","identity":{"base_sha":%q,"head_sha":%q},"dispositions":{"business_acceptance":"NOT_REQUIRED"},"blockers":[]}`, strings.Repeat("f", 40), headSHA), wantErr: true},
	} {
		worktrees := &testingPolicyWorktreeFixture{bundle: bundle, lock: lock, report: []byte(test.report)}
		gate.Worktrees = worktrees
		outcome, err := gate.VerifyTask(context.Background(), domain.TaskWorkspace{}, "run-1", baseSHA, headSHA)
		if test.wantErr {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		require.NotEqual(t, "DONE", outcome.LifecycleState)
	}
}

type testingPolicyWorktreeFixture struct {
	bundle   []byte
	lock     []byte
	report   []byte
	commands []string
}

func (*testingPolicyWorktreeFixture) Prepare(context.Context, domain.Project, domain.Task) (domain.TaskWorkspace, error) {
	panic("not used")
}

func (*testingPolicyWorktreeFixture) Inspect(context.Context, domain.Project, domain.TaskWorkspace) (domain.WorkspaceState, error) {
	panic("not used")
}

func (f *testingPolicyWorktreeFixture) RunCheck(_ context.Context, _ domain.TaskWorkspace, command string) (domain.WorkspaceCheckResult, error) {
	f.commands = append(f.commands, command)
	return domain.WorkspaceCheckResult{Command: command, ExitCode: 0}, nil
}

func (f *testingPolicyWorktreeFixture) ReadArtifact(_ context.Context, _ domain.TaskWorkspace, path string, _ int64) ([]byte, error) {
	switch path {
	case ".ai/testing/policy/policy-lock.json":
		return f.lock, nil
	case ".ai/testing/policy/policy-runner.cjs":
		return f.bundle, nil
	case "test-results/run-1/agent-dod.v1.json":
		return f.report, nil
	default:
		return nil, fmt.Errorf("unexpected artifact path %q", path)
	}
}

func (*testingPolicyWorktreeFixture) Commit(context.Context, domain.Project, domain.Task, domain.TaskWorkspace, []string) (string, error) {
	panic("not used")
}
