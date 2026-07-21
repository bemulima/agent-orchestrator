package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/adapters/http/handlers"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
	healthuc "github.com/bemulima/agent-orchestrator/internal/usecase/health"
	projectuc "github.com/bemulima/agent-orchestrator/internal/usecase/project"
)

type routerChecker struct {
	name string
	err  error
}

func TestRouter_ProjectAPI(t *testing.T) {
	project := domain.Project{ID: "project-id", Name: "fixture", Status: domain.ProjectStatusAnalyzed}
	projectHandler := &handlers.ProjectHandler{
		Connect: connectProjectFake{result: projectuc.ConnectResult{Project: project}},
		Get:     getProjectFake{project: project},
		List:    listProjectsFake{projects: []domain.Project{project}},
		Scan:    scanProjectFake{result: projectuc.ScanResult{Project: project}},
		LatestReport: latestReportFake{result: projectuc.LatestDiscoveryResult{
			Snapshot: domain.ServiceSnapshot{ID: "snapshot-id", ProjectID: project.ID},
		}},
	}
	router := NewRouter(RouterDependencies{
		HealthHandler:  handlers.HealthHandler{Readiness: healthuc.CheckReadiness{}},
		ProjectHandler: projectHandler,
	})

	tests := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPost, path: "/api/v1/projects/connect", body: `{"path":"/projects/fixture"}`},
		{method: http.MethodGet, path: "/api/v1/projects"},
		{method: http.MethodGet, path: "/api/v1/projects/project-id"},
		{method: http.MethodPost, path: "/api/v1/projects/project-id/scan"},
		{method: http.MethodGet, path: "/api/v1/projects/project-id/reports/latest"},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, bytes.NewBufferString(test.body))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d, body=%s", test.method, test.path, response.Code, response.Body.String())
		}
	}
}

func TestRouter_ProjectConnectRejectsUnknownJSONField(t *testing.T) {
	projectHandler := &handlers.ProjectHandler{
		Connect: connectProjectFake{}, Get: getProjectFake{}, List: listProjectsFake{},
		Scan: scanProjectFake{}, LatestReport: latestReportFake{},
	}
	router := NewRouter(RouterDependencies{
		HealthHandler:  handlers.HealthHandler{Readiness: healthuc.CheckReadiness{}},
		ProjectHandler: projectHandler,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/connect", bytes.NewBufferString(`{"path":"/projects/fixture","secret":"value"}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
}

func TestRouter_ProjectConnectRejectsTrailingJSONData(t *testing.T) {
	projectHandler := &handlers.ProjectHandler{
		Connect: connectProjectFake{}, Get: getProjectFake{}, List: listProjectsFake{},
		Scan: scanProjectFake{}, LatestReport: latestReportFake{},
	}
	router := NewRouter(RouterDependencies{
		HealthHandler:  handlers.HealthHandler{Readiness: healthuc.CheckReadiness{}},
		ProjectHandler: projectHandler,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/connect", bytes.NewBufferString(`{"path":"/projects/fixture"} trailing`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
}

func (f routerChecker) Name() string               { return f.name }
func (f routerChecker) Ping(context.Context) error { return f.err }

func TestRouter_Health(t *testing.T) {
	router := newTestRouter(routerChecker{name: "postgres"})
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != healthuc.StatusOK {
		t.Fatalf("body status = %q, want %q", body["status"], healthuc.StatusOK)
	}
}

func TestRouter_ReadyReportsDependencyFailure(t *testing.T) {
	router := newTestRouter(routerChecker{name: "postgres", err: errors.New("secret details")})
	request := httptest.NewRequest(http.MethodGet, "/ready", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if got := response.Body.String(); got == "" || contains(got, "secret details") {
		t.Fatalf("response leaked dependency error: %q", got)
	}
}

func TestRouter_NotFoundUsesErrorEnvelope(t *testing.T) {
	router := newTestRouter(routerChecker{name: "postgres"})
	request := httptest.NewRequest(http.MethodGet, "/missing", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	var body handlers.ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "not_found" {
		t.Fatalf("error code = %q, want not_found", body.Error.Code)
	}
}

func newTestRouter(checkers ...routerChecker) http.Handler {
	dependencies := make([]repository.HealthChecker, 0, len(checkers))
	for _, checker := range checkers {
		dependencies = append(dependencies, checker)
	}
	return NewRouter(RouterDependencies{
		HealthHandler: handlers.HealthHandler{
			Readiness: healthuc.CheckReadiness{Dependencies: dependencies},
			Timeout:   DefaultHealthTimeout,
		},
	})
}

func contains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}

type connectProjectFake struct {
	result projectuc.ConnectResult
	err    error
}

func (f connectProjectFake) Handle(context.Context, projectuc.ConnectInput) (projectuc.ConnectResult, error) {
	return f.result, f.err
}

type getProjectFake struct {
	project domain.Project
	err     error
}

func (f getProjectFake) Handle(context.Context, string) (domain.Project, error) {
	return f.project, f.err
}

type listProjectsFake struct {
	projects []domain.Project
	err      error
}

func (f listProjectsFake) Handle(context.Context) ([]domain.Project, error) {
	return f.projects, f.err
}

type scanProjectFake struct {
	result projectuc.ScanResult
	err    error
}

func (f scanProjectFake) Handle(context.Context, string) (projectuc.ScanResult, error) {
	return f.result, f.err
}

type latestReportFake struct {
	result projectuc.LatestDiscoveryResult
	err    error
}

func (f latestReportFake) Handle(context.Context, string) (projectuc.LatestDiscoveryResult, error) {
	return f.result, f.err
}
