package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type architectureCatalogCurrentFake struct {
	value domain.ArchitectureCatalog
	err   error
}

func (f architectureCatalogCurrentFake) Handle(context.Context) (domain.ArchitectureCatalog, error) {
	return f.value, f.err
}

type architectureCatalogServiceFake struct {
	value domain.ArchitectureCatalogService
	err   error
}

func (f architectureCatalogServiceFake) Handle(context.Context, string) (domain.ArchitectureCatalogService, error) {
	return f.value, f.err
}

type architectureCatalogOperationFake struct {
	value domain.ArchitectureCatalogOperation
	err   error
}

func (f architectureCatalogOperationFake) Handle(context.Context, string, string) (domain.ArchitectureCatalogOperation, error) {
	return f.value, f.err
}

type architectureCatalogPlatformMermaidFake struct {
	value string
	err   error
}

func (f architectureCatalogPlatformMermaidFake) Handle(context.Context) (string, error) {
	return f.value, f.err
}

type architectureCatalogServiceMermaidFake struct {
	value string
	err   error
}

func (f architectureCatalogServiceMermaidFake) Handle(context.Context, string) (string, error) {
	return f.value, f.err
}

type architectureCatalogOperationMermaidFake struct {
	value string
	err   error
}

func (f architectureCatalogOperationMermaidFake) Handle(context.Context, string, string) (string, error) {
	return f.value, f.err
}

func TestArchitectureCatalogHandlerReturnsCurrentAndGeneratedMermaid(t *testing.T) {
	handler := ArchitectureCatalogHandler{
		Current:          architectureCatalogCurrentFake{value: domain.ArchitectureCatalog{Mode: domain.ArchitectureCatalogModeCurrent}},
		Service:          architectureCatalogServiceFake{value: domain.ArchitectureCatalogService{Source: domain.ArchitectureCatalogSourceStatus{ProjectID: "teacher"}}},
		Operation:        architectureCatalogOperationFake{value: domain.ArchitectureCatalogOperation{Manifest: domain.ArchitectureOperationManifest{ID: "health"}}},
		PlatformMermaid:  architectureCatalogPlatformMermaidFake{value: "%% GENERATED\nflowchart LR\n"},
		ServiceMermaid:   architectureCatalogServiceMermaidFake{value: "%% GENERATED\nflowchart LR\n"},
		OperationMermaid: architectureCatalogOperationMermaidFake{value: "%% GENERATED\nflowchart TD\n"},
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/architecture/platform", nil)
	response := httptest.NewRecorder()
	handler.GetPlatform(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "\"mode\":\"CURRENT\"") {
		t.Fatalf("platform response = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/architecture/platform/mermaid", nil)
	response = httptest.NewRecorder()
	handler.GetPlatformMermaid(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" || !strings.HasPrefix(response.Body.String(), "%% GENERATED") {
		t.Fatalf("platform mermaid response = %d %q %s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/architecture/platform/services/teacher/mermaid", nil)
	request = withRouteParams(request, map[string]string{"projectId": "teacher"})
	response = httptest.NewRecorder()
	handler.GetServiceMermaid(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" || !strings.HasPrefix(response.Body.String(), "%% GENERATED") {
		t.Fatalf("mermaid response = %d %q %s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
}

func TestArchitectureCatalogHandlerMapsDomainErrors(t *testing.T) {
	handler := ArchitectureCatalogHandler{Operation: architectureCatalogOperationFake{err: domain.ErrNotFound}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/architecture/platform/services/missing/operations/nope", nil)
	request = withRouteParams(request, map[string]string{"projectId": "missing", "operationId": "nope"})
	response := httptest.NewRecorder()
	handler.GetOperation(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "not_found") {
		t.Fatalf("error response = %d %s", response.Code, response.Body.String())
	}

	handler.Current = architectureCatalogCurrentFake{err: errors.New("storage failure")}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/architecture/platform", nil)
	response = httptest.NewRecorder()
	handler.GetPlatform(response, request)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "storage failure") {
		t.Fatalf("unexpected error response = %d %s", response.Code, response.Body.String())
	}

	handler.PlatformMermaid = architectureCatalogPlatformMermaidFake{err: domain.ErrConflict}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/architecture/platform/mermaid", nil)
	response = httptest.NewRecorder()
	handler.GetPlatformMermaid(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "conflict") {
		t.Fatalf("platform mermaid error response = %d %s", response.Code, response.Body.String())
	}
}

func withRouteParams(request *http.Request, values map[string]string) *http.Request {
	routeContext := chi.NewRouteContext()
	for key, value := range values {
		routeContext.URLParams.Add(key, value)
	}
	return request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
}
