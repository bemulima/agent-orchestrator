package shardexecution

import (
	"context"
	"fmt"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/planning"
)

// reusableVerifiedAttempt retains an immutable verified result only after the
// current approval, commit, frozen files and candidate checks pass again.
func (s Service) reusableVerifiedAttempt(ctx context.Context, prepared PreparedShard, history []domain.ShardAttempt) (*domain.ShardAttempt, error) {
	attempt, err := verifiedResumeCandidate(prepared, history)
	if err != nil || attempt == nil {
		return attempt, err
	}
	inputs, err := s.loadApprovedInputs(ctx, prepared.WorkPackage.PlanID)
	if err != nil {
		return nil, err
	}
	shard, found := inputs.shardByID[prepared.WorkPackage.ShardID]
	if !found {
		return nil, fmt.Errorf("verified shard is no longer approved: %w", domain.ErrConflict)
	}
	baseline := inputs.baselines[shard.TaskID]
	canonical, err := planning.BuildWorkPackage(shard, inputs.tasks[shard.TaskID], baseline)
	if err != nil {
		return nil, err
	}
	if !sameJSON(canonical, prepared.WorkPackage) || baseline.ContractBaselineCommit != attempt.BaselineCommit {
		return nil, fmt.Errorf("verified shard package no longer matches approval: %w", domain.ErrConflict)
	}
	project, found := inputs.projects[canonical.ProjectID]
	if !found {
		return nil, fmt.Errorf("verified shard project is missing: %w", domain.ErrNotFound)
	}
	if err := s.revalidateVerifiedWorkspace(ctx, project, baseline, *attempt); err != nil {
		return nil, err
	}
	return attempt, nil
}

func verifiedResumeCandidate(prepared PreparedShard, history []domain.ShardAttempt) (*domain.ShardAttempt, error) {
	var latest *domain.ShardAttempt
	for i := range history {
		attempt := history[i]
		if attempt.ShardID == prepared.WorkPackage.ShardID && (latest == nil || attempt.AttemptNumber > latest.AttemptNumber) {
			latest = &attempt
		}
	}
	if latest == nil || latest.Status != domain.ShardAttemptVerified {
		return nil, nil
	}
	value := prepared.WorkPackage
	if !sameJSON(latest.WorkPackage, value) || latest.BaselineCommit != value.ExecutionBase.Revision ||
		latest.PlanID != value.PlanID || latest.TaskID != value.TaskID || latest.ShardID != value.ShardID ||
		latest.Workspace.BaseCommit != value.ExecutionBase.Revision || prepared.Workspace.BaseCommit != value.ExecutionBase.Revision {
		return nil, fmt.Errorf("verified attempt package or baseline changed: %w", domain.ErrConflict)
	}
	if latest.FinishedAt == nil || latest.Red == nil || !latest.Red.Semantic || len(latest.Red.TestPaths) == 0 ||
		latest.Green == nil || !latest.Green.Passed || len(latest.Green.Commands) == 0 ||
		len(latest.CommitSHA) != 40 || strings.Trim(latest.CommitSHA, "0123456789abcdef") != "" ||
		latest.WorkerThreadID == "" || len(latest.ChangedFiles) == 0 ||
		len(latest.ChangedFiles) > value.WriteScope.MaxFiles || !scopeAllowsFiles(value, latest.ChangedFiles) {
		return nil, fmt.Errorf("verified attempt lacks complete semantic RED, GREEN, commit or scope evidence: %w", domain.ErrConflict)
	}
	for _, testPath := range latest.Red.TestPaths {
		if !contains(latest.ChangedFiles, testPath) {
			return nil, fmt.Errorf("verified semantic RED test missing from committed scope: %w", domain.ErrConflict)
		}
	}
	for _, check := range latest.Green.Commands {
		if check.ExitCode != 0 {
			return nil, fmt.Errorf("verified attempt contains failed GREEN evidence: %w", domain.ErrConflict)
		}
	}
	return latest, nil
}

func (s Service) revalidateVerifiedWorkspace(ctx context.Context, project domain.Project, baseline domain.ContractBaseline, attempt domain.ShardAttempt) error {
	if s.Worktrees == nil {
		return fmt.Errorf("verified attempt inspection is unavailable: %w", domain.ErrInvalidStatus)
	}
	verifier, ok := s.Worktrees.(CommitVerifier)
	if !ok {
		return fmt.Errorf("verified attempt commit verifier is unavailable: %w", domain.ErrInvalidStatus)
	}
	inspectClean := func() error {
		// Inspect reports the diff against BaseCommit, so inspect against the
		// verified HEAD to detect only uncommitted drift, retaining the original
		// frozen base in the persisted attempt and commit-parent verification.
		inspectionWorkspace := attempt.Workspace
		inspectionWorkspace.BaseCommit = attempt.CommitSHA
		state, err := s.Worktrees.Inspect(ctx, project, inspectionWorkspace)
		if err != nil {
			return err
		}
		if state.HeadCommit != attempt.CommitSHA || len(state.ChangedFiles) != 0 || strings.TrimSpace(state.Diff) != "" {
			return fmt.Errorf("verified workspace HEAD or committed content changed: %w", domain.ErrConflict)
		}
		return nil
	}
	if err := inspectClean(); err != nil {
		return err
	}
	if err := verifyWorkingFrozenFiles(ctx, s.Worktrees, attempt.Workspace, baseline); err != nil {
		return err
	}
	// VerifyCommit binds the complete immutable diff (including the original RED
	// tests) to this single-parent commit; transient snapshots are not reconstructed.
	if err := verifier.VerifyCommit(ctx, project, attempt.Workspace, attempt.CommitSHA, attempt.BaselineCommit, attempt.ChangedFiles); err != nil {
		return err
	}
	commands := uniqueCommands(attempt.WorkPackage.Verification.CandidateCommands)
	if len(commands) == 0 {
		return fmt.Errorf("verified attempt has no candidate verification commands: %w", domain.ErrValidation)
	}
	for _, command := range commands {
		result, err := s.Worktrees.RunCheck(ctx, attempt.Workspace, command)
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("verified attempt GREEN rerun failed: %s: %w", command, domain.ErrConflict)
		}
	}
	if err := inspectClean(); err != nil {
		return err
	}
	return verifyWorkingFrozenFiles(ctx, s.Worktrees, attempt.Workspace, baseline)
}
