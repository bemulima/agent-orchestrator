package workitem

import (
	"context"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestFakePublicationIsStableAcrossGatewayRestarts(t *testing.T) {
	project := domain.Project{ID: "00000000-0000-4000-8000-000000000001"}
	item := domain.WorkItem{Kind: domain.WorkItemIssue, IdempotencyKey: "plan:task:issue"}

	first, err := NewFakeGateway().PublishIssue(context.Background(), project, item)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewFakeGateway().PublishIssue(context.Background(), project, item)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.Number <= 0 {
		t.Fatalf("publications are not restart-stable: first=%#v second=%#v", first, second)
	}

	other, err := NewFakeGateway().PublishIssue(context.Background(), project, domain.WorkItem{
		Kind: domain.WorkItemIssue, IdempotencyKey: "plan:other-task:issue",
	})
	if err != nil {
		t.Fatal(err)
	}
	if other.Number == first.Number {
		t.Fatalf("different work items share fake number %d", first.Number)
	}
}
