//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	pgadapter "github.com/bemulima/agent-orchestrator/internal/adapters/postgres"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestInitialMigrationCreatesCoreTables(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is required for integration tests")
	}

	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	defer pool.Close()

	rows, err := pool.Query(context.Background(), `
SELECT tablename
FROM pg_tables
WHERE schemaname = 'public'
ORDER BY tablename`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()

	actual := make(map[string]bool)
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		actual[table] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}

	expected := []string{
		"approval", "artifact", "audit_event", "command", "contract",
		"gitlab_link", "onboarding_run", "plan", "project", "service_capability",
		"service_ownership", "service_relation", "service_snapshot", "task",
		"task_attempt", "task_dependency", "telegram_user", "topology_revision",
		"topology_service", "contract_drift", "plan_run", "task_review",
		"gitlab_webhook_event",
		"telegram_update", "telegram_callback", "telegram_poll_state",
	}
	missing := make([]string, 0)
	for _, table := range expected {
		if !actual[table] {
			missing = append(missing, table)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("missing tables: %v", missing)
	}
}

func TestTelegramRepositoryDeduplicatesUpdatesAndConsumesBoundCallbacks(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	ctx := context.Background()
	repository := pgadapter.TelegramRepoPG{Pool: pool}
	resourceID := uuid.NewString()
	updateID := time.Now().UnixNano()
	userID := int64(700000000 + time.Now().UnixNano()%100000000)
	chatID := -userID
	botKey := strings.Repeat("a", 64)
	validHash := strings.Repeat("b", 64)
	expiredHash := strings.Repeat("c", 64)
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM telegram_update WHERE update_id = $1`, updateID)
		_, _ = pool.Exec(ctx, `DELETE FROM telegram_callback WHERE resource_id = $1`, resourceID)
		_, _ = pool.Exec(ctx, `DELETE FROM telegram_poll_state WHERE bot_key = $1`, botKey)
		_, _ = pool.Exec(ctx, `DELETE FROM telegram_user WHERE telegram_user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_event WHERE resource_id = $1`, resourceID)
	}()
	receipt := domain.TelegramUpdateReceipt{
		UpdateID: updateID, Source: "polling", Checksum: strings.Repeat("d", 64),
		TelegramUserID: &userID, TelegramChatID: &chatID, ReceivedAt: time.Now().UTC(),
	}
	claimed, err := repository.BeginUpdate(ctx, receipt)
	if err != nil || !claimed {
		t.Fatalf("BeginUpdate() = %t, %v", claimed, err)
	}
	claimed, err = repository.BeginUpdate(ctx, receipt)
	if err != nil || claimed {
		t.Fatalf("duplicate BeginUpdate() = %t, %v", claimed, err)
	}
	if err := repository.FinishUpdate(ctx, updateID, domain.TelegramUpdateStatusProcessed); err != nil {
		t.Fatalf("FinishUpdate() error = %v", err)
	}
	if offset, err := repository.GetPollOffset(ctx, botKey); err != nil || offset != 0 {
		t.Fatalf("GetPollOffset() = %d, %v", offset, err)
	}
	if err := repository.SavePollOffset(ctx, botKey, updateID+1); err != nil {
		t.Fatalf("SavePollOffset() error = %v", err)
	}
	if err := repository.SavePollOffset(ctx, botKey, updateID); err != nil {
		t.Fatalf("SavePollOffset(lower) error = %v", err)
	}
	if offset, err := repository.GetPollOffset(ctx, botKey); err != nil || offset != updateID+1 {
		t.Fatalf("monotonic GetPollOffset() = %d, %v", offset, err)
	}

	now := time.Now().UTC()
	grant := domain.TelegramCallbackGrant{
		TokenHash: validHash, Action: "approve", ResourceType: "plan", ResourceID: resourceID,
		TelegramUserID: userID, TelegramChatID: chatID, Status: domain.TelegramCallbackStatusPending,
		CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute),
	}
	if err := repository.SaveCallback(ctx, grant); err != nil {
		t.Fatalf("SaveCallback() error = %v", err)
	}
	if _, err := repository.ConsumeCallback(ctx, validHash, userID+1, chatID, now); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("cross-user ConsumeCallback() error = %v", err)
	}
	consumed, err := repository.ConsumeCallback(ctx, validHash, userID, chatID, now)
	if err != nil || consumed.Status != domain.TelegramCallbackStatusConsumed || consumed.ConsumedAt == nil {
		t.Fatalf("ConsumeCallback() = %#v, %v", consumed, err)
	}
	if _, err := repository.ConsumeCallback(ctx, validHash, userID, chatID, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("replayed ConsumeCallback() error = %v", err)
	}
	expired := grant
	expired.TokenHash = expiredHash
	expired.Action = "reject"
	expired.CreatedAt = now.Add(-2 * time.Minute)
	expired.ExpiresAt = now.Add(-time.Minute)
	if err := repository.SaveCallback(ctx, expired); err != nil {
		t.Fatalf("SaveCallback(expired fixture) error = %v", err)
	}
	if _, err := repository.ConsumeCallback(ctx, expiredHash, userID, chatID, now); !errors.Is(err, domain.ErrInvalidStatus) {
		t.Fatalf("expired ConsumeCallback() error = %v", err)
	}
}

func TestGitLabRepositoryIdempotentlyPersistsLinksAndWebhookState(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	ctx := context.Background()
	repository := pgadapter.GitLabRepoPG{Pool: pool}
	resourceID := uuid.NewString()
	ignoredEventID := "ignored-" + uuid.NewString()
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gitlab_webhook_event WHERE event_uuid IN ($1, $2, $3, $4, $5, $6)`,
			"issue-"+resourceID, "merge-"+resourceID, "pipeline-"+resourceID,
			ignoredEventID, "invalid-"+resourceID, "reopen-"+resourceID)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_event WHERE resource_id = $1`, resourceID)
		_, _ = pool.Exec(ctx, `DELETE FROM gitlab_link WHERE resource_id = $1`, resourceID)
	}()
	issueIID := int64(17)
	first, err := repository.SaveGitLabLink(ctx, domain.GitLabLink{
		ResourceType: domain.GitLabResourceTask, ResourceID: resourceID, GitLabProjectID: 42,
		IssueIID: &issueIID, URL: "https://gitlab.example.test/group/service/-/issues/17", ExternalState: "opened",
	})
	if err != nil {
		t.Fatalf("SaveGitLabLink(issue) error = %v", err)
	}
	mergeRequestIID := int64(9)
	second, err := repository.SaveGitLabLink(ctx, domain.GitLabLink{
		ResourceType: domain.GitLabResourceTask, ResourceID: resourceID, GitLabProjectID: 42,
		IssueIID: &issueIID, MergeRequestIID: &mergeRequestIID,
		URL: "https://gitlab.example.test/group/service/-/merge_requests/9", ExternalState: "opened",
	})
	if err != nil || second.ID != first.ID || second.IssueIID == nil || second.MergeRequestIID == nil {
		t.Fatalf("SaveGitLabLink(MR) = %#v, %v", second, err)
	}
	issueEvent := domain.GitLabWebhookEvent{
		EventUUID: "issue-" + resourceID, EventType: "Issue Hook", ObjectKind: "issue",
		GitLabProjectID: 42, ObjectIID: issueIID, ExternalState: "closed",
		PayloadChecksum: strings.Repeat("a", 64), ReceivedAt: time.Now().UTC(),
	}
	processed, err := repository.ApplyGitLabWebhook(ctx, issueEvent)
	if err != nil || processed.Status != "processed" || processed.Link == nil || processed.Link.ExternalState != "closed" {
		t.Fatalf("ApplyGitLabWebhook(issue) = %#v, %v", processed, err)
	}
	duplicate, err := repository.ApplyGitLabWebhook(ctx, issueEvent)
	if err != nil || !duplicate.Duplicate || duplicate.Link == nil {
		t.Fatalf("duplicate ApplyGitLabWebhook() = %#v, %v", duplicate, err)
	}
	invalid := issueEvent
	invalid.EventUUID = "invalid-" + resourceID
	invalid.ExternalState = "merged"
	if _, err := repository.ApplyGitLabWebhook(ctx, invalid); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("invalid issue transition error = %v", err)
	}
	mergeEvent := domain.GitLabWebhookEvent{
		EventUUID: "merge-" + resourceID, EventType: "Merge Request Hook", ObjectKind: "merge_request",
		GitLabProjectID: 42, ObjectIID: mergeRequestIID, ExternalState: "merged",
		PayloadChecksum: strings.Repeat("b", 64), ReceivedAt: time.Now().UTC(),
	}
	merged, err := repository.ApplyGitLabWebhook(ctx, mergeEvent)
	if err != nil || merged.Link == nil || merged.Link.ExternalState != "merged" {
		t.Fatalf("ApplyGitLabWebhook(MR) = %#v, %v", merged, err)
	}
	reopened := mergeEvent
	reopened.EventUUID = "reopen-" + resourceID
	reopened.ExternalState = "opened"
	if _, err := repository.ApplyGitLabWebhook(ctx, reopened); !errors.Is(err, domain.ErrInvalidStatus) {
		t.Fatalf("reopened merged MR error = %v", err)
	}
	pipelineEvent := domain.GitLabWebhookEvent{
		EventUUID: "pipeline-" + resourceID, EventType: "Pipeline Hook", ObjectKind: "pipeline",
		GitLabProjectID: 42, ObjectIID: mergeRequestIID, ExternalState: "unknown", PipelineStatus: "success",
		PayloadChecksum: strings.Repeat("c", 64), ReceivedAt: time.Now().UTC(),
	}
	pipeline, err := repository.ApplyGitLabWebhook(ctx, pipelineEvent)
	if err != nil || pipeline.Link == nil || pipeline.Link.PipelineStatus != "success" || pipeline.Link.ExternalState != "merged" {
		t.Fatalf("ApplyGitLabWebhook(pipeline) = %#v, %v", pipeline, err)
	}
	ignored, err := repository.ApplyGitLabWebhook(ctx, domain.GitLabWebhookEvent{
		EventUUID: ignoredEventID, EventType: "Issue Hook", ObjectKind: "issue",
		GitLabProjectID: 999, ObjectIID: 999, ExternalState: "opened",
		PayloadChecksum: strings.Repeat("d", 64), ReceivedAt: time.Now().UTC(),
	})
	if err != nil || ignored.Status != "ignored" || ignored.Link != nil {
		t.Fatalf("ignored ApplyGitLabWebhook() = %#v, %v", ignored, err)
	}
}

func TestTaskExecutionRepositoryPersistsThreadsVerificationReviewAndArtifacts(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	ctx := context.Background()
	projects := pgadapter.ProjectRepoPG{Pool: pool}
	plans := pgadapter.PlanningRepoPG{Pool: pool}
	executions := pgadapter.TaskExecutionRepoPG{Pool: pool}
	path := "/fixtures/" + uuid.NewString()
	project, err := projects.Upsert(ctx, domain.Project{
		Name: "execution-" + uuid.NewString(), Status: domain.ProjectStatusAnalyzed,
		RepositoryRole: domain.RepositoryRoleService, SourceIdentity: "integration:" + uuid.NewString(),
		LocalPath: &path, DefaultBranch: "main", CurrentBranch: "main", HeadCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	var revisionID string
	if err := pool.QueryRow(ctx, `
INSERT INTO topology_revision (
    fingerprint, project_count, service_count, capability_count,
    ownership_count, contract_count, relation_count, drift_count
) VALUES ($1, 1, 1, 0, 0, 0, 0, 0) RETURNING id`, strings.Repeat("e", 64)).Scan(&revisionID); err != nil {
		t.Fatalf("insert topology revision: %v", err)
	}
	command, err := plans.CreateCommand(ctx, domain.Command{
		Source: domain.CommandSourceAPI, Text: "execution fixture", Status: domain.CommandStatusReceived,
		IdempotencyKey: "integration:" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("CreateCommand() error = %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `
DELETE FROM audit_event WHERE resource_id IN (
    SELECT $1::uuid
    UNION SELECT $2::uuid
    UNION SELECT id FROM plan WHERE command_id = $2
    UNION SELECT task.id FROM task JOIN plan ON plan.id = task.plan_id WHERE plan.command_id = $2
    UNION SELECT plan_run.id FROM plan_run JOIN plan ON plan.id = plan_run.plan_id WHERE plan.command_id = $2
)`, project.ID, command.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM approval WHERE resource_type = 'plan' AND resource_id IN (SELECT id FROM plan WHERE command_id = $1)`, command.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM command WHERE id = $1`, command.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM topology_revision WHERE id = $1`, revisionID)
		_, _ = pool.Exec(ctx, `DELETE FROM project WHERE id = $1`, project.ID)
	}()
	bundle, err := plans.CreatePlan(ctx, command, domain.PlannerInput{
		CommandID: command.ID, CommandText: command.Text, TopologyRevisionID: revisionID,
	}, domain.PlannerOutput{
		Summary: "execution fixture", RiskLevel: domain.RiskLevelLow,
		Tasks: []domain.PlannedTask{{
			Key: "fixture", ProjectID: project.ID, Role: "coder", Title: "fixture", Description: "fixture",
			AcceptanceCriteria: []string{"passes"}, WriteScope: []string{"internal/**"}, ModelProfile: "standard",
			RiskLevel: domain.RiskLevelLow, VerificationCommands: []string{"go test ./..."},
		}},
	})
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	bundle, err = plans.ApprovePlan(ctx, bundle.Plan.ID, "integration", "approved")
	if err != nil {
		t.Fatalf("ApprovePlan() error = %v", err)
	}
	run, bundle, err := plans.PrepareRun(ctx, bundle.Plan.ID, 1)
	if err != nil {
		t.Fatalf("PrepareRun() error = %v", err)
	}
	if _, err := plans.UpdateRunStatus(ctx, run.ID, domain.PlanRunStatusRunning, ""); err != nil {
		t.Fatalf("start run: %v", err)
	}
	task := bundle.Tasks[0]
	if _, err := plans.MarkTaskReady(ctx, run.ID, task.ID); err != nil {
		t.Fatalf("MarkTaskReady() error = %v", err)
	}
	executionContext, err := executions.GetExecutionContext(ctx, task.ID)
	if err != nil || executionContext.Project.ID != project.ID || executionContext.Command.ID != command.ID {
		t.Fatalf("GetExecutionContext() = %#v, %v", executionContext, err)
	}
	workspace := domain.TaskWorkspace{Path: "/worktrees/fixture", BranchName: "ai/task-fixture", BaseCommit: project.HeadCommit}
	attempt, err := executions.BeginAttempt(ctx, task.ID, "workflow:"+uuid.NewString(), workspace, 3)
	if err != nil {
		t.Fatalf("BeginAttempt() error = %v", err)
	}
	reused, err := executions.BeginAttempt(ctx, task.ID, attempt.WorkflowID, workspace, 3)
	if err != nil || reused.ID != attempt.ID {
		t.Fatalf("reused BeginAttempt() = %#v, %v", reused, err)
	}
	attempt, err = executions.AttachAgentThread(ctx, attempt.ID, "coder-thread-"+uuid.NewString())
	if err != nil || attempt.AgentThreadID == nil {
		t.Fatalf("AttachAgentThread() = %#v, %v", attempt, err)
	}
	if _, err := executions.BeginReview(ctx, attempt.ID, 1, *attempt.AgentThreadID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("BeginReview(coder thread) error = %v", err)
	}
	reviewerThread := "review-thread-" + uuid.NewString()
	if _, err := executions.BeginReview(ctx, attempt.ID, 1, reviewerThread); err != nil {
		t.Fatalf("BeginReview() error = %v", err)
	}
	if _, err := executions.CreateReview(ctx, attempt.ID, 1, reviewerThread, domain.ReviewerResult{
		Status: domain.ReviewApproved, Summary: "approved", BlockingIssues: []domain.ReviewIssue{},
		NonBlockingIssues: []domain.ReviewIssue{}, Risks: []string{}, SuggestedChecks: []string{},
	}); err != nil {
		t.Fatalf("CreateReview() error = %v", err)
	}
	storedArtifact, err := executions.StoreArtifact(ctx, domain.Artifact{
		TaskID: task.ID, Type: "report", Name: "fixture", URI: "task-worktree://fixture/report.json",
		Checksum: strings.Repeat("a", 64), Metadata: json.RawMessage(`{"path":"report.json"}`),
	})
	if err != nil || storedArtifact.ID == "" {
		t.Fatalf("StoreArtifact() = %#v, %v", storedArtifact, err)
	}
	completed, err := executions.CompleteAttempt(ctx, attempt.ID, domain.AgentResult{
		Status: domain.AgentResultCompleted, Summary: "done", FilesChanged: []string{"internal/fixture.go"},
	}, domain.VerificationReport{Status: "passed", ChangedFiles: []string{"internal/fixture.go"}, VerifiedAt: time.Now().UTC()}, strings.Repeat("b", 40))
	if err != nil || completed.Status != domain.TaskAttemptStatusCompleted || completed.ReviewCount != 1 {
		t.Fatalf("CompleteAttempt() = %#v, %v", completed, err)
	}
	attempts, err := executions.ListAttempts(ctx, task.ID)
	if err != nil || len(attempts) != 1 || attempts[0].AgentThreadID == nil {
		t.Fatalf("ListAttempts() = %#v, %v", attempts, err)
	}
	artifacts, err := executions.ListArtifacts(ctx, task.ID)
	if err != nil || len(artifacts) != 1 || artifacts[0].Checksum != storedArtifact.Checksum {
		t.Fatalf("ListArtifacts() = %#v, %v", artifacts, err)
	}
}

func TestOnboardingRepositoryEnforcesApprovalStateMachine(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	projects := pgadapter.ProjectRepoPG{Pool: pool}
	runs := pgadapter.OnboardingRepoPG{Pool: pool}
	path := "/fixtures/" + uuid.NewString()
	project, err := projects.Upsert(context.Background(), domain.Project{
		Name: "onboarding-integration", Status: domain.ProjectStatusAnalyzed,
		RepositoryRole: domain.RepositoryRoleService, SourceIdentity: "integration:" + uuid.NewString(),
		LocalPath: &path, DefaultBranch: "main", CurrentBranch: "main", HeadCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	now := time.Now().UTC()
	report := domain.DiscoveryReport{
		SchemaVersion: 1, ProjectID: project.ID, ProjectName: project.Name,
		RepositoryRole: project.RepositoryRole, RepositoryPath: path,
		CommitSHA: "abc123", Branch: "main", ContentChecksum: "checksum", StartedAt: now, CompletedAt: now,
	}
	snapshot, err := projects.SaveDiscovery(context.Background(), project, domain.ServiceSnapshot{
		CommitSHA: "abc123", Branch: "main", ContentChecksum: "checksum",
		ServiceKind: domain.ServiceKindBackendService, Status: string(domain.ProjectStatusAnalyzed),
	}, report)
	if err != nil {
		t.Fatalf("SaveDiscovery() error = %v", err)
	}
	proposal := domain.OnboardingProposal{
		SchemaVersion: 1, Generator: "integration", ProjectID: project.ID,
		SnapshotID: snapshot.ID, BaseCommit: snapshot.CommitSHA, Checksum: "proposal-checksum",
		Files: []domain.ProposedFile{{
			Path: "AGENTS.md", Content: "managed\n", Action: domain.ProposalFileCreate,
			Checksum: "file-checksum", Explanation: "integration fixture",
		}},
	}
	run, err := runs.CreateOrGet(context.Background(), domain.OnboardingRun{
		ProjectID: project.ID, SnapshotID: snapshot.ID, Status: domain.OnboardingStatusProposalReady,
		BaseCommit: snapshot.CommitSHA, BaseBranch: snapshot.Branch,
		ProposalChecksum: proposal.Checksum, Proposal: proposal, UnifiedDiff: "fixture diff",
		Checks: []domain.OnboardingCheck{},
	})
	if err != nil {
		t.Fatalf("CreateOrGet() error = %v", err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_event WHERE resource_id IN ($1, $2)`, project.ID, run.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM gitlab_link WHERE resource_type = 'onboarding_run' AND resource_id = $1`, run.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM approval WHERE resource_type = 'onboarding_run' AND resource_id = $1`, run.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, project.ID)
	}()
	if run.Status != domain.OnboardingStatusAwaitingApproval || run.ApprovalID == nil {
		t.Fatalf("prepared run = %#v", run)
	}
	reused, err := runs.CreateOrGet(context.Background(), domain.OnboardingRun{
		ProjectID: project.ID, SnapshotID: snapshot.ID, Status: domain.OnboardingStatusProposalReady,
		BaseCommit: snapshot.CommitSHA, BaseBranch: snapshot.Branch,
		ProposalChecksum: proposal.Checksum, Proposal: proposal, UnifiedDiff: "fixture diff",
		Checks: []domain.OnboardingCheck{},
	})
	if err != nil || reused.ID != run.ID {
		t.Fatalf("idempotent CreateOrGet() = %#v, %v", reused, err)
	}
	if _, err := runs.BeginApply(context.Background(), run.ID); !errors.Is(err, domain.ErrApprovalNeeded) {
		t.Fatalf("BeginApply() before approval error = %v", err)
	}
	approved, err := runs.Approve(context.Background(), run.ID, "integration-owner", "approved")
	if err != nil || approved.Status != domain.OnboardingStatusAwaitingApproval {
		t.Fatalf("Approve() = %#v, %v", approved, err)
	}
	applying, err := runs.BeginApply(context.Background(), run.ID)
	if err != nil || applying.Status != domain.OnboardingStatusApplying {
		t.Fatalf("BeginApply() = %#v, %v", applying, err)
	}
	if _, err := runs.BeginApply(context.Background(), run.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("concurrent BeginApply() error = %v, want conflict", err)
	}
	published, err := runs.RecordPublication(context.Background(), run.ID, domain.OnboardingPublication{
		Published: true, GitLabProjectID: 42, MergeRequestIID: 7,
		MergeRequestURL: "https://gitlab.example.test/group/project/-/merge_requests/7",
	})
	if err != nil || published.Status != domain.OnboardingStatusMRCreated || published.MergeRequestURL == nil {
		t.Fatalf("RecordPublication() = %#v, %v", published, err)
	}
	completed, err := runs.CompleteApply(context.Background(), run.ID, domain.OnboardingApplyResult{
		WorktreePath: "/worktrees/orders", BranchName: "ai/onboard-orders", CommitSHA: "def456",
		Checks: []domain.OnboardingCheck{{Name: "write_scope", Status: "passed"}},
	})
	if err != nil || completed.Status != domain.OnboardingStatusCompleted || completed.CommitSHA == nil || *completed.CommitSHA != "def456" ||
		completed.MergeRequestURL == nil {
		t.Fatalf("CompleteApply() = %#v, %v", completed, err)
	}
}

func TestProjectRepositoryPersistsIdempotentDiscovery(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	repository := pgadapter.ProjectRepoPG{Pool: pool}
	identity := "integration:" + uuid.NewString()
	path := "/fixtures/" + uuid.NewString()
	projectInput := domain.Project{
		Name: "integration-project", Status: domain.ProjectStatusConnected,
		RepositoryRole: domain.RepositoryRoleService, SourceIdentity: identity,
		LocalPath: &path, DefaultBranch: "main", CurrentBranch: "main",
		HeadCommit: "abc123", IsDirty: true,
	}
	project, err := repository.Upsert(context.Background(), projectInput)
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_event WHERE resource_id = $1`, project.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, project.ID)
	}()
	duplicate, err := repository.Upsert(context.Background(), projectInput)
	if err != nil {
		t.Fatalf("duplicate Upsert() error = %v", err)
	}
	if duplicate.ID != project.ID {
		t.Fatalf("duplicate ID = %q, want %q", duplicate.ID, project.ID)
	}

	now := time.Now().UTC()
	report := domain.DiscoveryReport{
		SchemaVersion: 1, ProjectID: project.ID, ProjectName: project.Name,
		RepositoryRole: project.RepositoryRole, RepositoryPath: path,
		CommitSHA: "abc123", Branch: "main", IsDirty: true,
		ContentChecksum: "checksum-one",
		StartedAt:       now, CompletedAt: now,
		Facts: []domain.Evidence{{
			Category: "classification", Name: "service_kind", Value: "backend_service",
			Confidence: .9, SourcePath: "go.mod", Explanation: "fixture evidence",
		}},
	}
	snapshotInput := domain.ServiceSnapshot{
		CommitSHA: "abc123", Branch: "main", IsDirty: true,
		ContentChecksum: "checksum-one",
		ServiceKind:     domain.ServiceKindBackendService, Language: "go",
		Confidence: .9, Status: string(domain.ProjectStatusAnalyzed),
	}
	first, err := repository.SaveDiscovery(context.Background(), project, snapshotInput, report)
	if err != nil {
		t.Fatalf("first SaveDiscovery() error = %v", err)
	}
	reused, err := repository.SaveDiscovery(context.Background(), project, snapshotInput, report)
	if err != nil {
		t.Fatalf("reused SaveDiscovery() error = %v", err)
	}
	if reused.ID != first.ID || reused.Version != 1 {
		t.Fatalf("reused snapshot = %#v, want first snapshot", reused)
	}
	snapshotInput.ContentChecksum = "checksum-two"
	report.ContentChecksum = "checksum-two"
	second, err := repository.SaveDiscovery(context.Background(), project, snapshotInput, report)
	if err != nil {
		t.Fatalf("second distinct SaveDiscovery() error = %v", err)
	}
	if first.Version != 1 || second.Version != 2 {
		t.Fatalf("distinct snapshot versions = %d, %d", first.Version, second.Version)
	}
	latest, latestReport, err := repository.GetLatestDiscovery(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("GetLatestDiscovery() error = %v", err)
	}
	if latest.ID != second.ID || latestReport.CommitSHA != report.CommitSHA {
		t.Fatalf("latest snapshot/report = %#v / %#v", latest, latestReport)
	}
	if !json.Valid(latest.RawReport) {
		t.Fatalf("raw report is invalid JSON: %s", latest.RawReport)
	}
}

func TestTopologyRepositoryReplacesCatalogIdempotently(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	ctx := context.Background()
	projects := pgadapter.ProjectRepoPG{Pool: pool}
	topologies := pgadapter.TopologyRepoPG{Pool: pool}

	createSource := func(name string, role domain.RepositoryRole, kind domain.ServiceKind) (domain.Project, domain.ServiceSnapshot) {
		path := "/fixtures/" + uuid.NewString()
		project, err := projects.Upsert(ctx, domain.Project{
			Name: name, Status: domain.ProjectStatusAnalyzed, RepositoryRole: role,
			SourceIdentity: "integration:" + uuid.NewString(), LocalPath: &path,
			DefaultBranch: "main", CurrentBranch: "main", HeadCommit: "abc123",
		})
		if err != nil {
			t.Fatalf("Upsert(%s) error = %v", name, err)
		}
		now := time.Now().UTC()
		report := domain.DiscoveryReport{
			SchemaVersion: 1, ProjectID: project.ID, ProjectName: name, RepositoryRole: role,
			RepositoryPath: path, CommitSHA: "abc123", Branch: "main", ContentChecksum: "checksum-" + name,
			StartedAt: now, CompletedAt: now,
		}
		snapshot, err := projects.SaveDiscovery(ctx, project, domain.ServiceSnapshot{
			CommitSHA: "abc123", Branch: "main", ContentChecksum: report.ContentChecksum,
			ServiceKind: kind, Purpose: name + " purpose", Status: string(domain.ProjectStatusAnalyzed),
		}, report)
		if err != nil {
			t.Fatalf("SaveDiscovery(%s) error = %v", name, err)
		}
		return project, snapshot
	}
	producer, producerSnapshot := createSource("topology-producer", domain.RepositoryRoleService, domain.ServiceKindBackendService)
	consumer, consumerSnapshot := createSource("topology-consumer", domain.RepositoryRoleFrontend, domain.ServiceKindFrontendApplication)
	var first, second domain.TopologyCatalog
	var err error
	defer func() {
		for _, revisionID := range []string{first.Revision.ID, second.Revision.ID} {
			if revisionID != "" {
				_, _ = pool.Exec(ctx, `DELETE FROM audit_event WHERE resource_id = $1`, revisionID)
				_, _ = pool.Exec(ctx, `DELETE FROM topology_revision WHERE id = $1`, revisionID)
			}
		}
		_, _ = pool.Exec(ctx, `DELETE FROM audit_event WHERE resource_id IN ($1, $2)`, producer.ID, consumer.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM project WHERE id IN ($1, $2)`, producer.ID, consumer.ID)
	}()
	code := "http:get:/api/{version}/orders"
	producerID, consumerID := producer.ID, consumer.ID
	now := time.Now().UTC()
	catalog := domain.TopologyCatalog{
		Revision: domain.TopologyRevision{
			Fingerprint:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			ProjectCount: 2, ServiceCount: 2, CapabilityCount: 1, OwnershipCount: 1,
			ContractCount: 2, RelationCount: 1, DriftCount: 1,
		},
		Services: []domain.TopologyService{
			{ProjectID: producer.ID, SnapshotID: producerSnapshot.ID, Name: producer.Name, RepositoryRole: producer.RepositoryRole, ServiceKind: producerSnapshot.ServiceKind},
			{ProjectID: consumer.ID, SnapshotID: consumerSnapshot.ID, Name: consumer.Name, RepositoryRole: consumer.RepositoryRole, ServiceKind: consumerSnapshot.ServiceKind},
		},
		Capabilities: []domain.ServiceCapability{{
			ProjectID: producer.ID, SnapshotID: producerSnapshot.ID, Code: "orders", Name: "orders",
			Confidence: .9, Source: "routes.go",
		}},
		Ownership: []domain.ServiceOwnership{{
			ProjectID: producer.ID, SnapshotID: producerSnapshot.ID, ResourceType: "database_table",
			ResourceName: "orders", Confidence: .9, Source: "db/001.sql",
		}},
		Contracts: []domain.Contract{
			{ProjectID: producer.ID, SnapshotID: producerSnapshot.ID, Code: code, Type: domain.ContractTypeHTTP,
				Version: "v1", Direction: domain.ContractDirectionProvides, Definition: json.RawMessage(`{"path":"/api/v1/orders"}`),
				SourcePath: "routes.go", Checksum: "producer-checksum", DiscoveredAt: now},
			{ProjectID: consumer.ID, SnapshotID: consumerSnapshot.ID, Code: code, Type: domain.ContractTypeHTTP,
				Version: "v2", Direction: domain.ContractDirectionConsumes, Definition: json.RawMessage(`{"path":"/api/v2/orders"}`),
				SourcePath: "client.ts", Checksum: "consumer-checksum", DiscoveredAt: now},
		},
		Relations: []domain.ServiceRelation{{
			SnapshotID: consumerSnapshot.ID, SourceProjectID: consumer.ID, TargetProjectID: producer.ID,
			RelationType: domain.RelationConsumes, ContractCode: &code, Confidence: .9, Source: "client.ts",
		}},
		Drifts: []domain.ContractDrift{{
			ProducerProjectID: &producerID, ConsumerProjectID: &consumerID, ContractCode: code,
			ContractType: domain.ContractTypeHTTP, ProducerVersion: "v1", ConsumerVersion: "v2",
			Difference: json.RawMessage(`{"version_mismatch":true}`), Severity: domain.DriftSeverityError,
			SuggestedAction: "align versions",
		}},
	}

	first, err = topologies.Replace(ctx, catalog)
	if err != nil {
		t.Fatalf("first Replace() error = %v", err)
	}
	reused, err := topologies.Replace(ctx, catalog)
	if err != nil {
		t.Fatalf("reused Replace() error = %v", err)
	}
	if reused.Revision.ID != first.Revision.ID || len(reused.Services) != 2 || len(reused.Drifts) != 1 {
		t.Fatalf("reused catalog = %#v", reused)
	}
	catalog.Revision.Fingerprint = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	second, err = topologies.Replace(ctx, catalog)
	if err != nil {
		t.Fatalf("changed Replace() error = %v", err)
	}
	if second.Revision.ID == first.Revision.ID || second.Contracts[0].RevisionID != second.Revision.ID {
		t.Fatalf("changed catalog revision = %#v", second.Revision)
	}
}

func TestPlanningRepositoryStateMachineAndIdempotency(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	ctx := context.Background()
	projects := pgadapter.ProjectRepoPG{Pool: pool}
	plans := pgadapter.PlanningRepoPG{Pool: pool}
	createdProjects := make([]domain.Project, 0, 2)
	for _, name := range []string{"planning-producer", "planning-consumer"} {
		path := "/fixtures/" + uuid.NewString()
		project, err := projects.Upsert(ctx, domain.Project{
			Name: name, Status: domain.ProjectStatusAnalyzed, RepositoryRole: domain.RepositoryRoleService,
			SourceIdentity: "integration:" + uuid.NewString(), LocalPath: &path,
			DefaultBranch: "main", CurrentBranch: "main", HeadCommit: "abc123",
		})
		if err != nil {
			t.Fatalf("Upsert(%s) error = %v", name, err)
		}
		createdProjects = append(createdProjects, project)
	}
	var topologyRevisionID string
	if err := pool.QueryRow(ctx, `
INSERT INTO topology_revision (
    fingerprint, project_count, service_count, capability_count,
    ownership_count, contract_count, relation_count, drift_count
) VALUES ($1, 2, 2, 0, 0, 0, 1, 0)
RETURNING id`, strings.Repeat("c", 64)).Scan(&topologyRevisionID); err != nil {
		t.Fatalf("insert topology revision: %v", err)
	}
	var command domain.Command
	var bundle domain.PlanBundle
	var run domain.PlanRun
	defer func() {
		resourceIDs := []string{command.ID, bundle.Plan.ID, run.ID}
		for _, task := range bundle.Tasks {
			resourceIDs = append(resourceIDs, task.ID)
		}
		for _, resourceID := range resourceIDs {
			if resourceID != "" {
				_, _ = pool.Exec(ctx, `DELETE FROM audit_event WHERE resource_id = $1`, resourceID)
			}
		}
		if bundle.Approval != nil {
			_, _ = pool.Exec(ctx, `DELETE FROM approval WHERE id = $1`, bundle.Approval.ID)
		}
		if command.ID != "" {
			_, _ = pool.Exec(ctx, `
DELETE FROM audit_event WHERE resource_id IN (
    SELECT id FROM plan WHERE command_id = $1
    UNION SELECT task.id FROM task JOIN plan ON plan.id = task.plan_id WHERE plan.command_id = $1
    UNION SELECT plan_run.id FROM plan_run JOIN plan ON plan.id = plan_run.plan_id WHERE plan.command_id = $1
)`, command.ID)
			_, _ = pool.Exec(ctx, `DELETE FROM approval WHERE resource_type = 'plan' AND resource_id IN (SELECT id FROM plan WHERE command_id = $1)`, command.ID)
			_, _ = pool.Exec(ctx, `DELETE FROM command WHERE id = $1`, command.ID)
		}
		_, _ = pool.Exec(ctx, `DELETE FROM topology_revision WHERE id = $1`, topologyRevisionID)
		for _, project := range createdProjects {
			_, _ = pool.Exec(ctx, `DELETE FROM audit_event WHERE resource_id = $1`, project.ID)
			_, _ = pool.Exec(ctx, `DELETE FROM project WHERE id = $1`, project.ID)
		}
	}()

	commandInput := domain.Command{
		Source: domain.CommandSourceAPI, Text: "change planning fixtures",
		Status: domain.CommandStatusReceived, IdempotencyKey: "integration:" + uuid.NewString(),
	}
	var err error
	command, err = plans.CreateCommand(ctx, commandInput)
	if err != nil {
		t.Fatalf("CreateCommand() error = %v", err)
	}
	reusedCommand, err := plans.CreateCommand(ctx, commandInput)
	if err != nil || reusedCommand.ID != command.ID {
		t.Fatalf("reused CreateCommand() = %#v, %v", reusedCommand, err)
	}
	input := domain.PlannerInput{
		CommandID: command.ID, CommandText: command.Text, TopologyRevisionID: topologyRevisionID,
		RequestedProjectIDs: []string{createdProjects[0].ID, createdProjects[1].ID},
	}
	output := domain.PlannerOutput{
		Summary: "integration plan", RiskLevel: domain.RiskLevelMedium, Risks: []string{},
		Tasks: []domain.PlannedTask{
			{Key: "producer", ProjectID: createdProjects[0].ID, Role: "backend-coder", Title: "producer", Description: command.Text,
				AcceptanceCriteria: []string{"producer passes"}, WriteScope: []string{"internal/**"}, ModelProfile: "standard",
				Priority: 2, RiskLevel: domain.RiskLevelMedium, VerificationCommands: []string{"go test ./..."}},
			{Key: "consumer", ProjectID: createdProjects[1].ID, Role: "backend-coder", Title: "consumer", Description: command.Text,
				AcceptanceCriteria: []string{"consumer passes"}, WriteScope: []string{"internal/**"}, ModelProfile: "standard",
				Priority: 1, RiskLevel: domain.RiskLevelMedium, VerificationCommands: []string{"go test ./..."}},
		},
		Dependencies: []domain.PlannedDependency{{TaskKey: "consumer", DependsOnTaskKey: "producer", DependencyType: "consumes"}},
	}
	bundle, err = plans.CreatePlan(ctx, command, input, output)
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	reusedPlan, err := plans.CreatePlan(ctx, command, input, output)
	if err != nil || reusedPlan.Plan.ID != bundle.Plan.ID || len(reusedPlan.Tasks) != 2 || len(reusedPlan.Dependencies) != 1 {
		t.Fatalf("reused CreatePlan() = %#v, %v", reusedPlan, err)
	}
	if _, _, err := plans.PrepareRun(ctx, bundle.Plan.ID, 2); !errors.Is(err, domain.ErrApprovalNeeded) {
		t.Fatalf("PrepareRun() before approval error = %v", err)
	}
	bundle, err = plans.ApprovePlan(ctx, bundle.Plan.ID, "integration-owner", "approved")
	if err != nil || bundle.Plan.Status != domain.PlanStatusApproved || bundle.Approval == nil || bundle.Approval.Status != string(domain.ApprovalStatusApproved) {
		t.Fatalf("ApprovePlan() = %#v, %v", bundle, err)
	}
	repeatedApproval, err := plans.ApprovePlan(ctx, bundle.Plan.ID, "integration-owner", "approved")
	if err != nil || repeatedApproval.Plan.Status != domain.PlanStatusApproved {
		t.Fatalf("repeated ApprovePlan() = %#v, %v", repeatedApproval, err)
	}
	run, bundle, err = plans.PrepareRun(ctx, bundle.Plan.ID, 2)
	if err != nil {
		t.Fatalf("PrepareRun() error = %v", err)
	}
	reusedRun, _, err := plans.PrepareRun(ctx, bundle.Plan.ID, 2)
	if err != nil || reusedRun.ID != run.ID {
		t.Fatalf("reused PrepareRun() = %#v, %v", reusedRun, err)
	}
	run, err = plans.AttachTemporalRun(ctx, run.ID, "temporal-run-id")
	if err != nil || run.TemporalRunID == nil || *run.TemporalRunID != "temporal-run-id" {
		t.Fatalf("AttachTemporalRun() = %#v, %v", run, err)
	}
	run, err = plans.UpdateRunStatus(ctx, run.ID, domain.PlanRunStatusRunning, "")
	if err != nil {
		t.Fatalf("UpdateRunStatus(running) error = %v", err)
	}
	producerTask, consumerTask := bundle.Tasks[0], bundle.Tasks[1]
	if producerTask.Priority < consumerTask.Priority {
		producerTask, consumerTask = consumerTask, producerTask
	}
	if _, err := plans.MarkTaskReady(ctx, run.ID, producerTask.ID); err != nil {
		t.Fatalf("MarkTaskReady(producer) error = %v", err)
	}
	if _, err := plans.RecordTaskResult(ctx, run.ID, domain.TaskResult{TaskID: producerTask.ID, Status: domain.TaskStatusCompleted}); err != nil {
		t.Fatalf("RecordTaskResult(producer) error = %v", err)
	}
	if _, err := plans.MarkTaskReady(ctx, run.ID, consumerTask.ID); err != nil {
		t.Fatalf("MarkTaskReady(consumer) error = %v", err)
	}
	if _, err := plans.UpdateRunStatus(ctx, run.ID, domain.PlanRunStatusPaused, ""); err != nil {
		t.Fatalf("pause run error = %v", err)
	}
	if _, err := plans.UpdateRunStatus(ctx, run.ID, domain.PlanRunStatusRunning, ""); err != nil {
		t.Fatalf("resume run error = %v", err)
	}
	if _, err := plans.RecordTaskResult(ctx, run.ID, domain.TaskResult{TaskID: consumerTask.ID, Status: domain.TaskStatusCompleted}); err != nil {
		t.Fatalf("RecordTaskResult(consumer) error = %v", err)
	}
	run, err = plans.UpdateRunStatus(ctx, run.ID, domain.PlanRunStatusCompleted, "")
	if err != nil || run.Status != domain.PlanRunStatusCompleted {
		t.Fatalf("complete run = %#v, %v", run, err)
	}
	completed, err := plans.GetPlan(ctx, bundle.Plan.ID)
	if err != nil || completed.Plan.Status != domain.PlanStatusCompleted || completed.Run == nil || completed.Run.Status != domain.PlanRunStatusCompleted {
		t.Fatalf("completed plan = %#v, %v", completed, err)
	}
}

func TestInitialMigrationEnforcesIdempotencyConstraints(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()

	t.Run("project local path", func(t *testing.T) {
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin transaction: %v", err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()

		_, err = tx.Exec(context.Background(), `
INSERT INTO project (name, status, local_path, source_identity)
VALUES ('fixture', 'connected', '/fixtures/project', 'local:/fixtures/project')`)
		if err != nil {
			t.Fatalf("insert first project: %v", err)
		}
		_, err = tx.Exec(context.Background(), `
INSERT INTO project (name, status, local_path, source_identity)
VALUES ('fixture-duplicate', 'connected', '/fixtures/project', 'local:/fixtures/project-duplicate')`)
		assertUniqueViolation(t, err)
	})

	t.Run("command idempotency key", func(t *testing.T) {
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin transaction: %v", err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()

		_, err = tx.Exec(context.Background(), `
INSERT INTO command (source, text, status, idempotency_key)
VALUES ('api', 'fixture command', 'received', 'fixture-key')`)
		if err != nil {
			t.Fatalf("insert first command: %v", err)
		}
		_, err = tx.Exec(context.Background(), `
INSERT INTO command (source, text, status, idempotency_key)
VALUES ('api', 'duplicate command', 'received', 'fixture-key')`)
		assertUniqueViolation(t, err)
	})
}

func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is required for integration tests")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	return pool
}

func assertUniqueViolation(t *testing.T, err error) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23505" {
		t.Fatalf("error = %v, want PostgreSQL unique violation", err)
	}
}
