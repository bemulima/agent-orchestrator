package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestCurrentFanoutAttemptsSelectsLatestForCurrentBaselineAndPreservesDrift(t *testing.T) {
	finished := time.Now()
	makeAttempt := func(shard, baseline, commit string, number int) domain.ShardAttempt {
		return domain.ShardAttempt{ShardID: shard, AttemptNumber: number, BaselineCommit: commit,
			Status: domain.ShardAttemptFailed, FinishedAt: &finished,
			WorkPackage: domain.WorkPackage{ExecutionBase: domain.ShardExecutionBase{
				Kind: domain.ShardBaseContractBaseline, Revision: commit, ContractBaselineID: baseline,
			}},
		}
	}
	history := []domain.ShardAttempt{
		makeAttempt("b", "old", "old-commit", 1),
		makeAttempt("a", "new", "new-commit", 3),
		makeAttempt("a", "new", "new-commit", 2),
		makeAttempt("b", "new", "new-commit", 2),
	}
	attempts, err := currentFanoutAttempts(history, "new", "new-commit")
	if err != nil || len(attempts) != 2 || attempts[0].ShardID != "a" || attempts[0].AttemptNumber != 3 || attempts[1].ShardID != "b" {
		t.Fatalf("current attempts = %#v, %v", attempts, err)
	}
	if len(history) != 4 {
		t.Fatal("history was changed")
	}
	history[1].WorkPackage.ExecutionBase.Revision = "wrong"
	if _, err := currentFanoutAttempts(history, "new", "new-commit"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("latest current baseline drift hidden: %v", err)
	}
	history[1].WorkPackage.ExecutionBase.Revision = "new-commit"
	history[0].Status = domain.ShardAttemptRunning
	if _, err := currentFanoutAttempts(history, "new", "new-commit"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("running historical attempt hidden: %v", err)
	}
}
