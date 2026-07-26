package handlers

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	uiuc "github.com/bemulima/agent-orchestrator/internal/usecase/ui"
)

type eventQueriesFake struct {
	activity domain.ActivityPage
}

func (eventQueriesFake) Dashboard(context.Context) (domain.Dashboard, error) {
	return domain.Dashboard{}, nil
}
func (eventQueriesFake) Plans(context.Context, uiuc.ListInput) (domain.PlanSummaryPage, error) {
	return domain.PlanSummaryPage{}, nil
}
func (eventQueriesFake) Runs(context.Context, uiuc.ListInput) (domain.RunSummaryPage, error) {
	return domain.RunSummaryPage{}, nil
}
func (eventQueriesFake) Tasks(context.Context, uiuc.ListInput) (domain.TaskSummaryPage, error) {
	return domain.TaskSummaryPage{}, nil
}
func (eventQueriesFake) Approvals(context.Context, uiuc.ListInput) (domain.ApprovalSummaryPage, error) {
	return domain.ApprovalSummaryPage{}, nil
}
func (f eventQueriesFake) Activity(context.Context, uiuc.ListInput) (domain.ActivityPage, error) {
	return f.activity, nil
}

func TestWriteEventsUsesCurrentActivityAsInitialBaseline(t *testing.T) {
	latest := domain.ActivityEvent{ID: "00000000-0000-4000-8000-000000000002", ResourceType: "task", CreatedAt: time.Now()}
	handler := UIHandler{Queries: eventQueriesFake{activity: domain.ActivityPage{Items: []domain.ActivityEvent{latest}}}}
	response := httptest.NewRecorder()
	lastID, err := handler.writeEvents(context.Background(), response, response, "")
	if err != nil {
		t.Fatal(err)
	}
	if lastID != latest.ID || !strings.Contains(response.Body.String(), ": connected") || strings.Contains(response.Body.String(), "event: task.updated") {
		t.Fatalf("last ID = %q, body = %q", lastID, response.Body.String())
	}
}
