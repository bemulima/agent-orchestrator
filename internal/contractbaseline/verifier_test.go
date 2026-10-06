package contractbaseline

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestContractDiffRejectsImplementationBodyAndUnexpectedPaths(t *testing.T) {
	baseFile := "package usecase\ntype ApplicationCommandResult interface { Execute() error }\nfunc helper() error { return nil }\n"
	root, base := initContractGit(t, map[string]string{
		"go.mod":                     "module example.test/fixture\n\ngo 1.24\n",
		"internal/usecase/result.go": baseFile,
	})
	profile := verifierGoProfile()
	refs := []domain.ContractReference{{Path: "internal/usecase/result.go", Symbol: "ApplicationCommandResult", RouteID: "backend.usecase"}}
	files := []domain.ContractBaselineFile{{Path: "internal/usecase/result.go", SHA256: contentHash([]byte(baseFile))}}
	candidate := strings.Replace(baseFile, "return nil", "panic(\"changed implementation\")", 1)
	report, err := VerifyContractDiff(context.Background(), root, base, profile, files, refs,
		[]string{"internal/usecase/result.go"}, "complete diff", func(_ context.Context, path string) ([]byte, error) {
			if path == "internal/usecase/result.go" {
				return []byte(candidate), nil
			}
			return nil, os.ErrNotExist
		})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || !strings.Contains(strings.Join(report.Reasons, " "), "implementation/value declaration") {
		t.Fatalf("changed implementation body passed mechanical verification: %#v", report)
	}

	report, err = VerifyContractDiff(context.Background(), root, base, profile, files, refs,
		[]string{"db/migrations/019_business_logic.sql"}, "unexpected SQL diff", func(context.Context, string) ([]byte, error) {
			return []byte("SELECT 1;"), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || !strings.Contains(strings.Join(report.Reasons, " "), "unexpected file") {
		t.Fatalf("unexpected SQL path passed mechanical verification: %#v", report)
	}
}

func TestContractDiffRejectsApprovedPathOutsideProfileLocation(t *testing.T) {
	root, base := initContractGit(t, map[string]string{"go.mod": "module example.test/fixture\n\ngo 1.24\n"})
	path := "internal/transport/contracts.go"
	content := []byte("package usecase\ntype ApplicationCommandResult interface { Execute() error }\n")
	files := []domain.ContractBaselineFile{{Path: path, SHA256: contentHash(content)}}
	refs := []domain.ContractReference{{Path: path, Symbol: "ApplicationCommandResult", RouteID: "backend.usecase"}}
	report, err := VerifyContractDiff(context.Background(), root, base, verifierGoProfile(), files, refs,
		[]string{path}, "complete diff", func(context.Context, string) ([]byte, error) { return content, nil })
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || !strings.Contains(strings.Join(report.Reasons, " "), "approved contract path is unsafe") {
		t.Fatalf("profile-external contract path passed verification: %#v", report)
	}
}

func TestGoContractVerifierAcceptsTypeOnlyBoundariesAndApprovedErrorSentinel(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "go.mod", []byte("module example.test/fixture\n\ngo 1.24\n"))
	content := []byte(`package usecase
import (
	"errors"
	"time"
)
var ErrInvalidRequest = errors.New("invalid request")
type ApplicationCommandResult interface { Availability(AvailabilityQuery) AvailabilityResult }
type AvailabilityQuery struct { ResourceID string; Start, End time.Time }
type AvailabilityInterval struct { Start, End time.Time }
type AvailabilityResult struct { Intervals []AvailabilityInterval }
`)
	parsed, err := parser.ParseFile(token.NewFileSet(), "internal/usecase/application_command_result.go", content, parser.AllErrors|parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	profile := verifierGoProfile()
	profile.Routes[0].ContractSurface.AllowedValues = []string{"ErrInvalidRequest"}
	refs := []domain.ContractReference{{Path: "internal/usecase/application_command_result.go", Symbol: "ApplicationCommandResult", RouteID: "backend.usecase"}}
	if err := verifyGoContractFile(context.Background(), root, refs[0].Path, content, nil, false, profile, refs); err != nil {
		t.Fatalf("type-only application contract and approved error sentinel were rejected: %v", err)
	}
	if err := validateFileByProfile(context.Background(), root, profile, "backend.usecase", refs[0].Path, "usecase", content, true); err != nil {
		t.Fatalf("generated owner-surface validation rejected the approved error sentinel: %v", err)
	}
	if parsed == nil {
		t.Fatal("expected parsed contract")
	}

	unsafe := strings.Replace(string(content), "ErrInvalidRequest", "UnapprovedError", 1)
	if err := verifyGoContractFile(context.Background(), root, refs[0].Path, []byte(unsafe), nil, false, profile, refs); err == nil || !strings.Contains(err.Error(), "approved behavior-free contract value") {
		t.Fatalf("unapproved package variable error = %v", err)
	}
	if err := validateFileByProfile(context.Background(), root, profile, "backend.usecase", refs[0].Path, "usecase", []byte(unsafe), true); err == nil || !strings.Contains(err.Error(), "unapproved variable declaration") {
		t.Fatalf("generated owner-surface validation accepted an unapproved package variable: %v", err)
	}
}

func TestContractDiffVerifierChecksFullBaselineIncludingMaterializerOnlyFiles(t *testing.T) {
	root, base := initContractGit(t, map[string]string{"go.mod": "module example.test/fixture\n\ngo 1.24\n"})
	applicationPath := "internal/usecase/application_command_result.go"
	repositoryPath := "internal/usecase/repository_port.go"
	application := []byte(`package usecase
import (
	"errors"
	"time"
)
var ErrInvalidRequest = errors.New("invalid request")
type ApplicationCommandResult interface { Availability(AvailabilityQuery) AvailabilityResult }
type AvailabilityQuery struct { ResourceID string; Start, End time.Time }
type AvailabilityInterval struct { Start, End time.Time }
type AvailabilityResult struct { Intervals []AvailabilityInterval }
`)
	repository := []byte(`package usecase
import "context"
type RepositoryPort interface { FindAvailability(context.Context, AvailabilityQuery) (AvailabilityResult, error) }
`)
	files := []domain.ContractBaselineFile{
		{Path: applicationPath, SHA256: contentHash(application), Generated: true},
		{Path: repositoryPath, SHA256: contentHash(repository), Generated: true},
	}
	refs := []domain.ContractReference{
		{Kind: "application-command-result", Path: applicationPath, Symbol: "ApplicationCommandResult", RouteID: "backend.usecase"},
		{Kind: "repository-port", Path: repositoryPath, Symbol: "RepositoryPort", RouteID: "backend.usecase"},
	}
	profile := verifierGoProfile()
	profile.Routes[0].ContractSurface.AllowedValues = []string{"ErrInvalidRequest"}
	baselineChanges := []string{applicationPath, repositoryPath}
	report, err := VerifyContractDiff(context.Background(), root, base, profile, files, refs, baselineChanges,
		"both materializer outputs from approved source to final candidate", func(_ context.Context, relative string) ([]byte, error) {
			switch relative {
			case applicationPath:
				return application, nil
			case repositoryPath:
				return repository, nil
			default:
				return nil, os.ErrNotExist
			}
		})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || !samePathSets(report.ChangedFiles, baselineChanges) || !samePathSets(hashPaths(report.FileHashes), baselineChanges) {
		t.Fatalf("full baseline verifier omitted deterministic materializer changes: %#v", report)
	}
}

func samePathSets(first, second []string) bool {
	if len(first) != len(second) {
		return false
	}
	seen := make(map[string]struct{}, len(first))
	for _, value := range first {
		seen[value] = struct{}{}
	}
	for _, value := range second {
		if _, ok := seen[value]; !ok {
			return false
		}
	}
	return true
}

func hashPaths(files []domain.ContractBaselineFile) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return paths
}

func TestCommittedBaselineProvesParentPathsAndContentHashes(t *testing.T) {
	root, base := initContractGit(t, map[string]string{"go.mod": "module example.test/fixture\n\ngo 1.24\n"})
	content := []byte("package usecase\ntype ApplicationCommandResult interface { Execute() error }\n")
	path := "internal/usecase/result.go"
	writeFixtureFile(t, root, path, content)
	gitFixture(t, root, "add", "--", path)
	gitFixture(t, root, "commit", "-m", "feat(ai): contract baseline")
	commit := strings.TrimSpace(gitFixture(t, root, "rev-parse", "HEAD"))
	profile := verifierGoProfile()
	refs := []domain.ContractReference{{Path: path, Symbol: "ApplicationCommandResult", RouteID: "backend.usecase"}}
	files := []domain.ContractBaselineFile{{Path: path, SHA256: contentHash(content), Generated: true}}
	validation := VerifyCommittedBaseline(context.Background(), root, base, commit, profile, files, refs)
	if !validation.Passed {
		t.Fatalf("valid contract-only commit did not verify: %#v", validation)
	}
	files[0].SHA256 = strings.Repeat("0", 64)
	validation = VerifyCommittedBaseline(context.Background(), root, base, commit, profile, files, refs)
	if validation.Passed || validation.InspectionFailed || !strings.Contains(strings.Join(validation.Reasons, " "), "hash does not match") {
		t.Fatalf("commit with persisted content-hash drift passed: %#v", validation)
	}
	files[0].SHA256 = contentHash(content)
	refs = append(refs, domain.ContractReference{Path: "internal/usecase/missing.go", Symbol: "MissingBoundary", RouteID: "backend.usecase"})
	validation = VerifyCommittedBaseline(context.Background(), root, base, commit, profile, files, refs)
	if validation.Passed || !strings.Contains(strings.Join(validation.Reasons, " "), "no committed file") {
		t.Fatalf("baseline with an unmaterialized approved boundary passed: %#v", validation)
	}
}

func TestCommittedBaselineVerificationDistinguishesGitInspectionFailure(t *testing.T) {
	validation := VerifyCommittedBaseline(context.Background(), t.TempDir(), strings.Repeat("a", 40),
		strings.Repeat("b", 40), verifierGoProfile(),
		[]domain.ContractBaselineFile{{Path: "internal/usecase/result.go", SHA256: strings.Repeat("c", 64)}},
		[]domain.ContractReference{{Path: "internal/usecase/result.go", Symbol: "Result", RouteID: "backend.usecase"}})
	if validation.Passed || !validation.InspectionFailed {
		t.Fatalf("uninspectable Git baseline did not report inspection failure: %#v", validation)
	}
}

func initContractGit(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	root := t.TempDir()
	for path, value := range files {
		writeFixtureFile(t, root, path, []byte(value))
	}
	gitFixture(t, root, "init", "-q")
	gitFixture(t, root, "config", "user.name", "Contract Test")
	gitFixture(t, root, "config", "user.email", "contract@example.test")
	gitFixture(t, root, "add", "--all")
	gitFixture(t, root, "commit", "-m", "chore: source base")
	return root, strings.TrimSpace(gitFixture(t, root, "rev-parse", "HEAD"))
}

func writeFixtureFile(t *testing.T, root, relative string, content []byte) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitFixture(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(string(output)))
	}
	return string(output)
}

func verifierGoProfile() agentcontrol.Profile {
	return agentcontrol.Profile{ID: "go.canonical", ContractLocation: agentcontrol.ContractLocation{
		Language: "go", Extension: ".go", StandardLibraryImports: "allow", ThirdPartyImportPolicy: "project_dependencies",
	}, Routes: []agentcontrol.ProfileRoute{
		{ID: "backend.usecase", Targets: []string{"internal/usecase/**"},
			ContractSurface:     &agentcontrol.ContractSurface{Directory: "internal/usecase", PackageName: "usecase"},
			AllowedDependencies: []string{"backend.domain"}},
		{ID: "backend.domain", Targets: []string{"internal/domain/**"}},
		{ID: "backend.infrastructure.persistence", Targets: []string{"internal/infrastructure/persistence/**"}},
	}}
}

func TestRepositoryPortWithPersistenceImplementerRequiresCallableSurface(t *testing.T) {
	profile := verifierGoProfile()
	profile.Routes[1].ContractSurface = &agentcontrol.ContractSurface{Directory: "internal/domain", PackageName: "domain"}
	boundary := domain.PlannedContractBoundary{Kind: "repository-port", Owner: domain.RouteReference{ProjectID: "fixture", RouteID: "backend.domain"}, Consumers: []domain.RouteReference{{ProjectID: "fixture", RouteID: "backend.usecase"}, {ProjectID: "fixture", RouteID: "backend.infrastructure.persistence"}}, Implementers: []domain.RouteReference{{ProjectID: "fixture", RouteID: "backend.infrastructure.persistence"}}}
	for _, test := range []struct {
		name, declaration string
		valid             bool
	}{
		{"empty interface", "type RepositoryPort interface {}", false},
		{"marker struct", "type RepositoryPort struct {}", false},
		{"callable domain port", "type AvailabilityQuery struct { ResourceID string }; type AvailabilityInterval struct {}; type RepositoryPort interface { FindAvailable(AvailabilityQuery) ([]AvailabilityInterval, error) }", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, base := initContractGit(t, map[string]string{"go.mod": "module example.test/fixture\n\ngo 1.24\n"})
			relative := "internal/domain/repository_port.go"
			content := []byte("package domain\n" + test.declaration + "\n")
			files := []domain.ContractBaselineFile{{Path: relative, SHA256: contentHash(content)}}
			refs := []domain.ContractReference{{Kind: boundary.Kind, Path: relative, Symbol: "RepositoryPort", RouteID: boundary.Owner.RouteID, Relation: "implements"}}
			report, err := VerifyContractDiff(context.Background(), root, base, profile, files, refs, []string{relative}, "full contract diff", func(context.Context, string) ([]byte, error) { return content, nil })
			if err != nil {
				t.Fatal(err)
			}
			if report.Passed != test.valid {
				t.Fatalf("callable completeness accepted=%v want=%v: %v", report.Passed, test.valid, report.Reasons)
			}
			writeFixtureFile(t, root, relative, content)
			gitFixture(t, root, "add", "--all")
			gitFixture(t, root, "commit", "-m", "fixture contract candidate")
			commit := strings.TrimSpace(gitFixture(t, root, "rev-parse", "HEAD"))
			validation := VerifyCommittedBaseline(context.Background(), root, base, commit, profile, files, refs)
			if validation.Passed != test.valid {
				t.Fatalf("committed candidate usable=%v want=%v: %v", validation.Passed, test.valid, validation.Reasons)
			}
		})
	}
}
