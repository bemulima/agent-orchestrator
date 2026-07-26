package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/adapters/http/handlers"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	healthuc "github.com/bemulima/agent-orchestrator/internal/usecase/health"
	uiuc "github.com/bemulima/agent-orchestrator/internal/usecase/ui"
)

type uiQueryFake struct{}

func (uiQueryFake) Dashboard(context.Context) (domain.Dashboard, error) {
	return domain.Dashboard{}, nil
}
func (uiQueryFake) Plans(context.Context, uiuc.ListInput) (domain.PlanSummaryPage, error) {
	return domain.PlanSummaryPage{Items: []domain.PlanSummary{}}, nil
}
func (uiQueryFake) Runs(context.Context, uiuc.ListInput) (domain.RunSummaryPage, error) {
	return domain.RunSummaryPage{Items: []domain.RunSummary{}}, nil
}
func (uiQueryFake) Tasks(context.Context, uiuc.ListInput) (domain.TaskSummaryPage, error) {
	return domain.TaskSummaryPage{Items: []domain.TaskSummary{}}, nil
}
func (uiQueryFake) Approvals(context.Context, uiuc.ListInput) (domain.ApprovalSummaryPage, error) {
	return domain.ApprovalSummaryPage{Items: []domain.ApprovalSummary{}}, nil
}
func (uiQueryFake) Activity(context.Context, uiuc.ListInput) (domain.ActivityPage, error) {
	return domain.ActivityPage{Items: []domain.ActivityEvent{}}, nil
}

func TestRouterUIReadAPI(t *testing.T) {
	router := NewRouter(RouterDependencies{
		HealthHandler: handlers.HealthHandler{Readiness: healthuc.CheckReadiness{}},
		UIHandler:     &handlers.UIHandler{Queries: uiQueryFake{}},
	})
	for _, path := range []string{"/api/v1/dashboard", "/api/v1/plans", "/api/v1/runs", "/api/v1/tasks", "/api/v1/approvals", "/api/v1/activity"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestRouterUIRejectsInvalidLimit(t *testing.T) {
	router := NewRouter(RouterDependencies{
		HealthHandler: handlers.HealthHandler{Readiness: healthuc.CheckReadiness{}},
		UIHandler:     &handlers.UIHandler{Queries: uiQueryFake{}},
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/plans?limit=101", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
}
