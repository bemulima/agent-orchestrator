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

func TestRouterTopologyAPI(t *testing.T) {
	project := domain.Project{ID: "orders-id", Name: "orders"}
	service := domain.TopologyService{ProjectID: project.ID, Name: project.Name}
	catalog := domain.TopologyCatalog{
		Revision:  domain.TopologyRevision{ID: "revision-id", Fingerprint: "fingerprint"},
		Services:  []domain.TopologyService{service},
		Contracts: []domain.Contract{{ProjectID: project.ID, Code: "http:get:/orders", Type: domain.ContractTypeHTTP}},
		Drifts:    []domain.ContractDrift{{ContractCode: "http:get:/orders", Severity: domain.DriftSeverityError}},
	}
	handler := &handlers.TopologyHandler{
		Rebuild: topologyCatalogFake{catalog: catalog}, Get: topologyCatalogFake{catalog: catalog},
		Services:  topologyServicesFake{services: catalog.Services},
		Contracts: topologyContractsFake{contracts: catalog.Contracts},
		Drift:     topologyDriftFake{drifts: catalog.Drifts},
		ProjectQuery: topologyProjectQueryFake{view: domain.ProjectTopology{
			Project: project, Dependencies: []domain.TopologyService{service}, Consumers: []domain.TopologyService{service},
			Contracts: catalog.Contracts, Impact: []domain.TopologyService{service},
		}},
	}
	router := NewRouter(RouterDependencies{
		HealthHandler: handlers.HealthHandler{Readiness: healthuc.CheckReadiness{}}, TopologyHandler: handler,
	})
	tests := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/topology/rebuild"},
		{http.MethodGet, "/api/v1/topology?q=orders"},
		{http.MethodGet, "/api/v1/topology/services?kind="},
		{http.MethodGet, "/api/v1/topology/contracts?type=http"},
		{http.MethodGet, "/api/v1/topology/contract-drift?severity=error"},
		{http.MethodGet, "/api/v1/projects/orders-id/dependencies"},
		{http.MethodGet, "/api/v1/projects/orders-id/contracts"},
		{http.MethodGet, "/api/v1/projects/orders-id/consumers"},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d, body=%s", test.method, test.path, response.Code, response.Body.String())
		}
		if response.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("%s content type = %q", test.path, response.Header().Get("Content-Type"))
		}
	}
}

type topologyCatalogFake struct{ catalog domain.TopologyCatalog }

func (f topologyCatalogFake) Handle(context.Context) (domain.TopologyCatalog, error) {
	return f.catalog, nil
}

type topologyServicesFake struct{ services []domain.TopologyService }

func (f topologyServicesFake) Handle(context.Context) ([]domain.TopologyService, error) {
	return f.services, nil
}

type topologyContractsFake struct{ contracts []domain.Contract }

func (f topologyContractsFake) Handle(context.Context) ([]domain.Contract, error) {
	return f.contracts, nil
}

type topologyDriftFake struct{ drifts []domain.ContractDrift }

func (f topologyDriftFake) Handle(context.Context) ([]domain.ContractDrift, error) {
	return f.drifts, nil
}

type topologyProjectQueryFake struct{ view domain.ProjectTopology }

func (f topologyProjectQueryFake) Handle(context.Context, string) (domain.ProjectTopology, error) {
	return f.view, nil
}
