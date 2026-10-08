package shardexecution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

const setupShell = `package postgres
import ("context"; "errors"; "example/internal/domain"; "github.com/jackc/pgx/v5/pgxpool")
var ErrNotImplemented = errors.New("not implemented")
type Repository struct { pool *pgxpool.Pool }
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }
func (r *Repository) Find(ctx context.Context, id string) ([]domain.Item, error) { return nil, ErrNotImplemented }
`

func TestValidateREDSetup(t *testing.T) {
	for _, tc := range []struct {
		name, source, file string
		valid              bool
	}{
		{"shell", setupShell, "internal/adapters/postgres/repository.go", true},
		{"canonical persistence shell", setupShell, "internal/infrastructure/persistence/postgres/availability.go", true},
		{"query", strings.Replace(setupShell, "return nil, ErrNotImplemented", `r.pool.Query(ctx, "SELECT 1"); return nil, ErrNotImplemented`, 1), "internal/adapters/postgres/repository.go", false},
		{"wrong signature", strings.Replace(setupShell, "id string", "id int", 1), "internal/adapters/postgres/repository.go", false},
		{"mapping", strings.Replace(setupShell, "return nil, ErrNotImplemented", "return []domain.Item{}, ErrNotImplemented", 1), "internal/adapters/postgres/repository.go", false},
		{"sibling", setupShell, "internal/usecase/repository.go", false},
		{"contract", setupShell, "internal/domain/repository_port.go", false},
		{"constructor behavior", strings.Replace(setupShell, "return &Repository{pool: pool}", "panic(\"setup\"); return &Repository{pool: pool}", 1), "internal/adapters/postgres/repository.go", false},
		{"extra field", strings.Replace(setupShell, "pool *pgxpool.Pool", "pool *pgxpool.Pool; cached []domain.Item", 1), "internal/adapters/postgres/repository.go", false},
		{"fallback", strings.Replace(setupShell, "return nil, ErrNotImplemented", "return nil, nil", 1), "internal/adapters/postgres/repository.go", false},
		{"alias import", strings.Replace(setupShell, `"context"`, `c "context"`, 1), "internal/adapters/postgres/repository.go", false},
		{"wrong receiver", strings.Replace(setupShell, "r *Repository", "r *OtherRepository", 1), "internal/adapters/postgres/repository.go", false},
		{"extra behavior", setupShell + "\nfunc helper() {}", "internal/adapters/postgres/repository.go", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write := func(path, data string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0644); err != nil {
					t.Fatal(err)
				}
			}
			write("internal/domain/repository_port.go", `package domain; import "context"; type RepositoryPort interface { Find(context.Context, string) ([]Item, error) }`)
			write(tc.file, tc.source)
			wp := domain.WorkPackage{Route: "backend.infrastructure.persistence", WriteScope: domain.ShardWriteScope{Allow: []string{"internal/adapters/postgres/**", "internal/infrastructure/persistence/postgres/availability.go"}}, Contracts: domain.WorkPackageContracts{ReadOnlyPaths: []string{"internal/domain/repository_port.go"}}}
			err := ValidateREDSetup(root, wp, []string{tc.file})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}

func TestValidateConstructorREDSetup(t *testing.T) {
	for _, route := range []string{"backend.transport.http", "backend.usecase"} {
		target, dependency, path := "AvailabilityUsecase", "domain.RepositoryPort", "internal/usecase/availability.go"
		imp := "example/internal/domain"
		if route == "backend.transport.http" {
			target, dependency, path, imp = "AvailabilityHandler", "usecase.ApplicationCommandResult", "internal/transport/http/availability.go", "example/internal/usecase"
		}
		shell := "package implementation\nimport \"" + imp + "\"\ntype " + target + " struct { dependency " + dependency + " }\nfunc New" + target + "(dependency " + dependency + ") *" + target + " { return &" + target + "{dependency: dependency} }"
		for _, tc := range []struct {
			name, source string
			valid        bool
		}{
			{"allocation", shell, true},
			{"business method", shell + "\nfunc (s *" + target + ") Execute() {}", false},
			{"constructor validation", strings.Replace(shell, "return &", "if dependency == nil { panic(\"nil\") }; return &", 1), false},
			{"extra dependency", strings.Replace(shell, "struct {", "struct { extra string;", 1), false},
			{"dependency call", strings.Replace(shell, "dependency: dependency", "dependency: dependency.Call()", 1), false},
			{"unsafe", strings.Replace(shell, "import ", "import \"unsafe\"\nimport ", 1), false},
			{"wrong boundary", strings.ReplaceAll(shell, dependency, "domain.OtherBoundary"), false},
		} {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				root := t.TempDir()
				full := filepath.Join(root, path)
				if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(tc.source), 0644); err != nil {
					t.Fatal(err)
				}
				wp := domain.WorkPackage{Route: route, WriteScope: domain.ShardWriteScope{Allow: []string{path}}}
				if err := ValidateREDSetup(root, wp, []string{path}); (err == nil) != tc.valid {
					t.Fatalf("valid=%v err=%v", tc.valid, err)
				}
			})
		}
	}
}

func TestRemediationPromptNamesFindingRegressions(t *testing.T) {
	request := &ShardRemediation{RequestID: "review", PriorCommit: "previous", Findings: []string{"A", "B", "C"}}
	for route, marker := range map[string]string{"backend.transport.http": "application call count exactly zero", "backend.usecase": "NewAvailabilityUsecase", "backend.infrastructure.persistence": "a row ending exactly at query Start is excluded"} {
		value := remediationPrompt(request, route)
		if !strings.Contains(value, marker) || !strings.Contains(value, "Do not use reflect or unsafe") {
			t.Fatalf("missing bounded remediation guidance for %s", route)
		}
	}
	if remediationPrompt(nil, "backend.usecase") != "" {
		t.Fatal("ordinary worker prompt unexpectedly changed")
	}
}

func TestConstructorSetupPreservesVerifiedHTTPPreimage(t *testing.T) {
	prior := `package http
import ("example/internal/usecase"; "net/http")
type AvailabilityHandler struct { application usecase.ApplicationCommandResult }
func (h *AvailabilityHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }
`
	ctor := `func NewAvailabilityHandler(application usecase.ApplicationCommandResult) *AvailabilityHandler { return &AvailabilityHandler{application: application} }`
	path := "internal/transport/http/availability.go"
	for _, tc := range []struct {
		name, source string
		valid        bool
	}{
		{"new allocation", prior + ctor, true},
		{"changes old HTTP behavior", strings.Replace(prior, "200", "400", 1) + ctor, false},
		{"adds method", prior + ctor + "\nfunc (h *AvailabilityHandler) Other() {}", false},
		{"constructor behavior", prior + strings.Replace(ctor, "return &", "panic(\"bad\"); return &", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			full := filepath.Join(root, path)
			if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(tc.source), 0644); err != nil {
				t.Fatal(err)
			}
			wp := domain.WorkPackage{Route: "backend.transport.http", WriteScope: domain.ShardWriteScope{Allow: []string{path}}}
			if err := ValidateREDSetup(root, wp, []string{path}, map[string]string{path: prior}); (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}
