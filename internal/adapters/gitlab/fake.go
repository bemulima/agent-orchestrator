package gitlab

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

// FakeAdapter is an in-memory GitLab boundary for application and workflow
// tests. Counters expose whether retries caused duplicate external writes.
type FakeAdapter struct {
	mu sync.Mutex

	Projects map[string]domain.GitLabProject
	Issues   map[string]domain.GitLabIssue
	MRs      map[string]domain.GitLabMergeRequest
	Comments map[string]struct{}
	Links    map[string]struct{}
	Branches map[string]struct{}

	IssueCreates   int
	MRCreates      int
	CommentCreates int
	LinkCreates    int
	Pushes         int
	BranchCreates  int
}

func NewFakeAdapter() *FakeAdapter {
	return &FakeAdapter{
		Projects: make(map[string]domain.GitLabProject), Issues: make(map[string]domain.GitLabIssue),
		MRs: make(map[string]domain.GitLabMergeRequest), Comments: make(map[string]struct{}),
		Links:    make(map[string]struct{}),
		Branches: make(map[string]struct{}),
	}
}

func (*FakeAdapter) Configured() bool { return true }
func (*FakeAdapter) DryRun() bool     { return false }

func (f *FakeAdapter) ResolveProject(_ context.Context, reference string) (domain.GitLabProject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if project, ok := f.Projects[reference]; ok {
		return project, nil
	}
	id := stableDryRunID(reference)
	project := domain.GitLabProject{ID: id, Reference: strconv.FormatInt(id, 10), WebURL: "https://gitlab.example.test/" + reference}
	f.Projects[reference] = project
	f.Projects[project.Reference] = project
	return project, nil
}

func (f *FakeAdapter) ResolveConnectedProject(ctx context.Context, project domain.Project) (domain.GitLabProject, error) {
	if project.GitLabProjectID != nil && *project.GitLabProjectID > 0 {
		return f.ResolveProject(ctx, strconv.FormatInt(*project.GitLabProjectID, 10))
	}
	if project.GitURL == nil {
		return domain.GitLabProject{}, fmt.Errorf("project Git URL is required: %w", domain.ErrValidation)
	}
	_, path, err := splitGitRemote(*project.GitURL)
	if err != nil {
		return domain.GitLabProject{}, err
	}
	return f.ResolveProject(ctx, path)
}

func (f *FakeAdapter) EnsureIssue(_ context.Context, spec domain.GitLabIssueSpec) (domain.GitLabIssue, error) {
	if err := validateIssueSpec(spec); err != nil {
		return domain.GitLabIssue{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if issue, ok := f.Issues[spec.IdempotencyKey]; ok {
		issue.Title = spec.Title
		issue.State = spec.State
		f.Issues[spec.IdempotencyKey] = issue
		return issue, nil
	}
	f.IssueCreates++
	issue := domain.GitLabIssue{
		ProjectID: spec.Project.ID, IID: int64(f.IssueCreates), Title: spec.Title, State: spec.State,
		WebURL: fmt.Sprintf("https://gitlab.example.test/projects/%d/issues/%d", spec.Project.ID, f.IssueCreates),
	}
	f.Issues[spec.IdempotencyKey] = issue
	return issue, nil
}

func (f *FakeAdapter) EnsureComment(_ context.Context, _ domain.GitLabIssue, body, key string) error {
	if key == "" || body == "" {
		return fmt.Errorf("comment key and body are required: %w", domain.ErrValidation)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.Comments[key]; !ok {
		f.Comments[key] = struct{}{}
		f.CommentCreates++
	}
	return nil
}

func (f *FakeAdapter) EnsureIssueLink(_ context.Context, source, target domain.GitLabIssue) error {
	key := fmt.Sprintf("%d:%d:%d:%d", source.ProjectID, source.IID, target.ProjectID, target.IID)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.Links[key]; !ok {
		f.Links[key] = struct{}{}
		f.LinkCreates++
	}
	return nil
}

func (f *FakeAdapter) PushBranch(_ context.Context, project domain.Project, _, branch string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Pushes++
	key := project.ID + ":" + branch
	if _, ok := f.Branches[key]; !ok {
		f.Branches[key] = struct{}{}
		f.BranchCreates++
	}
	return nil
}

func (f *FakeAdapter) EnsureMergeRequest(
	_ context.Context,
	spec domain.GitLabMergeRequestSpec,
) (domain.GitLabMergeRequest, error) {
	if err := validateMergeRequestSpec(spec); err != nil {
		return domain.GitLabMergeRequest{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if mergeRequest, ok := f.MRs[spec.IdempotencyKey]; ok {
		return mergeRequest, nil
	}
	f.MRCreates++
	mergeRequest := domain.GitLabMergeRequest{
		ProjectID: spec.Project.ID, IID: int64(f.MRCreates), State: "opened",
		SourceBranch: spec.SourceBranch, TargetBranch: spec.TargetBranch,
		WebURL: fmt.Sprintf("https://gitlab.example.test/projects/%d/merge_requests/%d", spec.Project.ID, f.MRCreates),
	}
	f.MRs[spec.IdempotencyKey] = mergeRequest
	return mergeRequest, nil
}

var _ repository.GitLabGateway = (*FakeAdapter)(nil)
