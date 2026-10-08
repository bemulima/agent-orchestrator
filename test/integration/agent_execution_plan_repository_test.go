//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	pgadapter "github.com/bemulima/agent-orchestrator/internal/adapters/postgres"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestAgentShardMigrationRoundTripsBaselineAndRollsBack(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	ctx := context.Background()
	projectRepo := pgadapter.ProjectRepoPG{Pool: pool}
	plans := pgadapter.PlanningRepoPG{Pool: pool}
	path := "/fixtures/contract-baseline-" + uuid.NewString()
	project, err := projectRepo.Upsert(ctx, domain.Project{
		Name: "contract-baseline-" + uuid.NewString(), Status: domain.ProjectStatusAnalyzed,
		RepositoryRole: domain.RepositoryRoleService, SourceIdentity: "integration:" + uuid.NewString(),
		LocalPath: &path, DefaultBranch: "main", CurrentBranch: "main", HeadCommit: strings.Repeat("a", 40),
	})
	if err != nil {
		t.Fatalf("create project fixture: %v", err)
	}
	var topologyID string
	if err := pool.QueryRow(ctx, `
INSERT INTO topology_revision (
    fingerprint, project_count, service_count, capability_count,
    ownership_count, contract_count, relation_count, drift_count
) VALUES ($1, 1, 0, 0, 0, 0, 0, 0) RETURNING id`, strings.Repeat("f", 64)).Scan(&topologyID); err != nil {
		t.Fatalf("create topology fixture: %v", err)
	}
	command, err := plans.CreateCommand(ctx, domain.Command{
		Source: domain.CommandSourceAPI, Text: "contract baseline migration fixture", Status: domain.CommandStatusReceived,
		IdempotencyKey: "integration:" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create command fixture: %v", err)
	}
	bundle, err := plans.CreatePlan(ctx, command, domain.PlannerInput{
		CommandID: command.ID, CommandText: command.Text, TopologyRevisionID: topologyID,
	}, domain.PlannerOutput{Summary: "contract baseline fixture", RiskLevel: domain.RiskLevelLow, Tasks: []domain.PlannedTask{{
		Key: "contract-freeze", ProjectID: project.ID, Role: "coder", Title: "freeze contracts", Description: "fixture",
		AcceptanceCriteria: []string{"roundtrips"}, WriteScope: []string{"internal/usecase/**"}, ModelProfile: "standard",
		RiskLevel: domain.RiskLevelLow, VerificationCommands: []string{"go test ./..."},
	}}})
	if err != nil {
		t.Fatalf("create plan fixture: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_event WHERE resource_id IN (
SELECT $1::uuid UNION SELECT $2::uuid UNION SELECT id FROM plan WHERE command_id = $2
UNION SELECT task.id FROM task JOIN plan ON plan.id = task.plan_id WHERE plan.command_id = $2
)`, project.ID, command.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM command WHERE id = $1`, command.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM topology_revision WHERE id = $1`, topologyID)
		_, _ = pool.Exec(ctx, `DELETE FROM project WHERE id = $1`, project.ID)
	}()
	bundle = approveIssueBackedPlan(t, pool, bundle)
	task := bundle.Tasks[0]
	repository := pgadapter.AgentExecutionPlanRepoPG{Pool: pool}
	baseline := domain.ContractBaseline{
		ID: uuid.NewString(), PlanID: bundle.Plan.ID, TaskID: task.ID, RepositoryProjectID: project.ID,
		Repository: project.Name, ProfileID: "go.canonical", PlanningContractState: domain.ContractPlanFreezeNeeded,
		ExecutionState: domain.ContractBaselineFrozen, ApprovedPlanFingerprint: bundle.Plan.Fingerprint,
		MaterializedContracts: []domain.ContractReference{{
			Kind: "application-command-result", RepositoryProjectID: project.ID, Path: "internal/usecase/result.go",
			Symbol: "ApplicationCommandResult", RouteID: "backend.usecase", Relation: "implements",
		}},
		Files:           []domain.ContractBaselineFile{{Path: "internal/usecase/result.go", SHA256: strings.Repeat("b", 64), Generated: true}},
		AggregateSHA256: strings.Repeat("c", 64), ContractBaselineCommit: strings.Repeat("d", 40),
		ContractAgentPassed: true, ContractAgentThreadID: "contract-agent-thread", ContractAgentResultSHA256: strings.Repeat("e", 64),
		MechanicalReviewPassed: true, MechanicalReviewSHA256: strings.Repeat("f", 64),
		ContractVerificationPassed: true, ContractVerification: "go test ./... passed",
		IndependentReviewPassed: true, IndependentReviewerID: "contract-review-thread", IndependentReviewStatus: "PASS",
		IndependentReviewSHA256: strings.Repeat("a", 64), ContractPlanFingerprint: strings.Repeat("b", 64),
		ProfileFingerprint: strings.Repeat("c", 64), RepositoryRevision: strings.Repeat("d", 40),
		Validation: domain.ContractBaselineValidation{Passed: true}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := repository.SaveContractBaseline(ctx, baseline); err != nil {
		t.Fatalf("save contract baseline: %v", err)
	}
	roundTrip := func() {
		t.Helper()
		items, err := repository.ListContractBaselines(ctx, bundle.Plan.ID)
		if err != nil || len(items) != 1 {
			t.Fatalf("ListContractBaselines() = %#v, %v", items, err)
		}
		if items[0].ID != baseline.ID || items[0].ContractBaselineCommit != baseline.ContractBaselineCommit ||
			items[0].ContractAgentThreadID != baseline.ContractAgentThreadID || !items[0].IndependentReviewPassed ||
			items[0].Files[0].SHA256 != baseline.Files[0].SHA256 {
			t.Fatalf("ContractBaseline roundtrip lost freeze evidence: %#v", items[0])
		}
	}
	roundTrip()
	_, err = pool.Exec(ctx, `INSERT INTO contract_baseline (
id, plan_id, task_id, repository_project_id, profile_id, planning_contract_state,
execution_state, payload
) VALUES ($1, $2, $3, $4, 'go.canonical', 'FREEZE_REQUIRED', 'INVALID', '{}'::jsonb)`,
		uuid.NewString(), bundle.Plan.ID, task.ID, project.ID)
	if err == nil {
		t.Fatal("invalid execution_state passed the migration constraint")
	}
	assertAgentPlanCheckViolation(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO contract_baseline (
id, plan_id, task_id, repository_project_id, profile_id, planning_contract_state,
execution_state, payload
) VALUES ($1, $2, $3, $4, 'go.canonical', 'FREEZE_REQUIRED', 'BLOCKED', '[]'::jsonb)`,
		uuid.NewString(), bundle.Plan.ID, task.ID, project.ID)
	if err == nil {
		t.Fatal("non-object baseline payload passed the migration constraint")
	}
	assertAgentPlanCheckViolation(t, err)

	oldFanout := domain.ShardFanoutExecution{
		PlanID: baseline.PlanID, ContractBaselineID: baseline.ID, BaselineCommit: baseline.ContractBaselineCommit,
		State: "FANOUT_RUNNING", Barrier: domain.ShardBarrierPending, Composition: "PENDING",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := repository.SaveShardFanout(ctx, oldFanout); err != nil {
		t.Fatal(err)
	}
	candidate := oldFanout
	candidate.ContractBaselineID = uuid.NewString()
	if err := repository.SaveShardFanout(ctx, candidate); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("active fanout replacement error = %v", err)
	}

	retired := baseline
	retired.ExecutionState = domain.ContractBaselineInvalidated
	if err := repository.SaveContractBaseline(ctx, retired); err != nil {
		t.Fatalf("retire baseline: %v", err)
	}
	replacement := baseline
	replacement.ID = uuid.NewString()
	// Equal timestamps must not make latest-version selection ambiguous.
	replacement.ContractBaselineCommit = strings.Repeat("e", 40)
	if err := repository.SaveContractBaseline(ctx, replacement); err != nil {
		t.Fatalf("save replacement baseline: %v", err)
	}
	items, err := repository.ListContractBaselines(ctx, bundle.Plan.ID)
	if err != nil || len(items) != 1 || items[0].ID != replacement.ID {
		t.Fatalf("latest baseline selection = %#v, %v", items, err)
	}
	if err := repository.SaveContractBaseline(ctx, baseline); !errors.Is(err, domain.ErrInvalidStatus) {
		t.Fatalf("reviving retired baseline error = %v", err)
	}
	history, err := repository.GetContractBaseline(ctx, retired.ID)
	if err != nil || history.ExecutionState != domain.ContractBaselineInvalidated || history.ContractBaselineCommit != retired.ContractBaselineCommit {
		t.Fatalf("historical baseline changed: %#v, %v", history, err)
	}
	extra := replacement
	extra.ID = uuid.NewString()
	if err := repository.SaveContractBaseline(ctx, extra); err == nil {
		t.Fatal("multiple current baselines accepted for one task")
	}
	down021, up021 := migration021SQL(t)
	if _, err := pool.Exec(ctx, down021); err == nil {
		t.Fatal("rollback discarded or allowed duplicate version history")
	}

	candidate.ContractBaselineID, candidate.BaselineCommit = replacement.ID, replacement.ContractBaselineCommit
	if err := repository.SaveShardFanout(ctx, candidate); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("running retired fanout replacement error = %v", err)
	}
	oldFanout.State, oldFanout.Barrier = "BARRIER_BLOCKED", domain.ShardBarrierBlocked
	if err := repository.SaveShardFanout(ctx, oldFanout); err != nil {
		t.Fatal(err)
	}
	pendingReplacement := replacement
	pendingReplacement.ExecutionState = domain.ContractBaselinePending
	if err := repository.SaveContractBaseline(ctx, pendingReplacement); err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveShardFanout(ctx, candidate); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("unfrozen replacement fanout error = %v", err)
	}
	if err := repository.SaveContractBaseline(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	assertCurrentFanoutHistory(t, repository, baseline, replacement)
	var supersededPayload []byte
	if err := pool.QueryRow(ctx, `SELECT payload->'prior_execution' FROM audit_event
WHERE resource_id=$1 AND action='plan.shard_fanout_superseded'`, baseline.PlanID).Scan(&supersededPayload); err != nil {
		t.Fatalf("superseded fanout audit missing: %v", err)
	}
	var historicalFanout domain.ShardFanoutExecution
	if err := json.Unmarshal(supersededPayload, &historicalFanout); err != nil || historicalFanout.ContractBaselineID != baseline.ID || historicalFanout.State != "BARRIER_BLOCKED" {
		t.Fatalf("superseded fanout evidence changed: %#v, %v", historicalFanout, err)
	}

	down019, up019 := migration019SQL(t)
	if _, err := pool.Exec(ctx, down019); err != nil {
		t.Fatalf("apply migration 019 down in disposable database: %v", err)
	}
	assertProposedWorkItemCancellation(t, pool, bundle, false)
	if _, err := pool.Exec(ctx, up019); err != nil {
		t.Fatalf("reapply migration 019 in disposable database: %v", err)
	}
	assertProposedWorkItemCancellation(t, pool, bundle, true)

	// Roll schema versions back in reverse order inside a fixture transaction.
	// Remove only the replacement fixture there to exercise the successful 021
	// rollback; rollback of this transaction restores all historical evidence.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM shard_fanout_execution WHERE plan_id = $1`, replacement.PlanID); err != nil {
		t.Fatalf("remove fanout fixture inside rollback transaction: %v", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM contract_baseline WHERE id = $1`, replacement.ID); err != nil {
		t.Fatalf("remove replacement fixture inside rollback transaction: %v", err)
	}
	down020, up020 := migration020SQL(t)
	down018, up018 := migration018SQL(t)
	for _, step := range []struct{ name, sql string }{
		{"021 down", down021}, {"020 down", down020}, {"018 down", down018},
	} {
		if _, err := tx.Exec(ctx, step.sql); err != nil {
			t.Fatalf("apply migration %s in disposable transaction: %v", step.name, err)
		}
	}
	for _, table := range []string{"architectural_shard", "contract_baseline", "fanout_readiness", "shard_attempt", "shard_fanout_execution"} {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil || exists {
			t.Fatalf("down migration left table %s: exists=%t error=%v", table, exists, err)
		}
	}
	for _, table := range []string{"project", "plan", "task"} {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil || !exists {
			t.Fatalf("down migration damaged existing table %s: exists=%t error=%v", table, exists, err)
		}
	}
	for _, step := range []struct{ name, sql string }{
		{"018 up", up018}, {"020 up", up020}, {"021 up", up021},
	} {
		if _, err := tx.Exec(ctx, step.sql); err != nil {
			t.Fatalf("reapply migration %s in disposable transaction: %v", step.name, err)
		}
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("restore fixture history after migration roundtrip: %v", err)
	}
	baseline = replacement
	roundTrip()
	history, err = repository.GetContractBaseline(ctx, retired.ID)
	if err != nil || history.ExecutionState != domain.ContractBaselineInvalidated || history.ContractBaselineCommit != retired.ContractBaselineCommit {
		t.Fatalf("migration roundtrip changed historical baseline: %#v, %v", history, err)
	}

}

func assertProposedWorkItemCancellation(t *testing.T, pool *pgxpool.Pool, bundle domain.PlanBundle, wantAllowed bool) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin cancellation fixture transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO work_item (
    plan_id, project_id, kind, provider, issue_type, status, title, body,
    labels, milestone, assignees, agent_role, agent_thread_id, complexity,
    model_profile, plan_fingerprint, idempotency_key
) VALUES ($1, $2, 'issue', 'github', 'task', 'proposed', 'fixture', 'fixture',
    '[]'::jsonb, '', '[]'::jsonb, 'issue-manager', 'migration-test', 'low',
    'standard', $3, $4) RETURNING id`, bundle.Plan.ID, bundle.Tasks[0].ProjectID,
		bundle.Plan.Fingerprint, "migration-test:"+uuid.NewString()).Scan(&id); err != nil {
		t.Fatalf("insert proposed work item fixture: %v", err)
	}
	_, err = tx.Exec(ctx, `UPDATE work_item SET status = 'cancelled' WHERE id = $1`, id)
	if wantAllowed && err != nil {
		t.Fatalf("cancel proposed work item without external issue: %v", err)
	}
	if !wantAllowed {
		assertAgentPlanCheckViolation(t, err)
	}
}

func assertAgentPlanCheckViolation(t *testing.T, err error) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23514" {
		t.Fatalf("constraint error = %v, want PostgreSQL check violation", err)
	}
}

func migration018SQL(t *testing.T) (string, string) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve migration fixture source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "../.."))
	read := func(name string) string {
		content, err := os.ReadFile(filepath.Join(root, "db", "migrations", name))
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		return string(content)
	}
	return read("018_agent_shard_execution.down.sql"), read("018_agent_shard_execution.up.sql")
}

func migration019SQL(t *testing.T) (string, string) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve migration fixture source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "../.."))
	read := func(name string) string {
		content, err := os.ReadFile(filepath.Join(root, "db", "migrations", name))
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		return string(content)
	}
	return read("019_allow_cancelled_issue_proposals.down.sql"), read("019_allow_cancelled_issue_proposals.up.sql")
}

func migration021SQL(t *testing.T) (string, string) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve migration fixture source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "../.."))
	read := func(name string) string {
		content, err := os.ReadFile(filepath.Join(root, "db", "migrations", name))
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		return string(content)
	}
	return read("021_contract_baseline_versions.down.sql"), read("021_contract_baseline_versions.up.sql")
}

func migration020SQL(t *testing.T) (string, string) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve migration fixture source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "../.."))
	read := func(name string) string {
		content, err := os.ReadFile(filepath.Join(root, "db", "migrations", name))
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		return string(content)
	}
	return read("020_shard_fanout_execution.down.sql"), read("020_shard_fanout_execution.up.sql")
}

func assertCurrentFanoutHistory(t *testing.T, repository pgadapter.AgentExecutionPlanRepoPG, old, current domain.ContractBaseline) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	shards := make([]domain.ArchitecturalShard, 3)
	for i := range shards {
		shards[i] = domain.ArchitecturalShard{
			ID: uuid.NewString(), PlanID: current.PlanID, TaskID: current.TaskID,
			RepositoryProjectID: current.RepositoryProjectID, ProfileID: current.ProfileID,
			RouteID: []string{"http", "usecase", "persistence"}[i], Status: "PLANNED", CreatedAt: now,
		}
	}
	if err := repository.SaveArchitecturalShards(ctx, current.PlanID, current.TaskID, shards); err != nil {
		t.Fatal(err)
	}
	for _, shard := range shards {
		for number := 1; number <= 3; number++ {
			base := current
			if number == 1 {
				base = old
			}
			attempt := domain.ShardAttempt{
				ID: uuid.NewString(), PlanID: current.PlanID, TaskID: current.TaskID, ShardID: shard.ID,
				AttemptNumber: number, BaselineCommit: base.ContractBaselineCommit,
				Status: domain.ShardAttemptFailed, WorkerStartedAt: now, FinishedAt: &now, CreatedAt: now, UpdatedAt: now,
				WorkPackage: domain.WorkPackage{ExecutionBase: domain.ShardExecutionBase{
					Kind: domain.ShardBaseContractBaseline, Revision: base.ContractBaselineCommit, ContractBaselineID: base.ID,
				}},
			}
			if err := repository.SaveShardAttempt(ctx, attempt); err != nil {
				t.Fatal(err)
			}
		}
	}
	historyBefore, err := repository.ListShardAttempts(ctx, current.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	active := historyBefore[0]
	active.Status, active.FinishedAt = domain.ShardAttemptRunning, nil
	if err := repository.SaveShardAttempt(ctx, active); err != nil {
		t.Fatal(err)
	}
	blockedReplacement := domain.ShardFanoutExecution{
		PlanID: current.PlanID, ContractBaselineID: current.ID, BaselineCommit: current.ContractBaselineCommit,
		State: "FANOUT_RUNNING", Barrier: domain.ShardBarrierPending, Composition: "PENDING", CreatedAt: now, UpdatedAt: now,
	}
	if err := repository.SaveShardFanout(ctx, blockedReplacement); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("replacement while historical worker active error = %v", err)
	}
	active.Status, active.FinishedAt = domain.ShardAttemptFailed, &now
	if err := repository.SaveShardAttempt(ctx, active); err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveShardFanout(ctx, domain.ShardFanoutExecution{
		PlanID: current.PlanID, ContractBaselineID: current.ID, BaselineCommit: current.ContractBaselineCommit,
		State: "BARRIER_BLOCKED", Barrier: domain.ShardBarrierBlocked, Composition: "PENDING", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	// Re-preparation must update stable shard rows without cascading away attempts.
	shards[0].ExecutionBase = domain.ShardExecutionBase{Kind: domain.ShardBaseContractBaseline, Revision: current.ContractBaselineCommit, ContractBaselineID: current.ID}
	if err := repository.SaveArchitecturalShards(ctx, current.PlanID, current.TaskID, shards); err != nil {
		t.Fatal(err)
	}
	historyAfterPreparation, err := repository.ListShardAttempts(ctx, current.PlanID)
	if err != nil || len(historyAfterPreparation) != 9 {
		t.Fatalf("re-preparation erased historical attempts: %#v, %v", historyAfterPreparation, err)
	}
	if err := repository.SaveArchitecturalShards(ctx, current.PlanID, current.TaskID, shards[:2]); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("removing shard with historical attempts error = %v", err)
	}
	persistedShards, err := repository.ListArchitecturalShards(ctx, current.PlanID)
	if err != nil || len(persistedShards) != 3 {
		t.Fatalf("rejected shard removal changed projection: %#v, %v", persistedShards, err)
	}
	view, err := repository.GetShardFanout(ctx, current.PlanID)
	if err != nil || len(view.Attempts) != 3 {
		t.Fatalf("current fanout = %#v, %v", view, err)
	}
	for _, attempt := range view.Attempts {
		if attempt.AttemptNumber != 3 || attempt.BaselineCommit != current.ContractBaselineCommit {
			t.Fatalf("fanout included historical attempt: %#v", attempt)
		}
	}
	history, err := repository.ListShardAttempts(ctx, current.PlanID)
	if err != nil || len(history) != 9 {
		t.Fatalf("full attempt history = %#v, %v", history, err)
	}
}
