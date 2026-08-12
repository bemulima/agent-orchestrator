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

func TestProjectLifecycle_ArchivesAndRestoresPreviousStatus(t *testing.T) {
	repository := newMemoryProjectRepository()
	path := "/allowed/fixture"
	created, err := repository.Upsert(context.Background(), domain.Project{
		Name: "fixture", SourceIdentity: "local:fixture", LocalPath: &path,
		Status: domain.ProjectStatusAnalyzed, RepositoryRole: domain.RepositoryRoleService,
	})
	if err != nil {
		t.Fatal(err)
	}

	archived, err := (ArchiveProject{Projects: repository}).Handle(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("ArchiveProject.Handle() error = %v", err)
	}
	if archived.Status != domain.ProjectStatusArchived || archived.ArchivedAt == nil ||
		archived.ArchivedFrom == nil || *archived.ArchivedFrom != domain.ProjectStatusAnalyzed {
		t.Fatalf("archived project = %#v", archived)
	}
	if projects, listErr := repository.List(context.Background()); listErr != nil || len(projects) != 0 {
		t.Fatalf("active projects after archive = %#v, error = %v", projects, listErr)
	}
	if _, scanErr := (ScanProject{Projects: repository}).Handle(context.Background(), created.ID); !errors.Is(scanErr, domain.ErrInvalidStatus) {
		t.Fatalf("archived scan error = %v, want invalid status", scanErr)
	}
	sources := &fakeProjectSource{source: domain.RepositorySource{
		Name: "fixture", Identity: "local:fixture", LocalPath: path, HeadCommit: "unexpected-new-head",
	}}
	connect := ConnectProject{Projects: repository, Sources: sources, Scan: ScanProject{Projects: repository}}
	if _, connectErr := connect.Handle(context.Background(), ConnectInput{LocalPath: path}); !errors.Is(connectErr, domain.ErrInvalidStatus) {
		t.Fatalf("archived reconnect error = %v, want invalid status", connectErr)
	}
	unchanged, err := repository.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() archived project error = %v", err)
	}
	if unchanged.HeadCommit != created.HeadCommit {
		t.Fatalf("archived reconnect changed head commit to %q", unchanged.HeadCommit)
	}

	restored, err := (RestoreProject{Projects: repository}).Handle(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("RestoreProject.Handle() error = %v", err)
	}
	if restored.Status != domain.ProjectStatusAnalyzed || restored.ArchivedAt != nil || restored.ArchivedFrom != nil {
		t.Fatalf("restored project = %#v", restored)
	}
}

func TestProjectLifecycle_RejectsScanningArchiveAndActiveRestore(t *testing.T) {
	repository := newMemoryProjectRepository()
	path := "/allowed/fixture"
	created, err := repository.Upsert(context.Background(), domain.Project{
		Name: "fixture", SourceIdentity: "local:fixture", LocalPath: &path,
		Status: domain.ProjectStatusScanning, RepositoryRole: domain.RepositoryRoleService,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, archiveErr := (ArchiveProject{Projects: repository}).Handle(context.Background(), created.ID); !errors.Is(archiveErr, domain.ErrInvalidStatus) {
		t.Fatalf("scanning archive error = %v, want invalid status", archiveErr)
	}
	if _, restoreErr := (RestoreProject{Projects: repository}).Handle(context.Background(), created.ID); !errors.Is(restoreErr, domain.ErrInvalidStatus) {
		t.Fatalf("active restore error = %v, want invalid status", restoreErr)
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

func TestGetProject_UsesNameLookupWithoutSendingNameToUUIDRepositoryQuery(t *testing.T) {
	repository := newMemoryProjectRepository()
	path := "/allowed/fixture"
	created, err := repository.Upsert(context.Background(), domain.Project{
		Name: "fixture", SourceIdentity: "local:fixture", LocalPath: &path,
	})
	if err != nil {
		t.Fatal(err)
	}
	repository.getErr = errors.New("invalid UUID input")
	got, err := (GetProject{Projects: repository}).Handle(context.Background(), "fixture")
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if got.ID != created.ID || repository.getCalls != 0 {
		t.Fatalf("project = %#v, UUID Get calls = %d", got, repository.getCalls)
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
	getCalls   int
	getErr     error
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
		if existing.Status == domain.ProjectStatusArchived {
			return existing, nil
		}
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
	r.getCalls++
	if r.getErr != nil {
		return domain.Project{}, r.getErr
	}
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
		if project.Status == domain.ProjectStatusArchived {
			continue
		}
		projects = append(projects, project)
	}
	return projects, nil
}

func (r *memoryProjectRepository) ListAll(context.Context) ([]domain.Project, error) {
	projects := make([]domain.Project, 0, len(r.projects))
	for _, project := range r.projects {
		projects = append(projects, project)
	}
	return projects, nil
}

func (r *memoryProjectRepository) Archive(_ context.Context, id string) (domain.Project, error) {
	project, exists := r.projects[id]
	if !exists {
		return domain.Project{}, domain.ErrNotFound
	}
	if project.Status == domain.ProjectStatusArchived {
		return project, nil
	}
	if project.Status == domain.ProjectStatusScanning {
		return domain.Project{}, domain.ErrInvalidStatus
	}
	now := time.Now()
	previous := project.Status
	project.Status = domain.ProjectStatusArchived
	project.ArchivedAt = &now
	project.ArchivedFrom = &previous
	r.projects[id] = project
	return project, nil
}

func (r *memoryProjectRepository) Restore(_ context.Context, id string) (domain.Project, error) {
	project, exists := r.projects[id]
	if !exists {
		return domain.Project{}, domain.ErrNotFound
	}
	if project.Status != domain.ProjectStatusArchived || project.ArchivedFrom == nil {
		return domain.Project{}, domain.ErrInvalidStatus
	}
	project.Status = *project.ArchivedFrom
	project.ArchivedAt = nil
	project.ArchivedFrom = nil
	r.projects[id] = project
	return project, nil
}

func (r *memoryProjectRepository) UpdateSourceState(_ context.Context, id string, status domain.ProjectStatus, source domain.RepositorySource) (domain.Project, error) {
	project, err := r.Get(context.Background(), id)
	if err != nil {
		return domain.Project{}, err
	}
	if project.Status == domain.ProjectStatusArchived {
		return domain.Project{}, domain.ErrNotFound
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
		latestReport := r.reports[project.ID][len(r.reports[project.ID])-1]
		if latest.CommitSHA == snapshot.CommitSHA && latest.Branch == snapshot.Branch &&
			latest.IsDirty == snapshot.IsDirty && latest.ContentChecksum == snapshot.ContentChecksum &&
			latestReport.SchemaVersion == report.SchemaVersion {
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
