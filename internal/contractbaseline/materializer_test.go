package contractbaseline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestMaterializationIsDeterministicAndIdempotent(t *testing.T) {
	root := newGoFixture(t)
	profile := goContractProfile()
	materializer := FileMaterializer{}
	first, err := materializer.Materialize(context.Background(), root, "project", "fixture", profile,
		[]domain.PlannedContractBoundary{boundary("application-command-result")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := materializer.Materialize(context.Background(), root, "project", "fixture", profile,
		[]domain.PlannedContractBoundary{boundary("application-command-result")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Files) != 1 || len(second.Files) != 1 || first.Files[0] != second.Files[0] ||
		first.Contracts[0] != second.Contracts[0] {
		t.Fatalf("materialization changed across retries: first=%#v second=%#v", first, second)
	}
	if first.Files[0].Generated != true || first.Files[0].SHA256 == "" {
		t.Fatalf("generated artifact is not content addressed: %#v", first.Files[0])
	}
	if first.Files[0].Path != "internal/usecase/application_command_result.go" ||
		first.Contracts[0].RouteID != "backend.usecase" || strings.Contains(first.Files[0].Path, "/contracts/") {
		t.Fatalf("contract did not use its declared owner surface: files=%#v references=%#v", first.Files, first.Contracts)
	}
}

func TestMaterializerRejectsOwnerWithoutDeclaredContractSurface(t *testing.T) {
	root := newGoFixture(t)
	profile := goContractProfile()
	profile.Routes[0].ContractSurface = nil
	_, err := (FileMaterializer{}).Materialize(context.Background(), root, "project", "fixture", profile,
		[]domain.PlannedContractBoundary{boundary("application-command-result")}, nil)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("owner without a contract surface error = %v, want validation error", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "internal", "contracts")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("materializer created a generic contracts layer: %v", statErr)
	}
}

func TestPlannedContractReferenceBindsOwnerPath(t *testing.T) {
	profile := goContractProfile()
	approved := boundary("application-command-result")
	references, err := PlannedContractReferences("project", profile, []domain.PlannedContractBoundary{approved}, nil)
	if err != nil || len(references) != 1 || references[0].Path != approved.TargetPath {
		t.Fatalf("owner path was not resolved from the profile: refs=%#v err=%v", references, err)
	}
	approved.TargetPath = "internal/contracts/application_command_result.go"
	if _, err := PlannedContractReferences("project", profile, []domain.PlannedContractBoundary{approved}, nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("unapproved generic owner path was accepted: %v", err)
	}
}

func TestMaterializerRejectsPathTraversal(t *testing.T) {
	root := newGoFixture(t)
	profile := goContractProfile()
	profile.Routes[0].ContractSurface.Directory = "../outside"
	if _, err := (FileMaterializer{}).Materialize(context.Background(), root, "project", "fixture", profile,
		[]domain.PlannedContractBoundary{boundary("application-command-result")}, nil); err == nil {
		t.Fatal("path traversal was accepted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "outside")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path traversal created an outside artifact: %v", err)
	}
}

func TestProtectedContractPathCollisionBlocksMaterialization(t *testing.T) {
	root := newGoFixture(t)
	path := filepath.Join(root, "internal", "usecase", "application_command_result.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package usecase\n// owner file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := (FileMaterializer{}).Materialize(context.Background(), root, "project", "fixture", goContractProfile(),
		[]domain.PlannedContractBoundary{boundary("application-command-result")}, nil)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("collision error = %v, want conflict", err)
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil || string(content) != "package usecase\n// owner file\n" {
		t.Fatalf("protected file was changed: err=%v content=%q", readErr, content)
	}
}

func TestVerifyBaselineInvalidatesProfileAndSourceDrift(t *testing.T) {
	tests := []struct {
		name           string
		mutateProfile  bool
		mutateSource   bool
		mutateRevision bool
	}{
		{name: "profile mismatch", mutateProfile: true},
		{name: "persisted file hash drift", mutateSource: true},
		{name: "base revision drift", mutateRevision: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, profile, plan, baseline := committedBaselineFixture(t)
			profileFingerprint, revision := "profile-fingerprint", "revision-1"
			if test.mutateProfile {
				profileFingerprint = "changed-profile"
			}
			if test.mutateRevision {
				revision = "revision-2"
			}
			if test.mutateSource {
				baseline.Files[0].SHA256 = strings.Repeat("0", 64)
			}
			verified := VerifyBaseline(context.Background(), FileMaterializer{}, root, baseline, baseline.ApprovedPlanFingerprint, plan,
				profile, profileFingerprint, revision, time.Now().Add(time.Second))
			if verified.ExecutionState != domain.ContractBaselineInvalidated || verified.Validation.Passed || len(verified.Validation.Reasons) == 0 {
				t.Fatalf("drift did not invalidate baseline: %#v", verified)
			}
		})
	}
}

func TestFrozenContractBaselineRequiresIndependentReview(t *testing.T) {
	root, profile, plan, baseline := committedBaselineFixture(t)
	baseline.IndependentReviewPassed = false
	baseline.IndependentReviewerID = ""
	baseline.IndependentReviewStatus = ""
	baseline.IndependentReviewSHA256 = ""
	verified := VerifyBaseline(context.Background(), FileMaterializer{}, root, baseline, baseline.ApprovedPlanFingerprint, plan,
		profile, baseline.ProfileFingerprint, baseline.RepositoryRevision, time.Now().UTC())
	if verified.ExecutionState != domain.ContractBaselineInvalidated || verified.Validation.Passed {
		t.Fatalf("baseline without independent review remained frozen: %#v", verified)
	}
}

func committedBaselineFixture(t *testing.T) (string, agentcontrol.Profile, domain.ContractPlan, domain.ContractBaseline) {
	t.Helper()
	root, sourceRevision := initContractGit(t, map[string]string{"go.mod": "module example.test/service\n\ngo 1.24\n"})
	profile := goContractProfile()
	plan := domain.ContractPlan{State: domain.ContractPlanFreezeNeeded, Required: true, FreezeRequired: true,
		Boundaries: []domain.PlannedContractBoundary{boundary("application-command-result")}}
	contractFingerprint, err := PlanFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}
	materialized, err := (FileMaterializer{}).Materialize(context.Background(), root, "project", "fixture", profile, plan.Boundaries, nil)
	if err != nil {
		t.Fatal(err)
	}
	contractPath := filepath.Join(root, filepath.FromSlash(materialized.Files[0].Path))
	content := []byte("package usecase\ntype ApplicationCommandResult interface { Execute() error }\n")
	if err := os.WriteFile(contractPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, root, "add", "--", materialized.Files[0].Path)
	gitFixture(t, root, "commit", "-m", "feat(ai): contract baseline")
	commit := strings.TrimSpace(gitFixture(t, root, "rev-parse", "HEAD"))
	materialized.Files[0].SHA256 = contentHash(content)
	now := time.Now().UTC()
	baseline := domain.ContractBaseline{
		ID: BaselineID("plan", "task", "project"), PlanID: "plan", TaskID: "task",
		RepositoryProjectID: "project", ProfileID: profile.ID,
		PlanningContractState: domain.ContractPlanFreezeNeeded, ExecutionState: domain.ContractBaselineFrozen,
		ApprovedPlanFingerprint: "approved", MaterializedContracts: materialized.Contracts, Files: materialized.Files,
		AggregateSHA256: AggregateFilesSHA256(materialized.Files), ContractBaselineCommit: commit,
		ContractAgentPassed: true, ContractAgentThreadID: "agent", ContractAgentResultSHA256: strings.Repeat("a", 64),
		MechanicalReviewPassed: true, MechanicalReviewSHA256: strings.Repeat("b", 64),
		ContractVerificationPassed: true, ContractVerification: "go test ./... passed",
		IndependentReviewPassed: true, IndependentReviewerID: "reviewer", IndependentReviewStatus: "PASS",
		IndependentReviewSHA256: strings.Repeat("c", 64),
		ContractPlanFingerprint: contractFingerprint, ProfileFingerprint: "profile-fingerprint",
		RepositoryRevision: sourceRevision, Validation: domain.ContractBaselineValidation{Passed: true},
		CreatedAt: now, UpdatedAt: now,
	}
	return root, profile, plan, baseline
}

func newGoFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/service\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func goContractProfile() agentcontrol.Profile {
	return agentcontrol.Profile{ID: "go.canonical", ContractLocation: agentcontrol.ContractLocation{
		Language: "go", Extension: ".go", StandardLibraryImports: "allow", ThirdPartyImportPolicy: "project_dependencies",
	}, Routes: []agentcontrol.ProfileRoute{{ID: "backend.usecase", Targets: []string{"internal/usecase/**"},
		ContractSurface:     &agentcontrol.ContractSurface{Directory: "internal/usecase", PackageName: "usecase"},
		AllowedDependencies: []string{"backend.domain"}}, {ID: "backend.domain", Targets: []string{"internal/domain/**"}}}}
}

func boundary(kind string) domain.PlannedContractBoundary {
	return domain.PlannedContractBoundary{
		Kind: kind, TargetPath: "internal/usecase/" + strings.ReplaceAll(kind, "-", "_") + ".go",
		Owner:     domain.RouteReference{ProjectID: "project", RouteID: "backend.usecase"},
		Consumers: []domain.RouteReference{{ProjectID: "project", RouteID: "backend.transport.http"}},
		Rationale: "approved fixture boundary",
	}
}
