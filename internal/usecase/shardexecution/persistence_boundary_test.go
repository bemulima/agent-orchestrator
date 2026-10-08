package shardexecution

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestPersistenceBoundaryRequiresRealPostgresDriverSchemaAndIntegrationTest(t *testing.T) {
	root := t.TempDir()
	packageValue := domain.WorkPackage{Verification: domain.WorkPackageVerification{
		TestPaths: []string{"internal/infrastructure/persistence/postgres/availability_test.go"},
	}}
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.test/canary\n")
	write("internal/infrastructure/persistence/postgres/availability_test.go", "package postgres\n")
	if persistenceBoundaryExists(root, packageValue) {
		t.Fatal("empty adapter test file unexpectedly counted as a meaningful database boundary")
	}

	write("go.mod", "module example.test/canary\nrequire github.com/jackc/pgx/v5 v5.7.0\n")
	write("db/migrations/001_schema.sql", "CREATE TABLE availability(resource_id text);\n")
	write("internal/infrastructure/persistence/postgres/availability_test.go", `package postgres
import "github.com/jackc/pgx/v5"
func TestPostgres(t *testing.T) { url := os.Getenv("TEST_DATABASE_URL"); _ = pgx.Connect(ctx, url) }
`)
	if !persistenceBoundaryExists(root, packageValue) {
		t.Fatal("real PostgreSQL integration fixture was not recognized")
	}
	write("internal/infrastructure/persistence/postgres/availability_test.go", "package postgres\n// sqlmock\n")
	if persistenceBoundaryExists(root, packageValue) {
		t.Fatal("mocked SQL test unexpectedly counted as a real PostgreSQL boundary")
	}
}
