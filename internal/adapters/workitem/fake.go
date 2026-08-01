package workitem

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
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

func (f *FakeGateway) PublishIssue(_ context.Context, project domain.Project, item domain.WorkItem) (domain.WorkItemPublication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := publicationKey(project.ID, item)
	if value, ok := f.Published[key]; ok {
		return value, nil
	}
	if f.DryRunMode {
		return domain.WorkItemPublication{Number: 1, URL: "https://example.invalid/dry-run/issue", State: "preview"}, nil
	}
	f.IssueCreates++
	number := stablePublicationNumber(project.ID, string(item.Kind), item.IdempotencyKey)
	value := domain.WorkItemPublication{
		Number: number, URL: fmt.Sprintf("https://github.example.test/issues/%d", number), State: "open",
	}
	f.Published[key] = value
	return value, nil
}

func (f *FakeGateway) PublishPullRequest(_ context.Context, project domain.Project, item domain.WorkItem) (domain.WorkItemPublication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := publicationKey(project.ID, item)
	if value, ok := f.Published[key]; ok {
		return value, nil
	}
	if f.DryRunMode {
		return domain.WorkItemPublication{Number: 1, URL: "https://example.invalid/dry-run/pull", State: "preview"}, nil
	}
	f.PullCreates++
	number := stablePublicationNumber(project.ID, string(item.Kind), item.IdempotencyKey)
	value := domain.WorkItemPublication{
		Number: number, URL: fmt.Sprintf("https://github.example.test/pull/%d", number), State: "open",
	}
	f.Published[key] = value
	return value, nil
}

// stablePublicationNumber makes the fake gateway idempotent across process
// restarts. Sequential in-memory counters reused numbers after a restart and
// collided with persisted work items, leaving a partially published plan
// impossible to resume.
func stablePublicationNumber(projectID, kind, idempotencyKey string) int64 {
	sum := sha256.Sum256([]byte(projectID + "\x00" + kind + "\x00" + idempotencyKey))
	number := int64(binary.BigEndian.Uint64(sum[:8]) & math.MaxInt64)
	if number == 0 {
		return 1
	}
	return number
}

func publicationKey(projectID string, item domain.WorkItem) string {
	return projectID + "\x00" + string(item.Kind) + "\x00" + item.IdempotencyKey
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
