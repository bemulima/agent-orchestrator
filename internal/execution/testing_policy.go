package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

const (
	// These pins are refreshed only with the reviewed central Testing Policy bundle rollout.
	TestingPolicySourceCommit = "45df64b0204fd7b8bdd637b09330c57eb75b0b70"
	TestingPolicySemanticsSHA = "5a0422683ae97659d8f152df5a2b60d45d1893f9"
	TestingPolicyBundleSHA256 = "9ace903a18535e299a267be49480e48bf5d0a623dfbb95ac46a4928f4a831907"
)

var taskRunIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type TestingPolicyGate interface {
	VerifyTask(context.Context, domain.TaskWorkspace, string, string, string) (TestingPolicyOutcome, error)
}

type TestingPolicyOutcome struct {
	LifecycleState string
	ReportPath     string
	ReportBytes    []byte
	Checks         []domain.VerificationCheck
}

type BundleTestingPolicyGate struct {
	Worktrees            repository.TaskWorktree
	ExpectedSourceCommit string
	ExpectedSemanticsSHA string
	ExpectedBundleSHA256 string
}

type testingPolicyLock struct {
	LockSchemaVersion string `json:"lock_schema_version"`
	PolicyVersion     string `json:"policy_version"`
	MatrixSchema      string `json:"matrix_schema"`
	SourceRepository  string `json:"source_repository"`
	SourceCommit      string `json:"policy_source_commit"`
	SemanticsSHA      string `json:"policy_semantics_sha"`
	BundleSHA256      string `json:"bundle_sha256"`
}

type testingPolicyDodReport struct {
	SchemaVersion  string `json:"schema_version"`
	LifecycleState string `json:"lifecycle_state"`
	Identity       *struct {
		BaseSHA string `json:"base_sha"`
		HeadSHA string `json:"head_sha"`
	} `json:"identity"`
	Blockers []struct {
		Code     string `json:"code"`
		Severity string `json:"severity"`
		Subject  string `json:"subject"`
		Message  string `json:"message"`
	} `json:"blockers"`
}

func (g BundleTestingPolicyGate) VerifyTask(
	ctx context.Context,
	workspace domain.TaskWorkspace,
	runID, baseSHA, headSHA string,
) (TestingPolicyOutcome, error) {
	if g.Worktrees == nil || !taskRunIDPattern.MatchString(runID) || !fullSHA(baseSHA) || !fullSHA(headSHA) {
		return TestingPolicyOutcome{}, fmt.Errorf("testing policy task identity is invalid: %w", domain.ErrValidation)
	}
	expectedSourceCommit := g.ExpectedSourceCommit
	if expectedSourceCommit == "" {
		expectedSourceCommit = TestingPolicySourceCommit
	}
	expectedBundleSHA := g.ExpectedBundleSHA256
	if expectedBundleSHA == "" {
		expectedBundleSHA = TestingPolicyBundleSHA256
	}
	expectedSemanticsSHA := g.ExpectedSemanticsSHA
	if expectedSemanticsSHA == "" {
		expectedSemanticsSHA = TestingPolicySemanticsSHA
	}
	if !fullSHA(expectedSourceCommit) || !fullSHA(expectedSemanticsSHA) || !fullDigest(expectedBundleSHA) ||
		strings.HasPrefix(expectedSourceCommit, "REPLACE_") || strings.HasPrefix(expectedSemanticsSHA, "REPLACE_") || strings.HasPrefix(expectedBundleSHA, "REPLACE_") {
		return TestingPolicyOutcome{}, fmt.Errorf("trusted Testing Policy bundle pin is not configured: %w", domain.ErrInvalidStatus)
	}

	lockBytes, err := g.Worktrees.ReadArtifact(ctx, workspace, ".ai/testing/policy/policy-lock.json", 64<<10)
	if err != nil {
		return TestingPolicyOutcome{}, fmt.Errorf("Testing Policy lock is unavailable: %w", domain.ErrValidation)
	}
	bundleBytes, err := g.Worktrees.ReadArtifact(ctx, workspace, ".ai/testing/policy/policy-runner.cjs", 10<<20)
	if err != nil {
		return TestingPolicyOutcome{}, fmt.Errorf("Testing Policy bundle is unavailable: %w", domain.ErrValidation)
	}
	var lock testingPolicyLock
	if err := json.Unmarshal(lockBytes, &lock); err != nil {
		return TestingPolicyOutcome{}, fmt.Errorf("Testing Policy lock is malformed: %w", domain.ErrValidation)
	}
	digest := sha256.Sum256(bundleBytes)
	bundleSHA := hex.EncodeToString(digest[:])
	if lock.LockSchemaVersion != "testing-policy-bundle.v2" || lock.PolicyVersion != "testing-policy.v1" || lock.MatrixSchema != "required-test-matrix.v1" ||
		lock.SourceRepository != "bemulima/learning-platform-verification" || lock.SourceCommit != expectedSourceCommit ||
		lock.SemanticsSHA != expectedSemanticsSHA || lock.BundleSHA256 != expectedBundleSHA || bundleSHA != expectedBundleSHA {
		return TestingPolicyOutcome{}, fmt.Errorf("Testing Policy bundle does not match the orchestrator's reviewed pin: %w", domain.ErrConflict)
	}

	var outcome TestingPolicyOutcome
	verifyLock := "node20 .ai/testing/policy/policy-runner.cjs verify-lock --lock .ai/testing/policy/policy-lock.json"
	check, err := g.Worktrees.RunCheck(ctx, workspace, verifyLock)
	if err != nil || check.ExitCode != 0 {
		return TestingPolicyOutcome{}, fmt.Errorf("Testing Policy bundle preflight failed: %s: %w", check.Output, domain.ErrValidation)
	}
	outcome.Checks = append(outcome.Checks, domain.VerificationCheck{Name: "testing_policy:bundle", Status: "passed", Details: "reviewed policy bundle checksum and identity validated", ExitCode: intPointer(check.ExitCode)})

	verifyCommand := fmt.Sprintf("node20 .ai/testing/policy/policy-runner.cjs verify --repo . --command verify:pr --output-dir . --run-id %s --base %s --head %s", runID, baseSHA, headSHA)
	policyCheck, policyErr := g.Worktrees.RunCheck(ctx, workspace, verifyCommand)
	policyStatus := "passed"
	if policyErr != nil || policyCheck.ExitCode != 0 {
		policyStatus = "failed"
	}
	policyDetails := policyCheck.Output
	if policyErr != nil {
		policyDetails = policyErr.Error()
	}
	outcome.Checks = append(outcome.Checks, domain.VerificationCheck{Name: "testing_policy:verify:pr", Status: policyStatus, Details: boundedDetails(policyDetails), ExitCode: intPointer(policyCheck.ExitCode)})

	aggregatePath := fmt.Sprintf("test-results/%s/verify/pr/test-result.v1.json", runID)
	outcome.ReportPath = fmt.Sprintf("test-results/%s/agent-dod.v1.json", runID)
	dodCommand := fmt.Sprintf("node20 .ai/testing/policy/policy-runner.cjs agent-dod-from-run --repo . --aggregate %s --base %s --head %s --business-acceptance not-required --output %s", aggregatePath, baseSHA, headSHA, outcome.ReportPath)
	dodCheck, dodErr := g.Worktrees.RunCheck(ctx, workspace, dodCommand)
	if dodErr != nil {
		return outcome, fmt.Errorf("agent Definition of Done evaluation could not run: %w", dodErr)
	}
	if dodCheck.ExitCode != 0 && dodCheck.ExitCode != 2 {
		return outcome, fmt.Errorf("agent Definition of Done evaluation failed: %s: %w", dodCheck.Output, domain.ErrValidation)
	}
	bytes, err := g.Worktrees.ReadArtifact(ctx, workspace, outcome.ReportPath, maxArtifactBytes)
	if err != nil {
		return outcome, fmt.Errorf("agent Definition of Done report is unavailable: %w", domain.ErrValidation)
	}
	var report testingPolicyDodReport
	if err := json.Unmarshal(bytes, &report); err != nil || report.SchemaVersion != "agent-dod.v1" || report.LifecycleState == "" ||
		report.Identity == nil || report.Identity.BaseSHA != baseSHA || report.Identity.HeadSHA != headSHA {
		return outcome, fmt.Errorf("agent Definition of Done report is invalid: %w", domain.ErrValidation)
	}
	outcome.ReportBytes = bytes
	outcome.LifecycleState = report.LifecycleState
	if report.LifecycleState == "DONE" && len(report.Blockers) == 0 && dodCheck.ExitCode == 0 && policyStatus == "passed" {
		outcome.Checks = append(outcome.Checks, domain.VerificationCheck{Name: "agent_definition_of_done", Status: "passed", Details: "agent-dod.v1 reports DONE with exact matrix and result evidence", ExitCode: intPointer(0)})
		return outcome, nil
	}
	if outcome.LifecycleState == "DONE" {
		outcome.LifecycleState = "BLOCKED"
	}
	details := report.LifecycleState
	for _, blocker := range report.Blockers {
		details += "; " + blocker.Code + ": " + blocker.Message
	}
	outcome.Checks = append(outcome.Checks, domain.VerificationCheck{Name: "agent_definition_of_done", Status: "failed", Details: boundedDetails(details), ExitCode: intPointer(dodCheck.ExitCode)})
	return outcome, nil
}

func fullSHA(value string) bool { return regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(value) }

func fullDigest(value string) bool { return regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(value) }

func intPointer(value int) *int { return &value }
