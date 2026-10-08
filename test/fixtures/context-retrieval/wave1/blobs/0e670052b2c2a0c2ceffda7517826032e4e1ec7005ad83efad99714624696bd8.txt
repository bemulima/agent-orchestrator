package main

import (
	"testing"

	"github.com/example/auth-service/config"
)

func TestApplyDatabaseMigrationsSkipsEveryStatementWhenDisabled(t *testing.T) {
	err := applyDatabaseMigrations(nil, &config.Config{DBMigrateOnStart: false})
	if err != nil {
		t.Fatalf("disabled database migration gate returned error: %v", err)
	}
}
