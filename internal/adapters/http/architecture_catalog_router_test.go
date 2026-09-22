package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/adapters/http/handlers"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	healthuc "github.com/bemulima/agent-orchestrator/internal/usecase/health"
)

type architectureCatalogRouterCurrentFake struct{}

func (architectureCatalogRouterCurrentFake) Handle(context.Context) (domain.ArchitectureCatalog, error) {
	return domain.ArchitectureCatalog{Mode: domain.ArchitectureCatalogModeCurrent}, nil
}

type architectureCatalogRouterServiceFake struct{}

func (architectureCatalogRouterServiceFake) Handle(context.Context, string) (domain.ArchitectureCatalogService, error) {
	return domain.ArchitectureCatalogService{Source: domain.ArchitectureCatalogSourceStatus{ProjectID: "teacher"}}, nil
}

type architectureCatalogRouterOperationFake struct{}

func (architectureCatalogRouterOperationFake) Handle(context.Context, string, string) (domain.ArchitectureCatalogOperation, error) {
	return domain.ArchitectureCatalogOperation{Manifest: domain.ArchitectureOperationManifest{ID: "health"}}, nil
}

type architectureCatalogRouterPlatformMermaidFake struct{}

func (architectureCatalogRouterPlatformMermaidFake) Handle(context.Context) (string, error) {
	return "%% GENERATED\nflowchart LR\n", nil
}

type architectureCatalogRouterServiceMermaidFake struct{}

func (architectureCatalogRouterServiceMermaidFake) Handle(context.Context, string) (string, error) {
	return "flowchart LR\n", nil
}

type architectureCatalogRouterOperationMermaidFake struct{}

func (architectureCatalogRouterOperationMermaidFake) Handle(context.Context, string, string) (string, error) {
	return "flowchart TD\n", nil
}

func TestRouterArchitectureCatalogAPI(t *testing.T) {
	router := NewRouter(RouterDependencies{
		HealthHandler: handlers.HealthHandler{Readiness: healthuc.CheckReadiness{}},
		ArchitectureCatalogHandler: &handlers.ArchitectureCatalogHandler{
			Current:          architectureCatalogRouterCurrentFake{},
			Service:          architectureCatalogRouterServiceFake{},
			Operation:        architectureCatalogRouterOperationFake{},
			PlatformMermaid:  architectureCatalogRouterPlatformMermaidFake{},
			ServiceMermaid:   architectureCatalogRouterServiceMermaidFake{},
			OperationMermaid: architectureCatalogRouterOperationMermaidFake{},
		},
	})
	tests := []struct {
		path        string
		contentType string
	}{
		{path: "/api/v1/architecture/platform", contentType: "application/json"},
		{path: "/api/v1/architecture/platform/mermaid", contentType: "text/plain; charset=utf-8"},
		{path: "/api/v1/architecture/platform/services/teacher", contentType: "application/json"},
		{path: "/api/v1/architecture/platform/services/teacher/operations/health", contentType: "application/json"},
		{path: "/api/v1/architecture/platform/services/teacher/mermaid", contentType: "text/plain; charset=utf-8"},
		{path: "/api/v1/architecture/platform/services/teacher/operations/health/mermaid", contentType: "text/plain; charset=utf-8"},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body=%s", test.path, response.Code, response.Body.String())
		}
		if response.Header().Get("Content-Type") != test.contentType {
			t.Fatalf("GET %s content type = %q, want %q", test.path, response.Header().Get("Content-Type"), test.contentType)
		}
	}
}
