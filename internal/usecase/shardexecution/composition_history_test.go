package shardexecution

import (
	"context"
	"fmt"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestCompositionHistoryRetainsDetachedImmutablePriorAttempt(t *testing.T) {
	prior := domain.CompositionAttempt{ID: "old", Status: domain.ShardAttemptVerified, CommitSHA: "old-commit", REDSetup: &domain.ShardREDSetupEvidence{Sources: map[string]string{"main.go": "old-source"}}}
	previous := domain.ShardFanoutExecution{CompositionAttempt: &prior}
	before := compactJSON(previous)
	history, err := retainCompositionHistory(previous)
	if err != nil || len(history) != 1 {
		t.Fatalf("history=%v err=%v", history, err)
	}
	history[0].REDSetup.Sources["main.go"] = "different"
	if compactJSON(previous) != before {
		t.Fatal("prior attempt audit mutated")
	}
	previous.CompositionHistory = []domain.CompositionAttempt{prior}
	history, err = retainCompositionHistory(previous)
	if err != nil || len(history) != 1 {
		t.Fatal("duplicate historical identity")
	}
	previous.CompositionHistory[0].CommitSHA = "tampered"
	if _, err := retainCompositionHistory(previous); err == nil {
		t.Fatal("conflicting historical identity accepted")
	}
}

func TestCompositionChangedEffectiveCommitSetRequiresNewAttemptButAllowsOldRegressionFixture(t *testing.T) {
	root, shard, baseline, execution := compositionFixture(t)
	old, err := BuildCompositionWorkPackage(root, shard, baseline, execution)
	if err != nil {
		t.Fatal(err)
	}
	prior := domain.CompositionAttempt{ID: "prior", Status: domain.ShardAttemptVerified, WorkPackage: old, CommitSHA: "old-composition", Green: &domain.ShardGreenEvidence{Passed: true, Commands: []domain.WorkspaceCheckResult{{Command: "go test ./... -count=1", ExitCode: 0}}}, Red: &domain.ShardREDEvidence{Semantic: true, TestPaths: old.Verification.TestPaths}, REDSetup: &domain.ShardREDSetupEvidence{MechanicalReview: "PASS", Sources: map[string]string{"cmd/availability-service/main.go": compositionEmptySource}}, ChangedFiles: []string{"cmd/availability-service/main.go", "cmd/availability-service/main_test.go"}}
	execution.Attempts[0].CommitSHA = "remediated-worker"
	execution.AssemblyCommit = "fresh-input"
	updated, err := BuildCompositionWorkPackage(root, shard, baseline, execution)
	if err != nil {
		t.Fatal(err)
	}
	if sameJSON(old, updated) || old.ID == updated.ID {
		t.Fatal("changed upstream input weakened immutable package")
	}
	before := compactJSON(prior)
	fixture := compositionReplayCandidate(updated, []domain.CompositionAttempt{prior})
	if fixture == nil || fixture.ID != prior.ID {
		t.Fatal("matching assertion fixture unavailable")
	}
	if compactJSON(prior) != before {
		t.Fatal("old composition result changed")
	}
	changed := updated
	changed.APIs = append([]domain.CompositionAPI(nil), updated.APIs...)
	changed.APIs[0].Signature = "changed"
	if compositionReplayCandidate(changed, []domain.CompositionAttempt{prior}) != nil {
		t.Fatal("fixture replay ignored API drift")
	}
	changed = updated
	changed.WriteScope.Allow = []string{"other.go"}
	if compositionReplayCandidate(changed, []domain.CompositionAttempt{prior}) != nil {
		t.Fatal("fixture replay ignored scope drift")
	}
	changed = updated
	changed.ExecutionBase.Revision = "changed-frozen-base"
	if compositionReplayCandidate(changed, []domain.CompositionAttempt{prior}) != nil {
		t.Fatal("fixture replay ignored frozen baseline drift")
	}
	prior.Status = domain.ShardAttemptBlocked
	if compositionReplayCandidate(updated, []domain.CompositionAttempt{prior}) != nil {
		t.Fatal("nonverified assertion fixture used")
	}
}

func TestCompositionBudgetDenialIsExplicitAndNotAutomaticallyRecoverable(t *testing.T) {
	attempt := &domain.CompositionAttempt{}
	service := Service{}
	service.recordCompositionBudgetDenial(context.Background(), attempt, "wiring implementation after semantic RED", fmt.Errorf("deep-model five-hour budget is exhausted: %w", domain.ErrApprovalNeeded))
	if attempt.BudgetBlocker == nil || attempt.BudgetBlocker.RemainingCompositionAgentCalls != 1 || attempt.BudgetBlocker.AdditionalRunsRequired != 2 {
		t.Fatalf("missing blocker evidence %+v", attempt.BudgetBlocker)
	}
	if compositionFormattingRecoveryAllowed(domain.ShardFanoutExecution{CompositionAttempt: attempt}) {
		t.Fatal("budget exhaustion automatically recovered")
	}
	other := &domain.CompositionAttempt{}
	service.recordCompositionBudgetDenial(context.Background(), other, "RED_SETUP", fmt.Errorf("other approval: %w", domain.ErrApprovalNeeded))
	if other.BudgetBlocker != nil {
		t.Fatal("unrelated approval classified as budget")
	}
}
