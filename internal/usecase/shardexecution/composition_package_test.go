package shardexecution

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func compositionFixture(t *testing.T) (string, domain.ArchitecturalShard, domain.ContractBaseline, domain.ShardFanoutExecution) {
	t.Helper()
	root := t.TempDir()
	sources := map[string]string{
		"internal/domain/port.go":                             "package domain; type RepositoryPort interface { Find() error }",
		"internal/usecase/contract.go":                        "package usecase; type ApplicationCommandResult interface { Get() error }",
		"internal/usecase/availability.go":                    "package usecase; func NewAvailabilityUsecase(repository domain.RepositoryPort) *AvailabilityUsecase { return nil }",
		"internal/transport/http/availability.go":             "package http; func NewAvailabilityHandler(application usecase.ApplicationCommandResult) *AvailabilityHandler { return nil }",
		"internal/infrastructure/persistence/availability.go": "package persistence; func NewAvailabilityStore(pool *pgxpool.Pool) *AvailabilityStore { return nil }",
	}
	for path, source := range sources {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	base := domain.ShardExecutionBase{Kind: domain.ShardBaseContractBaseline, Revision: "base", ContractBaselineID: "baseline"}
	baseline := domain.ContractBaseline{ID: "baseline", PlanID: "plan", TaskID: "task", RepositoryProjectID: "project", ProfileID: "go", ProfileFingerprint: "profile", ExecutionState: domain.ContractBaselineFrozen, ApprovedPlanFingerprint: "owner-fingerprint", ContractBaselineCommit: "base", Validation: domain.ContractBaselineValidation{Passed: true}, Files: []domain.ContractBaselineFile{{Path: "internal/domain/port.go"}, {Path: "internal/usecase/contract.go"}}}
	shard := domain.ArchitecturalShard{ID: "composition", PlanID: "plan", TaskID: "task", RepositoryProjectID: "project", Repository: "availability", ProfileID: "go", ProfileFingerprint: "profile", RouteID: "backend.composition", ExecutionBase: base, Status: domain.ShardStatusComposition, Phase: "after_workers", WriteScope: domain.ShardWriteScope{Allow: []string{"cmd/availability-service/main.go", "cmd/availability-service/main_test.go"}, TestPaths: []string{"cmd/availability-service/main_test.go"}, MaxFiles: 2}}
	execution := domain.ShardFanoutExecution{PlanID: "plan", ContractBaselineID: "baseline", BaselineCommit: "base", Barrier: domain.ShardBarrierReady, AssemblyCommit: "assembled", AssemblyWorkspace: domain.TaskWorkspace{Path: root}}
	for route, path := range map[string]string{"backend.usecase": "internal/usecase/availability.go", "backend.transport.http": "internal/transport/http/availability.go", "backend.infrastructure.persistence": "internal/infrastructure/persistence/availability.go"} {
		execution.Attempts = append(execution.Attempts, domain.ShardAttempt{ShardID: route, Status: domain.ShardAttemptVerified, BaselineCommit: "base", CommitSHA: "commit-" + route, ChangedFiles: []string{path}, Green: &domain.ShardGreenEvidence{Passed: true}, WorkPackage: domain.WorkPackage{Route: route, ProjectID: "project", ExecutionBase: base, WriteScope: domain.ShardWriteScope{Allow: []string{path}}}})
	}
	return root, shard, baseline, execution
}

func TestCompositionPackageDerivedBoundedAndImmutable(t *testing.T) {
	root, shard, baseline, execution := compositionFixture(t)
	first, err := BuildCompositionWorkPackage(root, shard, baseline, execution)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.APIs) != 5 || len(first.EffectiveCommits) != 3 || len(first.ReadOnlyPaths) != 5 {
		t.Fatalf("wrong bounded context: %+v", first)
	}
	execution.Attempts[0], execution.Attempts[2] = execution.Attempts[2], execution.Attempts[0]
	reordered, err := BuildCompositionWorkPackage(root, shard, baseline, execution)
	if err != nil || first.ID != reordered.ID {
		t.Fatalf("package depends onfinish order: %v", err)
	}
	execution.AssemblyCommit = "newtip"
	changed, err := BuildCompositionWorkPackage(root, shard, baseline, execution)
	if err != nil || changed.ID == first.ID {
		t.Fatal("assemblytip missing from immutableidentity")
	}
	execution.AssemblyCommit = "assembled"
	baseline.ApprovedPlanFingerprint = "new-owner-fingerprint"
	changed, err = BuildCompositionWorkPackage(root, shard, baseline, execution)
	if err != nil || changed.ID == first.ID {
		t.Fatal("approvedfingerprint missingfrom identity")
	}
}

func TestCompositionPackageRejectsScopeAndBarrierDrift(t *testing.T) {
	for _, kind := range []string{"scope", "barrier", "baseline", "unverified", "missing constructor"} {
		t.Run(kind, func(t *testing.T) {
			root, shard, baseline, execution := compositionFixture(t)
			switch kind {
			case "scope":
				shard.WriteScope.Allow = append(shard.WriteScope.Allow, "internal/usecase/**")
			case "barrier":
				execution.Barrier = domain.ShardBarrierPending
			case "baseline":
				execution.Attempts[0].BaselineCommit = "other"
			case "unverified":
				execution.Attempts[0].Status = domain.ShardAttemptRunning
			case "missing constructor":
				if err := os.WriteFile(filepath.Join(root, "internal/usecase/availability.go"), []byte("package usecase"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := BuildCompositionWorkPackage(root, shard, baseline, execution); err == nil {
				t.Fatal("invalid compositionpackageaccepted")
			}
		})
	}
}
