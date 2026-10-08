package main

import (
	"errors"
	"strings"
	"testing"
)

func TestMigrationCloseErrorReportsCloseFailureAfterSuccessfulRun(t *testing.T) {
	closeErr := errors.New("source close failed")

	err := migrationCloseError(nil, closeErr, nil)

	if !errors.Is(err, closeErr) {
		t.Fatalf("migrationCloseError() = %v, want it to report close failure", err)
	}
	if !strings.Contains(err.Error(), "close migration source: source close failed") {
		t.Fatalf("migrationCloseError() = %q, want close context", err)
	}
}

func TestMigrationCloseErrorPreservesMigrationFailureAndReportsCloseFailure(t *testing.T) {
	migrationErr := errors.New("migration failed")
	closeErr := errors.New("source close failed")

	err := migrationCloseError(migrationErr, closeErr, nil)

	if !errors.Is(err, migrationErr) {
		t.Fatalf("migrationCloseError() = %v, want original migration failure preserved", err)
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("migrationCloseError() = %v, want close failure reported", err)
	}
	if !strings.HasPrefix(err.Error(), migrationErr.Error()) {
		t.Fatalf("migrationCloseError() = %q, want migration failure first", err)
	}
}
