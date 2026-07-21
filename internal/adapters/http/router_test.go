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
	onboardinguc "github.com/bemulima/agent-orchestrator/internal/usecase/onboarding"
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

func TestRouter_OnboardingAPI(t *testing.T) {
	run := domain.OnboardingRun{ID: "run-id", ProjectID: "project-id", UnifiedDiff: "--- a/AGENTS.md\n+++ b/AGENTS.md\n"}
	onboardingHandler := &handlers.OnboardingHandler{
		Prepare: prepareOnboardingFake{run: run},
		Get:     getOnboardingFake{run: run},
		Approve: approveOnboardingFake{run: run},
		Reject:  rejectOnboardingFake{run: run},
		Apply:   applyOnboardingFake{output: onboardinguc.ApplyOutput{Run: run, Result: domain.OnboardingApplyResult{DryRun: true}}},
	}
	router := NewRouter(RouterDependencies{
		HealthHandler:     handlers.HealthHandler{Readiness: healthuc.CheckReadiness{}},
		OnboardingHandler: onboardingHandler,
	})
	tests := []struct {
		method string
		path   string
		body   string
		status int
		typeIs string
	}{
		{method: http.MethodPost, path: "/api/v1/projects/project-id/onboard", body: `{"dry_run":true}`, status: http.StatusCreated},
		{method: http.MethodGet, path: "/api/v1/onboarding-runs/run-id", status: http.StatusOK},
		{method: http.MethodGet, path: "/api/v1/onboarding-runs/run-id/diff", status: http.StatusOK, typeIs: "text/x-diff; charset=utf-8"},
		{method: http.MethodPost, path: "/api/v1/onboarding-runs/run-id/approve", body: `{"actor":"owner"}`, status: http.StatusOK},
		{method: http.MethodPost, path: "/api/v1/onboarding-runs/run-id/reject", body: `{"actor":"owner"}`, status: http.StatusOK},
		{method: http.MethodPost, path: "/api/v1/onboarding-runs/run-id/apply", body: `{"dry_run":true}`, status: http.StatusOK},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, bytes.NewBufferString(test.body))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s %s status = %d, body=%s", test.method, test.path, response.Code, response.Body.String())
		}
		if test.typeIs != "" && response.Header().Get("Content-Type") != test.typeIs {
			t.Fatalf("%s content type = %q", test.path, response.Header().Get("Content-Type"))
		}
	}
}

func TestRouter_OnboardingRejectsUnknownJSONField(t *testing.T) {
	router := NewRouter(RouterDependencies{
		HealthHandler: handlers.HealthHandler{Readiness: healthuc.CheckReadiness{}},
		OnboardingHandler: &handlers.OnboardingHandler{
			Prepare: prepareOnboardingFake{}, Get: getOnboardingFake{},
			Approve: approveOnboardingFake{}, Reject: rejectOnboardingFake{}, Apply: applyOnboardingFake{},
		},
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/project-id/onboard", bytes.NewBufferString(`{"dry_run":true,"write_source":true}`))
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

type prepareOnboardingFake struct {
	run domain.OnboardingRun
	err error
}

func (f prepareOnboardingFake) Handle(context.Context, onboardinguc.PrepareInput) (domain.OnboardingRun, error) {
	return f.run, f.err
}

type getOnboardingFake struct {
	run domain.OnboardingRun
	err error
}

func (f getOnboardingFake) Handle(context.Context, string) (domain.OnboardingRun, error) {
	return f.run, f.err
}

type approveOnboardingFake struct {
	run domain.OnboardingRun
	err error
}

func (f approveOnboardingFake) Handle(context.Context, onboardinguc.DecideInput) (domain.OnboardingRun, error) {
	return f.run, f.err
}

type rejectOnboardingFake struct {
	run domain.OnboardingRun
	err error
}

func (f rejectOnboardingFake) Handle(context.Context, onboardinguc.DecideInput) (domain.OnboardingRun, error) {
	return f.run, f.err
}

type applyOnboardingFake struct {
	output onboardinguc.ApplyOutput
	err    error
}

func (f applyOnboardingFake) Handle(context.Context, onboardinguc.ApplyInput) (onboardinguc.ApplyOutput, error) {
	return f.output, f.err
}
