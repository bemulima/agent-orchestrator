package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/course-dev-orchestrator/internal/adapters/http/handlers"
	"github.com/example/course-dev-orchestrator/internal/domain/repository"
	healthuc "github.com/example/course-dev-orchestrator/internal/usecase/health"
)

type routerChecker struct {
	name string
	err  error
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
