package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bemulima/agent-orchestrator/internal/agent"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

func TestServiceCompletesFixtureWithSeparateReviewerThread(t *testing.T) {
	validator, err := agent.NewValidator()
	require.NoError(t, err)
	repo := newFakeExecutionRepository()
	worktrees := successfulExecutionWorktree()
	runner := &sequenceRunner{results: []json.RawMessage{completedCoderResult(), approvedReviewResult()}}
	service := fixtureService(repo, worktrees, runner, validator)

	result, err := service.Execute(context.Background(), "task-1", "workflow-1")
	require.NoError(t, err)
	require.Equal(t, domain.TaskStatusCompleted, result.Result.Status)
	require.True(t, repo.completed)
	require.True(t, worktrees.committed)
	require.Equal(t, []domain.AgentRunRole{domain.AgentRunCoder, domain.AgentRunReviewer}, runner.roles)
	require.Contains(t, runner.prompts[0], `"connected_landscape"`)
	require.Contains(t, runner.prompts[0], "orders-service")
	require.Contains(t, runner.prompts[1], "orders-service")
	require.Contains(t, runner.prompts[0], "course-wiki")
	require.NotContains(t, runner.prompts[0], "raw-contract-shape")
	require.Equal(t, "coder-thread", repo.coderThread)
	require.Equal(t, []string{"review-thread-1"}, repo.reviewThreads)
	require.NotEqual(t, repo.coderThread, repo.reviewThreads[0])
}

func TestServiceResumesCoderAndUsesANewReviewerThread(t *testing.T) {
	validator, err := agent.NewValidator()
	require.NoError(t, err)
	repo := newFakeExecutionRepository()
	worktrees := successfulExecutionWorktree()
	runner := &sequenceRunner{results: []json.RawMessage{
		completedCoderResult(), changesReviewResult(), completedCoderResult(), approvedReviewResult(),
	}}
	service := fixtureService(repo, worktrees, runner, validator)

	result, err := service.Execute(context.Background(), "task-1", "workflow-1")
	require.NoError(t, err)
	require.Equal(t, domain.TaskStatusCompleted, result.Result.Status)
	require.Equal(t, []string{"", "", "coder-thread", ""}, runner.requestThreads)
	require.Equal(t, []string{"review-thread-1", "review-thread-2"}, repo.reviewThreads)
}

func TestServiceSchedulesBoundedRequiredTask(t *testing.T) {
	validator, err := agent.NewValidator()
	require.NoError(t, err)
	repo := newFakeExecutionRepository()
	worktrees := successfulExecutionWorktree()
	runner := &sequenceRunner{results: []json.RawMessage{json.RawMessage(`{
  "status":"blocked","summary":"dependency required","files_changed":[],"checks":[],"artifacts":[],
  "blockers":["orders API is missing"],
  "required_tasks":[{"service":"orders","role":"implementation","title":"add API","description":"add endpoint","reason":"consumer needs it","acceptance_criteria":["endpoint exists"]}],
  "risks":[],"notes_for_reviewer":[]
}`)}}
	service := fixtureService(repo, worktrees, runner, validator)

	result, err := service.Execute(context.Background(), "task-1", "workflow-1")
	require.NoError(t, err)
	require.Equal(t, domain.TaskStatusBlocked, result.Result.Status)
	require.NotNil(t, result.RequiredSchedule)
	require.Equal(t, []string{"required-task"}, result.RequiredSchedule.ParentDependencies)
	require.Equal(t, domain.TaskAttemptStatusBlocked, repo.failedStatus)
}

func TestServiceStopsAfterMaximumReviewerChanges(t *testing.T) {
	validator, err := agent.NewValidator()
	require.NoError(t, err)
	repo := newFakeExecutionRepository()
	worktrees := successfulExecutionWorktree()
	runner := &sequenceRunner{results: []json.RawMessage{
		completedCoderResult(), changesReviewResult(), completedCoderResult(), changesReviewResult(),
	}}
	service := fixtureService(repo, worktrees, runner, validator)

	result, err := service.Execute(context.Background(), "task-1", "workflow-1")
	require.NoError(t, err)
	require.Equal(t, domain.TaskStatusChangesRequested, result.Result.Status)
	require.Equal(t, domain.TaskAttemptStatusChangesRequested, repo.failedStatus)
	require.False(t, repo.completed)
	require.False(t, worktrees.committed)
	require.Len(t, repo.reviewThreads, 2)
}

func fixtureService(
	repo *fakeExecutionRepository,
	worktrees *fakeWorktree,
	runner repository.AgentRunner,
	validator repository.AgentResultValidator,
) Service {
	return Service{
		Repository: repo, Worktrees: worktrees, Runner: runner, Validator: validator,
		Verifier: Verifier{Worktrees: worktrees}, Models: map[string]string{"standard": "fixture-model"},
		ReviewModel: "fixture-review", MaxTaskAttempts: 3, MaxReviewAttempts: 2,
		MaxReplans: 2, MaxRequiredTaskDepth: 3,
	}
}

func successfulExecutionWorktree() *fakeWorktree {
	return &fakeWorktree{
		state: domain.WorkspaceState{ChangedFiles: []string{"internal/order.go"}, Diff: "+fixture"},
		checks: map[string]domain.WorkspaceCheckResult{
			"git diff --check": {Command: "git diff --check"},
			"go test ./...":    {Command: "go test ./..."},
		},
	}
}

func completedCoderResult() json.RawMessage {
	return json.RawMessage(`{
  "status":"completed","summary":"implemented","files_changed":["internal/order.go"],
  "checks":[{"name":"go test ./...","status":"passed","details":"ok"}],
  "artifacts":[],"blockers":[],"required_tasks":[],"risks":[],"notes_for_reviewer":[]
}`)
}

func approvedReviewResult() json.RawMessage {
	return json.RawMessage(`{
  "status":"approved","summary":"looks good","blocking_issues":[],"non_blocking_issues":[],"risks":[],"suggested_checks":[]
}`)
}

func changesReviewResult() json.RawMessage {
	return json.RawMessage(`{
  "status":"changes_requested","summary":"fix edge case",
  "blocking_issues":[{"path":"internal/order.go","line":1,"message":"handle empty input"}],
  "non_blocking_issues":[],"risks":[],"suggested_checks":["go test ./..."]
}`)
}

type sequenceRunner struct {
	results        []json.RawMessage
	roles          []domain.AgentRunRole
	requestThreads []string
	prompts        []string
	coderThread    string
	reviewCount    int
}

func (r *sequenceRunner) Run(
	ctx context.Context,
	request domain.AgentRunRequest,
	callback repository.AgentThreadCallback,
) (domain.AgentRunResponse, error) {
	if len(r.results) == 0 {
		return domain.AgentRunResponse{}, fmt.Errorf("no fixture runner result")
	}
	r.roles = append(r.roles, request.Role)
	r.requestThreads = append(r.requestThreads, request.ThreadID)
	r.prompts = append(r.prompts, request.Prompt)
	threadID := request.ThreadID
	if request.Role == domain.AgentRunCoder && threadID == "" {
		threadID = "coder-thread"
		r.coderThread = threadID
	}
	if request.Role == domain.AgentRunReviewer {
		r.reviewCount++
		threadID = fmt.Sprintf("review-thread-%d", r.reviewCount)
	}
	if err := callback(ctx, threadID); err != nil {
		return domain.AgentRunResponse{}, err
	}
	result := r.results[0]
	r.results = r.results[1:]
	return domain.AgentRunResponse{ThreadID: threadID, Result: result}, nil
}

type fakeExecutionRepository struct {
	executionContext domain.TaskExecutionContext
	attempt          domain.TaskAttempt
	coderThread      string
	reviewThreads    []string
	completed        bool
	failedStatus     domain.TaskAttemptStatus
}

func newFakeExecutionRepository() *fakeExecutionRepository {
	return &fakeExecutionRepository{
		executionContext: domain.TaskExecutionContext{
			Task: domain.Task{
				ID: "task-1", ProjectID: "project-1", PlanID: "plan-1", Title: "fixture task",
				ModelProfile: "standard", WriteScope: []string{"internal/**"},
				VerificationCommands: []string{"go test ./..."}, AcceptanceCriteria: []string{"works"},
			},
			Project: domain.Project{ID: "project-1", Name: "fixture", HeadCommit: "base"},
			Plan:    domain.Plan{ID: "plan-1", Summary: "fixture plan"},
			Command: domain.Command{ID: "command-1", Text: "change fixture"},
			Topology: domain.TopologyCatalog{
				Revision: domain.TopologyRevision{ID: "topology-1"},
				Services: []domain.TopologyService{{ProjectID: "project-2", Name: "orders-service"}},
				Contracts: []domain.Contract{{
					ProjectID: "project-2", Code: "http:get:/orders", Type: domain.ContractTypeHTTP,
					Definition: json.RawMessage(`{"detail":"raw-contract-shape"}`), SourcePath: "openapi/orders.yaml",
				}},
			},
			ConnectedProjects: []domain.ConnectedProjectKnowledge{{
				ProjectID: "project-docs", Name: "course-wiki", RepositoryRole: domain.RepositoryRoleDocumentation,
				Purpose: "Platform business documentation",
			}},
		},
		attempt: domain.TaskAttempt{ID: "attempt-1", TaskID: "task-1", AttemptNumber: 1, Status: domain.TaskAttemptStatusRunning},
	}
}

func (r *fakeExecutionRepository) GetExecutionContext(context.Context, string) (domain.TaskExecutionContext, error) {
	return r.executionContext, nil
}
func (r *fakeExecutionRepository) BeginAttempt(context.Context, string, string, domain.TaskWorkspace, int) (domain.TaskAttempt, error) {
	return r.attempt, nil
}
func (r *fakeExecutionRepository) AttachAgentThread(_ context.Context, _, threadID string) (domain.TaskAttempt, error) {
	r.coderThread = threadID
	r.attempt.AgentThreadID = &r.coderThread
	return r.attempt, nil
}
func (r *fakeExecutionRepository) HeartbeatAttempt(context.Context, string) error { return nil }
func (r *fakeExecutionRepository) SetAttemptStatus(_ context.Context, _ string, status domain.TaskAttemptStatus) error {
	r.attempt.Status = status
	return nil
}
func (r *fakeExecutionRepository) CompleteAttempt(context.Context, string, domain.AgentResult, domain.VerificationReport, string) (domain.TaskAttempt, error) {
	r.completed = true
	r.attempt.Status = domain.TaskAttemptStatusCompleted
	return r.attempt, nil
}
func (r *fakeExecutionRepository) FailAttempt(_ context.Context, _ string, status domain.TaskAttemptStatus, _ string, _ any) error {
	r.failedStatus = status
	r.attempt.Status = status
	return nil
}
func (r *fakeExecutionRepository) BeginReview(_ context.Context, _ string, number int, threadID string) (domain.TaskReview, error) {
	if threadID == r.coderThread {
		return domain.TaskReview{}, fmt.Errorf("reviewer reused coder thread")
	}
	r.reviewThreads = append(r.reviewThreads, threadID)
	return domain.TaskReview{ID: fmt.Sprintf("review-%d", number), AgentThreadID: threadID, Status: domain.ReviewRunning}, nil
}
func (r *fakeExecutionRepository) CreateReview(_ context.Context, _ string, number int, threadID string, result domain.ReviewerResult) (domain.TaskReview, error) {
	return domain.TaskReview{ID: fmt.Sprintf("review-%d", number), AgentThreadID: threadID, Status: result.Status}, nil
}
func (r *fakeExecutionRepository) StoreArtifact(context.Context, domain.Artifact) (domain.Artifact, error) {
	return domain.Artifact{}, nil
}
func (r *fakeExecutionRepository) ListAttempts(context.Context, string) ([]domain.TaskAttempt, error) {
	return nil, nil
}
func (r *fakeExecutionRepository) ListArtifacts(context.Context, string) ([]domain.Artifact, error) {
	return nil, nil
}
func (r *fakeExecutionRepository) AddRequiredTasks(context.Context, string, []domain.RequiredTask, int, int) (domain.RequiredTaskSchedule, error) {
	return domain.RequiredTaskSchedule{
		Tasks: []domain.ScheduledTask{{TaskID: "required-task"}}, ParentDependencies: []string{"required-task"},
	}, nil
}
func (r *fakeExecutionRepository) ResetTaskForRetry(context.Context, string, int) (domain.Task, error) {
	return domain.Task{}, nil
}
