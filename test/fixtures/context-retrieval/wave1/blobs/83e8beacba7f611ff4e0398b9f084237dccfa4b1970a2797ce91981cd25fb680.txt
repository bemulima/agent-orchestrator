// Command user-service-migrate applies the repository SQL migrations through DB_DSN.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationTable = "schema_migrations"

type state struct {
	database      string
	role          string
	superuser     bool
	historyExists bool
	tableCount    int
	version       uint
	dirty         bool
}

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() (runErr error) {
	action := "status"
	if len(os.Args) > 1 {
		action = os.Args[1]
	}
	if action != "status" && action != "up" && action != "down" {
		return fmt.Errorf("usage: user-service-migrate [status|up|down]")
	}

	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		return errors.New("DB_DSN is required")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect PostgreSQL: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping PostgreSQL: %w", err)
	}

	current, err := inspect(ctx, pool)
	if err != nil {
		return fmt.Errorf("inspect migration state: %w", err)
	}
	if action == "status" {
		printStatus(current)
		return nil
	}
	if !current.historyExists && current.tableCount > 0 {
		return fmt.Errorf("refusing to migrate an untracked schema with %d existing public table(s); inspect and establish migration history manually", current.tableCount)
	}

	migrations, err := openMigrations(dsn)
	if err != nil {
		return fmt.Errorf("open migrations: %w", err)
	}
	defer func() {
		sourceErr, databaseErr := migrations.Close()
		runErr = migrationCloseError(runErr, sourceErr, databaseErr)
	}()

	switch action {
	case "up":
		err = migrations.Up()
	case "down":
		err = migrations.Down()
	}
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("%s migrations: %w", action, err)
	}

	current, err = inspect(ctx, pool)
	if err != nil {
		return fmt.Errorf("inspect migration state after %s: %w", action, err)
	}
	printStatus(current)
	return nil
}

func migrationCloseError(runErr, sourceErr, databaseErr error) error {
	closeErrs := make([]error, 0, 2)
	if sourceErr != nil {
		closeErrs = append(closeErrs, fmt.Errorf("close migration source: %w", sourceErr))
	}
	if databaseErr != nil {
		closeErrs = append(closeErrs, fmt.Errorf("close migration database: %w", databaseErr))
	}
	if len(closeErrs) == 0 {
		return runErr
	}
	return errors.Join(runErr, errors.Join(closeErrs...))
}

func openMigrations(dsn string) (*migrate.Migrate, error) {
	directory, err := filepath.Abs("migrations")
	if err != nil {
		return nil, err
	}
	return migrate.New("file://"+filepath.ToSlash(directory), dsn)
}

func inspect(ctx context.Context, pool *pgxpool.Pool) (state, error) {
	current := state{}
	if err := pool.QueryRow(ctx, "SELECT current_database(), current_user, r.rolsuper FROM pg_roles r WHERE r.rolname = current_user").Scan(&current.database, &current.role, &current.superuser); err != nil {
		return state{}, err
	}
	if err := pool.QueryRow(ctx, "SELECT to_regclass('public."+migrationTable+"') IS NOT NULL").Scan(&current.historyExists); err != nil {
		return state{}, err
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tablename <> $1", migrationTable).Scan(&current.tableCount); err != nil {
		return state{}, err
	}
	if current.historyExists {
		if err := pool.QueryRow(ctx, "SELECT version, dirty FROM "+migrationTable+" LIMIT 1").Scan(&current.version, &current.dirty); err != nil {
			return state{}, err
		}
	}
	return current, nil
}

func printStatus(current state) {
	history := "absent"
	if current.historyExists {
		history = "present"
	}
	fmt.Printf("database=%s role=%s superuser=%t public_tables=%d migration_history=%s version=%d dirty=%t\n", current.database, current.role, current.superuser, current.tableCount, history, current.version, current.dirty)
}
