package planning

import (
	"strings"
	"testing"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/contractbaseline"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestIncompleteContractsBlockFanoutReadiness(t *testing.T) {
	input := readyGateFixture()
	input.ContractPlan = domain.ContractPlan{
		State: domain.ContractPlanFreezeNeeded, Required: true, FreezeRequired: true,
		Boundaries: []domain.PlannedContractBoundary{{
			Kind: "application-command-result", TargetPath: "internal/usecase/result.go", Owner: domain.RouteReference{ProjectID: "project", RouteID: "backend.usecase"},
			Consumers: []domain.RouteReference{{ProjectID: "project", RouteID: "backend.transport.http"}}, Rationale: "shared contract",
		}},
	}
	input.Baselines[0].PlanningContractState = domain.ContractPlanFreezeNeeded
	input.Baselines[0].ExecutionState = domain.ContractBaselinePending
	input.Baselines[0].Validation = domain.ContractBaselineValidation{Passed: false, Reasons: []string{"not persisted"}}
	result := EvaluateFanoutReadiness(input)
	if result.State != domain.FanoutReadinessBlocked || !strings.Contains(strings.Join(result.Reasons, " "), "not frozen") {
		t.Fatalf("incomplete contract baseline did not block fan-out: %#v", result)
	}
}

func TestNotRequiredContractsCanUseApprovedSourceBase(t *testing.T) {
	result := EvaluateFanoutReadiness(readyGateFixture())
	if result.State != domain.FanoutReadinessReady {
		t.Fatalf("valid source-base-only plan did not reach readiness: %#v", result)
	}
}

func TestFrozenContractBaselineRequiresCommitAgentAndIndependentReview(t *testing.T) {
	input := readyGateFixture()
	input.ContractPlan = domain.ContractPlan{
		State: domain.ContractPlanFreezeNeeded, Required: true, FreezeRequired: true,
		Boundaries: []domain.PlannedContractBoundary{{
			Kind: "application-command-result", TargetPath: "internal/usecase/result.go", Owner: domain.RouteReference{ProjectID: "project", RouteID: "backend.usecase"},
			Consumers: []domain.RouteReference{{ProjectID: "project", RouteID: "backend.transport.http"}}, Rationale: "shared contract",
		}},
	}
	baseline := input.Baselines[0]
	baseline.PlanningContractState = domain.ContractPlanFreezeNeeded
	baseline.ExecutionState = domain.ContractBaselineFrozen
	baseline.ApprovedPlanFingerprint = *input.Plan.ApprovedFingerprint
	baseline.ContractBaselineCommit = strings.Repeat("a", 40)
	baseline.Files = []domain.ContractBaselineFile{{Path: "internal/usecase/result.go", SHA256: strings.Repeat("b", 64)}}
	baseline.AggregateSHA256 = contractbaseline.AggregateFilesSHA256(baseline.Files)
	baseline.ContractPlanFingerprint, _ = contractbaseline.PlanFingerprint(input.ContractPlan)
	baseline.ContractAgentPassed = true
	baseline.ContractAgentThreadID = "contract-agent-thread"
	baseline.ContractAgentResultSHA256 = strings.Repeat("b", 64)
	baseline.MechanicalReviewPassed = true
	baseline.MechanicalReviewSHA256 = strings.Repeat("c", 64)
	baseline.ContractVerificationPassed = true
	baseline.ContractVerification = "go test ./... passed"
	baseline.IndependentReviewPassed = true
	baseline.IndependentReviewerID = "independent-reviewer"
	baseline.IndependentReviewStatus = "PASS"
	baseline.IndependentReviewSHA256 = strings.Repeat("d", 64)
	baseline.Validation = domain.ContractBaselineValidation{Passed: true}
	baseline.MaterializedContracts = []domain.ContractReference{{
		Kind: "application-command-result", RepositoryProjectID: "project", Path: "internal/usecase/result.go",
		RouteID: "backend.usecase", Relation: "implements",
	}}
	input.Baselines = []domain.ContractBaseline{baseline}
	input.Shards[0].ExecutionBase = domain.ShardExecutionBase{
		Kind: domain.ShardBaseContractBaseline, Revision: baseline.ContractBaselineCommit, ContractBaselineID: baseline.ID,
	}
	input.Shards[0].Implements = baseline.MaterializedContracts
	result := EvaluateFanoutReadiness(input)
	if result.State != domain.FanoutReadinessReady {
		t.Fatalf("complete frozen baseline evidence did not satisfy readiness: %#v", result)
	}
	siblingsInput := input
	siblingsInput.Routing.Routes = append(siblingsInput.Routing.Routes, domain.RoutedTarget{
		RouteReference: domain.RouteReference{ProjectID: "project", RouteID: "backend.transport.http"},
		Paths:          []string{"internal/transport/http/handler.go"}, EvidenceIDs: []string{"http-evidence"},
	})
	sibling := siblingsInput.Shards[0]
	sibling.ID, sibling.RouteID = "sibling", "backend.transport.http"
	sibling.Targets = siblingsInput.Routing.Routes[1]
	sibling.ExecutionBase = input.Shards[0].ExecutionBase
	sibling.Implements = nil
	sibling.Consumes = []domain.ContractReference{{
		Kind: "application-command-result", RepositoryProjectID: "project", Path: "internal/usecase/result.go",
		RouteID: "backend.transport.http", Relation: "consumes",
	}}
	sibling.WriteScope.Allow = []string{"internal/transport/http/handler.go"}
	siblingsInput.Shards = append(siblingsInput.Shards, sibling)
	result = EvaluateFanoutReadiness(siblingsInput)
	if result.State != domain.FanoutReadinessReady {
		t.Fatalf("siblings with a common frozen commit did not reach readiness: %#v", result)
	}

	baseline.IndependentReviewPassed = false
	input.Baselines = []domain.ContractBaseline{baseline}
	result = EvaluateFanoutReadiness(input)
	if result.State != domain.FanoutReadinessBlocked {
		t.Fatalf("baseline without independent review passed readiness: %#v", result)
	}
	baseline.IndependentReviewPassed = true
	baseline.IndependentReviewStatus = "PASS"
	baseline.IndependentReviewerID = "reviewer"
	baseline.IndependentReviewSHA256 = strings.Repeat("e", 64)
	baseline.Validation = domain.ContractBaselineValidation{Passed: false, Reasons: []string{"committed file hash drift"}}
	input.Baselines = []domain.ContractBaseline{baseline}
	result = EvaluateFanoutReadiness(input)
	if result.State != domain.FanoutReadinessBlocked {
		t.Fatalf("drifted baseline preserved READY_FOR_FANOUT: %#v", result)
	}
}

func TestSiblingContractDependentShardsMustShareFrozenCommit(t *testing.T) {
	input := readyGateFixture()
	input.ContractPlan = domain.ContractPlan{State: domain.ContractPlanFreezeNeeded, Required: true, FreezeRequired: true,
		Boundaries: []domain.PlannedContractBoundary{{
			Kind: "application-command-result", TargetPath: "internal/usecase/result.go", Owner: domain.RouteReference{ProjectID: "project", RouteID: "backend.usecase"},
			Consumers: []domain.RouteReference{{ProjectID: "project", RouteID: "backend.transport.http"}},
		}}}
	baseline := input.Baselines[0]
	baseline.PlanningContractState = domain.ContractPlanFreezeNeeded
	baseline.ExecutionState = domain.ContractBaselineFrozen
	baseline.ApprovedPlanFingerprint = *input.Plan.ApprovedFingerprint
	baseline.ContractBaselineCommit = strings.Repeat("a", 40)
	baseline.Files = []domain.ContractBaselineFile{{Path: "internal/usecase/result.go", SHA256: strings.Repeat("b", 64)}}
	baseline.AggregateSHA256 = contractbaseline.AggregateFilesSHA256(baseline.Files)
	baseline.ContractPlanFingerprint, _ = contractbaseline.PlanFingerprint(input.ContractPlan)
	baseline.ContractAgentPassed, baseline.MechanicalReviewPassed = true, true
	baseline.ContractAgentThreadID, baseline.IndependentReviewerID = "agent", "reviewer"
	baseline.ContractAgentResultSHA256, baseline.MechanicalReviewSHA256 = strings.Repeat("c", 64), strings.Repeat("d", 64)
	baseline.ContractVerificationPassed, baseline.ContractVerification = true, "go test ./... passed"
	baseline.IndependentReviewPassed, baseline.IndependentReviewStatus = true, "PASS"
	baseline.IndependentReviewSHA256 = strings.Repeat("e", 64)
	baseline.MaterializedContracts = []domain.ContractReference{{Kind: "application-command-result", RepositoryProjectID: "project", Path: "internal/usecase/result.go", RouteID: "backend.usecase", Relation: "implements"}}
	baseline.Validation = domain.ContractBaselineValidation{Passed: true}
	input.Baselines = []domain.ContractBaseline{baseline}
	input.Shards[0].ExecutionBase = domain.ShardExecutionBase{Kind: domain.ShardBaseContractBaseline, Revision: baseline.ContractBaselineCommit, ContractBaselineID: baseline.ID}
	input.Shards[0].Implements = baseline.MaterializedContracts
	sibling := input.Shards[0]
	sibling.ID, sibling.RouteID = "sibling", "backend.transport.http"
	sibling.ExecutionBase.Revision = strings.Repeat("f", 40)
	sibling.Consumes = baseline.MaterializedContracts
	input.Shards = append(input.Shards, sibling)
	result := EvaluateFanoutReadiness(input)
	if result.State != domain.FanoutReadinessBlocked || !strings.Contains(strings.Join(result.Reasons, " "), "common contract base") {
		t.Fatalf("sibling shard with a different contract commit passed readiness: %#v", result)
	}
}

func TestConflictingShardScopesBlockFanoutReadiness(t *testing.T) {
	input := readyGateFixture()
	input.Shards = []domain.ArchitecturalShard{
		{ID: "shard-a", TaskID: "task", RepositoryProjectID: "project", RouteID: "backend.usecase", ProfileFingerprint: "profile", Parallel: true, Phase: "workers", WriteScope: domain.ShardWriteScope{Allow: []string{"internal/usecase/service.go"}}},
		{ID: "shard-b", TaskID: "task", RepositoryProjectID: "project", RouteID: "backend.domain", ProfileFingerprint: "profile", Parallel: true, Phase: "workers", WriteScope: domain.ShardWriteScope{Allow: []string{"internal/usecase/service.go"}}},
	}
	result := EvaluateFanoutReadiness(input)
	if result.State != domain.FanoutReadinessBlocked || !strings.Contains(strings.Join(result.Reasons, " "), "conflicting write scopes") {
		t.Fatalf("conflicting write scopes did not block fan-out: %#v", result)
	}
}

func readyGateFixture() FanoutGateInput {
	now := time.Unix(100, 0).UTC()
	planFingerprint := "approved-plan-fingerprint"
	return FanoutGateInput{
		Plan:  domain.Plan{ID: "plan", Status: domain.PlanStatusApproved, Fingerprint: planFingerprint, ApprovedFingerprint: &planFingerprint},
		Tasks: []domain.Task{{ID: "task", PlanID: "plan", ProjectID: "project"}},
		Routing: domain.RoutingResult{
			Status:   domain.RoutingStatusResolved,
			Profiles: []domain.ArchitectureProfileResolution{{ProjectID: "project", Status: domain.ProfileResolutionResolved, ProfileID: "go.canonical", ProfileFingerprint: "profile"}},
			Routes: []domain.RoutedTarget{{
				RouteReference: domain.RouteReference{ProjectID: "project", RouteID: "backend.usecase"},
				Paths:          []string{"internal/usecase/service.go"}, EvidenceIDs: []string{"source-evidence"},
			}},
			OwnerReviewRequired: false,
		},
		ContractPlan: domain.ContractPlan{State: domain.ContractPlanNotRequired, Reason: "one route"},
		Shards: []domain.ArchitecturalShard{{
			ID: "shard", TaskID: "task", RepositoryProjectID: "project", RouteID: "backend.usecase",
			ProfileFingerprint: "profile", LocalIntent: "Implement the use case boundary.",
			Targets: domain.RoutedTarget{RouteReference: domain.RouteReference{ProjectID: "project", RouteID: "backend.usecase"},
				Paths: []string{"internal/usecase/service.go"}, EvidenceIDs: []string{"source-evidence"}},
			Acceptance:   []string{"Focused use case verification passes."},
			Verification: []domain.ShardVerification{{Kind: "usecase-unit"}},
			Parallel:     true, Phase: "workers",
			ExecutionBase: domain.ShardExecutionBase{Kind: domain.ShardBaseSourceRevision, Revision: "source-revision"},
			WriteScope:    domain.ShardWriteScope{Allow: []string{"internal/usecase/service.go"}, MaxFiles: 4},
		}},
		Baselines: []domain.ContractBaseline{{
			ID: "baseline", PlanID: "plan", TaskID: "task", RepositoryProjectID: "project",
			PlanningContractState: domain.ContractPlanNotRequired, ExecutionState: domain.ContractBaselineNotRequired,
			ApprovedPlanFingerprint: planFingerprint,
			Validation:              domain.ContractBaselineValidation{Passed: true},
		}},
		Fingerprints: map[string]string{"project": "profile"}, Now: now,
	}
}
