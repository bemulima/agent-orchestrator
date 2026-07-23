package onboarding

import (
	"context"
	"errors"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestPrepareRequiresCleanCurrentDiscoveryAndPersistsProposal(t *testing.T) {
	path := "/projects/orders"
	project := domain.Project{ID: "project-id", Name: "orders", LocalPath: &path, HeadCommit: "abc"}
	snapshot := domain.ServiceSnapshot{ID: "snapshot-id", ProjectID: project.ID, CommitSHA: "abc", Branch: "main"}
	report := domain.DiscoveryReport{ProjectID: project.ID, CommitSHA: "abc"}
	projects := &projectRepositoryFake{project: project, snapshot: snapshot, report: report}
	runs := &onboardingRepositoryFake{}
	generator := &generatorFake{proposal: domain.OnboardingProposal{Checksum: "checksum"}, diff: "diff"}
	useCase := Prepare{
		Projects:  projects,
		Sources:   sourceFake{source: domain.RepositorySource{HeadCommit: "abc"}},
		Runs:      runs,
		Generator: generator,
	}

	run, err := useCase.Handle(context.Background(), PrepareInput{ProjectID: project.ID})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if generator.calls != 1 || runs.created.ProposalChecksum != "checksum" || run.UnifiedDiff != "diff" {
		t.Fatalf("proposal was not persisted correctly: %#v", runs.created)
	}
	if runs.created.Status != domain.OnboardingStatusProposalReady {
		t.Fatalf("initial status = %q", runs.created.Status)
	}

	projects.snapshot.IsDirty = true
	if _, err := useCase.Handle(context.Background(), PrepareInput{ProjectID: project.ID}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("dirty snapshot error = %v, want conflict", err)
	}
	if generator.calls != 1 {
		t.Fatal("generator ran for a dirty snapshot")
	}
}

func TestPrepareUsesSemanticGeneratorOnlyWhenRequested(t *testing.T) {
	path := "/projects/orders"
	project := domain.Project{ID: "project-id", Name: "orders", LocalPath: &path, HeadCommit: "abc"}
	snapshot := domain.ServiceSnapshot{ID: "snapshot-id", ProjectID: project.ID, CommitSHA: "abc", Branch: "main"}
	report := domain.DiscoveryReport{ProjectID: project.ID, CommitSHA: "abc"}
	base := &generatorFake{proposal: domain.OnboardingProposal{Checksum: "base"}}
	semantic := &generatorFake{proposal: domain.OnboardingProposal{Checksum: "semantic"}}
	useCase := Prepare{
		Projects: &projectRepositoryFake{project: project, snapshot: snapshot, report: report},
		Sources:  sourceFake{source: domain.RepositorySource{HeadCommit: "abc"}},
		Runs:     &onboardingRepositoryFake{}, Generator: base, SemanticGenerator: semantic,
	}
	run, err := useCase.Handle(context.Background(), PrepareInput{ProjectID: project.ID, Semantic: true})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if semantic.calls != 1 || base.calls != 0 || run.ProposalChecksum != "semantic" {
		t.Fatalf("semantic generator selection failed: base=%d semantic=%d run=%#v", base.calls, semantic.calls, run)
	}
}

func TestApplyEnforcesApprovalButAllowsReadOnlyDryRun(t *testing.T) {
	path := "/projects/orders"
	project := domain.Project{ID: "project-id", LocalPath: &path}
	run := domain.OnboardingRun{ID: "00000000-0000-4000-8000-000000000001", ProjectID: project.ID, Status: domain.OnboardingStatusAwaitingApproval}
	projects := &projectRepositoryFake{project: project}
	runs := &onboardingRepositoryFake{run: run, beginErr: domain.ErrApprovalNeeded}
	worktrees := &worktreeFake{}
	useCase := Apply{Projects: projects, Runs: runs, Worktree: worktrees}

	output, err := useCase.Handle(context.Background(), ApplyInput{RunID: run.ID, DryRun: true})
	if err != nil {
		t.Fatalf("dry-run Handle() error = %v", err)
	}
	if !output.Result.DryRun || worktrees.dryRunCalls != 1 || runs.beginCalls != 0 {
		t.Fatalf("dry-run performed the wrong operations: output=%#v runs=%#v worktrees=%#v", output, runs, worktrees)
	}
	if _, err := useCase.Handle(context.Background(), ApplyInput{RunID: run.ID}); !errors.Is(err, domain.ErrApprovalNeeded) {
		t.Fatalf("unapproved Handle() error = %v, want approval required", err)
	}
	if worktrees.applyCalls != 0 {
		t.Fatal("unapproved apply reached the worktree adapter")
	}

	runs.beginErr = nil
	runs.run.Status = domain.OnboardingStatusApplying
	publisher := &publisherFake{publication: domain.OnboardingPublication{
		Published: true, GitLabProjectID: 42, MergeRequestIID: 7, MergeRequestURL: "https://gitlab.example/mr/7",
	}}
	useCase.Publisher = publisher
	output, err = useCase.Handle(context.Background(), ApplyInput{RunID: run.ID})
	if err != nil {
		t.Fatalf("approved Handle() error = %v", err)
	}
	if worktrees.applyCalls != 1 || publisher.calls != 1 || runs.publicationCalls != 1 ||
		runs.completeCalls != 1 || output.Run.Status != domain.OnboardingStatusCompleted || !output.Result.Publication.Published {
		t.Fatalf("approved apply was not completed: output=%#v", output)
	}
}

type projectRepositoryFake struct {
	project  domain.Project
	snapshot domain.ServiceSnapshot
	report   domain.DiscoveryReport
}

func (f *projectRepositoryFake) Upsert(context.Context, domain.Project) (domain.Project, error) {
	return f.project, nil
}
func (f *projectRepositoryFake) Get(context.Context, string) (domain.Project, error) {
	return f.project, nil
}
func (f *projectRepositoryFake) GetByName(context.Context, string) (domain.Project, error) {
	return f.project, nil
}
func (f *projectRepositoryFake) List(context.Context) ([]domain.Project, error) {
	return []domain.Project{f.project}, nil
}
func (f *projectRepositoryFake) UpdateSourceState(context.Context, string, domain.ProjectStatus, domain.RepositorySource) (domain.Project, error) {
	return f.project, nil
}
func (f *projectRepositoryFake) UpdateStatus(context.Context, string, domain.ProjectStatus) error {
	return nil
}
func (f *projectRepositoryFake) SaveDiscovery(context.Context, domain.Project, domain.ServiceSnapshot, domain.DiscoveryReport) (domain.ServiceSnapshot, error) {
	return f.snapshot, nil
}
func (f *projectRepositoryFake) GetLatestDiscovery(context.Context, string) (domain.ServiceSnapshot, domain.DiscoveryReport, error) {
	return f.snapshot, f.report, nil
}

type sourceFake struct {
	source domain.RepositorySource
}

func (f sourceFake) ConnectLocal(context.Context, string) (domain.RepositorySource, error) {
	return f.source, nil
}
func (f sourceFake) ConnectGit(context.Context, string) (domain.RepositorySource, error) {
	return f.source, nil
}
func (f sourceFake) Inspect(context.Context, string) (domain.RepositorySource, error) {
	return f.source, nil
}

type generatorFake struct {
	proposal domain.OnboardingProposal
	diff     string
	calls    int
}

func (f *generatorFake) Generate(context.Context, domain.Project, domain.ServiceSnapshot, domain.DiscoveryReport) (domain.OnboardingProposal, string, error) {
	f.calls++
	return f.proposal, f.diff, nil
}

type onboardingRepositoryFake struct {
	created          domain.OnboardingRun
	run              domain.OnboardingRun
	beginErr         error
	beginCalls       int
	completeCalls    int
	publicationCalls int
}

func (f *onboardingRepositoryFake) CreateOrGet(_ context.Context, run domain.OnboardingRun) (domain.OnboardingRun, error) {
	f.created, f.run = run, run
	return run, nil
}
func (f *onboardingRepositoryFake) Get(context.Context, string) (domain.OnboardingRun, error) {
	return f.run, nil
}
func (f *onboardingRepositoryFake) Approve(context.Context, string, string, string) (domain.OnboardingRun, error) {
	return f.run, nil
}
func (f *onboardingRepositoryFake) Reject(context.Context, string, string, string) (domain.OnboardingRun, error) {
	return f.run, nil
}
func (f *onboardingRepositoryFake) BeginApply(context.Context, string) (domain.OnboardingRun, error) {
	f.beginCalls++
	return f.run, f.beginErr
}
func (f *onboardingRepositoryFake) RecordPublication(context.Context, string, domain.OnboardingPublication) (domain.OnboardingRun, error) {
	f.publicationCalls++
	f.run.Status = domain.OnboardingStatusMRCreated
	return f.run, nil
}

type publisherFake struct {
	publication domain.OnboardingPublication
	calls       int
}

func (f *publisherFake) Publish(context.Context, domain.Project, domain.OnboardingRun, domain.OnboardingApplyResult) (domain.OnboardingPublication, error) {
	f.calls++
	return f.publication, nil
}
func (f *onboardingRepositoryFake) CompleteApply(_ context.Context, _ string, result domain.OnboardingApplyResult) (domain.OnboardingRun, error) {
	f.completeCalls++
	f.run.Status = domain.OnboardingStatusCompleted
	f.run.Checks = result.Checks
	return f.run, nil
}
func (f *onboardingRepositoryFake) FailApply(context.Context, string, string) error { return nil }

type worktreeFake struct {
	dryRunCalls int
	applyCalls  int
}

func (f *worktreeFake) DryRun(context.Context, domain.Project, domain.OnboardingRun) (domain.OnboardingApplyResult, error) {
	f.dryRunCalls++
	return domain.OnboardingApplyResult{DryRun: true, Checks: []domain.OnboardingCheck{}}, nil
}
func (f *worktreeFake) Apply(context.Context, domain.Project, domain.OnboardingRun) (domain.OnboardingApplyResult, error) {
	f.applyCalls++
	return domain.OnboardingApplyResult{CommitSHA: "commit", Checks: []domain.OnboardingCheck{}}, nil
}
