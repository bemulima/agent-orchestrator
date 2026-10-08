package domain

import "testing"

func TestStaleExecutionCannotReachCommitAfterNewerOrInvalidatedAttempt(t *testing.T) {
	old := TaskAttempt{ID: "old", TaskID: "task", AttemptNumber: 1, Status: TaskAttemptStatusVerification}
	if !TaskExecutionCurrent("old", 1, "task", []TaskAttempt{old}) {
		t.Fatal("current active attempt rejected")
	}
	for _, status := range []TaskAttemptStatus{TaskAttemptStatusCancelled, TaskAttemptStatusTimedOut, TaskAttemptStatusFailed, TaskAttemptStatusInfrastructureUnknown} {
		invalid := old
		invalid.Status = status
		if TaskExecutionCurrent("old", 1, "task", []TaskAttempt{invalid}) {
			t.Fatalf("late GREEN accepted after %s", status)
		}
	}
	newer := TaskAttempt{ID: "new", TaskID: "task", AttemptNumber: 2, Status: TaskAttemptStatusRunning}
	if TaskExecutionCurrent("old", 1, "task", []TaskAttempt{old, newer}) {
		t.Fatal("obsolete attempt accepted")
	}
	shard := ShardAttempt{ID: "old", ShardID: "shard", AttemptNumber: 1, Status: ShardAttemptRunning}
	next := ShardAttempt{ID: "new", ShardID: "shard", AttemptNumber: 2, Status: ShardAttemptRunning}
	if ShardExecutionCurrent("old", 1, "shard", []ShardAttempt{shard, next}) {
		t.Fatal("obsolete shard accepted")
	}
	shard.Status = ShardAttemptFailed
	if ShardExecutionCurrent("old", 1, "shard", []ShardAttempt{shard}) {
		t.Fatal("invalid shard accepted")
	}
}
