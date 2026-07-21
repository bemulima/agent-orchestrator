package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type topologyCatalogUseCase interface {
	Handle(context.Context) (domain.TopologyCatalog, error)
}

type topologyServicesUseCase interface {
	Handle(context.Context) ([]domain.TopologyService, error)
}

type topologyContractsUseCase interface {
	Handle(context.Context) ([]domain.Contract, error)
}

type topologyDriftUseCase interface {
	Handle(context.Context) ([]domain.ContractDrift, error)
}

type projectTopologyUseCase interface {
	Handle(context.Context, string) (domain.ProjectTopology, error)
}

type TopologyHandler struct {
	Rebuild      topologyCatalogUseCase
	Get          topologyCatalogUseCase
	Services     topologyServicesUseCase
	Contracts    topologyContractsUseCase
	Drift        topologyDriftUseCase
	ProjectQuery projectTopologyUseCase
}

func (h TopologyHandler) RebuildTopology(w http.ResponseWriter, r *http.Request) {
	catalog, err := h.Rebuild.Handle(r.Context())
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, catalog)
}

func (h TopologyHandler) GetTopology(w http.ResponseWriter, r *http.Request) {
	catalog, err := h.Get.Handle(r.Context())
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, filterCatalog(catalog, r.URL.Query().Get("q")))
}

func (h TopologyHandler) ListServices(w http.ResponseWriter, r *http.Request) {
	services, err := h.Services.Handle(r.Context())
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	role := r.URL.Query().Get("role")
	kind := r.URL.Query().Get("kind")
	filtered := make([]domain.TopologyService, 0, len(services))
	for _, service := range services {
		if role != "" && string(service.RepositoryRole) != role || kind != "" && string(service.ServiceKind) != kind {
			continue
		}
		if query != "" && !jsonContains(service, query) {
			continue
		}
		filtered = append(filtered, service)
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": filtered})
}

func (h TopologyHandler) ListContracts(w http.ResponseWriter, r *http.Request) {
	contracts, err := h.Contracts.Handle(r.Context())
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	projectID := r.URL.Query().Get("project_id")
	contractType := r.URL.Query().Get("type")
	direction := r.URL.Query().Get("direction")
	filtered := make([]domain.Contract, 0, len(contracts))
	for _, contract := range contracts {
		if projectID != "" && contract.ProjectID != projectID || contractType != "" && string(contract.Type) != contractType ||
			direction != "" && contract.Direction != direction {
			continue
		}
		if query != "" && !jsonContains(contract, query) {
			continue
		}
		filtered = append(filtered, contract)
	}
	writeJSON(w, http.StatusOK, map[string]any{"contracts": filtered})
}

func (h TopologyHandler) ListContractDrift(w http.ResponseWriter, r *http.Request) {
	drifts, err := h.Drift.Handle(r.Context())
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	severity := r.URL.Query().Get("severity")
	projectID := r.URL.Query().Get("project_id")
	filtered := make([]domain.ContractDrift, 0, len(drifts))
	for _, drift := range drifts {
		if severity != "" && string(drift.Severity) != severity || projectID != "" &&
			(pointerValue(drift.ProducerProjectID) != projectID && pointerValue(drift.ConsumerProjectID) != projectID) {
			continue
		}
		filtered = append(filtered, drift)
	}
	writeJSON(w, http.StatusOK, map[string]any{"contract_drift": filtered})
}

func (h TopologyHandler) ProjectDependencies(w http.ResponseWriter, r *http.Request) {
	view, err := h.ProjectQuery.Handle(r.Context(), chi.URLParam(r, "projectId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project": view.Project, "dependencies": view.Dependencies, "impact": view.Impact,
	})
}

func (h TopologyHandler) ProjectContracts(w http.ResponseWriter, r *http.Request) {
	view, err := h.ProjectQuery.Handle(r.Context(), chi.URLParam(r, "projectId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": view.Project, "contracts": view.Contracts})
}

func (h TopologyHandler) ProjectConsumers(w http.ResponseWriter, r *http.Request) {
	view, err := h.ProjectQuery.Handle(r.Context(), chi.URLParam(r, "projectId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project": view.Project, "consumers": view.Consumers, "impact": view.Impact,
	})
}

func filterCatalog(catalog domain.TopologyCatalog, query string) domain.TopologyCatalog {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return catalog
	}
	catalog.Services = filterJSON(catalog.Services, query)
	catalog.Capabilities = filterJSON(catalog.Capabilities, query)
	catalog.Ownership = filterJSON(catalog.Ownership, query)
	catalog.Contracts = filterJSON(catalog.Contracts, query)
	catalog.Relations = filterJSON(catalog.Relations, query)
	catalog.Drifts = filterJSON(catalog.Drifts, query)
	return catalog
}

func filterJSON[T any](values []T, query string) []T {
	result := make([]T, 0)
	for _, value := range values {
		if jsonContains(value, query) {
			result = append(result, value)
		}
	}
	return result
}

func jsonContains(value any, query string) bool {
	content, _ := json.Marshal(value)
	return strings.Contains(strings.ToLower(string(content)), query)
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
