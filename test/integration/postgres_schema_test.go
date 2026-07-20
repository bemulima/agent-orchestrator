//go:build integration

package integration

import (
	"context"
	"errors"
	"os"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
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
		"gitlab_link", "plan", "project", "service_capability",
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
INSERT INTO project (name, status, local_path) VALUES ('fixture', 'connected', '/fixtures/project')`)
		if err != nil {
			t.Fatalf("insert first project: %v", err)
		}
		_, err = tx.Exec(context.Background(), `
INSERT INTO project (name, status, local_path) VALUES ('fixture-duplicate', 'connected', '/fixtures/project')`)
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
