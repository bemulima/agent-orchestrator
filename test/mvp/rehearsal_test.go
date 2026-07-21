//go:build mvp

package mvp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	temporalclient "go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/bemulima/agent-orchestrator/internal/activities"
	gitadapter "github.com/bemulima/agent-orchestrator/internal/adapters/git"
	gitlabadapter "github.com/bemulima/agent-orchestrator/internal/adapters/gitlab"
	pgadapter "github.com/bemulima/agent-orchestrator/internal/adapters/postgres"
	temporaladapter "github.com/bemulima/agent-orchestrator/internal/adapters/temporal"
	"github.com/bemulima/agent-orchestrator/internal/agent"
	"github.com/bemulima/agent-orchestrator/internal/discovery"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
	executionengine "github.com/bemulima/agent-orchestrator/internal/execution"
	onboardingengine "github.com/bemulima/agent-orchestrator/internal/onboarding"
	planningengine "github.com/bemulima/agent-orchestrator/internal/planning"
	topologyengine "github.com/bemulima/agent-orchestrator/internal/topology"
	gitlabuc "github.com/bemulima/agent-orchestrator/internal/usecase/gitlab"
	onboardinguc "github.com/bemulima/agent-orchestrator/internal/usecase/onboarding"
	planninguc "github.com/bemulima/agent-orchestrator/internal/usecase/planning"
	projectuc "github.com/bemulima/agent-orchestrator/internal/usecase/project"
	telegramuc "github.com/bemulima/agent-orchestrator/internal/usecase/telegram"
	topologyuc "github.com/bemulima/agent-orchestrator/internal/usecase/topology"
	orchestratorworkflow "github.com/bemulima/agent-orchestrator/internal/workflow"
)

const (
	mvpTelegramUserID = int64(910000001)
	mvpTelegramChatID = int64(-910000001)
)

type fixtureIDs struct {
	projectID    string
	snapshotID   string
	onboardingID string
	revisionID   string
	commandID    string
	planID       string
	runID        string
	taskIDs      []string
	updateIDs    []int64
}

func TestFinalMVPRehearsal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	pool := mvpPool(t, ctx)
	defer pool.Close()
	requireEmptyDisposableDatabase(t, ctx, pool)

	ids := &fixtureIDs{}
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() { cleanupFixture(t, pool, ids) })
	}
	t.Cleanup(cleanup)

	root := t.TempDir()
	projectName := "mvp-fixture-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	sourcePath := createFixtureRepository(t, root, projectName)
	worktreeRoot := filepath.Join(root, "worktrees")

	projects := pgadapter.ProjectRepoPG{Pool: pool}
	source := gitadapter.ProjectSource{AllowedRoots: []string{root}, StoragePath: filepath.Join(root, "repositories")}
	scanner := discovery.NewScanner(discovery.Config{
		MaxFiles: 1000, MaxFileBytes: 1 << 20, MaxTotalBytes: 10 << 20, MaxDepth: 16,
	})
	scan := projectuc.ScanProject{Projects: projects, Sources: source, Scanner: scanner}
	connected, err := (projectuc.ConnectProject{Projects: projects, Sources: source, Scan: scan}).Handle(ctx, projectuc.ConnectInput{
		LocalPath: sourcePath, RepositoryRole: domain.RepositoryRoleService,
	})
	require.NoError(t, err)
	require.Equal(t, domain.ProjectStatusAnalyzed, connected.Project.Status)
	require.Equal(t, domain.ServiceKindBackendService, connected.Snapshot.ServiceKind)
	ids.projectID, ids.snapshotID = connected.Project.ID, connected.Snapshot.ID

	onboardingRuns := pgadapter.OnboardingRepoPG{Pool: pool}
	prepareOnboarding := onboardinguc.Prepare{
		Projects: projects, Sources: source, Runs: onboardingRuns,
		Generator: onboardingengine.NewGenerator(onboardingengine.GeneratorConfig{
			MaxFileBytes: 2 << 20, MaxTotalBytes: 10 << 20,
		}),
	}
	onboardingRun, err := prepareOnboarding.Handle(ctx, onboardinguc.PrepareInput{ProjectID: ids.projectID})
	require.NoError(t, err)
	require.Equal(t, domain.OnboardingStatusAwaitingApproval, onboardingRun.Status)
	ids.onboardingID = onboardingRun.ID
	onboardingRun, err = (onboardinguc.Approve{Runs: onboardingRuns}).Handle(ctx, onboardinguc.DecideInput{
		RunID: onboardingRun.ID, Actor: "mvp-owner", Comment: "fixture approval",
	})
	require.NoError(t, err)
	applyOnboarding := onboardinguc.Apply{
		Projects: projects, Runs: onboardingRuns,
		Worktree: gitadapter.OnboardingWorktree{
			StoragePath: filepath.Join(worktreeRoot, "onboarding"),
			AuthorName:  "MVP Fixture", AuthorEmail: "mvp@example.test",
		},
	}
	applied, err := applyOnboarding.Handle(ctx, onboardinguc.ApplyInput{RunID: onboardingRun.ID})
	require.NoError(t, err)
	require.Equal(t, domain.OnboardingStatusCompleted, applied.Run.Status)
	require.NotEmpty(t, applied.Result.CommitSHA)
	repeatedApply, err := applyOnboarding.Handle(ctx, onboardinguc.ApplyInput{RunID: onboardingRun.ID})
	require.NoError(t, err)
	require.Equal(t, applied.Result.CommitSHA, repeatedApply.Result.CommitSHA)

	topologyStore := pgadapter.TopologyRepoPG{Pool: pool}
	catalog, err := (topologyuc.Rebuild{
		Projects: projects, Catalog: topologyStore, Builder: topologyengine.Builder{},
	}).Handle(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, catalog.Revision.ProjectCount)
	require.Len(t, catalog.Services, 1)
	ids.revisionID = catalog.Revision.ID

	plans := pgadapter.PlanningRepoPG{Pool: pool}
	createCommand := planninguc.CreateCommand{Plans: plans}
	createPlan := planninguc.CreatePlan{
		Plans: plans, Topology: topologyStore,
		Planner:   planningengine.Planner{MaxParallelTasks: 1},
		Validator: planningengine.Validator{MaxParallelTasks: 1, MaxRequiredTaskDepth: 3},
	}
	getPlan := planninguc.GetPlan{Plans: plans}
	approvePlan := planninguc.ApprovePlan{Plans: plans}
	gitLabLinks := gitlabuc.Links{Links: pgadapter.GitLabRepoPG{Pool: pool}, Plans: plans}
	telegramGateway := &fakeTelegramGateway{}
	telegramService := telegramuc.NewService(
		pgadapter.TelegramRepoPG{Pool: pool}, telegramGateway,
		telegramuc.Operations{
			CreateCommand: createCommand, CreatePlan: createPlan, GetPlan: getPlan,
			ApprovePlan: approvePlan, GitLabLinks: gitLabLinks,
		},
		[]int64{mvpTelegramUserID}, []int64{mvpTelegramChatID}, 15*time.Minute,
	)
	baseUpdateID := time.Now().UTC().UnixNano()
	ids.updateIDs = append(ids.updateIDs, baseUpdateID)
	err = telegramService.Handle(ctx, telegramMessageUpdate(baseUpdateID,
		"/plan Add a verified feature to "+projectName), "polling")
	require.NoError(t, err)
	planMessage := telegramGateway.lastMessage()
	require.NotNil(t, planMessage.ReplyMarkup)
	require.Len(t, planMessage.ReplyMarkup.InlineKeyboard, 2)

	err = pool.QueryRow(ctx, `
SELECT command.id, plan.id
FROM command JOIN plan ON plan.command_id = command.id
WHERE command.idempotency_key = $1`, fmt.Sprintf("telegram:update:%d", baseUpdateID)).
		Scan(&ids.commandID, &ids.planID)
	require.NoError(t, err)
	bundle, err := plans.GetPlan(ctx, ids.planID)
	require.NoError(t, err)
	require.Len(t, bundle.Tasks, 1)
	ids.taskIDs = []string{bundle.Tasks[0].ID}

	approveData := planMessage.ReplyMarkup.InlineKeyboard[0][0].CallbackData
	ids.updateIDs = append(ids.updateIDs, baseUpdateID+1)
	require.NoError(t, telegramService.Handle(ctx,
		telegramCallbackUpdate(baseUpdateID+1, approveData), "webhook"))
	bundle, err = plans.GetPlan(ctx, ids.planID)
	require.NoError(t, err)
	require.Equal(t, domain.PlanStatusApproved, bundle.Plan.Status)

	ids.updateIDs = append(ids.updateIDs, baseUpdateID+2)
	require.NoError(t, telegramService.Handle(ctx,
		telegramCallbackUpdate(baseUpdateID+2, approveData), "webhook"))
	var approvalEvents int
	require.NoError(t, pool.QueryRow(ctx, `
SELECT count(*) FROM audit_event WHERE resource_id = $1 AND action = 'plan.approved'`, ids.planID).
		Scan(&approvalEvents))
	require.Equal(t, 1, approvalEvents)

	temporalHostPort := getenv("TEMPORAL_HOST_PORT", "localhost:7233")
	temporalNamespace := getenv("TEMPORAL_NAMESPACE", "default")
	temporalClient, err := temporalclient.Dial(temporalclient.Options{
		HostPort: temporalHostPort, Namespace: temporalNamespace,
	})
	require.NoError(t, err)
	defer temporalClient.Close()
	taskQueue := "mvp-rehearsal-" + uuid.NewString()
	executions := pgadapter.TaskExecutionRepoPG{Pool: pool}
	validator, err := agent.NewValidator()
	require.NoError(t, err)
	restartingAgent := newRestartingAgentRunner()
	taskWorktrees := gitadapter.TaskWorktree{
		StoragePath: filepath.Join(worktreeRoot, "tasks"),
		AuthorName:  "MVP Fixture", AuthorEmail: "mvp@example.test",
	}
	executor := &executionengine.Service{
		Repository: executions, Worktrees: taskWorktrees, Runner: restartingAgent, Validator: validator,
		Verifier:    executionengine.Verifier{Worktrees: taskWorktrees},
		Models:      map[string]string{"standard": "fixture-standard", "deep": "fixture-deep"},
		ReviewModel: "fixture-review", MaxTaskAttempts: 3, MaxReviewAttempts: 2,
		MaxReplans: 2, MaxRequiredTaskDepth: 3,
	}
	planActivities := &activities.PlanActivities{Plans: plans, TaskExecutions: executions, Executor: executor}
	firstWorker := startPlanWorker(t, temporalClient, taskQueue, planActivities)
	firstWorkerStopped := false
	defer func() {
		if !firstWorkerStopped {
			firstWorker.Stop()
		}
	}()

	planRunner := temporaladapter.PlanRunner{Client: temporalClient, TaskQueue: taskQueue}
	startPlan := planninguc.StartPlan{
		Plans: plans, Runner: planRunner, MaxParallelTasks: 1, MaxActivityAttempts: 3,
	}
	startedRun, err := startPlan.Handle(ctx, ids.planID)
	require.NoError(t, err)
	require.NotNil(t, startedRun.TemporalRunID)
	ids.runID = startedRun.ID
	reusedRun, err := startPlan.Handle(ctx, ids.planID)
	require.NoError(t, err)
	require.Equal(t, startedRun.ID, reusedRun.ID)
	require.Equal(t, *startedRun.TemporalRunID, *reusedRun.TemporalRunID)

	select {
	case <-restartingAgent.firstCoderStarted:
	case <-ctx.Done():
		t.Fatal("first coder activity did not start before the rehearsal timeout")
	}
	restartComposeStackDuringExecution(t, ctx, pool, temporalClient)
	firstWorker.Stop()
	firstWorkerStopped = true
	select {
	case <-restartingAgent.firstCoderCancelled:
	case <-time.After(10 * time.Second):
		t.Fatal("worker restart did not cancel the in-flight fixture coder")
	}

	secondWorker := startPlanWorker(t, temporalClient, taskQueue, planActivities)
	defer secondWorker.Stop()
	var workflowOutput orchestratorworkflow.PlanWorkflowOutput
	err = temporalClient.GetWorkflow(ctx, startedRun.WorkflowID, *startedRun.TemporalRunID).Get(ctx, &workflowOutput)
	require.NoError(t, err)
	require.Equal(t, domain.PlanRunStatusCompleted, workflowOutput.Status)

	bundle, err = plans.GetPlan(ctx, ids.planID)
	require.NoError(t, err)
	require.Equal(t, domain.PlanStatusCompleted, bundle.Plan.Status)
	require.NotNil(t, bundle.Run)
	require.Equal(t, domain.PlanRunStatusCompleted, bundle.Run.Status)
	require.Equal(t, domain.TaskStatusCompleted, bundle.Tasks[0].Status)
	attempts, err := executions.ListAttempts(ctx, bundle.Tasks[0].ID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, domain.TaskAttemptStatusCompleted, attempts[0].Status)
	require.NotNil(t, attempts[0].AgentThreadID)
	require.Equal(t, "mvp-coder-thread", *attempts[0].AgentThreadID)
	var reviewCount int
	require.NoError(t, pool.QueryRow(ctx, `
SELECT count(*) FROM task_review WHERE task_attempt_id = $1`, attempts[0].ID).Scan(&reviewCount))
	require.Equal(t, 1, reviewCount)
	coderCalls, reviewerCalls, resumed := restartingAgent.stats()
	require.Equal(t, 2, coderCalls)
	require.Equal(t, 1, reviewerCalls)
	require.True(t, resumed)

	fakeGitLab := gitlabadapter.NewFakeAdapter()
	gitLabSync := gitlabuc.Sync{
		Plans: plans, Projects: projects, TaskExecutions: executions,
		Links: pgadapter.GitLabRepoPG{Pool: pool}, Gateway: fakeGitLab,
		ControlProject: "group/mvp-control",
	}
	firstSync, err := gitLabSync.Handle(ctx, ids.planID)
	require.NoError(t, err)
	require.Len(t, firstSync.Items, 1)
	require.NotNil(t, firstSync.Items[0].MergeRequest)
	creates := []int{fakeGitLab.IssueCreates, fakeGitLab.MRCreates, fakeGitLab.CommentCreates,
		fakeGitLab.LinkCreates, fakeGitLab.BranchCreates}
	secondSync, err := gitLabSync.Handle(ctx, ids.planID)
	require.NoError(t, err)
	require.Equal(t, firstSync.PlanIssue.WebURL, secondSync.PlanIssue.WebURL)
	require.Equal(t, creates, []int{fakeGitLab.IssueCreates, fakeGitLab.MRCreates,
		fakeGitLab.CommentCreates, fakeGitLab.LinkCreates, fakeGitLab.BranchCreates})

	ids.updateIDs = append(ids.updateIDs, baseUpdateID+3, baseUpdateID+4)
	require.NoError(t, telegramService.Handle(ctx,
		telegramMessageUpdate(baseUpdateID+3, "/status plan "+ids.planID), "polling"))
	require.Contains(t, telegramGateway.lastMessage().Text, "completed")
	require.NoError(t, telegramService.Handle(ctx,
		telegramMessageUpdate(baseUpdateID+4, "/issues "+ids.planID), "webhook"))
	require.Contains(t, telegramGateway.lastMessage().Text, "https://gitlab.example.test/")

	require.Equal(t, connected.Project.HeadCommit, runGit(t, sourcePath, "rev-parse", "HEAD"))
	require.Empty(t, runGit(t, sourcePath, "status", "--porcelain=v1"))
	assertNoDuplicateFixtureRows(t, ctx, pool, ids)

	cleanup()
	assertFixtureCleaned(t, ctx, pool, ids)
}

func startPlanWorker(
	t *testing.T,
	client temporalclient.Client,
	taskQueue string,
	planActivities *activities.PlanActivities,
) worker.Worker {
	t.Helper()
	value := worker.New(client, taskQueue, worker.Options{WorkerStopTimeout: time.Second})
	value.RegisterWorkflow(orchestratorworkflow.PlanWorkflow)
	value.RegisterActivity(planActivities)
	require.NoError(t, value.Start())
	return value
}

type restartingAgentRunner struct {
	mu                  sync.Mutex
	coderCalls          int
	reviewerCalls       int
	resumed             bool
	firstStartedOnce    sync.Once
	firstCancelledOnce  sync.Once
	firstCoderStarted   chan struct{}
	firstCoderCancelled chan struct{}
}

func newRestartingAgentRunner() *restartingAgentRunner {
	return &restartingAgentRunner{
		firstCoderStarted: make(chan struct{}), firstCoderCancelled: make(chan struct{}),
	}
}

func (r *restartingAgentRunner) Run(
	ctx context.Context,
	request domain.AgentRunRequest,
	onThread repository.AgentThreadCallback,
) (domain.AgentRunResponse, error) {
	if request.Role == domain.AgentRunReviewer {
		r.mu.Lock()
		r.reviewerCalls++
		reviewNumber := r.reviewerCalls
		r.mu.Unlock()
		threadID := fmt.Sprintf("mvp-reviewer-thread-%d", reviewNumber)
		if err := onThread(ctx, threadID); err != nil {
			return domain.AgentRunResponse{}, err
		}
		return domain.AgentRunResponse{ThreadID: threadID, Result: json.RawMessage(`{
  "status":"approved","summary":"fixture change verified","blocking_issues":[],
  "non_blocking_issues":[],"risks":[],"suggested_checks":[]
}`)}, nil
	}

	r.mu.Lock()
	r.coderCalls++
	call := r.coderCalls
	if call > 1 && request.ThreadID == "mvp-coder-thread" {
		r.resumed = true
	}
	r.mu.Unlock()
	threadID := request.ThreadID
	if threadID == "" {
		threadID = "mvp-coder-thread"
	}
	if err := onThread(ctx, threadID); err != nil {
		return domain.AgentRunResponse{}, err
	}
	if call == 1 {
		r.firstStartedOnce.Do(func() { close(r.firstCoderStarted) })
		<-ctx.Done()
		r.firstCancelledOnce.Do(func() { close(r.firstCoderCancelled) })
		return domain.AgentRunResponse{}, ctx.Err()
	}
	featurePath := filepath.Join(request.WorkingDirectory, "internal", "value", "feature.go")
	if err := os.WriteFile(featurePath, []byte("package value\n\nfunc FeatureEnabled() bool { return true }\n"), 0o640); err != nil {
		return domain.AgentRunResponse{}, err
	}
	return domain.AgentRunResponse{ThreadID: threadID, Result: json.RawMessage(`{
  "status":"completed","summary":"implemented verified fixture feature",
  "files_changed":["internal/value/feature.go"],
  "checks":[
    {"name":"git diff --check","status":"passed","details":"ok"},
    {"name":"go test ./...","status":"passed","details":"ok"},
    {"name":"go vet ./...","status":"passed","details":"ok"}
  ],
  "artifacts":[],"blockers":[],"required_tasks":[],"risks":[],"notes_for_reviewer":[]
}`)}, nil
}

func (r *restartingAgentRunner) stats() (coderCalls, reviewerCalls int, resumed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.coderCalls, r.reviewerCalls, r.resumed
}

type fakeTelegramGateway struct {
	mu       sync.Mutex
	messages []domain.TelegramOutgoingMessage
	answers  []string
}

func (*fakeTelegramGateway) GetUpdates(context.Context, int64, int) ([]domain.TelegramUpdate, error) {
	return nil, nil
}

func (f *fakeTelegramGateway) SendMessage(_ context.Context, message domain.TelegramOutgoingMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, message)
	return nil
}

func (f *fakeTelegramGateway) AnswerCallback(_ context.Context, id, text string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, id+":"+text)
	return nil
}

func (*fakeTelegramGateway) SetWebhook(context.Context, string, string) error { return nil }
func (*fakeTelegramGateway) DeleteWebhook(context.Context, bool) error        { return nil }

func (f *fakeTelegramGateway) lastMessage() domain.TelegramOutgoingMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.messages) == 0 {
		return domain.TelegramOutgoingMessage{}
	}
	return f.messages[len(f.messages)-1]
}

func telegramMessageUpdate(updateID int64, text string) domain.TelegramUpdate {
	return domain.TelegramUpdate{UpdateID: updateID, Message: &domain.TelegramMessage{
		MessageID: updateID, From: domain.TelegramActor{ID: mvpTelegramUserID},
		Chat: domain.TelegramChat{ID: mvpTelegramChatID}, Text: text,
	}}
}

func telegramCallbackUpdate(updateID int64, data string) domain.TelegramUpdate {
	return domain.TelegramUpdate{UpdateID: updateID, CallbackQuery: &domain.TelegramCallbackQuery{
		ID: fmt.Sprintf("mvp-callback-%d", updateID), From: domain.TelegramActor{ID: mvpTelegramUserID},
		Message: &domain.TelegramMessage{MessageID: updateID, Chat: domain.TelegramChat{ID: mvpTelegramChatID}},
		Data:    data,
	}}
}

func createFixtureRepository(t *testing.T, root, projectName string) string {
	t.Helper()
	path := filepath.Join(root, projectName)
	require.NoError(t, os.MkdirAll(filepath.Join(path, "internal", "value"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(path, "go.mod"),
		[]byte("module example.test/"+projectName+"\n\ngo 1.23\n"), 0o640))
	require.NoError(t, os.WriteFile(filepath.Join(path, "internal", "value", "value.go"),
		[]byte("package value\n\nfunc Current() string { return \"before\" }\n"), 0o640))
	require.NoError(t, os.WriteFile(filepath.Join(path, "README.md"),
		[]byte("# MVP fixture\n\nDisposable Go service for orchestrator verification.\n"), 0o640))
	runGit(t, path, "init", "-b", "main")
	runGit(t, path, "remote", "add", "origin", "https://gitlab.example.test/group/"+projectName+".git")
	runGit(t, path, "add", "go.mod", "README.md", "internal/value/value.go")
	runGit(t, path, "-c", "user.name=MVP Fixture", "-c", "user.email=mvp@example.test",
		"commit", "--no-gpg-sign", "-m", "initial fixture")
	return path
}

func runGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	return strings.TrimSpace(string(output))
}

func mvpPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required for the MVP rehearsal")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	require.NoError(t, err)
	require.NoError(t, pool.Ping(ctx))
	return pool
}

func requireEmptyDisposableDatabase(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	counts := make([]int, 4)
	require.NoError(t, pool.QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM project),
    (SELECT count(*) FROM command),
    (SELECT count(*) FROM telegram_update),
    (SELECT count(*) FROM telegram_callback)`).Scan(&counts[0], &counts[1], &counts[2], &counts[3]))
	if counts[0] != 0 || counts[1] != 0 || counts[2] != 0 || counts[3] != 0 {
		t.Fatalf("MVP rehearsal requires an empty disposable database; counts project/command/update/callback = %v", counts)
	}
}

func restartComposeStackDuringExecution(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	client temporalclient.Client,
) {
	t.Helper()
	if getenv("MVP_RESTART_COMPOSE", "false") != "true" {
		return
	}
	composeFile := strings.TrimSpace(os.Getenv("MVP_COMPOSE_FILE"))
	if composeFile == "" || !filepath.IsAbs(composeFile) {
		t.Fatal("MVP_COMPOSE_FILE must be an absolute path when MVP_RESTART_COMPOSE=true")
	}
	restart := func(services ...string) {
		arguments := append([]string{"compose", "-f", composeFile, "restart"}, services...)
		command := exec.CommandContext(ctx, "docker", arguments...)
		output, err := command.CombinedOutput()
		require.NoError(t, err, "docker compose restart %v: %s", services, output)
	}
	restart("postgres")
	require.Eventually(t, func() bool {
		pingContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return pool.Ping(pingContext) == nil
	}, 45*time.Second, 500*time.Millisecond, "PostgreSQL did not recover after restart")
	restart("temporal")
	require.Eventually(t, func() bool {
		healthContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, err := client.CheckHealth(healthContext, nil)
		return err == nil
	}, 60*time.Second, time.Second, "Temporal did not recover after restart")
	restart("orchestrator", "worker")
}

func assertNoDuplicateFixtureRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ids *fixtureIDs) {
	t.Helper()
	var runs, attempts, reviews, planLinks int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM plan_run WHERE plan_id = $1`, ids.planID).Scan(&runs))
	require.NoError(t, pool.QueryRow(ctx, `
SELECT count(*) FROM task_attempt WHERE task_id = $1`, ids.taskIDs[0]).Scan(&attempts))
	require.NoError(t, pool.QueryRow(ctx, `
SELECT count(*) FROM task_review WHERE task_attempt_id IN (
    SELECT id FROM task_attempt WHERE task_id = $1
)`, ids.taskIDs[0]).Scan(&reviews))
	require.NoError(t, pool.QueryRow(ctx, `
SELECT count(*) FROM gitlab_link
WHERE (resource_type = 'plan' AND resource_id = $1)
   OR (resource_type = 'task' AND resource_id = $2)`, ids.planID, ids.taskIDs[0]).Scan(&planLinks))
	require.Equal(t, []int{1, 1, 1, 2}, []int{runs, attempts, reviews, planLinks})
}

func cleanupFixture(t *testing.T, pool *pgxpool.Pool, ids *fixtureIDs) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	statements := []struct {
		query string
		args  []any
	}{
		{`DELETE FROM telegram_update WHERE update_id = ANY($1)`, []any{ids.updateIDs}},
		{`DELETE FROM telegram_callback WHERE resource_id = NULLIF($1, '')::uuid`, []any{ids.planID}},
		{`DELETE FROM telegram_user WHERE telegram_user_id = $1`, []any{mvpTelegramUserID}},
		{`DELETE FROM gitlab_link WHERE resource_id = ANY($1::uuid[])`, []any{nonEmptyIDs(append([]string{ids.planID}, ids.taskIDs...))}},
		{`DELETE FROM command WHERE id = NULLIF($1, '')::uuid`, []any{ids.commandID}},
		{`DELETE FROM onboarding_run WHERE id = NULLIF($1, '')::uuid`, []any{ids.onboardingID}},
		{`DELETE FROM approval WHERE resource_id = ANY($1::uuid[])`, []any{nonEmptyIDs([]string{ids.planID, ids.onboardingID})}},
		{`DELETE FROM topology_revision WHERE id = NULLIF($1, '')::uuid`, []any{ids.revisionID}},
		{`DELETE FROM project WHERE id = NULLIF($1, '')::uuid`, []any{ids.projectID}},
		{`DELETE FROM audit_event WHERE resource_id = ANY($1::uuid[])`, []any{fixtureResourceIDs(ids)}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Errorf("MVP fixture cleanup failed for %q: %v", statement.query, err)
		}
	}
}

func fixtureResourceIDs(ids *fixtureIDs) []string {
	values := []string{ids.projectID, ids.onboardingID, ids.revisionID, ids.commandID, ids.planID, ids.runID}
	values = append(values, ids.taskIDs...)
	return nonEmptyIDs(values)
}

func nonEmptyIDs(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func assertFixtureCleaned(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ids *fixtureIDs) {
	t.Helper()
	var projects, commands, updates, callbacks, links, audits int
	require.NoError(t, pool.QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM project WHERE id = NULLIF($1, '')::uuid),
    (SELECT count(*) FROM command WHERE id = NULLIF($2, '')::uuid),
    (SELECT count(*) FROM telegram_update WHERE update_id = ANY($3)),
    (SELECT count(*) FROM telegram_callback WHERE resource_id = NULLIF($4, '')::uuid),
    (SELECT count(*) FROM gitlab_link WHERE resource_id = ANY($5::uuid[])),
    (SELECT count(*) FROM audit_event WHERE resource_id = ANY($6::uuid[]))`,
		ids.projectID, ids.commandID, ids.updateIDs, ids.planID,
		append([]string{ids.planID}, ids.taskIDs...), fixtureResourceIDs(ids)).
		Scan(&projects, &commands, &updates, &callbacks, &links, &audits))
	require.Equal(t, []int{0, 0, 0, 0, 0, 0}, []int{projects, commands, updates, callbacks, links, audits})
}

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

var _ repository.AgentRunner = (*restartingAgentRunner)(nil)
var _ repository.TelegramGateway = (*fakeTelegramGateway)(nil)
