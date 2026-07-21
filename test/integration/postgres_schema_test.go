//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
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
		"task_attempt", "task_dependency", "telegram_user",
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
VALUES ('api', 'fixture command', 'created', 'fixture-key')`)
		if err != nil {
			t.Fatalf("insert first command: %v", err)
		}
		_, err = tx.Exec(context.Background(), `
INSERT INTO command (source, text, status, idempotency_key)
VALUES ('api', 'duplicate command', 'created', 'fixture-key')`)
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
