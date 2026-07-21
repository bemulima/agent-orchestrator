package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type Service struct {
	Repository           repository.TaskExecutionRepository
	Worktrees            repository.TaskWorktree
	Runner               repository.AgentRunner
	Validator            repository.AgentResultValidator
	Verifier             Verifier
	Models               map[string]string
	ReviewModel          string
	MaxTaskAttempts      int
	MaxReviewAttempts    int
	MaxReplans           int
	MaxRequiredTaskDepth int
}

func (s Service) Execute(
	ctx context.Context,
	taskID, workflowID string,
) (domain.TaskExecutionOutcome, error) {
	if s.Repository == nil || s.Worktrees == nil || s.Runner == nil || s.Validator == nil {
		return domain.TaskExecutionOutcome{}, fmt.Errorf("task execution service is incomplete: %w", domain.ErrInvalidStatus)
	}
	executionContext, err := s.Repository.GetExecutionContext(ctx, taskID)
	if err != nil {
		return domain.TaskExecutionOutcome{}, err
	}
	workspace, err := s.Worktrees.Prepare(ctx, executionContext.Project, executionContext.Task)
	if err != nil {
		return domain.TaskExecutionOutcome{}, err
	}
	attempt, err := s.Repository.BeginAttempt(ctx, taskID, workflowID, workspace, s.maxTaskAttempts())
	if err != nil {
		return domain.TaskExecutionOutcome{}, err
	}
	if terminal, ok := terminalAttemptOutcome(attempt); ok {
		return terminal, nil
	}

	threadID := ""
	if attempt.AgentThreadID != nil {
		threadID = *attempt.AgentThreadID
	}
	feedback := ""
	nextReview := attempt.ReviewCount + 1
	for {
		prompt, err := coderPrompt(executionContext, feedback)
		if err != nil {
			return domain.TaskExecutionOutcome{}, err
		}
		response, err := s.Runner.Run(ctx, domain.AgentRunRequest{
			Role: domain.AgentRunCoder, ThreadID: threadID, WorkingDirectory: workspace.Path,
			Model: s.Models[executionContext.Task.ModelProfile], Prompt: prompt,
			OutputSchema: s.Validator.AgentSchema(),
		}, func(callbackContext context.Context, discoveredThreadID string) error {
			stored, attachErr := s.Repository.AttachAgentThread(callbackContext, attempt.ID, discoveredThreadID)
			if attachErr == nil && stored.AgentThreadID != nil {
				threadID = *stored.AgentThreadID
			}
			return attachErr
		})
		if err != nil {
			return domain.TaskExecutionOutcome{}, err
		}
		if threadID == "" || response.ThreadID != threadID {
			return domain.TaskExecutionOutcome{}, fmt.Errorf("coder thread was not durably attached: %w", domain.ErrConflict)
		}
		result, err := s.Validator.ValidateAgentResult(response.Result)
		if err != nil {
			_ = s.Repository.FailAttempt(ctx, attempt.ID, domain.TaskAttemptStatusFailed, err.Error(), nil)
			return outcome(taskID, domain.TaskStatusFailed, err.Error()), nil
		}

		switch result.Status {
		case domain.AgentResultBlocked:
			if len(result.RequiredTasks) == 0 {
				message := strings.Join(result.Blockers, "; ")
				if err := s.Repository.FailAttempt(ctx, attempt.ID, domain.TaskAttemptStatusBlocked, message, result); err != nil {
					return domain.TaskExecutionOutcome{}, err
				}
				return outcome(taskID, domain.TaskStatusBlocked, message), nil
			}
			schedule, scheduleErr := s.Repository.AddRequiredTasks(
				ctx, taskID, result.RequiredTasks, s.maxRequiredTaskDepth(), s.maxReplans(),
			)
			if scheduleErr != nil {
				_ = s.Repository.FailAttempt(ctx, attempt.ID, domain.TaskAttemptStatusFailed, scheduleErr.Error(), result)
				return outcome(taskID, domain.TaskStatusFailed, scheduleErr.Error()), nil
			}
			if err := s.Repository.FailAttempt(ctx, attempt.ID, domain.TaskAttemptStatusBlocked, strings.Join(result.Blockers, "; "), result); err != nil {
				return domain.TaskExecutionOutcome{}, err
			}
			blocked := outcome(taskID, domain.TaskStatusBlocked, strings.Join(result.Blockers, "; "))
			blocked.RequiredSchedule = &schedule
			return blocked, nil
		case domain.AgentResultFailed:
			message := result.Summary
			if err := s.Repository.FailAttempt(ctx, attempt.ID, domain.TaskAttemptStatusFailed, message, result); err != nil {
				return domain.TaskExecutionOutcome{}, err
			}
			return outcome(taskID, domain.TaskStatusFailed, message), nil
		case domain.AgentResultChangesRequired:
			message := result.Summary
			if err := s.Repository.FailAttempt(ctx, attempt.ID, domain.TaskAttemptStatusChangesRequested, message, result); err != nil {
				return domain.TaskExecutionOutcome{}, err
			}
			return outcome(taskID, domain.TaskStatusChangesRequested, message), nil
		case domain.AgentResultCompleted:
		default:
			return domain.TaskExecutionOutcome{}, fmt.Errorf("unsupported coder status %q: %w", result.Status, domain.ErrValidation)
		}

		if err := s.Repository.SetAttemptStatus(ctx, attempt.ID, domain.TaskAttemptStatusVerification); err != nil {
			return domain.TaskExecutionOutcome{}, err
		}
		verifier := s.Verifier
		if verifier.Worktrees == nil {
			verifier.Worktrees = s.Worktrees
		}
		report, state, verificationErr := verifier.Verify(ctx, executionContext, workspace, result)
		if verificationErr != nil {
			structured := map[string]any{"agent_result": result, "verification": report}
			if err := s.Repository.FailAttempt(ctx, attempt.ID, domain.TaskAttemptStatusChangesRequested, verificationErr.Error(), structured); err != nil {
				return domain.TaskExecutionOutcome{}, err
			}
			return outcome(taskID, domain.TaskStatusChangesRequested, verificationErr.Error()), nil
		}

		if nextReview > s.maxReviewAttempts() {
			message := fmt.Sprintf("task reached maximum of %d reviews", s.maxReviewAttempts())
			if err := s.Repository.FailAttempt(ctx, attempt.ID, domain.TaskAttemptStatusChangesRequested, message, result); err != nil {
				return domain.TaskExecutionOutcome{}, err
			}
			return outcome(taskID, domain.TaskStatusChangesRequested, message), nil
		}
		reviewPrompt, err := reviewerPrompt(executionContext, result, report, state)
		if err != nil {
			return domain.TaskExecutionOutcome{}, err
		}
		reviewThreadID := ""
		reviewResponse, err := s.Runner.Run(ctx, domain.AgentRunRequest{
			Role: domain.AgentRunReviewer, WorkingDirectory: workspace.Path,
			Model: s.ReviewModel, Prompt: reviewPrompt, OutputSchema: s.Validator.ReviewerSchema(),
		}, func(callbackContext context.Context, discoveredThreadID string) error {
			reviewThreadID = discoveredThreadID
			_, beginErr := s.Repository.BeginReview(callbackContext, attempt.ID, nextReview, discoveredThreadID)
			return beginErr
		})
		if err != nil {
			return domain.TaskExecutionOutcome{}, err
		}
		if reviewThreadID == "" || reviewThreadID == threadID || reviewResponse.ThreadID != reviewThreadID {
			return domain.TaskExecutionOutcome{}, fmt.Errorf("reviewer did not use a separate durable thread: %w", domain.ErrConflict)
		}
		review, err := s.Validator.ValidateReviewerResult(reviewResponse.Result)
		if err != nil {
			_ = s.Repository.FailAttempt(ctx, attempt.ID, domain.TaskAttemptStatusFailed, err.Error(), result)
			return outcome(taskID, domain.TaskStatusFailed, err.Error()), nil
		}
		if _, err := s.Repository.CreateReview(ctx, attempt.ID, nextReview, reviewThreadID, review); err != nil {
			return domain.TaskExecutionOutcome{}, err
		}
		if review.Status == domain.ReviewChangesRequested {
			if nextReview >= s.maxReviewAttempts() {
				message := "review changes remain after the maximum review count"
				if err := s.Repository.FailAttempt(ctx, attempt.ID, domain.TaskAttemptStatusChangesRequested, message, review); err != nil {
					return domain.TaskExecutionOutcome{}, err
				}
				return outcome(taskID, domain.TaskStatusChangesRequested, message), nil
			}
			if err := s.Repository.SetAttemptStatus(ctx, attempt.ID, domain.TaskAttemptStatusChangesRequested); err != nil {
				return domain.TaskExecutionOutcome{}, err
			}
			feedbackContent, _ := json.Marshal(review)
			feedback = "A separate reviewer requested changes. Address every blocking issue, re-run the allowed checks, and return the complete coder result again:\n" + string(feedbackContent)
			nextReview++
			continue
		}

		commitSHA, err := s.Worktrees.Commit(ctx, executionContext.Project, executionContext.Task, workspace, state.ChangedFiles)
		if err != nil {
			return domain.TaskExecutionOutcome{}, err
		}
		if err := s.storeArtifacts(ctx, attempt, workspace, result.Artifacts); err != nil {
			return domain.TaskExecutionOutcome{}, err
		}
		if _, err := s.Repository.CompleteAttempt(ctx, attempt.ID, result, report, commitSHA); err != nil {
			return domain.TaskExecutionOutcome{}, err
		}
		return outcome(taskID, domain.TaskStatusCompleted, ""), nil
	}
}

func (s Service) storeArtifacts(
	ctx context.Context,
	attempt domain.TaskAttempt,
	workspace domain.TaskWorkspace,
	claims []domain.AgentArtifactClaim,
) error {
	for _, claim := range claims {
		content, err := s.Worktrees.ReadArtifact(ctx, workspace, claim.Path, maxArtifactBytes)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(content)
		metadata, _ := json.Marshal(map[string]any{"path": claim.Path, "attempt_id": attempt.ID})
		if _, err := s.Repository.StoreArtifact(ctx, domain.Artifact{
			TaskID: attempt.TaskID, Type: claim.Type, Name: claim.Name,
			URI:      "task-worktree://" + attempt.ID + "/" + claim.Path,
			Checksum: hex.EncodeToString(digest[:]), Metadata: metadata,
		}); err != nil {
			return err
		}
	}
	return nil
}

func coderPrompt(executionContext domain.TaskExecutionContext, feedback string) (string, error) {
	payload := struct {
		Command      string                     `json:"command"`
		PlanSummary  string                     `json:"plan_summary"`
		Task         domain.Task                `json:"task"`
		Dependencies []domain.TaskDependencyRef `json:"dependencies"`
	}{
		Command: executionContext.Command.Text, PlanSummary: executionContext.Plan.Summary,
		Task: executionContext.Task, Dependencies: executionContext.Dependencies,
	}
	content, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode coder context: %w", err)
	}
	prompt := `You are the coder for exactly one persisted task in an isolated Git worktree.
Read and obey AGENTS.md plus relevant .ai/prompts and .ai/contracts files in this repository.
Implement only this task. Never edit outside task.write_scope. Do not commit, create branches, push, or modify another checkout.
Run only task.verification_commands and "git diff --check". Inspect actual Git status before answering.
Return only JSON matching the supplied schema. files_changed must exactly match actual changed and untracked files.
For each independently reproducible check, use its exact command as checks[].name. Do not claim checks you did not run.
If another repository must change first, return status blocked and a minimal required_tasks entry for that connected service.

Persisted context:
` + string(content)
	if feedback != "" {
		prompt += "\n\nRetry feedback:\n" + feedback
	}
	return prompt, nil
}

func reviewerPrompt(
	executionContext domain.TaskExecutionContext,
	result domain.AgentResult,
	report domain.VerificationReport,
	state domain.WorkspaceState,
) (string, error) {
	if len(state.Diff) > 256<<10 {
		state.Diff = state.Diff[:256<<10] + "\n[diff truncated; inspect the worktree directly]"
	}
	payload := struct {
		Task         domain.Task               `json:"task"`
		CoderResult  domain.AgentResult        `json:"coder_result"`
		Verification domain.VerificationReport `json:"verification"`
		GitState     domain.WorkspaceState     `json:"git_state"`
	}{executionContext.Task, result, report, state}
	content, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode reviewer context: %w", err)
	}
	return `You are an independent reviewer in a new, read-only agent thread.
Do not edit files and do not accept the coder's claims without checking the actual worktree.
Review the Git diff, untracked files, acceptance criteria, write scope, tests, migration safety, and contract changes.
Return only JSON matching the supplied reviewer schema. Approve only when no blocking issue remains.

Review context:
` + string(content), nil
}

func terminalAttemptOutcome(attempt domain.TaskAttempt) (domain.TaskExecutionOutcome, bool) {
	switch attempt.Status {
	case domain.TaskAttemptStatusCompleted:
		return outcome(attempt.TaskID, domain.TaskStatusCompleted, ""), true
	case domain.TaskAttemptStatusFailed:
		return outcome(attempt.TaskID, domain.TaskStatusFailed, errorValue(attempt.Error)), true
	case domain.TaskAttemptStatusBlocked:
		return outcome(attempt.TaskID, domain.TaskStatusBlocked, errorValue(attempt.Error)), true
	case domain.TaskAttemptStatusCancelled:
		return outcome(attempt.TaskID, domain.TaskStatusCancelled, errorValue(attempt.Error)), true
	default:
		return domain.TaskExecutionOutcome{}, false
	}
}

func outcome(taskID string, status domain.TaskStatus, message string) domain.TaskExecutionOutcome {
	return domain.TaskExecutionOutcome{Result: domain.TaskResult{TaskID: taskID, Status: status, Error: message}}
}

func errorValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s Service) maxTaskAttempts() int {
	if s.MaxTaskAttempts < 1 || s.MaxTaskAttempts > 3 {
		return 3
	}
	return s.MaxTaskAttempts
}

func (s Service) maxReviewAttempts() int {
	if s.MaxReviewAttempts < 1 || s.MaxReviewAttempts > 2 {
		return 2
	}
	return s.MaxReviewAttempts
}

func (s Service) maxReplans() int {
	if s.MaxReplans < 0 || s.MaxReplans > 2 {
		return 2
	}
	return s.MaxReplans
}

func (s Service) maxRequiredTaskDepth() int {
	if s.MaxRequiredTaskDepth < 1 || s.MaxRequiredTaskDepth > 10 {
		return 3
	}
	return s.MaxRequiredTaskDepth
}
