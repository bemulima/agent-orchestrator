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

	require.Equal(t, "51c441d95aea289f8050f85b35f1171f04e7ceb0", TestingPolicySourceCommit)
	require.Equal(t, "3a374a116118d07311dab69513fdb271b82ec7a91e07d966c3515a80ff408241", TestingPolicySemanticsSHA)
	require.Equal(t, "c172e13e9640f69b3a44aaf2912f993ca1b5aba5407067e187c125c423c7d0d9", TestingPolicyBundleSHA256)
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
		ExpectedSemanticsSHA: strings.Repeat("b", 64),
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
	semanticsSHA := strings.Repeat("b", 64)
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
	require.Empty(t, outcome.BusinessAcceptanceEvidencePath)
}

func TestBundleTestingPolicyGateForwardsFixedStructuredBusinessAcceptanceEvidencePath(t *testing.T) {
	bundle := []byte("trusted runner bundle")
	digest := sha256.Sum256(bundle)
	bundleSHA := hex.EncodeToString(digest[:])
	sourceSHA, semanticsSHA := strings.Repeat("a", 40), strings.Repeat("b", 64)
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
	report := []byte(fmt.Sprintf(`{"schema_version":"agent-dod.v1","lifecycle_state":"DONE","identity":{"base_sha":%q,"head_sha":%q},"dispositions":{"business_acceptance":"EVIDENCE_PRESENT"},"blockers":[]}`, baseSHA, headSHA))
	worktrees := &testingPolicyWorktreeFixture{
		bundle: bundle, lock: lock, report: report,
		businessAcceptanceEvidence: []byte(`{"schema_version":"business-acceptance-evidence.v1"}`),
	}
	gate := BundleTestingPolicyGate{
		Worktrees:            worktrees,
		ExpectedSourceCommit: sourceSHA,
		ExpectedSemanticsSHA: semanticsSHA,
		ExpectedBundleSHA256: bundleSHA,
	}
	outcome, err := gate.VerifyTask(context.Background(), domain.TaskWorkspace{}, "run-1", baseSHA, headSHA)
	require.NoError(t, err)
	require.Equal(t, businessAcceptanceEvidencePath, outcome.BusinessAcceptanceEvidencePath)
	require.Equal(t, fmt.Sprintf(
		"node20 .ai/testing/policy/policy-runner.cjs agent-dod-from-run --repo . --aggregate test-results/run-1/verify/pr/test-result.v1.json --base %s --head %s --business-acceptance-evidence %s --output test-results/run-1/agent-dod.v1.json",
		baseSHA, headSHA, businessAcceptanceEvidencePath,
	), worktrees.commands[2])
}

func TestBundleTestingPolicyGateFailsClosedWhenBusinessAcceptanceEvidencePathIsUnsafe(t *testing.T) {
	bundle := []byte("trusted runner bundle")
	digest := sha256.Sum256(bundle)
	bundleSHA := hex.EncodeToString(digest[:])
	sourceSHA, semanticsSHA := strings.Repeat("a", 40), strings.Repeat("b", 64)
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
	worktrees := &testingPolicyWorktreeFixture{
		bundle: bundle, lock: lock,
		report:                        []byte(fmt.Sprintf(`{"schema_version":"agent-dod.v1","lifecycle_state":"BLOCKED","identity":{"base_sha":%q,"head_sha":%q},"dispositions":{"business_acceptance":"PENDING"},"blockers":[{"code":"DOD_BUSINESS_ACCEPTANCE_INVALID","severity":"PENDING","subject":"business_acceptance","message":"unsafe artifact path"}]}`, baseSHA, headSHA)),
		businessAcceptanceEvidenceErr: fmt.Errorf("evidence resolves outside worktree: %w", domain.ErrForbidden),
	}
	gate := BundleTestingPolicyGate{
		Worktrees:            worktrees,
		ExpectedSourceCommit: sourceSHA,
		ExpectedSemanticsSHA: semanticsSHA,
		ExpectedBundleSHA256: bundleSHA,
	}
	_, err = gate.VerifyTask(context.Background(), domain.TaskWorkspace{}, "run-1", baseSHA, headSHA)
	require.ErrorIs(t, err, domain.ErrValidation)
	require.NotContains(t, strings.Join(worktrees.commands, "\n"), "agent-dod-from-run")
}

func TestBundleTestingPolicyGateRejectsMissingInvalidAndImpossibleBusinessAcceptance(t *testing.T) {
	bundle := []byte("trusted runner bundle")
	digest := sha256.Sum256(bundle)
	bundleSHA := hex.EncodeToString(digest[:])
	sourceSHA, semanticsSHA := strings.Repeat("a", 40), strings.Repeat("b", 64)
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
	sourceSHA, semanticsSHA := strings.Repeat("a", 40), strings.Repeat("b", 64)
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
	bundle                        []byte
	lock                          []byte
	report                        []byte
	businessAcceptanceEvidence    []byte
	businessAcceptanceEvidenceErr error
	commands                      []string
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
	case businessAcceptanceEvidencePath:
		if f.businessAcceptanceEvidenceErr != nil {
			return nil, f.businessAcceptanceEvidenceErr
		}
		if len(f.businessAcceptanceEvidence) == 0 {
			return nil, os.ErrNotExist
		}
		return f.businessAcceptanceEvidence, nil
	default:
		return nil, fmt.Errorf("unexpected artifact path %q", path)
	}
}

func (*testingPolicyWorktreeFixture) Commit(context.Context, domain.Project, domain.Task, domain.TaskWorkspace, []string) (string, error) {
	panic("not used")
}

func TestValidateTestingPolicyDodReportInvalidAcceptanceStaysPendingWithoutWeakeningBlockers(t *testing.T) {
	baseSHA, headSHA := strings.Repeat("d", 40), strings.Repeat("e", 40)
	for _, test := range []struct {
		code, severity, otherCode, otherSeverity, disposition string
		wantError                                             bool
	}{
		{"DOD_BUSINESS_ACCEPTANCE_INVALID", "PENDING", "", "", "PENDING", false},
		{"DOD_BUSINESS_ACCEPTANCE_INVALID", "BLOCKING", "", "", "PENDING", true},
		{"DOD_BUSINESS_ACCEPTANCE_INVALID", "PENDING", "", "", "EVIDENCE_PRESENT", true},
		{"DOD_BUSINESS_ACCEPTANCE_INVALID", "PENDING", "DOD_COMMAND_RESULT_MISSING", "PENDING", "PENDING", true},
		{"DOD_BUSINESS_ACCEPTANCE_INVALID", "PENDING", "DOD_IDENTITY_MISMATCH", "BLOCKING", "PENDING", true},
	} {
		raw := fmt.Sprintf(`{"schema_version":"agent-dod.v1","lifecycle_state":"BUSINESS_ACCEPTANCE_PENDING","identity":{"base_sha":%q,"head_sha":%q},"dispositions":{"business_acceptance":%q},"blockers":[{"code":%q,"severity":%q}]}`, baseSHA, headSHA, test.disposition, test.code, test.severity)
		var report testingPolicyDodReport
		require.NoError(t, json.Unmarshal([]byte(raw), &report))
		if test.otherCode != "" {
			report.Blockers = append(report.Blockers, struct {
				Code     string `json:"code"`
				Severity string `json:"severity"`
				Subject  string `json:"subject"`
				Message  string `json:"message"`
			}{Code: test.otherCode, Severity: test.otherSeverity})
		}
		err := validateTestingPolicyDodReport(report, baseSHA, headSHA)
		if test.wantError {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
	}
}

func TestBundleTestingPolicyGateReadsReviewedBundleAboveGenericArtifactLimit(t *testing.T) {
	bundle := make([]byte, (10<<20)+1)
	digest := sha256.Sum256(bundle)
	bundleSHA := hex.EncodeToString(digest[:])
	sourceSHA, semanticsSHA := strings.Repeat("a", 40), strings.Repeat("b", 64)
	baseSHA, headSHA := strings.Repeat("d", 40), strings.Repeat("e", 40)
	lock, err := json.Marshal(testingPolicyLock{LockSchemaVersion: "testing-policy-bundle.v2", PolicyVersion: "testing-policy.v1", MatrixSchema: "required-test-matrix.v1", SourceRepository: "bemulima/learning-platform-verification", SourceCommit: sourceSHA, SemanticsSHA: semanticsSHA, BundleSHA256: bundleSHA})
	require.NoError(t, err)
	report := []byte(fmt.Sprintf(`{"schema_version":"agent-dod.v1","lifecycle_state":"DONE","identity":{"base_sha":%q,"head_sha":%q},"dispositions":{"business_acceptance":"NOT_REQUIRED"},"blockers":[]}`, baseSHA, headSHA))
	worktrees := &boundedPolicyBundleFixture{testingPolicyWorktreeFixture: testingPolicyWorktreeFixture{bundle: bundle, lock: lock, report: report}}
	gate := BundleTestingPolicyGate{Worktrees: worktrees, ExpectedSourceCommit: sourceSHA, ExpectedSemanticsSHA: semanticsSHA, ExpectedBundleSHA256: bundleSHA}
	outcome, err := gate.VerifyTask(context.Background(), domain.TaskWorkspace{}, "run-1", baseSHA, headSHA)
	require.NoError(t, err)
	require.Equal(t, int64(32<<20), worktrees.bundleLimit)
	require.Equal(t, "DONE", outcome.LifecycleState)
	require.Len(t, worktrees.commands, 3)
}

type boundedPolicyBundleFixture struct {
	testingPolicyWorktreeFixture
	bundleLimit int64
}

func (f *boundedPolicyBundleFixture) ReadArtifact(ctx context.Context, workspace domain.TaskWorkspace, path string, limit int64) ([]byte, error) {
	if path == ".ai/testing/policy/policy-runner.cjs" {
		f.bundleLimit = limit
		if int64(len(f.bundle)) > limit {
			return nil, fmt.Errorf("reviewed policy bundle exceeds requested bound: %w", domain.ErrValidation)
		}
	}
	return f.testingPolicyWorktreeFixture.ReadArtifact(ctx, workspace, path, limit)
}
