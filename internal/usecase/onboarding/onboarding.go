package onboarding

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type PrepareInput struct {
	ProjectID string `json:"-"`
	DryRun    bool   `json:"dry_run"`
	Semantic  bool   `json:"semantic"`
}

type Prepare struct {
	Projects          repository.ProjectRepository
	Sources           repository.ProjectSource
	Runs              repository.OnboardingRepository
	Generator         repository.OnboardingGenerator
	SemanticGenerator repository.OnboardingGenerator
}

func (uc Prepare) Handle(ctx context.Context, input PrepareInput) (domain.OnboardingRun, error) {
	project, err := uc.Projects.Get(ctx, strings.TrimSpace(input.ProjectID))
	if err != nil {
		return domain.OnboardingRun{}, err
	}
	if project.LocalPath == nil || strings.TrimSpace(*project.LocalPath) == "" {
		return domain.OnboardingRun{}, fmt.Errorf("project has no local checkout: %w", domain.ErrInvalidStatus)
	}
	snapshot, report, err := uc.Projects.GetLatestDiscovery(ctx, project.ID)
	if err != nil {
		return domain.OnboardingRun{}, err
	}
	current, err := uc.Sources.Inspect(ctx, *project.LocalPath)
	if err != nil {
		return domain.OnboardingRun{}, err
	}
	if snapshot.IsDirty || current.IsDirty {
		return domain.OnboardingRun{}, fmt.Errorf("onboarding requires a clean discovery snapshot; commit changes and scan again: %w", domain.ErrConflict)
	}
	if current.Identity != project.SourceIdentity {
		return domain.OnboardingRun{}, fmt.Errorf("repository identity changed since connection; reconnect or restore the original remote: %w", domain.ErrConflict)
	}
	if current.HeadCommit != snapshot.CommitSHA {
		return domain.OnboardingRun{}, fmt.Errorf("repository changed since discovery; scan again: %w", domain.ErrConflict)
	}
	generator := uc.Generator
	if input.Semantic {
		generator = uc.SemanticGenerator
	}
	if generator == nil {
		return domain.OnboardingRun{}, fmt.Errorf("requested onboarding generator is unavailable: %w", domain.ErrInvalidStatus)
	}
	proposal, diff, err := generator.Generate(ctx, project, snapshot, report)
	if err != nil {
		return domain.OnboardingRun{}, err
	}
	run := domain.OnboardingRun{
		ProjectID:        project.ID,
		SnapshotID:       snapshot.ID,
		Status:           domain.OnboardingStatusProposalReady,
		DryRun:           input.DryRun,
		BaseCommit:       snapshot.CommitSHA,
		BaseBranch:       snapshot.Branch,
		ProposalChecksum: proposal.Checksum,
		Proposal:         proposal,
		UnifiedDiff:      diff,
		Checks:           []domain.OnboardingCheck{},
	}
	return uc.Runs.CreateOrGet(ctx, run)
}

type Get struct {
	Runs repository.OnboardingRepository
}

func (uc Get) Handle(ctx context.Context, runID string) (domain.OnboardingRun, error) {
	runID, err := validatedRunID(runID)
	if err != nil {
		return domain.OnboardingRun{}, err
	}
	return uc.Runs.Get(ctx, runID)
}

type DecideInput struct {
	RunID   string `json:"-"`
	Actor   string `json:"actor"`
	Comment string `json:"comment,omitempty"`
}

type Approve struct {
	Runs repository.OnboardingRepository
}

func (uc Approve) Handle(ctx context.Context, input DecideInput) (domain.OnboardingRun, error) {
	runID, err := validatedRunID(input.RunID)
	if err != nil {
		return domain.OnboardingRun{}, err
	}
	return uc.Runs.Approve(ctx, runID, strings.TrimSpace(input.Actor), strings.TrimSpace(input.Comment))
}

type Reject struct {
	Runs repository.OnboardingRepository
}

func (uc Reject) Handle(ctx context.Context, input DecideInput) (domain.OnboardingRun, error) {
	runID, err := validatedRunID(input.RunID)
	if err != nil {
		return domain.OnboardingRun{}, err
	}
	return uc.Runs.Reject(ctx, runID, strings.TrimSpace(input.Actor), strings.TrimSpace(input.Comment))
}

type ApplyInput struct {
	RunID  string `json:"-"`
	DryRun bool   `json:"dry_run"`
}

type ApplyOutput struct {
	Run    domain.OnboardingRun         `json:"run"`
	Result domain.OnboardingApplyResult `json:"result"`
}

type Apply struct {
	Projects  repository.ProjectRepository
	Runs      repository.OnboardingRepository
	Worktree  repository.OnboardingWorktree
	Publisher repository.OnboardingPublisher
}

func (uc Apply) Handle(ctx context.Context, input ApplyInput) (ApplyOutput, error) {
	runID, err := validatedRunID(input.RunID)
	if err != nil {
		return ApplyOutput{}, err
	}
	run, err := uc.Runs.Get(ctx, runID)
	if err != nil {
		return ApplyOutput{}, err
	}
	project, err := uc.Projects.Get(ctx, run.ProjectID)
	if err != nil {
		return ApplyOutput{}, err
	}
	if input.DryRun {
		result, dryRunErr := uc.Worktree.DryRun(ctx, project, run)
		return ApplyOutput{Run: run, Result: result}, dryRunErr
	}
	run, err = uc.Runs.BeginApply(ctx, run.ID)
	if err != nil {
		return ApplyOutput{}, err
	}
	if run.Status == domain.OnboardingStatusCompleted {
		return ApplyOutput{Run: run, Result: resultFromRun(run)}, nil
	}
	result, err := uc.Worktree.Apply(ctx, project, run)
	if err != nil {
		_ = uc.Runs.FailApply(ctx, run.ID, err.Error())
		return ApplyOutput{}, err
	}
	if uc.Publisher != nil {
		publication, publishErr := uc.Publisher.Publish(ctx, project, run, result)
		if publishErr != nil {
			_ = uc.Runs.FailApply(ctx, run.ID, publishErr.Error())
			return ApplyOutput{}, publishErr
		}
		result.Publication = publication
		publicationCheck := domain.OnboardingCheck{Name: "gitlab_publication", Status: "skipped", Details: publication.Details}
		if publication.Published {
			publicationCheck.Status = "passed"
			publicationCheck.Details = publication.MergeRequestURL
			run, err = uc.Runs.RecordPublication(ctx, run.ID, publication)
			if err != nil {
				_ = uc.Runs.FailApply(ctx, run.ID, err.Error())
				return ApplyOutput{}, err
			}
		}
		result.Checks = append(result.Checks, publicationCheck)
	}
	run, err = uc.Runs.CompleteApply(ctx, run.ID, result)
	if err != nil {
		return ApplyOutput{}, err
	}
	return ApplyOutput{Run: run, Result: result}, nil
}

func validatedRunID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if _, err := uuid.Parse(value); err != nil {
		return "", fmt.Errorf("run ID must be a UUID: %w", domain.ErrValidation)
	}
	return value, nil
}

func resultFromRun(run domain.OnboardingRun) domain.OnboardingApplyResult {
	result := domain.OnboardingApplyResult{Checks: run.Checks}
	if run.WorktreePath != nil {
		result.WorktreePath = *run.WorktreePath
	}
	if run.BranchName != nil {
		result.BranchName = *run.BranchName
	}
	if run.CommitSHA != nil {
		result.CommitSHA = *run.CommitSHA
	}
	if run.MergeRequestURL != nil {
		result.Publication.Published = true
		result.Publication.MergeRequestURL = *run.MergeRequestURL
	}
	return result
}
