package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/agentpolicy"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type Service struct {
	Repository        repository.TaskExecutionRepository
	Worktrees         repository.TaskWorktree
	Runner            repository.AgentRunner
	Validator         repository.AgentResultValidator
	Verifier          Verifier
	Models            map[string]string
	Reasoning         map[string]string
	ReviewModel       string
	ReviewReasoning   string
	Router            agentpolicy.Router
	MaxTaskAttempts   int
	MaxReviewAttempts int
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
	if executionContext.Project.Status == domain.ProjectStatusArchived {
		return domain.TaskExecutionOutcome{}, fmt.Errorf("archived project task cannot execute; restore it first: %w", domain.ErrInvalidStatus)
	}
	attempts, err := s.Repository.ListAttempts(ctx, taskID)
	if err != nil {
		return domain.TaskExecutionOutcome{}, err
	}
	retryFeedback := retryFeedbackFromAttempts(attempts)
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
	feedback := retryFeedback
	replaceThreadID := ""
	nextReview := attempt.ReviewCount + 1
	for {
		prompt, err := coderPrompt(executionContext, feedback)
		if err != nil {
			return domain.TaskExecutionOutcome{}, err
		}
		coderRoute := s.Router.Coder(executionContext.Task)
		if coderRoute.Model == "" {
			coderRoute = agentpolicy.Decision{Model: s.Models[executionContext.Task.ModelProfile], Reasoning: s.Reasoning[executionContext.Task.ModelProfile], Reason: "legacy task profile"}
		}
		response, err := s.Runner.Run(ctx, domain.AgentRunRequest{
			Role: domain.AgentRunCoder, ThreadID: threadID, WorkingDirectory: workspace.Path,
			Model: coderRoute.Model, ReasoningEffort: coderRoute.Reasoning, Prompt: prompt,
			OutputSchema: s.Validator.AgentSchema(),
			UsageContext: &domain.AgentUsageContext{ResourceType: "task", ResourceID: executionContext.Task.ID, RouteReason: coderRoute.Reason},
		}, func(callbackContext context.Context, discoveredThreadID string) error {
			var stored domain.TaskAttempt
			var attachErr error
			if replaceThreadID != "" {
				stored, attachErr = s.Repository.ReplaceAgentThread(callbackContext, attempt.ID, replaceThreadID, discoveredThreadID)
			} else {
				stored, attachErr = s.Repository.AttachAgentThread(callbackContext, attempt.ID, discoveredThreadID)
			}
			if attachErr == nil && stored.AgentThreadID != nil {
				threadID = *stored.AgentThreadID
				replaceThreadID = ""
			}
			return attachErr
		})
		if err != nil {
			message := "coder runner failed: " + err.Error()
			if failErr := s.Repository.FailAttempt(ctx, attempt.ID, domain.TaskAttemptStatusFailed, message, nil); failErr != nil {
				return domain.TaskExecutionOutcome{}, failErr
			}
			return outcome(taskID, domain.TaskStatusFailed, message), nil
		}
		if threadID == "" || response.ThreadID != threadID {
			return domain.TaskExecutionOutcome{}, fmt.Errorf("coder thread was not durably attached: %w", domain.ErrConflict)
		}
		result, err := s.Validator.ValidateAgentResult(response.Result)
		if err != nil {
			structured := map[string]any{"raw_agent_result": json.RawMessage(response.Result)}
			if failErr := s.Repository.FailAttempt(ctx, attempt.ID, domain.TaskAttemptStatusFailed, err.Error(), structured); failErr != nil {
				return domain.TaskExecutionOutcome{}, fmt.Errorf("persist invalid coder result after %v: %w", err, failErr)
			}
			return outcome(taskID, domain.TaskStatusFailed, err.Error()), nil
		}
		if promoted, ok := promoteVerifiableBlockedResult(result, executionContext.Task); ok {
			result = promoted
		}

		switch result.Status {
		case domain.AgentResultBlocked:
			message := strings.Join(result.Blockers, "; ")
			if len(result.RequiredTasks) > 0 {
				if message != "" {
					message += "; "
				}
				message += "обнаружены новые обязательные задачи; требуется новый plan fingerprint, обсуждение и одобрение"
			}
			if err := s.Repository.FailAttempt(ctx, attempt.ID, domain.TaskAttemptStatusBlocked, message, result); err != nil {
				return domain.TaskExecutionOutcome{}, err
			}
			return outcome(taskID, domain.TaskStatusBlocked, message), nil
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
		reviewRoute := s.Router.Reviewer(executionContext.Task)
		if reviewRoute.Model == "" {
			reviewRoute = agentpolicy.Decision{Model: s.ReviewModel, Reasoning: s.ReviewReasoning, Reason: "legacy review profile"}
		}
		reviewResponse, err := s.Runner.Run(ctx, domain.AgentRunRequest{
			Role: domain.AgentRunReviewer, WorkingDirectory: workspace.Path,
			Model: reviewRoute.Model, ReasoningEffort: reviewRoute.Reasoning,
			Prompt: reviewPrompt, OutputSchema: s.Validator.ReviewerSchema(),
			UsageContext: &domain.AgentUsageContext{ResourceType: "task", ResourceID: executionContext.Task.ID, RouteReason: reviewRoute.Reason},
		}, func(callbackContext context.Context, discoveredThreadID string) error {
			reviewThreadID = discoveredThreadID
			_, beginErr := s.Repository.BeginReview(callbackContext, attempt.ID, nextReview, discoveredThreadID)
			return beginErr
		})
		if err != nil {
			message := "reviewer runner failed: " + err.Error()
			if failErr := s.Repository.FailAttempt(ctx, attempt.ID, domain.TaskAttemptStatusFailed, message, result); failErr != nil {
				return domain.TaskExecutionOutcome{}, failErr
			}
			return outcome(taskID, domain.TaskStatusFailed, message), nil
		}
		if reviewThreadID == "" || reviewThreadID == threadID || reviewResponse.ThreadID != reviewThreadID {
			return domain.TaskExecutionOutcome{}, fmt.Errorf("reviewer did not use a separate durable thread: %w", domain.ErrConflict)
		}
		review, err := s.Validator.ValidateReviewerResult(reviewResponse.Result)
		if err != nil {
			structured := map[string]any{"agent_result": result, "raw_reviewer_result": json.RawMessage(reviewResponse.Result)}
			if failErr := s.Repository.FailAttempt(ctx, attempt.ID, domain.TaskAttemptStatusFailed, err.Error(), structured); failErr != nil {
				return domain.TaskExecutionOutcome{}, fmt.Errorf("persist invalid reviewer result after %v: %w", err, failErr)
			}
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
			replaceThreadID = threadID
			threadID = ""
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

func retryFeedbackFromAttempts(attempts []domain.TaskAttempt) string {
	for index := len(attempts) - 1; index >= 0; index-- {
		attempt := attempts[index]
		if attempt.Status == domain.TaskAttemptStatusRunning ||
			attempt.Status == domain.TaskAttemptStatusVerification ||
			attempt.Status == domain.TaskAttemptStatusReview ||
			attempt.Status == domain.TaskAttemptStatusCompleted {
			continue
		}
		structured := strings.TrimSpace(string(attempt.StructuredResult))
		if structured != "" && structured != "{}" && structured != "null" {
			return fmt.Sprintf(
				"A previous task attempt ended with status %q. Address its persisted result before returning the complete coder result again:\n%s",
				attempt.Status,
				structured,
			)
		}
		if attempt.Error != nil && strings.TrimSpace(*attempt.Error) != "" {
			return fmt.Sprintf(
				"A previous task attempt ended with status %q. Re-check and address this persisted failure before returning the complete coder result again:\n%s",
				attempt.Status,
				strings.TrimSpace(*attempt.Error),
			)
		}
	}
	return ""
}

func promoteVerifiableBlockedResult(result domain.AgentResult, task domain.Task) (domain.AgentResult, bool) {
	if result.Status != domain.AgentResultBlocked && result.Status != domain.AgentResultFailed {
		return result, false
	}
	if len(result.RequiredTasks) != 0 ||
		len(result.FilesChanged) == 0 || len(result.Blockers) == 0 {
		return result, false
	}
	allowed := map[string]struct{}{"git diff --check": {}}
	for _, command := range task.VerificationCommands {
		allowed[command] = struct{}{}
	}
	hasUnsuccessfulCheck := false
	for _, check := range result.Checks {
		if check.Status == domain.AgentCheckPassed {
			continue
		}
		if _, ok := allowed[check.Name]; !ok {
			return result, false
		}
		hasUnsuccessfulCheck = true
	}
	if !hasUnsuccessfulCheck {
		return result, false
	}

	blockers := append([]string(nil), result.Blockers...)
	result.Status = domain.AgentResultCompleted
	result.Blockers = nil
	for index := range result.Checks {
		if result.Checks[index].Status != domain.AgentCheckPassed {
			result.Checks[index].Status = domain.AgentCheckSkipped
			result.Checks[index].Details = "Coder sandbox could not complete this check; the independent verifier must decide it: " + result.Checks[index].Details
		}
	}
	result.NotesForReviewer = append(result.NotesForReviewer,
		"The coder reported a verification-only sandbox blocker. The orchestrator promoted the result only for independent verification: "+strings.Join(blockers, "; "))
	return result, true
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
		Command      string                             `json:"command"`
		PlanSummary  string                             `json:"plan_summary"`
		Project      domain.Project                     `json:"current_project"`
		Task         domain.Task                        `json:"task"`
		PlanTasks    []domain.Task                      `json:"approved_plan_tasks"`
		Projects     []domain.ConnectedProjectKnowledge `json:"connected_projects"`
		Topology     agentLandscape                     `json:"connected_landscape"`
		Dependencies []domain.TaskDependencyRef         `json:"dependencies"`
	}{
		Command: executionContext.Command.Text, PlanSummary: executionContext.Plan.Summary,
		Project: executionContext.Project, Task: executionContext.Task, PlanTasks: executionContext.PlanTasks,
		Projects:     executionContext.ConnectedProjects,
		Topology:     landscapeForAgent(executionContext.Topology),
		Dependencies: executionContext.Dependencies,
	}
	content, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode coder context: %w", err)
	}
	prompt := `You are the coder for exactly one persisted task in an isolated Git worktree.
Read and obey AGENTS.md plus relevant .ai/prompts and .ai/contracts files in this repository.
Use connected_landscape as the shared cross-project index of services, capabilities, ownership, contracts, relations, and drift.
Use connected_projects to identify every connected runtime and knowledge repository, including policy, documentation, content, and archive sources.
Treat its evidence paths as leads, verify assumptions against the current worktree, and never invent an undiscovered contract or business rule.
Implement only this task. Never edit outside task.write_scope. Do not commit, create branches, push, or modify another checkout.
Run only task.verification_commands and "git diff --check". Inspect actual Git status before answering.
Return only JSON matching the supplied schema. files_changed must exactly match actual changed and untracked files.
For each independently reproducible check, use its exact command as checks[].name. Do not claim checks you did not run.
If another repository must change first, return status blocked and a minimal required_tasks entry for that connected service.
required_tasks means strict prerequisites without which this current task cannot satisfy its acceptance criteria. It does not mean later rollout work.
If required_tasks is non-empty, status MUST be blocked. Never return completed together with required_tasks.

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
		Project      domain.Project                     `json:"current_project"`
		Task         domain.Task                        `json:"task"`
		PlanTasks    []domain.Task                      `json:"approved_plan_tasks"`
		Projects     []domain.ConnectedProjectKnowledge `json:"connected_projects"`
		Topology     agentLandscape                     `json:"connected_landscape"`
		CoderResult  domain.AgentResult                 `json:"coder_result"`
		Verification domain.VerificationReport          `json:"verification"`
		GitState     domain.WorkspaceState              `json:"git_state"`
	}{executionContext.Project, executionContext.Task, executionContext.PlanTasks, executionContext.ConnectedProjects,
		landscapeForAgent(executionContext.Topology), result, report, state}
	content, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode reviewer context: %w", err)
	}
	return `You are an independent reviewer in a new, read-only agent thread.
Do not edit files and do not accept the coder's claims without checking the actual worktree.
Review the Git diff, untracked files, acceptance criteria, write scope, tests, migration safety, and contract changes.
Cross-check affected services and contracts against connected_landscape; require a bounded cross-project task when another repository must change.
Treat a new task as blocking only when it is a prerequisite for the current task's acceptance criteria. Do not block on later rollout work,
and do not request work that is already represented in approved_plan_tasks.
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
	if s.MaxTaskAttempts < 1 || s.MaxTaskAttempts > 8 {
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
