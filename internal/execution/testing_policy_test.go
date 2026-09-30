package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

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
	report := []byte(fmt.Sprintf(`{"schema_version":"agent-dod.v1","lifecycle_state":"DONE","identity":{"base_sha":%q,"head_sha":%q},"blockers":[]}`, baseSHA, headSHA))
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
		"node .ai/testing/policy/policy-runner.cjs verify-lock --lock .ai/testing/policy/policy-lock.json",
		fmt.Sprintf("node .ai/testing/policy/policy-runner.cjs verify --repo . --command verify:pr --output-dir . --run-id run-1 --base %s --head %s", baseSHA, headSHA),
		fmt.Sprintf("node .ai/testing/policy/policy-runner.cjs agent-dod-from-run --repo . --aggregate test-results/run-1/verify/pr/test-result.v1.json --base %s --head %s --business-acceptance not-required --output test-results/run-1/agent-dod.v1.json", baseSHA, headSHA),
	}, worktrees.commands)
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
		{report: fmt.Sprintf(`{"schema_version":"agent-dod.v1","lifecycle_state":"DONE","identity":{"base_sha":%q,"head_sha":%q},"blockers":[{"code":"pending","severity":"PENDING"}]}`, baseSHA, headSHA)},
		{report: fmt.Sprintf(`{"schema_version":"agent-dod.v1","lifecycle_state":"DONE","identity":{"base_sha":%q,"head_sha":%q},"blockers":[]}`, strings.Repeat("f", 40), headSHA), wantErr: true},
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
