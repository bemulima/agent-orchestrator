package planning

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestBuildWorkPackageIsMinimalStableAndBoundToFrozenBaseline(t *testing.T) {
	shard, task, baseline := workPackageFixture()
	first, err := BuildWorkPackage(shard, task, baseline)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildWorkPackage(shard, task, baseline)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("work package serialization is not deterministic")
	}
	if first.ExecutionBase.Revision != baseline.ContractBaselineCommit ||
		first.ExecutionBase.ContractBaselineID != baseline.ID || first.ArchitectureProfile.Fingerprint != baseline.ProfileFingerprint {
		t.Fatal("work package is not bound to the frozen contract/profile evidence")
	}
	if strings.Contains(string(firstJSON), task.Description) || strings.Contains(string(firstJSON), "full business prompt") {
		t.Fatal("work package leaked broad business context")
	}
	if len(first.Contracts.ReadOnlyPaths) != 1 || first.Contracts.ReadOnlyPaths[0] != "internal/usecase/repository_port.go" {
		t.Fatalf("frozen contract paths are not bounded/read-only: %#v", first.Contracts.ReadOnlyPaths)
	}
	if err := ValidateWorkPackage(first); err != nil {
		t.Fatalf("valid work package rejected: %v", err)
	}
}

func TestBuildWorkPackageRejectsBaselineOrScopeDrift(t *testing.T) {
	shard, task, baseline := workPackageFixture()
	shard.ExecutionBase.Revision = strings.Repeat("b", 40)
	if _, err := BuildWorkPackage(shard, task, baseline); err == nil {
		t.Fatal("expected mismatched worker base to be rejected")
	}
	shard, task, baseline = workPackageFixture()
	packageValue, err := BuildWorkPackage(shard, task, baseline)
	if err != nil {
		t.Fatal(err)
	}
	packageValue.Targets.Paths = []string{"internal/transport/http/other.go"}
	if err := ValidateWorkPackage(packageValue); err == nil {
		t.Fatal("expected target outside approved write scope to be rejected")
	}
}

func TestWorkPackageRejectsWritableFrozenContractAndUnsafePath(t *testing.T) {
	shard, task, baseline := workPackageFixture()
	packageValue, err := BuildWorkPackage(shard, task, baseline)
	if err != nil {
		t.Fatal(err)
	}
	packageValue.WriteScope.Allow = append(packageValue.WriteScope.Allow, "internal/usecase/repository_port.go")
	if err := ValidateWorkPackage(packageValue); err == nil {
		t.Fatal("expected writable frozen contract to be rejected")
	}

	packageValue.WriteScope.Allow = []string{"../../outside.go"}
	packageValue.Contracts.ReadOnlyPaths = nil
	packageValue.Targets.Paths = []string{"../../outside.go"}
	if err := ValidateWorkPackage(packageValue); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
}

func TestWorkPackageAndFanoutRejectDeniedOrOverlappingWriteScopes(t *testing.T) {
	shard, task, baseline := workPackageFixture()
	packageValue, err := BuildWorkPackage(shard, task, baseline)
	if err != nil {
		t.Fatal(err)
	}
	denied := packageValue
	denied.WriteScope.Deny = append(denied.WriteScope.Deny, "internal/transport/http/**")
	if err := ValidateWorkPackage(denied); err == nil {
		t.Fatal("expected target hidden under a broad deny pattern to be rejected")
	}

	sibling := packageValue
	sibling.ShardID = "sibling"
	sibling.Route = "backend.transport.http"
	sibling.Targets.Paths = []string{"internal/transport/http/other.go"}
	sibling.WriteScope.Allow = []string{"internal/transport/http/**"}
	sibling.Verification.TestPaths = []string{}
	if err := ValidateDisjointWorkPackages([]domain.WorkPackage{packageValue, sibling}); err == nil {
		t.Fatal("expected sibling worker's broad overlapping scope to be rejected")
	}
}

func workPackageFixture() (domain.ArchitecturalShard, domain.Task, domain.ContractBaseline) {
	const planID = "plan-1"
	const taskID = "task-1"
	const projectID = "project-1"
	const baselineID = "baseline-1"
	const revision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const profileFingerprint = "sha256:profile"
	contract := domain.ContractReference{Kind: "repository-port", RepositoryProjectID: projectID,
		Path: "internal/usecase/repository_port.go", Symbol: "RepositoryPort", RouteID: "backend.usecase", Relation: "consumes"}
	shard := domain.ArchitecturalShard{
		ID: "shard-1", PlanID: planID, TaskID: taskID, RepositoryProjectID: projectID,
		Repository: "/repo", ProfileID: "go.canonical", ProfileFingerprint: profileFingerprint,
		RouteID: "backend.transport.http", ExecutionBase: domain.ShardExecutionBase{
			Kind: domain.ShardBaseContractBaseline, Revision: revision, ContractBaselineID: baselineID,
		}, Targets: domain.RoutedTarget{RouteReference: domain.RouteReference{ProjectID: projectID, RouteID: "backend.transport.http"},
			Paths: []string{"internal/transport/http/availability.go"}, Symbols: []string{"AvailabilityHandler"}},
		Consumes: []domain.ContractReference{contract}, WriteScope: domain.ShardWriteScope{
			Allow:             []string{"internal/transport/http/availability.go", "internal/transport/http/availability_test.go"},
			Deny:              []string{"internal/usecase/repository_port.go", "cmd/**"},
			ReadOnlyContracts: []string{"internal/usecase/repository_port.go"},
			TestPaths:         []string{"internal/transport/http/availability_test.go"}, MaxFiles: 4,
		}, Verification: []domain.ShardVerification{{Kind: "handler-test"}},
	}
	task := domain.Task{ID: taskID, PlanID: planID, ProjectID: projectID, Description: "full business prompt: must not be sent"}
	baseline := domain.ContractBaseline{
		ID: baselineID, PlanID: planID, TaskID: taskID, RepositoryProjectID: projectID,
		ProfileID: "go.canonical", ProfileFingerprint: profileFingerprint,
		ExecutionState: domain.ContractBaselineFrozen, ContractBaselineCommit: revision,
		Files:      []domain.ContractBaselineFile{{Path: "internal/usecase/repository_port.go", SHA256: "frozen-hash"}},
		Validation: domain.ContractBaselineValidation{Passed: true},
	}
	return shard, task, baseline
}

func TestWorkPackagePreservesCanonicalDirectoryDenyScope(t *testing.T) {
	shard, task, baseline := workPackageFixture()
	shard.WriteScope.Deny = append(shard.WriteScope.Deny, "cmd/", "internal/composition/")
	value, err := BuildWorkPackage(shard, task, baseline)
	if err != nil {
		t.Fatalf("canonical directory deny scope rejected: %v", err)
	}
	if !scopeContains(value.WriteScope.Deny, "cmd/server/main.go") ||
		scopeContains(value.WriteScope.Deny, "cmd-other/main.go") {
		t.Fatal("directory deny must cover descendants without covering siblings")
	}
	for _, unsafe := range []string{"/cmd/", "../cmd/", "cmd//", "cmd/../", "/"} {
		invalid := value
		invalid.WriteScope.Deny = append(append([]string(nil), value.WriteScope.Deny...), unsafe)
		if err := ValidateWorkPackage(invalid); err == nil {
			t.Fatalf("unsafe directory deny %q accepted", unsafe)
		}
	}
}
