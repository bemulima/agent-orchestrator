package shardplanning

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/bemulima/agent-orchestrator/internal/agentcontrol/localrepo"
	"github.com/bemulima/agent-orchestrator/internal/contractbaseline"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestPlanningFreezeRequiredKeepsExecutionBaselinePending(t *testing.T) {
	plan := domain.PlanBundle{Plan: domain.Plan{ID: "plan-1"}}
	task := domain.Task{ID: "task-1", PlanID: "plan-1", ProjectID: "project-1"}
	profile := agentcontrol.Profile{ID: "go.canonical"}
	baseline := newBaseline(plan, task, preparedRepository{
		project: domain.Project{ID: "project-1", Name: "fixture", HeadCommit: "base"},
		profile: profile, profileFP: "profile-fingerprint",
	}, domain.ContractPlan{State: domain.ContractPlanFreezeNeeded, Required: true, FreezeRequired: true},
		"contract-fingerprint", time.Now().UTC())
	if baseline.PlanningContractState != domain.ContractPlanFreezeNeeded || baseline.ExecutionState != domain.ContractBaselinePending {
		t.Fatalf("planning freeze was conflated with source freeze: %#v", baseline)
	}
}

func TestUnsupportedProfileRequiresOwnerReviewWithoutMaterializing(t *testing.T) {
	root := repositoryRoot(t)
	catalog, err := localrepo.LoadCatalogFromDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	localPath := t.TempDir()
	output := domain.PlannerOutput{
		Tasks: []domain.PlannedTask{{Key: "repo-task", ProjectID: "project-1"}},
		Routing: &domain.RoutingResult{
			Status: domain.RoutingStatusUnresolved, OwnerReviewRequired: true,
			Profiles: []domain.ArchitectureProfileResolution{{ProjectID: "project-1", Status: domain.ProfileResolutionUnresolved, Reason: "unsupported fixture"}},
		},
		ContractPlan: &domain.ContractPlan{State: domain.ContractPlanPlanned, Required: true, Reason: "route unresolved"},
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	bundle := domain.PlanBundle{
		Plan:  approvedTestPlan("plan-1", encoded),
		Tasks: []domain.Task{{ID: "task-1", PlanID: "plan-1", ProjectID: "project-1", PlannerKey: "repo-task"}},
	}
	plans := planReaderFake{bundle: bundle}
	projects := projectReaderFake{project: domain.Project{ID: "project-1", Name: "unsupported", LocalPath: &localPath, HeadCommit: "base"}}
	repositories := &repositoryInspectorFake{}
	materializer := &materializerFake{}
	store := newExecutionStoreFake()
	service := Service{
		Plans: plans, Projects: projects, Execution: store, Repositories: repositories,
		Catalog: catalog, Materializer: materializer, Now: func() time.Time { return time.Unix(100, 0).UTC() },
	}
	readiness, err := service.Prepare(context.Background(), "plan-1")
	if err != nil {
		t.Fatal(err)
	}
	if readiness.State != domain.FanoutReadinessOwnerReview || materializer.calls != 0 || repositories.calls != 0 {
		t.Fatalf("unsupported profile was not fail-closed: readiness=%#v materialize=%d inspect=%d", readiness, materializer.calls, repositories.calls)
	}
	if len(bundle.Tasks) != 1 || bundle.Tasks[0].ID != "task-1" || len(store.shards["task-1"]) != 0 {
		t.Fatalf("top-level Task was changed or incorrectly sharded: bundle=%#v shards=%#v", bundle.Tasks, store.shards)
	}
}

func TestPrepareBlocksBaselineWithoutContractCommitAndIndependentReview(t *testing.T) {
	root := repositoryRoot(t)
	catalog, err := localrepo.LoadCatalogFromDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	profile := catalog.Profiles["go.canonical"]
	profileFingerprint, err := agentcontrol.ProfileFingerprint(catalog, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	checkout := t.TempDir()
	writeFiles(t, checkout, map[string]string{
		"go.mod":                                    "module example.test/fixture\n\ngo 1.24\n",
		"internal/domain/lesson.go":                 "package domain\ntype Lesson struct{}\n",
		"internal/usecase/complete.go":              "package usecase\ntype Complete struct{}\n",
		"internal/transport/http/handler.go":        "package http\n",
		"internal/infrastructure/postgres/store.go": "package postgres\n",
	})
	routing := domain.RoutingResult{
		Status: domain.RoutingStatusResolved, Confidence: "high", OwnerReviewRequired: false,
		Profiles: []domain.ArchitectureProfileResolution{{ProjectID: "project-1", Status: domain.ProfileResolutionResolved, ProfileID: profile.ID, ProfileFingerprint: profileFingerprint}},
		Routes: []domain.RoutedTarget{
			{RouteReference: domain.RouteReference{ProjectID: "project-1", RouteID: "backend.usecase"}, Paths: []string{"internal/usecase/complete.go"}, EvidenceIDs: []string{"ev-usecase"}},
			{RouteReference: domain.RouteReference{ProjectID: "project-1", RouteID: "backend.domain"}, Paths: []string{"internal/domain/lesson.go"}, EvidenceIDs: []string{"ev-domain"}},
		},
		EvidenceIndex: []domain.RoutingEvidence{
			{ID: "ev-usecase", ProjectID: "project-1", Kind: "source", Path: "internal/usecase/complete.go", Symbol: "Complete"},
			{ID: "ev-domain", ProjectID: "project-1", Kind: "source", Path: "internal/domain/lesson.go", Symbol: "Lesson"},
		},
		Verification: []domain.RouteVerification{
			{RouteReference: domain.RouteReference{ProjectID: "project-1", RouteID: "backend.usecase"}, Boundary: "usecase-unit", EvidenceState: domain.VerificationEvidenceToAdd, Rationale: "add focused verification"},
			{RouteReference: domain.RouteReference{ProjectID: "project-1", RouteID: "backend.domain"}, Boundary: "domain-unit", EvidenceState: domain.VerificationEvidenceToAdd, Rationale: "add focused verification"},
		},
	}
	contractPlan := domain.ContractPlan{
		State: domain.ContractPlanFreezeNeeded, Required: true, FreezeRequired: true, Reason: "shared application result boundary",
		AffectedRoutes: []domain.RouteReference{{ProjectID: "project-1", RouteID: "backend.usecase"}, {ProjectID: "project-1", RouteID: "backend.domain"}},
		Boundaries: []domain.PlannedContractBoundary{{
			Kind: "application-command-result", Owner: domain.RouteReference{ProjectID: "project-1", RouteID: "backend.usecase"},
			Consumers: []domain.RouteReference{{ProjectID: "project-1", RouteID: "backend.domain"}}, Rationale: "approved interface boundary",
		}},
	}
	output := domain.PlannerOutput{
		Tasks:   []domain.PlannedTask{{Key: "repo-task", ProjectID: "project-1", ArchitecturalRoutes: []string{"backend.usecase", "backend.domain"}}},
		Routing: &routing, ContractPlan: &contractPlan,
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	bundle := domain.PlanBundle{
		Plan:  approvedTestPlan("plan-1", encoded),
		Tasks: []domain.Task{{ID: "task-1", PlanID: "plan-1", ProjectID: "project-1", PlannerKey: "repo-task", RiskLevel: domain.RiskLevelMedium}},
	}
	store := newExecutionStoreFake()
	materializer := &materializerFake{delegate: contractbaseline.FileMaterializer{}}
	service := Service{
		Plans:     planReaderFake{bundle: bundle},
		Projects:  projectReaderFake{project: domain.Project{ID: "project-1", Name: "fixture", LocalPath: &checkout, HeadCommit: "base"}},
		Execution: store, Repositories: &repositoryInspectorFake{revision: "base"},
		Catalog: catalog, Materializer: materializer,
		Now: func() time.Time { return time.Unix(100, 0).UTC() },
	}
	readiness, err := service.Prepare(context.Background(), "plan-1")
	if err != nil {
		t.Fatal(err)
	}
	if readiness.State != domain.FanoutReadinessBlocked {
		t.Fatalf("baseline lacking commit and independent review incorrectly became ready: %#v", readiness)
	}
	baseline := store.baselines["task-1"]
	if baseline.PlanningContractState != domain.ContractPlanFreezeNeeded || baseline.ExecutionState != domain.ContractBaselineBlocked ||
		baseline.Validation.Passed || baseline.AggregateSHA256 != "" || baseline.ContractBaselineCommit != "" ||
		baseline.ContractAgentPassed || baseline.IndependentReviewPassed || len(baseline.Files) != 0 || len(baseline.MaterializedContracts) != 0 {
		t.Fatalf("incomplete contract baseline was not persisted as blocked: %#v", baseline)
	}
	shards := store.shards["task-1"]
	if len(shards) != 2 || shards[0].TaskID != "task-1" || shards[1].TaskID != "task-1" {
		t.Fatalf("repository Task was not decomposed into internal shards: %#v", shards)
	}
	foundContractDependent := false
	for _, shard := range shards {
		if len(shard.Consumes)+len(shard.Implements) > 0 {
			foundContractDependent = true
			if shard.ExecutionBase.Kind != domain.ShardBaseContractBaseline || shard.ExecutionBase.Revision != "" || shard.ExecutionBase.ContractBaselineID != baseline.ID {
				t.Fatalf("contract-dependent shard did not retain the pending baseline reference: %#v", shard.ExecutionBase)
			}
		}
	}
	if !foundContractDependent {
		t.Fatal("no shard retained the planned contract dependency")
	}
	if materializer.calls != 0 {
		t.Fatalf("materializer wrote into the connected source checkout: calls=%d", materializer.calls)
	}
	if _, err := os.Stat(filepath.Join(checkout, "internal/contracts/application_command_result.go")); !os.IsNotExist(err) {
		t.Fatalf("Contract Freeze stub escaped managed isolation: %v", err)
	}
	// Retiring a baseline must start a distinct version without changing its evidence.
	retired := baseline
	retired.ExecutionState = domain.ContractBaselineInvalidated
	retired.ContractBaselineCommit = "historical-contract-commit"
	store.baselines["task-1"] = retired
	if _, err := service.Prepare(context.Background(), "plan-1"); err != nil {
		t.Fatal(err)
	}
	replacement := store.baselines["task-1"]
	if replacement.PredecessorBaselineID != retired.ID || replacement.ID == retired.ID || replacement.ContractBaselineCommit != "" || replacement.ExecutionState == domain.ContractBaselineInvalidated {
		t.Fatalf("retired baseline was revived or historical evidence reused: retired=%#v replacement=%#v", retired, replacement)
	}
	if replacement.ApprovedPlanFingerprint != retired.ApprovedPlanFingerprint || replacement.ContractPlanFingerprint != retired.ContractPlanFingerprint {
		t.Fatal("replacement changed the approved plan fingerprints")
	}
	if _, err := service.Prepare(context.Background(), "plan-1"); err != nil {
		t.Fatal(err)
	}
	if store.baselines["task-1"].ID != replacement.ID {
		t.Fatal("retry created another version for a non-retired baseline")
	}

}

func approvedTestPlan(id string, output []byte) domain.Plan {
	fingerprint := "approved-fingerprint-" + id
	return domain.Plan{
		ID: id, Status: domain.PlanStatusApproved, Fingerprint: fingerprint,
		ApprovedFingerprint: &fingerprint, PlannerOutput: output,
	}
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for relative, content := range files {
		fullPath := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
}

type planReaderFake struct{ bundle domain.PlanBundle }

func (f planReaderFake) GetPlan(context.Context, string) (domain.PlanBundle, error) {
	return f.bundle, nil
}

type projectReaderFake struct{ project domain.Project }

func (f projectReaderFake) Get(context.Context, string) (domain.Project, error) {
	return f.project, nil
}

type repositoryInspectorFake struct {
	calls    int
	revision string
}

func (f *repositoryInspectorFake) InspectWithAllowedChanges(context.Context, string, []string) (domain.RepositorySource, error) {
	f.calls++
	return domain.RepositorySource{HeadCommit: f.revision}, nil
}

type materializerFake struct {
	calls    int
	delegate contractbaseline.Materializer
}

func (f *materializerFake) Materialize(ctx context.Context, root, projectID, repository string, profile agentcontrol.Profile, boundaries []domain.PlannedContractBoundary, evidence []domain.RoutingEvidence) (contractbaseline.Materialization, error) {
	f.calls++
	if f.delegate != nil {
		return f.delegate.Materialize(ctx, root, projectID, repository, profile, boundaries, evidence)
	}
	return contractbaseline.Materialization{}, nil
}

func (f *materializerFake) Validate(ctx context.Context, root string, profile agentcontrol.Profile, files []domain.ContractBaselineFile, contracts []domain.ContractReference) domain.ContractBaselineValidation {
	if f.delegate != nil {
		return f.delegate.Validate(ctx, root, profile, files, contracts)
	}
	return domain.ContractBaselineValidation{Passed: true}
}

type executionStoreFake struct {
	shards    map[string][]domain.ArchitecturalShard
	baselines map[string]domain.ContractBaseline
	readiness domain.FanoutReadinessEvidence
}

func newExecutionStoreFake() *executionStoreFake {
	return &executionStoreFake{shards: map[string][]domain.ArchitecturalShard{}, baselines: map[string]domain.ContractBaseline{}}
}

func (f *executionStoreFake) SaveArchitecturalShards(_ context.Context, _, taskID string, shards []domain.ArchitecturalShard) error {
	f.shards[taskID] = append([]domain.ArchitecturalShard(nil), shards...)
	return nil
}

func (f *executionStoreFake) ListArchitecturalShards(context.Context, string) ([]domain.ArchitecturalShard, error) {
	var result []domain.ArchitecturalShard
	for _, shards := range f.shards {
		result = append(result, shards...)
	}
	return result, nil
}

func (f *executionStoreFake) SaveContractBaseline(_ context.Context, baseline domain.ContractBaseline) error {
	f.baselines[baseline.TaskID] = baseline
	return nil
}

func (f *executionStoreFake) ListContractBaselines(context.Context, string) ([]domain.ContractBaseline, error) {
	var result []domain.ContractBaseline
	for _, baseline := range f.baselines {
		result = append(result, baseline)
	}
	return result, nil
}

func (f *executionStoreFake) SaveFanoutReadiness(_ context.Context, evidence domain.FanoutReadinessEvidence) error {
	f.readiness = evidence
	return nil
}

func (f *executionStoreFake) GetFanoutReadiness(context.Context, string) (domain.FanoutReadinessEvidence, error) {
	return f.readiness, nil
}
