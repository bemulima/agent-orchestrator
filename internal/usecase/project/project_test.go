package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestConnectProject_IsIdempotentAndScansEveryConnection(t *testing.T) {
	repository := newMemoryProjectRepository()
	source := domain.RepositorySource{
		Name: "fixture", Identity: "git:example.test/team/fixture", LocalPath: "/allowed/fixture",
		GitURL: "https://example.test/team/fixture.git", DefaultBranch: "main", CurrentBranch: "main",
		HeadCommit: "abc123",
	}
	sources := &fakeProjectSource{source: source}
	scanner := &fakeScanner{}
	scan := ScanProject{Projects: repository, Sources: sources, Scanner: scanner}
	useCase := ConnectProject{Projects: repository, Sources: sources, Scan: scan}

	first, err := useCase.Handle(context.Background(), ConnectInput{LocalPath: source.LocalPath})
	if err != nil {
		t.Fatalf("first Handle() error = %v", err)
	}
	second, err := useCase.Handle(context.Background(), ConnectInput{LocalPath: source.LocalPath})
	if err != nil {
		t.Fatalf("second Handle() error = %v", err)
	}
	if first.Project.ID != second.Project.ID || len(repository.projects) != 1 {
		t.Fatalf("connect was not idempotent: first=%#v second=%#v projects=%d", first.Project, second.Project, len(repository.projects))
	}
	if scanner.calls != 2 || second.Snapshot.Version != 1 || len(repository.snapshots[first.Project.ID]) != 1 {
		t.Fatalf("scanner calls=%d snapshot version=%d", scanner.calls, second.Snapshot.Version)
	}
	if second.Project.Status != domain.ProjectStatusAnalyzed {
		t.Fatalf("status = %q", second.Project.Status)
	}
}

func TestConnectProject_ValidatesInputAndRole(t *testing.T) {
	repository := newMemoryProjectRepository()
	sources := &fakeProjectSource{}
	useCase := ConnectProject{Projects: repository, Sources: sources}
	for _, input := range []ConnectInput{
		{},
		{LocalPath: "/one", GitURL: "https://example.test/two.git"},
		{LocalPath: "/one", RepositoryRole: "invalid"},
	} {
		_, err := useCase.Handle(context.Background(), input)
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("Handle(%#v) error = %v, want validation", input, err)
		}
	}
}

func TestScanProject_MarksFailedWhenScannerFails(t *testing.T) {
	repository := newMemoryProjectRepository()
	path := "/allowed/fixture"
	project, err := repository.Upsert(context.Background(), domain.Project{
		Name: "fixture", SourceIdentity: "local:fixture", LocalPath: &path,
		Status: domain.ProjectStatusConnected, RepositoryRole: domain.RepositoryRoleService,
	})
	if err != nil {
		t.Fatal(err)
	}
	source := domain.RepositorySource{Name: "fixture", Identity: "local:fixture", LocalPath: path, HeadCommit: "abc"}
	useCase := ScanProject{
		Projects: repository,
		Sources:  &fakeProjectSource{source: source},
		Scanner:  &fakeScanner{err: errors.New("scan failed")},
	}
	_, err = useCase.Handle(context.Background(), project.ID)
	if err == nil {
		t.Fatal("Handle() error = nil")
	}
	updated, _ := repository.Get(context.Background(), project.ID)
	if updated.Status != domain.ProjectStatusFailed {
		t.Fatalf("status = %q, want failed", updated.Status)
	}
}

func TestSnapshotFromReport(t *testing.T) {
	report := domain.DiscoveryReport{
		CommitSHA: "abc", Branch: "feature", IsDirty: true,
		Facts: []domain.Evidence{
			{Category: "classification", Name: "service_kind", Value: "frontend_application", Confidence: .96},
			{Category: "stack", Name: "runtime", Value: "node", Confidence: .98},
			{Category: "stack", Name: "framework", Value: "nextjs", Confidence: .98},
			{Category: "purpose", Name: "summary", Value: "Student frontend", Confidence: .85},
		},
	}
	snapshot := snapshotFromReport("project", report)
	if snapshot.ServiceKind != domain.ServiceKindFrontendApplication || snapshot.Language != "javascript" ||
		snapshot.Framework != "nextjs" || snapshot.Purpose != "Student frontend" || !snapshot.IsDirty {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

type fakeProjectSource struct {
	source domain.RepositorySource
	err    error
}

func (f *fakeProjectSource) ConnectLocal(context.Context, string) (domain.RepositorySource, error) {
	return f.source, f.err
}

func (f *fakeProjectSource) ConnectGit(context.Context, string) (domain.RepositorySource, error) {
	return f.source, f.err
}

func (f *fakeProjectSource) Inspect(context.Context, string) (domain.RepositorySource, error) {
	return f.source, f.err
}

type fakeScanner struct {
	calls int
	err   error
}

func (f *fakeScanner) Scan(_ context.Context, project domain.Project, source domain.RepositorySource) (domain.DiscoveryReport, error) {
	f.calls++
	if f.err != nil {
		return domain.DiscoveryReport{}, f.err
	}
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	return domain.DiscoveryReport{
		SchemaVersion: 1, ProjectID: project.ID, ProjectName: project.Name,
		RepositoryRole: project.RepositoryRole, RepositoryPath: source.LocalPath,
		CommitSHA: source.HeadCommit, Branch: source.CurrentBranch, IsDirty: source.IsDirty,
		ContentChecksum: "fixture-checksum",
		StartedAt:       now, CompletedAt: now,
		Facts: []domain.Evidence{{
			Category: "classification", Name: "service_kind", Value: "backend_service",
			Confidence: .8, SourcePath: "go.mod", Explanation: "fixture",
		}},
	}, nil
}

type memoryProjectRepository struct {
	projects   map[string]domain.Project
	identities map[string]string
	snapshots  map[string][]domain.ServiceSnapshot
	reports    map[string][]domain.DiscoveryReport
	nextID     int
}

func newMemoryProjectRepository() *memoryProjectRepository {
	return &memoryProjectRepository{
		projects: make(map[string]domain.Project), identities: make(map[string]string),
		snapshots: make(map[string][]domain.ServiceSnapshot), reports: make(map[string][]domain.DiscoveryReport),
	}
}

func (r *memoryProjectRepository) Upsert(_ context.Context, project domain.Project) (domain.Project, error) {
	if id, exists := r.identities[project.SourceIdentity]; exists {
		existing := r.projects[id]
		existing.HeadCommit = project.HeadCommit
		existing.CurrentBranch = project.CurrentBranch
		existing.IsDirty = project.IsDirty
		r.projects[id] = existing
		return existing, nil
	}
	r.nextID++
	project.ID = fmt.Sprintf("project-%d", r.nextID)
	project.CreatedAt = time.Now()
	project.UpdatedAt = project.CreatedAt
	r.projects[project.ID] = project
	r.identities[project.SourceIdentity] = project.ID
	return project, nil
}

func (r *memoryProjectRepository) Get(_ context.Context, id string) (domain.Project, error) {
	project, exists := r.projects[id]
	if !exists {
		return domain.Project{}, domain.ErrNotFound
	}
	return project, nil
}

func (r *memoryProjectRepository) GetByName(_ context.Context, name string) (domain.Project, error) {
	var found *domain.Project
	for _, project := range r.projects {
		if project.Name != name {
			continue
		}
		if found != nil {
			return domain.Project{}, domain.ErrConflict
		}
		copy := project
		found = &copy
	}
	if found == nil {
		return domain.Project{}, domain.ErrNotFound
	}
	return *found, nil
}

func (r *memoryProjectRepository) List(context.Context) ([]domain.Project, error) {
	projects := make([]domain.Project, 0, len(r.projects))
	for _, project := range r.projects {
		projects = append(projects, project)
	}
	return projects, nil
}

func (r *memoryProjectRepository) UpdateSourceState(_ context.Context, id string, status domain.ProjectStatus, source domain.RepositorySource) (domain.Project, error) {
	project, err := r.Get(context.Background(), id)
	if err != nil {
		return domain.Project{}, err
	}
	project.Status = status
	project.CurrentBranch = source.CurrentBranch
	project.HeadCommit = source.HeadCommit
	project.IsDirty = source.IsDirty
	r.projects[id] = project
	return project, nil
}

func (r *memoryProjectRepository) UpdateStatus(_ context.Context, id string, status domain.ProjectStatus) error {
	project, err := r.Get(context.Background(), id)
	if err != nil {
		return err
	}
	project.Status = status
	r.projects[id] = project
	return nil
}

func (r *memoryProjectRepository) SaveDiscovery(_ context.Context, project domain.Project, snapshot domain.ServiceSnapshot, report domain.DiscoveryReport) (domain.ServiceSnapshot, error) {
	if existing := r.snapshots[project.ID]; len(existing) > 0 {
		latest := existing[len(existing)-1]
		if latest.CommitSHA == snapshot.CommitSHA && latest.Branch == snapshot.Branch &&
			latest.IsDirty == snapshot.IsDirty && latest.ContentChecksum == snapshot.ContentChecksum {
			project.Status = domain.ProjectStatusAnalyzed
			r.projects[project.ID] = project
			return latest, nil
		}
	}
	snapshot.ID = fmt.Sprintf("snapshot-%d", len(r.snapshots[project.ID])+1)
	snapshot.Version = len(r.snapshots[project.ID]) + 1
	snapshot.ProjectID = project.ID
	snapshot.DiscoveredAt = time.Now()
	snapshot.RawReport, _ = json.Marshal(report)
	r.snapshots[project.ID] = append(r.snapshots[project.ID], snapshot)
	r.reports[project.ID] = append(r.reports[project.ID], report)
	project.Status = domain.ProjectStatusAnalyzed
	r.projects[project.ID] = project
	return snapshot, nil
}

func (r *memoryProjectRepository) GetLatestDiscovery(_ context.Context, projectID string) (domain.ServiceSnapshot, domain.DiscoveryReport, error) {
	if len(r.snapshots[projectID]) == 0 {
		return domain.ServiceSnapshot{}, domain.DiscoveryReport{}, domain.ErrNotFound
	}
	index := len(r.snapshots[projectID]) - 1
	return r.snapshots[projectID][index], r.reports[projectID][index], nil
}
