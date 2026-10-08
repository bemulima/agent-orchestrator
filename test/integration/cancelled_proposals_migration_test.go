//go:build integration

package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	pgadapter "github.com/bemulima/agent-orchestrator/internal/adapters/postgres"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// A fresh namespace preserves the real disposable integration database and
// proves the deployed repository operation against the pre-fix schema.
func proposalMigrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	admin := integrationPool(t)
	schema := "proposal_m019_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx := context.Background()
	_, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	require.NoError(t, err)
	config := admin.Config().Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Close()
		_, e := admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		if e != nil {
			t.Errorf("cleanup fixture schema: %v", e)
		}
		admin.Close()
	})
	files, err := filepath.Glob("../../db/migrations/*.up.sql")
	require.NoError(t, err)
	sort.Strings(files)
	for _, file := range files {
		if filepath.Base(file)[:3] > "017" {
			continue
		}
		raw, e := os.ReadFile(file)
		require.NoError(t, e)
		_, e = pool.Exec(ctx, string(raw))
		require.NoError(t, e, file)
	}
	return pool
}

func proposalMigrationSQL(t *testing.T, direction string) string {
	t.Helper()
	raw, err := os.ReadFile("../../db/migrations/019_allow_cancelled_issue_proposals." + direction + ".sql")
	require.NoError(t, err)
	return string(raw)
}

func proposalFixture(t *testing.T, pool *pgxpool.Pool) (domain.Command, domain.PlanBundle, string) {
	t.Helper()
	ctx := context.Background()
	repo := pgadapter.PlanningRepoPG{Pool: pool}
	command, err := repo.CreateCommand(ctx, domain.Command{Source: domain.CommandSourceAPI, Text: "migration supersession fixture", Status: domain.CommandStatusReceived, IdempotencyKey: uuid.NewString()})
	require.NoError(t, err)
	var projectID, itemID, topologyID string
	fixturePath := "/fixtures/" + uuid.NewString()
	project, err := (pgadapter.ProjectRepoPG{Pool: pool}).Upsert(ctx, domain.Project{Name: "proposal-fixture", Status: domain.ProjectStatusAnalyzed, RepositoryRole: domain.RepositoryRoleService, SourceIdentity: "fixture:" + uuid.NewString(), LocalPath: &fixturePath, DefaultBranch: "main", CurrentBranch: "main", HeadCommit: "fixture"})
	require.NoError(t, err)
	projectID = project.ID
	err = pool.QueryRow(ctx, `INSERT INTO topology_revision(fingerprint,project_count,service_count,capability_count,ownership_count,contract_count,relation_count,drift_count) VALUES ($1,0,0,0,0,0,0,0) RETURNING id`, strings.ReplaceAll(uuid.NewString(), "-", "")+strings.ReplaceAll(uuid.NewString(), "-", "")).Scan(&topologyID)
	require.NoError(t, err)
	bundle, err := repo.CreatePlan(ctx, command, domain.PlannerInput{CommandID: command.ID, CommandText: command.Text, TopologyRevisionID: topologyID}, domain.PlannerOutput{Summary: "predecessor", RiskLevel: domain.RiskLevelLow, Tasks: []domain.PlannedTask{{Key: "fixture", ProjectID: projectID, Role: "coder", Title: "fixture", Description: "fixture", AcceptanceCriteria: []string{"fixture"}, WriteScope: []string{"internal/value.go"}, VerificationCommands: []string{}, ModelProfile: "fast", Priority: 1, RiskLevel: domain.RiskLevelLow}}})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO approval(resource_type,resource_id,action,status) VALUES ('plan',$1,'run','pending')`, bundle.Plan.ID)
	require.NoError(t, err)
	err = pool.QueryRow(ctx, `INSERT INTO work_item(plan_id,project_id,kind,provider,issue_type,status,title,body,labels,milestone,assignees,agent_role,agent_thread_id,complexity,model_profile,plan_fingerprint,idempotency_key) VALUES ($1,$2,'issue','github','task','proposed','fixture','fixture','[]','','[]','issue-manager','fixture','low','fast',$3,$4) RETURNING id`, bundle.Plan.ID, projectID, strings.Repeat("a", 64), uuid.NewString()).Scan(&itemID)
	require.NoError(t, err)
	return command, bundle, itemID
}

func proposalRow(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var row string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT row_to_json(work_item)::text FROM work_item WHERE id=$1`, id).Scan(&row))
	return row
}

func TestCancelledProposalMigrationPreservesSupersessionAndRefusesUnsafeDowngrade(t *testing.T) {
	pool := proposalMigrationPool(t)
	ctx := context.Background()
	repo := pgadapter.PlanningRepoPG{Pool: pool}
	_, predecessor, itemID := proposalFixture(t, pool)
	independentCommand, independent, independentID := proposalFixture(t, pool)
	successorCommand, err := repo.CreateCommand(ctx, domain.Command{Source: domain.CommandSourceAPI, Text: "explicit successor", Status: domain.CommandStatusReceived, IdempotencyKey: uuid.NewString()})
	require.NoError(t, err)
	input := domain.PlannerInput{CommandID: successorCommand.ID, CommandText: successorCommand.Text, SupersedesPlanID: predecessor.Plan.ID, TopologyRevisionID: *predecessor.Plan.TopologyRevisionID}
	output := domain.PlannerOutput{Summary: "successor", RiskLevel: domain.RiskLevelLow}
	before := proposalRow(t, pool, itemID)
	approvalBefore := proposalApprovalRow(t, pool, predecessor.Plan.ID)
	taskBefore := proposalTaskRows(t, pool, predecessor.Plan.ID)
	independentBefore := proposalRow(t, pool, independentID)
	_, err = repo.CreatePlan(ctx, successorCommand, input, output)
	var pgerr *pgconn.PgError
	require.True(t, errors.As(err, &pgerr))
	require.Equal(t, "23514", pgerr.Code)
	require.Equal(t, "work_item_check1", pgerr.ConstraintName)
	require.Equal(t, before, proposalRow(t, pool, itemID))
	require.Equal(t, approvalBefore, proposalApprovalRow(t, pool, predecessor.Plan.ID))
	require.Equal(t, taskBefore, proposalTaskRows(t, pool, predecessor.Plan.ID))
	got, err := repo.GetPlan(ctx, predecessor.Plan.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlanStatusDiscussion, got.Plan.Status)
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM plan WHERE command_id=$1`, successorCommand.ID).Scan(&count))
	require.Zero(t, count)
	t.Log("RED characterized: real CreatePlan rejected by old constraint; proposal and predecessor unchanged; successor rolled back")
	_, err = pool.Exec(ctx, proposalMigrationSQL(t, "up"))
	require.NoError(t, err)
	successor, err := repo.CreatePlan(ctx, successorCommand, input, output)
	require.NoError(t, err)
	require.Equal(t, predecessor.Plan.ID, *successor.Plan.SupersedesPlanID)
	got, err = repo.GetPlan(ctx, predecessor.Plan.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlanStatusCancelled, got.Plan.Status)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM task WHERE plan_id=$1 AND status='cancelled'`, predecessor.Plan.ID).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM approval WHERE resource_id=$1 AND status='cancelled'`, predecessor.Plan.ID).Scan(&count))
	require.Equal(t, 1, count)
	var status string
	var number *int64
	var url string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status,external_number,external_url FROM work_item WHERE id=$1`, itemID).Scan(&status, &number, &url))
	require.Equal(t, "cancelled", status)
	require.Nil(t, number)
	require.Empty(t, url)
	require.Equal(t, independentBefore, proposalRow(t, pool, independentID))
	got, err = repo.GetPlan(ctx, independent.Plan.ID)
	require.NoError(t, err)
	require.Equal(t, independentCommand.ID, got.Plan.CommandID)
	require.Equal(t, domain.PlanStatusDiscussion, got.Plan.Status)
	cancelledBefore := proposalRow(t, pool, itemID)
	_, err = pool.Exec(ctx, proposalMigrationSQL(t, "down"))
	require.True(t, errors.As(err, &pgerr))
	require.Equal(t, "P0001", pgerr.Code)
	require.Equal(t, cancelledBefore, proposalRow(t, pool, itemID))
	require.Equal(t, independentBefore, proposalRow(t, pool, independentID))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE conrelid='work_item'::regclass AND conname='work_item_external_reference_check'`).Scan(&count))
	require.Equal(t, 1, count)
	t.Log("GREEN: supersession cancels unissued proposal; incompatible down refuses without changing row history or constraint")
}

func TestCancelledProposalMigrationCompatibleRoundTripAndReferencePairs(t *testing.T) {
	pool := proposalMigrationPool(t)
	ctx := context.Background()
	_, _, itemID := proposalFixture(t, pool)
	before := proposalRow(t, pool, itemID)
	_, err := pool.Exec(ctx, proposalMigrationSQL(t, "up"))
	require.NoError(t, err)
	for _, tc := range []struct {
		name, status string
		number       *int64
		url          string
		valid        bool
	}{
		{"proposed unissued", "proposed", nil, "", true}, {"cancelled unissued", "cancelled", nil, "", true}, {"published unissued", "published", nil, "", false},
		{"cancelled URL only", "cancelled", nil, "https://fixture.invalid/1", false}, {"cancelled number only", "cancelled", proposalNumber(1), "", false}, {"cancelled zero", "cancelled", proposalNumber(0), "https://fixture.invalid/1", false},
		{"cancelled issued", "cancelled", proposalNumber(1), "https://fixture.invalid/1", true}, {"published issued", "published", proposalNumber(1), "https://fixture.invalid/1", true}, {"proposed issued", "proposed", proposalNumber(1), "https://fixture.invalid/1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, e := pool.Begin(ctx)
			require.NoError(t, e)
			defer tx.Rollback(ctx)
			_, e = tx.Exec(ctx, `UPDATE work_item SET status=$2,external_number=$3,external_url=$4 WHERE id=$1`, itemID, tc.status, tc.number, tc.url)
			if tc.valid {
				require.NoError(t, e)
			} else {
				var p *pgconn.PgError
				require.True(t, errors.As(e, &p))
				require.Equal(t, "23514", p.Code)
			}
		})
	}
	require.Equal(t, before, proposalRow(t, pool, itemID))
	_, err = pool.Exec(ctx, proposalMigrationSQL(t, "down"))
	require.NoError(t, err)
	require.Equal(t, before, proposalRow(t, pool, itemID))
	_, err = pool.Exec(ctx, `UPDATE work_item SET status='cancelled' WHERE id=$1`, itemID)
	var pgerr *pgconn.PgError
	require.True(t, errors.As(err, &pgerr))
	require.Equal(t, "23514", pgerr.Code)
	require.Equal(t, "work_item_check1", pgerr.ConstraintName)
	_, err = pool.Exec(ctx, proposalMigrationSQL(t, "up"))
	require.NoError(t, err)
	require.Equal(t, before, proposalRow(t, pool, itemID))
}
func proposalNumber(n int64) *int64 { return &n }

func proposalApprovalRow(t *testing.T, pool *pgxpool.Pool, planID string) string {
	t.Helper()
	var row string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT row_to_json(approval)::text FROM approval WHERE resource_id=$1`, planID).Scan(&row))
	return row
}
func proposalTaskRows(t *testing.T, pool *pgxpool.Pool, planID string) string {
	t.Helper()
	var row string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT json_agg(task ORDER BY id)::text FROM task WHERE plan_id=$1`, planID).Scan(&row))
	return row
}
