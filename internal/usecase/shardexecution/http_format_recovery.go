package shardexecution

import (
	"context"
	"fmt"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func httpFormatRecoveryCandidate(prepared PreparedShard, history []domain.ShardAttempt) (*domain.ShardAttempt, error) {
	if prepared.Remediation == nil || prepared.WorkPackage.Route != "backend.transport.http" {
		return nil, nil
	}
	var latest *domain.ShardAttempt
	for index := range history {
		value := history[index]
		if value.ShardID == prepared.WorkPackage.ShardID && (latest == nil || value.AttemptNumber > latest.AttemptNumber) {
			latest = &value
		}
	}
	if latest == nil || latest.Workspace.Path != prepared.Workspace.Path {
		return nil, nil
	}
	if !httpFormatRecoveryEligible(prepared, *latest) {
		return nil, fmt.Errorf("same HTTP workspace requires explicit retry; not an eligible timestamp formatting recovery: %w", domain.ErrConflict)
	}
	return latest, nil
}

func httpFormatRecoveryEligible(prepared PreparedShard, attempt domain.ShardAttempt) bool {
	if prepared.Remediation == nil || prepared.WorkPackage.Route != "backend.transport.http" || attempt.Status != domain.ShardAttemptFailed || attempt.Phase != domain.ShardPhasePrepared || attempt.Red != nil || attempt.Green != nil || attempt.Implementation != nil || attempt.REDSetup != nil || attempt.CommitSHA != "" || len(attempt.FormatRecoveryHistory) != 0 || attempt.ID == "" || attempt.WorkerThreadID == "" || attempt.Model == "" || attempt.ReasoningEffort == "" || attempt.StartedAt.IsZero() || attempt.FinishedAt == nil || len(attempt.Blockers) != 1 || len(attempt.Verification) != 0 || len(attempt.ChangedFiles) != 0 || len(attempt.PhaseHistory) == 0 {
		return false
	}
	if !sameJSON(attempt.WorkPackage, prepared.WorkPackage) || !sameJSON(attempt.Workspace, prepared.Workspace) || attempt.BaselineCommit != prepared.WorkPackage.ExecutionBase.Revision {
		return false
	}
	for _, phase := range attempt.PhaseHistory {
		if phase.Phase != domain.ShardPhasePrepared {
			return false
		}
	}
	failure := attempt.Blockers[0]
	if !strings.HasPrefix(failure, "decode semantic RED test-only WorkerResult: strict JSON decode: parsing time ") || !strings.Contains(failure, "2006-01-02T15:04:05Z07:00") {
		return false
	}
	preimage := attempt.RemediationPreimage
	return preimage != nil && preimage.RequestID == prepared.Remediation.RequestID && preimage.PriorCommit == prepared.Remediation.PriorCommit && preimage.BaselineCommit == prepared.WorkPackage.ExecutionBase.Revision && len(preimage.Sources) > 0 && len(preimage.Sources) == len(preimage.Hashes)
}

func (s Service) validateHTTPFormatRecoveryWorkspace(ctx context.Context, prepared PreparedShard, attempt domain.ShardAttempt, project domain.Project) ([]string, error) {
	state, err := s.Worktrees.Inspect(ctx, project, prepared.Workspace)
	if err != nil {
		return nil, err
	}
	if state.HeadCommit != prepared.WorkPackage.ExecutionBase.Revision || !scopeAllowsFiles(prepared.WorkPackage, state.ChangedFiles) {
		return nil, fmt.Errorf("HTTP recovery HEAD or scope drift: %w", domain.ErrConflict)
	}
	production := []string{}
	for path, source := range attempt.RemediationPreimage.Sources {
		data, err := s.Worktrees.ReadArtifact(ctx, prepared.Workspace, path, 1<<20)
		if err != nil {
			return nil, err
		}
		hash := contentHash(data)
		if string(data) != source || hash != attempt.RemediationPreimage.Hashes[path] || contentHash([]byte(source)) != hash {
			return nil, fmt.Errorf("HTTP recovery altered preimage behavior: %w", domain.ErrConflict)
		}
		production = append(production, path)
	}
	tests := []string{}
	for _, path := range state.ChangedFiles {
		if !contains(production, path) {
			tests = append(tests, path)
		}
	}
	if len(tests) == 0 || !pathsWithin(tests, prepared.WorkPackage.Verification.TestPaths) || !samePaths(state.ChangedFiles, append(append([]string(nil), production...), tests...)) {
		return nil, fmt.Errorf("HTTP recovery candidate changed non-test owned source: %w", domain.ErrWriteScope)
	}
	for _, path := range tests {
		if !strings.HasSuffix(path, "_test.go") {
			return nil, domain.ErrWriteScope
		}
		if _, err := s.Worktrees.ReadArtifact(ctx, prepared.Workspace, path, 1<<20); err != nil {
			return nil, err
		}
	}
	return cleanSorted(tests), nil
}
