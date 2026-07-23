package workitem

import (
	"context"
	"fmt"
	"sync"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type FakeGateway struct {
	mu sync.Mutex

	ProjectMetadata map[string]repository.ProjectIssueMetadata
	ExistingIssues  map[string]domain.WorkItemPublication
	Published       map[string]domain.WorkItemPublication
	IssueCreates    int
	PullCreates     int
	BranchPushes    int
	DryRunMode      bool
}

func NewFakeGateway() *FakeGateway {
	return &FakeGateway{
		ProjectMetadata: make(map[string]repository.ProjectIssueMetadata),
		ExistingIssues:  make(map[string]domain.WorkItemPublication),
		Published:       make(map[string]domain.WorkItemPublication),
	}
}

func (*FakeGateway) Configured() bool { return true }
func (f *FakeGateway) DryRun() bool   { return f.DryRunMode }

func (f *FakeGateway) Metadata(_ context.Context, project domain.Project) (repository.ProjectIssueMetadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if value, ok := f.ProjectMetadata[project.ID]; ok {
		return value, nil
	}
	return repository.ProjectIssueMetadata{
		Labels: []string{"тип::задача"}, Milestones: []string{"Ближайший релиз"},
		Assignees: []string{"owner"}, Reviewers: []string{"owner"},
	}, nil
}

func (f *FakeGateway) GetIssue(_ context.Context, project domain.Project, number int64) (domain.WorkItemPublication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if value, ok := f.ExistingIssues[fmt.Sprintf("%s:%d", project.ID, number)]; ok {
		return value, nil
	}
	return domain.WorkItemPublication{}, domain.ErrNotFound
}

func (f *FakeGateway) PublishIssue(_ context.Context, _ domain.Project, item domain.WorkItem) (domain.WorkItemPublication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if value, ok := f.Published[item.IdempotencyKey]; ok {
		return value, nil
	}
	if f.DryRunMode {
		return domain.WorkItemPublication{Number: 1, URL: "https://example.invalid/dry-run/issue", State: "preview"}, nil
	}
	f.IssueCreates++
	value := domain.WorkItemPublication{
		Number: int64(f.IssueCreates), URL: fmt.Sprintf("https://github.example.test/issues/%d", f.IssueCreates), State: "open",
	}
	f.Published[item.IdempotencyKey] = value
	return value, nil
}

func (f *FakeGateway) PublishPullRequest(_ context.Context, _ domain.Project, item domain.WorkItem) (domain.WorkItemPublication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if value, ok := f.Published[item.IdempotencyKey]; ok {
		return value, nil
	}
	if f.DryRunMode {
		return domain.WorkItemPublication{Number: 1, URL: "https://example.invalid/dry-run/pull", State: "preview"}, nil
	}
	f.PullCreates++
	value := domain.WorkItemPublication{
		Number: int64(f.PullCreates), URL: fmt.Sprintf("https://github.example.test/pull/%d", f.PullCreates), State: "open",
	}
	f.Published[item.IdempotencyKey] = value
	return value, nil
}

func (f *FakeGateway) PublishBranch(_ context.Context, _ domain.Project, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.DryRunMode {
		f.BranchPushes++
	}
	return nil
}

var _ repository.WorkItemGateway = (*FakeGateway)(nil)
