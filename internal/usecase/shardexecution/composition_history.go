package shardexecution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/usecase/agentusage"
)

func retainCompositionHistory(previous domain.ShardFanoutExecution) ([]domain.CompositionAttempt, error) {
	values := append([]domain.CompositionAttempt(nil), previous.CompositionHistory...)
	if previous.CompositionAttempt != nil {
		existing := false
		for _, value := range values {
			if value.ID == previous.CompositionAttempt.ID {
				if !sameJSON(value, *previous.CompositionAttempt) {
					return nil, fmt.Errorf("composition history identity changed: %w", domain.ErrConflict)
				}
				existing = true
			}
		}
		if !existing {
			values = append(values, *previous.CompositionAttempt)
		}
	}
	// Detach maps, slices and pointers from the previous immutable projection.
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	var result []domain.CompositionAttempt
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func compositionReplayCandidate(wp domain.CompositionWorkPackage, history []domain.CompositionAttempt) *domain.CompositionAttempt {
	for index := len(history) - 1; index >= 0; index-- {
		prior := history[index]
		if prior.Status != domain.ShardAttemptVerified || prior.CommitSHA == "" || prior.Green == nil || !prior.Green.Passed || len(prior.Green.Commands) == 0 || prior.Red == nil || !prior.Red.Semantic || prior.REDSetup == nil || prior.REDSetup.MechanicalReview != "PASS" || len(prior.REDSetup.Sources) != 1 || len(prior.Red.TestPaths) != 1 || len(prior.ChangedFiles) != 2 {
			continue
		}
		checksPassed := true
		for _, check := range prior.Green.Commands {
			if check.ExitCode != 0 {
				checksPassed = false
			}
		}
		if !checksPassed {
			continue
		}
		old, now := prior.WorkPackage, wp
		// Input provenance changes mandate a NEW attempt. Only assertion/scaffold
		// bytes may be used as fixtures when responsibilities and actual APIs match.
		old.ID, now.ID = "", ""
		old.AssemblyCommit, now.AssemblyCommit = "", ""
		old.EffectiveCommits, now.EffectiveCommits = nil, nil
		if !sameJSON(old, now) || !samePaths(prior.Red.TestPaths, wp.Verification.TestPaths) {
			continue
		}
		return &prior
	}
	return nil
}

func (s Service) replayCompositionRegression(ctx context.Context, project domain.Project, attempt *domain.CompositionAttempt, prior domain.CompositionAttempt) ([]string, error) {
	materializer, ok := s.Worktrees.(interface {
		MaterializeCompositionRegression(context.Context, domain.Project, domain.CompositionAttempt, domain.TaskWorkspace, domain.CompositionWorkPackage) (map[string]string, error)
	})
	if !ok {
		return nil, fmt.Errorf("managed composition fixture replay unavailable: %w", domain.ErrInvalidStatus)
	}
	sources, err := materializer.MaterializeCompositionRegression(ctx, project, prior, attempt.Workspace, attempt.WorkPackage)
	if err != nil {
		return nil, err
	}
	hashes := map[string]string{}
	production := []string{}
	for path, source := range sources {
		hashes[path] = contentHash([]byte(source))
		if !strings.HasSuffix(path, "_test.go") {
			production = append(production, path)
		}
	}
	if err := ValidateCompositionWiring(attempt.Workspace.Path, attempt.WorkPackage, production, true); err != nil {
		return nil, err
	}
	attempt.FixtureReplay = &domain.CompositionFixtureReplay{PriorAttemptID: prior.ID, PriorCommit: prior.CommitSHA, PriorWorkPackageID: prior.WorkPackage.ID, NewWorkPackageID: attempt.WorkPackage.ID, RecordedAt: s.now(), SourceHashes: hashes}
	return production, nil
}

func isCompositionBudgetDenial(err error) bool {
	return errors.Is(err, domain.ErrApprovalNeeded) && strings.Contains(err.Error(), "deep-model five-hour budget is exhausted")
}

func (s Service) recordCompositionBudgetDenial(ctx context.Context, attempt *domain.CompositionAttempt, phase string, err error) {
	if !isCompositionBudgetDenial(err) {
		return
	}
	remaining := 3
	if phase == "semantic RED test-only" {
		remaining = 2
	}
	if phase == "wiring implementation after semantic RED" {
		remaining = 1
	}
	evidence := &domain.CompositionBudgetBlocker{RecordedAt: s.now(), Phase: phase, Reason: "BUDGET_EXHAUSTED; owner approval required before any further agent admission", RemainingCompositionAgentCalls: remaining, AdditionalRunsRequired: remaining + 1}
	var runner agentusage.TrackedRunner
	switch value := s.Runner.(type) {
	case agentusage.TrackedRunner:
		runner = value
	case *agentusage.TrackedRunner:
		runner = *value
	default:
		attempt.BudgetBlocker = evidence
		return
	}
	evidence.Mode = runner.BudgetMode
	evidence.Limit = runner.DeepRunLimit
	if runner.Usage != nil {
		window, readErr := runner.Usage.AgentUsageWindow(ctx, s.now().Add(-5*time.Hour))
		if readErr == nil {
			evidence.CountAvailable = true
			for _, value := range window.ByModel {
				if value.Key == runner.DeepModel {
					evidence.FiveHourRuns = value.Runs
				}
			}
		}
	}
	attempt.BudgetBlocker = evidence
}
