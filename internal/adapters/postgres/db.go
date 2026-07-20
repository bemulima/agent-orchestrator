package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect creates a pgx connection pool. Call Ping before reporting ready.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, url)
}

// HealthRepoPG implements the infrastructure readiness contract.
type HealthRepoPG struct {
	Pool *pgxpool.Pool
}

func (HealthRepoPG) Name() string { return "postgres" }

func (r HealthRepoPG) Ping(ctx context.Context) error {
	return r.Pool.Ping(ctx)
}
