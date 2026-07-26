//go:build integration

package integration

import (
	"context"
	"testing"

	pgadapter "github.com/bemulima/agent-orchestrator/internal/adapters/postgres"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestUIReadRepositoryQueries(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	repository := pgadapter.UIReadRepoPG{Pool: pool}
	ctx := context.Background()

	if _, err := repository.Dashboard(ctx); err != nil {
		t.Fatalf("Dashboard() error = %v", err)
	}
	queries := []struct {
		name string
		run  func() error
	}{
		{name: "plans", run: func() error { _, _, err := repository.ListPlans(ctx, domain.PageQuery{Limit: 5}); return err }},
		{name: "runs", run: func() error { _, _, err := repository.ListRuns(ctx, domain.PageQuery{Limit: 5}); return err }},
		{name: "tasks", run: func() error { _, _, err := repository.ListTasks(ctx, domain.PageQuery{Limit: 5}); return err }},
		{name: "approvals", run: func() error { _, _, err := repository.ListApprovals(ctx, domain.PageQuery{Limit: 5}); return err }},
		{name: "activity", run: func() error { _, _, err := repository.ListActivity(ctx, domain.PageQuery{Limit: 5}); return err }},
	}
	for _, query := range queries {
		if err := query.run(); err != nil {
			t.Fatalf("List %s error = %v", query.name, err)
		}
	}
}
