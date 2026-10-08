package shardexecution

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type BarrierResult struct {
	State   domain.ShardBarrierState `json:"state"`
	Reasons []string                 `json:"reasons"`
}

func EvaluateBarrier(requiredShardIDs []string, attempts []domain.ShardAttempt, baselineCommit string) BarrierResult {
	byShard := make(map[string]domain.ShardAttempt, len(attempts))
	for _, attempt := range attempts {
		if attempt.BaselineCommit != baselineCommit || attempt.WorkPackage.ExecutionBase.Revision != baselineCommit {
			return BarrierResult{State: domain.ShardBarrierReplanRequired, Reasons: []string{"worker attempt used a different frozen baseline"}}
		}
		byShard[attempt.ShardID] = attempt
	}
	missingOrFailed := []string{}
	for _, shardID := range requiredShardIDs {
		attempt, exists := byShard[shardID]
		if !exists {
			missingOrFailed = append(missingOrFailed, shardID+": no attempt")
			continue
		}
		if attempt.Status == domain.ShardAttemptContractChange || attempt.Status == domain.ShardAttemptReplan {
			return BarrierResult{State: domain.ShardBarrierReplanRequired, Reasons: []string{shardID + ": " + string(attempt.Status)}}
		}
		if attempt.Status != domain.ShardAttemptVerified || attempt.CommitSHA == "" || attempt.Red == nil ||
			!attempt.Red.Semantic || attempt.Green == nil || !attempt.Green.Passed || attempt.FinishedAt == nil {
			missingOrFailed = append(missingOrFailed, shardID+": attempt is not fully verified")
		}
	}
	if len(missingOrFailed) > 0 {
		sort.Strings(missingOrFailed)
		return BarrierResult{State: domain.ShardBarrierBlocked, Reasons: missingOrFailed}
	}
	return BarrierResult{State: domain.ShardBarrierReady, Reasons: []string{}}
}

type WorkerInterval struct {
	ShardID    string
	StartedAt  time.Time
	FinishedAt time.Time
}

func WorkerIntervals(attempts []domain.ShardAttempt) []WorkerInterval {
	result := make([]WorkerInterval, 0, len(attempts))
	for _, attempt := range attempts {
		if attempt.WorkerStartedAt.IsZero() || attempt.FinishedAt == nil {
			continue
		}
		result = append(result, WorkerInterval{ShardID: attempt.ShardID, StartedAt: attempt.WorkerStartedAt, FinishedAt: *attempt.FinishedAt})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].StartedAt.Equal(result[j].StartedAt) {
			return result[i].ShardID < result[j].ShardID
		}
		return result[i].StartedAt.Before(result[j].StartedAt)
	})
	return result
}

func MaxSimultaneousWorkers(attempts []domain.ShardAttempt) int {
	type event struct {
		at    time.Time
		delta int
	}
	events := []event{}
	for _, interval := range WorkerIntervals(attempts) {
		if !interval.FinishedAt.After(interval.StartedAt) {
			continue
		}
		events = append(events, event{at: interval.StartedAt, delta: 1}, event{at: interval.FinishedAt, delta: -1})
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].at.Equal(events[j].at) {
			return events[i].delta < events[j].delta
		}
		return events[i].at.Before(events[j].at)
	})
	active, maximum := 0, 0
	for _, item := range events {
		active += item.delta
		if active > maximum {
			maximum = active
		}
	}
	return maximum
}

func barrierError(result BarrierResult) error {
	if result.State == domain.ShardBarrierReady {
		return nil
	}
	return fmt.Errorf("fan-out barrier %s: %s", result.State, strings.Join(result.Reasons, "; "))
}
