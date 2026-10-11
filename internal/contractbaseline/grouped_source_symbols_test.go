package contractbaseline

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

const groupedRepositorySymbols = "CoursePlanRepository, GoalGraphRepository, PolicyRepository, ReadinessGoalGraphRepository"

const groupedRepositorySource = `package repository
type CoursePlanRepository interface { FindPlan() error }
type GoalGraphRepository interface { FindGraph() error }
type PolicyRepository interface { FindPolicy() error }
type ReadinessGoalGraphRepository interface { FindReadiness() error }
`

func groupedSourceFixture(t *testing.T, symbols, source string) (string, agentcontrol.Profile, []domain.PlannedContractBoundary, []domain.RoutingEvidence) {
	t.Helper()
	root := newGoFixture(t)
	catalog, err := agentcontrol.LoadCatalog(os.DirFS(filepath.Join("..", "..")))
	if err != nil {
		t.Fatal(err)
	}
	const relative = "internal/domain/repository/course_plan.go"
	writeFixtureFile(t, root, relative, []byte(source))
	var boundaries []domain.PlannedContractBoundary
	for _, kind := range []string{"domain-model", "repository-port"} {
		boundaries = append(boundaries, domain.PlannedContractBoundary{
			Kind: kind, Existing: true, SourceEvidenceID: "source-repositories", TargetPath: relative,
			Owner:     domain.RouteReference{ProjectID: "project", RouteID: "backend.domain"},
			Consumers: []domain.RouteReference{{ProjectID: "project", RouteID: "backend.usecase"}},
		})
	}
	evidence := []domain.RoutingEvidence{{
		ID: "source-repositories", ProjectID: "project", Kind: "source", Path: relative,
		Symbol: symbols, Checksum: "sha256:" + contentHash([]byte(source)),
	}}
	return root, catalog.Profiles["go.canonical"], boundaries, evidence
}

func TestGroupedExistingSourceSymbolsMaterializeAndVerifyWithoutChangingReferences(t *testing.T) {
	root, profile, boundaries, evidence := groupedSourceFixture(t, groupedRepositorySymbols, groupedRepositorySource)
	planned, err := PlannedContractReferences("project", profile, boundaries, evidence)
	if err != nil {
		t.Fatal(err)
	}
	materialized, err := (FileMaterializer{}).Materialize(context.Background(), root, "project", "fixture", profile, boundaries, evidence)
	if err != nil {
		t.Fatalf("canonical grouped source interfaces were rejected: %v", err)
	}
	if len(materialized.Contracts) != 2 || len(materialized.Paths) != 1 || !reflect.DeepEqual(materialized.Contracts, planned) {
		t.Fatalf("grouped source changed approved boundary references: planned=%#v materialized=%#v", planned, materialized)
	}
	for _, ref := range materialized.Contracts {
		if ref.Symbol != groupedRepositorySymbols {
			t.Fatalf("grouped evidence identity changed: %#v", ref)
		}
	}
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(evidence[0].Path)))
	if err != nil || string(content) != groupedRepositorySource {
		t.Fatalf("existing source was changed: err=%v content=%q", err, content)
	}
	if err := verifyGoContractFile(context.Background(), root, evidence[0].Path, content, content, true, profile, materialized.Contracts); err != nil {
		t.Fatalf("canonical grouped source failed mechanical verification: %v", err)
	}
}

func TestGroupedExistingSourceSymbolsRejectMalformedOrMissingMembers(t *testing.T) {
	for _, symbols := range []string{
		groupedRepositorySymbols + ", MissingLastRepository",
		"CoursePlanRepository,GoalGraphRepository",
		"CoursePlanRepository,  GoalGraphRepository",
		" CoursePlanRepository, GoalGraphRepository",
		"CoursePlanRepository, GoalGraphRepository ",
		"CoursePlanRepository, ",
		"CoursePlanRepository, type",
		"CoursePlanRepository, CoursePlanRepository",
	} {
		t.Run(symbols, func(t *testing.T) {
			root, profile, boundaries, evidence := groupedSourceFixture(t, symbols, groupedRepositorySource)
			if _, err := (FileMaterializer{}).Materialize(context.Background(), root, "project", "fixture", profile, boundaries, evidence); err == nil {
				t.Fatal("invalid or missing grouped source member was materialized")
			}
			refs, err := PlannedContractReferences("project", profile, boundaries, evidence)
			if err != nil {
				t.Fatal(err)
			}
			content := []byte(groupedRepositorySource)
			if err := verifyGoContractFile(context.Background(), root, evidence[0].Path, content, content, true, profile, refs); err == nil {
				t.Fatal("invalid or missing grouped source member passed mechanical verification")
			}
		})
	}
}

func TestGroupedRepositorySymbolsRequireEveryMemberToBeCallable(t *testing.T) {
	source := strings.Replace(groupedRepositorySource, "FindReadiness() error", "", 1)
	root, profile, boundaries, evidence := groupedSourceFixture(t, groupedRepositorySymbols, source)
	refs, err := PlannedContractReferences("project", profile, boundaries, evidence)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte(source)
	err = verifyGoContractFile(context.Background(), root, evidence[0].Path, content, content, true, profile, refs)
	if err == nil || !strings.Contains(err.Error(), `repository port "ReadinessGoalGraphRepository" has no exported callable operation signature`) {
		t.Fatalf("last non-callable repository member was not specifically rejected: %v", err)
	}
}

func TestGeneratedGoContractSymbolsRemainSingleIdentifiers(t *testing.T) {
	root := newGoFixture(t)
	profile := goContractProfile()
	relative := "internal/usecase/application_command_result.go"
	content := []byte("package usecase\ntype ApplicationCommandResult interface{}\ntype OtherResult interface{}\n")
	symbols := "ApplicationCommandResult, OtherResult"
	if err := validateGeneratedArtifact(context.Background(), root, profile, "backend.usecase", relative, symbols, "usecase", content); err == nil {
		t.Fatal("generated grouped symbol was accepted")
	}
	refs := []domain.ContractReference{{Kind: "application-command-result", Path: relative, Symbol: symbols, RouteID: "backend.usecase"}}
	if err := verifyGoContractFile(context.Background(), root, relative, content, nil, false, profile, refs); err == nil {
		t.Fatal("new grouped symbol passed mechanical verification")
	}
}

func TestGroupedSymbolsAreRejectedForGeneratedFilesAlreadyInSourceBase(t *testing.T) {
	const relative = "internal/usecase/application_command_result.go"
	source := "package usecase\ntype ApplicationCommandResult interface{}\ntype OtherResult interface{}\n"
	root, base := initContractGit(t, map[string]string{
		"go.mod": "module example.test/fixture\n\ngo 1.24\n", relative: source,
	})
	refs := []domain.ContractReference{{Kind: "application-command-result", Path: relative, Symbol: "ApplicationCommandResult, OtherResult", RouteID: "backend.usecase"}}
	files := []domain.ContractBaselineFile{{Path: relative, Generated: true, SHA256: contentHash([]byte(source))}}
	for _, changed := range [][]string{{relative}, nil} {
		report, err := VerifyContractDiff(context.Background(), root, base, goContractProfile(), files, refs, changed, "fixture diff", func(context.Context, string) ([]byte, error) {
			return []byte(source), nil
		})
		if err != nil || !strings.Contains(strings.Join(report.Reasons, " "), "invalid Go contract symbol") {
			t.Fatalf("generated grouped reference with existing base was not rejected: report=%#v err=%v", report, err)
		}
	}
	candidate := []byte(source + "type AdditionalResult struct{}\n")
	writeFixtureFile(t, root, relative, candidate)
	gitFixture(t, root, "add", "--", relative)
	gitFixture(t, root, "commit", "-m", "fixture contract change")
	commit := strings.TrimSpace(gitFixture(t, root, "rev-parse", "HEAD"))
	files[0].SHA256 = contentHash(candidate)
	validation := VerifyCommittedBaseline(context.Background(), root, base, commit, goContractProfile(), files, refs)
	if validation.Passed || !strings.Contains(strings.Join(validation.Reasons, " "), "invalid Go contract symbol") {
		t.Fatalf("committed generated grouped reference was not rejected: %#v", validation)
	}
}

func TestGroupedSymbolsCannotAddMissingSourceBaseMemberInCandidate(t *testing.T) {
	base := strings.Replace(groupedRepositorySource, "type ReadinessGoalGraphRepository interface { FindReadiness() error }\n", "", 1)
	root, profile, boundaries, evidence := groupedSourceFixture(t, groupedRepositorySymbols, base)
	refs, err := PlannedContractReferences("project", profile, boundaries, evidence)
	if err != nil {
		t.Fatal(err)
	}
	err = verifyGoContractFile(context.Background(), root, evidence[0].Path, []byte(groupedRepositorySource), []byte(base), true, profile, refs)
	if err == nil || !strings.Contains(err.Error(), `source-base boundary type "ReadinessGoalGraphRepository" is missing`) {
		t.Fatalf("candidate invented missing approved source member: %v", err)
	}
}
