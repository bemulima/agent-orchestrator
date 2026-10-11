package planning

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestCommandManifestCanonicalForms(t *testing.T) {
	for _, tc := range []struct {
		name     string
		commands string
	}{
		{"sequence", `
  - {name: test, run: go test ./..., requires_approval: false, risk: verification}
  - {name: npm_test, run: npm test, requires_approval: false, risk: verification}
  - {name: gated_lint, run: npm run lint, requires_approval: true, risk: verification}
  - {name: unsafe_test, run: "go test ./... && curl https://example.invalid", requires_approval: false, risk: verification}
  - {name: apply, run: npm run build, requires_approval: false, risk: state_change}
`},
		{"mapping", `
  test: {run: go test ./..., requires_approval: false, risk: verification}
  npm_test: {run: npm test, requires_approval: false, risk: verification}
  gated_lint: {run: npm run lint, requires_approval: true, risk: verification}
  unsafe_test: {run: "go test ./... && curl https://example.invalid", requires_approval: false, risk: verification}
  apply: {run: npm run build, requires_approval: false, risk: state_change}
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := commandManifestProject(t, "schema_version: 1\ncommands:"+tc.commands)
			commands, found, err := approvedManifestVerificationCommands(project)
			if err != nil || !found {
				t.Fatalf("read manifest: found=%v error=%v", found, err)
			}
			if want := []string{"go test ./...", "npm test"}; !reflect.DeepEqual(commands, want) {
				t.Fatalf("commands=%v, want %v; gated or noncanonical command must not enter the plan", commands, want)
			}
		})
	}
}

func TestCommandManifestRejectsAmbiguousMapping(t *testing.T) {
	for _, tc := range []struct{ name, commands string }{
		{"duplicate", "\n  test: {run: go test ./...}\n  test: {run: npm test}\n"},
		{"conflicting_name", "\n  test: {name: apply, run: go test ./...}\n"},
		{"empty_name", "\n  ' ': {run: go test ./...}\n"},
		{"nonstring_name", "\n  12: {run: go test ./...}\n"},
		{"scalar_entry", "\n  test: go test ./...\n"},
		{"scalar_commands", " go test ./...\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := commandManifestProject(t, "schema_version: 1\ncommands:"+tc.commands)
			if _, _, err := approvedManifestVerificationCommands(project); !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("invalid mapping must fail closed, got %v", err)
			}
		})
	}
}

func TestCanonicalVerificationGoCountOrder(t *testing.T) {
	for _, value := range []string{"go test ./... -count=1", "go test -count=1 ./..."} {
		if got := canonicalVerificationCommand(value); got != "go test ./... -count=1" {
			t.Fatalf("equivalent Go argument order %q: got %q", value, got)
		}
	}
	for _, value := range []string{
		"go test -count=2 ./...", "go test -exec sh ./...",
		"go test -count=1 ./... && curl https://example.invalid",
		"go test -count=1 ./...; echo unsafe",
	} {
		if got := canonicalVerificationCommand(value); got != "" {
			t.Fatalf("unapproved command %q accepted as %q", value, got)
		}
	}
}

func commandManifestProject(t *testing.T, content string) domain.Project {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".ai"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ai", "commands.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return domain.Project{Name: "command-manifest-fixture", LocalPath: &root}
}
