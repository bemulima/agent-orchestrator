package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

const (
	// These pins are refreshed only with the reviewed central Testing Policy bundle rollout.
	TestingPolicySourceCommit = "1af1b514bb50d02602e8e2e5243ff2e5e95caee4"
	TestingPolicySemanticsSHA = "3a374a116118d07311dab69513fdb271b82ec7a91e07d966c3515a80ff408241"
	TestingPolicyBundleSHA256 = "0b7cc6f3ab02d090d55e663c65b3cf998d4d37e45fdc0720e1d47deffdcd8b20"
)

var taskRunIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

const (
	businessAcceptanceEvidencePath = "test-results/business-acceptance-evidence.v1.json"
	businessAcceptanceEvidenceType = "business_acceptance_evidence"
	businessAcceptanceEvidenceName = "business-acceptance-evidence.v1"
)

type TestingPolicyGate interface {
	VerifyTask(context.Context, domain.TaskWorkspace, string, string, string) (TestingPolicyOutcome, error)
}

type TestingPolicyOutcome struct {
	LifecycleState                 string
	ReportPath                     string
	ReportBytes                    []byte
	BusinessAcceptanceEvidencePath string
	Checks                         []domain.VerificationCheck
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
	Dispositions *struct {
		BusinessAcceptance *string `json:"business_acceptance"`
	} `json:"dispositions"`
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
	if !fullSHA(expectedSourceCommit) || !fullDigest(expectedSemanticsSHA) || !fullDigest(expectedBundleSHA) ||
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

	_, evidenceErr := g.Worktrees.ReadArtifact(ctx, workspace, businessAcceptanceEvidencePath, maxArtifactBytes)
	hasBusinessAcceptanceEvidence := evidenceErr == nil
	if evidenceErr != nil && !errors.Is(evidenceErr, os.ErrNotExist) {
		return outcome, fmt.Errorf("business acceptance evidence artifact is unsafe or unavailable: %w", domain.ErrValidation)
	}
	if hasBusinessAcceptanceEvidence {
		outcome.BusinessAcceptanceEvidencePath = businessAcceptanceEvidencePath
	}

	aggregatePath := fmt.Sprintf("test-results/%s/verify/pr/test-result.v1.json", runID)
	outcome.ReportPath = fmt.Sprintf("test-results/%s/agent-dod.v1.json", runID)
	dodCommand := fmt.Sprintf("node20 .ai/testing/policy/policy-runner.cjs agent-dod-from-run --repo . --aggregate %s --base %s --head %s", aggregatePath, baseSHA, headSHA)
	if hasBusinessAcceptanceEvidence {
		dodCommand += " --business-acceptance-evidence " + businessAcceptanceEvidencePath
	}
	dodCommand += " --output " + outcome.ReportPath
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
	if err := json.Unmarshal(bytes, &report); err != nil || validateTestingPolicyDodReport(report, baseSHA, headSHA) != nil {
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

func validateTestingPolicyDodReport(report testingPolicyDodReport, baseSHA, headSHA string) error {
	if report.SchemaVersion != "agent-dod.v1" || report.Identity == nil ||
		report.Identity.BaseSHA != baseSHA || report.Identity.HeadSHA != headSHA {
		return fmt.Errorf("report schema or identity is invalid")
	}
	switch report.LifecycleState {
	case "IMPLEMENTATION_IN_PROGRESS", "IMPLEMENTATION_COMPLETE", "VERIFICATION_PENDING", "DONE", "BLOCKED", "BUSINESS_ACCEPTANCE_PENDING":
	default:
		return fmt.Errorf("report lifecycle state is invalid")
	}
	if report.Dispositions == nil || report.Dispositions.BusinessAcceptance == nil {
		return fmt.Errorf("business acceptance disposition is missing")
	}
	acceptance := *report.Dispositions.BusinessAcceptance
	switch acceptance {
	case "NOT_REQUIRED", "EVIDENCE_PRESENT", "PENDING":
	default:
		return fmt.Errorf("business acceptance disposition is invalid")
	}

	hasAcceptancePendingBlocker := false
	hasOtherPendingBlocker := false
	hasBlockingBlocker := false
	for _, blocker := range report.Blockers {
		if blocker.Code == "DOD_BUSINESS_ACCEPTANCE_PENDING" || blocker.Code == "DOD_BUSINESS_ACCEPTANCE_INVALID" {
			hasAcceptancePendingBlocker = true
			if blocker.Severity != "PENDING" {
				return fmt.Errorf("business acceptance blocker has an invalid severity")
			}
		}
		if blocker.Severity == "PENDING" && blocker.Code != "DOD_BUSINESS_ACCEPTANCE_PENDING" && blocker.Code != "DOD_BUSINESS_ACCEPTANCE_INVALID" {
			hasOtherPendingBlocker = true
		}
		if blocker.Severity == "BLOCKING" {
			hasBlockingBlocker = true
		}
	}
	if hasAcceptancePendingBlocker && acceptance != "PENDING" {
		return fmt.Errorf("business acceptance blocker conflicts with its disposition")
	}
	if report.LifecycleState == "DONE" && acceptance == "PENDING" {
		return fmt.Errorf("DONE conflicts with pending business acceptance")
	}
	if report.LifecycleState == "BUSINESS_ACCEPTANCE_PENDING" &&
		(acceptance != "PENDING" || !hasAcceptancePendingBlocker || hasOtherPendingBlocker || hasBlockingBlocker) {
		return fmt.Errorf("business acceptance lifecycle conflicts with its disposition or blockers")
	}
	return nil
}

func fullSHA(value string) bool { return regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(value) }

func fullDigest(value string) bool { return regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(value) }

func intPointer(value int) *int { return &value }
