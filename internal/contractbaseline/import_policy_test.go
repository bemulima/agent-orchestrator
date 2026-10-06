package contractbaseline

import (
	"context"
	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoContractImportPolicySeparatesStandardModuleAndForbiddenDependencies(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "go.mod", []byte("module example.test/fixture\n\ngo 1.24\n"))
	profile := verifierGoProfile()
	tests := []struct {
		name        string
		content     string
		wantAllowed bool
		wantClass   GoImportClass
	}{
		{
			name:        "standard library contract imports pass",
			content:     "package usecase\nimport (\"context\"; \"encoding/json\"; \"errors\"; \"time\")\ntype AvailabilityQuery struct { Context context.Context; Start, End time.Time; Payload json.RawMessage }\n",
			wantAllowed: true,
			wantClass:   GoImportStandardLibrary,
		},
		{
			name:        "same module architecture dependency passes",
			content:     "package usecase\nimport \"example.test/fixture/internal/domain\"\ntype AvailabilityQuery struct { Resource domain.ResourceID }\n",
			wantAllowed: true,
			wantClass:   GoImportSameModule,
		},
		{
			name:        "usecase importing postgres infrastructure fails",
			content:     "package usecase\nimport \"example.test/fixture/internal/infrastructure/persistence\"\ntype AvailabilityQuery interface { Store() persistence.Store }\n",
			wantAllowed: false,
			wantClass:   GoImportCrossLayer,
		},
		{
			name:        "unknown third party dependency fails",
			content:     "package usecase\nimport \"github.com/unapproved/client\"\ntype AvailabilityQuery interface { Client() client.API }\n",
			wantAllowed: false,
			wantClass:   GoImportThirdParty,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := parseGoArtifact("internal/usecase/contract.go", []byte(test.content))
			if err != nil {
				t.Fatal(err)
			}
			err = goImportsAllowed(context.Background(), root, profile, "backend.usecase", parsed)
			if test.wantAllowed && err != nil {
				t.Fatalf("goImportsAllowed() error = %v", err)
			}
			if !test.wantAllowed && (err == nil || !strings.Contains(err.Error(), string(test.wantClass))) {
				t.Fatalf("goImportsAllowed() error = %v, want %s rejection", err, test.wantClass)
			}
		})
	}
}

func TestGoContractImportPolicyRequiresEvidenceForThirdPartyAndExternalServices(t *testing.T) {
	root := t.TempDir()
	goMod := "module example.test/fixture\n\ngo 1.24\n\nrequire github.com/acme/payments v1.2.3\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	profile := verifierGoProfile()
	dependencies, err := goModuleDependencies(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	standard, err := goStandardPackages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	class, allowed := classifyGoImport(profile, "backend.usecase", "example.test/fixture", dependencies, standard, "github.com/acme/payments/client")
	if class != GoImportThirdParty || !allowed {
		t.Fatalf("project go.mod dependency classification = %s, allowed=%v", class, allowed)
	}
	profile.ContractLocation.ExternalServiceImports = []string{"github.com/acme/payments"}
	class, allowed = classifyGoImport(profile, "backend.usecase", "example.test/fixture", dependencies, standard, "github.com/acme/payments/client")
	if class != GoImportExternalService || !allowed {
		t.Fatalf("profile-approved external service classification = %s, allowed=%v", class, allowed)
	}
	profile.ContractLocation.ExternalServiceImports = []string{"github.com/acme/unlisted"}
	class, allowed = classifyGoImport(profile, "backend.usecase", "example.test/fixture", dependencies, standard, "github.com/acme/payments/client")
	if class != GoImportThirdParty || !allowed {
		t.Fatalf("declared project dependency without service label classification = %s, allowed=%v", class, allowed)
	}
	profile.ContractLocation.StandardLibraryImports = "deny"
	class, allowed = classifyGoImport(profile, "backend.usecase", "example.test/fixture", dependencies, standard, "errors")
	if class != GoImportStandardLibrary || allowed {
		t.Fatalf("explicit standard-library denial = %s, allowed=%v", class, allowed)
	}
}

func TestCanonicalPersistenceAndTransportDependencyEnforcement(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "go.mod", []byte("module example.test/fixture\n\ngo 1.24\n"))
	catalog, err := agentcontrol.LoadCatalog(os.DirFS(filepath.Join("..", "..")))
	if err != nil {
		t.Fatal(err)
	}
	profile := catalog.Profiles["go.canonical"]
	for _, test := range []struct {
		route, path, dependency string
		allowed                 bool
	}{
		{"backend.infrastructure.persistence", "internal/infrastructure/persistence/port.go", "internal/domain", true},
		{"backend.infrastructure.persistence", "internal/infrastructure/persistence/port.go", "internal/usecase", false},
		{"backend.transport.http", "internal/transport/http/port.go", "internal/usecase", true},
		{"backend.transport.http", "internal/transport/http/port.go", "internal/infrastructure/persistence", false},
	} {
		parsed, err := parseGoArtifact(test.path, []byte("package boundary\nimport x \"example.test/fixture/"+test.dependency+"\"\ntype Port interface { Get() x.Result }\n"))
		if err != nil {
			t.Fatal(err)
		}
		err = goImportsAllowed(context.Background(), root, profile, test.route, parsed)
		if (err == nil) != test.allowed {
			t.Fatalf("%s -> %s: %v", test.route, test.dependency, err)
		}
	}
}
