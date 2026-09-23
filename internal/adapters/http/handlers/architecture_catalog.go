package handlers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bemulima/agent-orchestrator/internal/architecturepresentation"
	"github.com/bemulima/agent-orchestrator/internal/architectureprocess"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type architectureCatalogCurrentUseCase interface {
	Handle(context.Context) (domain.ArchitectureCatalog, error)
}
type architectureCatalogServiceUseCase interface {
	Handle(context.Context, string) (domain.ArchitectureCatalogService, error)
}
type architectureCatalogOperationUseCase interface {
	Handle(context.Context, string, string) (domain.ArchitectureCatalogOperation, error)
}
type architectureCatalogPlatformMermaidUseCase interface {
	Handle(context.Context) (string, error)
}
type architectureCatalogServiceMermaidUseCase interface {
	Handle(context.Context, string) (string, error)
}
type architectureCatalogOperationMermaidUseCase interface {
	Handle(context.Context, string, string) (string, error)
}

// ArchitectureCatalogHandler serves the manifest-backed Architecture CURRENT
// hierarchy. It deliberately has no mutation endpoint: manifests are owned by
// their repositories and discovery supplies the immutable CURRENT input.
type ArchitectureCatalogHandler struct {
	Current          architectureCatalogCurrentUseCase
	Service          architectureCatalogServiceUseCase
	Operation        architectureCatalogOperationUseCase
	PlatformMermaid  architectureCatalogPlatformMermaidUseCase
	ServiceMermaid   architectureCatalogServiceMermaidUseCase
	OperationMermaid architectureCatalogOperationMermaidUseCase
	ProcessRoot      string
	Presentation     architecturepresentation.Projector
}

func (h ArchitectureCatalogHandler) GetPlatform(w http.ResponseWriter, r *http.Request) {
	result, err := h.Current.Handle(r.Context())
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// GetPlatformPresentation provides the additive V2 service-only graph model.
// It is deliberately separate from the canonical catalog response.
func (h ArchitectureCatalogHandler) GetPlatformPresentation(w http.ResponseWriter, r *http.Request) {
	catalog, err := h.Current.Handle(r.Context())
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	view := architecturepresentation.PlatformViewMode(r.URL.Query().Get("view"))
	if view == "" {
		view = architecturepresentation.PlatformViewBusiness
	}
	result, err := h.Presentation.Platform(r.Context(), catalog, view)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h ArchitectureCatalogHandler) GetServicePresentation(w http.ResponseWriter, r *http.Request) {
	catalog, err := h.Current.Handle(r.Context())
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	result, err := h.Presentation.Service(r.Context(), catalog, chi.URLParam(r, "projectId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h ArchitectureCatalogHandler) GetOperationPresentation(w http.ResponseWriter, r *http.Request) {
	catalog, err := h.Current.Handle(r.Context())
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	result, err := h.Presentation.Operation(r.Context(), catalog, chi.URLParam(r, "projectId"), chi.URLParam(r, "operationId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h ArchitectureCatalogHandler) GetService(w http.ResponseWriter, r *http.Request) {
	result, err := h.Service.Handle(r.Context(), chi.URLParam(r, "projectId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h ArchitectureCatalogHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	result, err := h.Operation.Handle(r.Context(), chi.URLParam(r, "projectId"), chi.URLParam(r, "operationId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// ListProcesses returns only version-controlled, confirmed CURRENT processes.
// Candidate/unverified processes are never elevated to CURRENT by this route.
func (h ArchitectureCatalogHandler) ListProcesses(w http.ResponseWriter, r *http.Request) {
	processes, err := architectureprocess.LoadCurrent(h.ProcessRoot)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Mode      string                         `json:"mode"`
		Processes []architectureprocess.Manifest `json:"processes"`
	}{Mode: domain.ArchitectureCatalogModeCurrent, Processes: processes})
}

// GetPlatformMermaid returns the deterministic Mermaid projection of the
// complete Architecture CURRENT platform graph.
func (h ArchitectureCatalogHandler) GetPlatformMermaid(w http.ResponseWriter, r *http.Request) {
	result, err := h.PlatformMermaid.Handle(r.Context())
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeMermaid(w, result)
}

func (h ArchitectureCatalogHandler) GetServiceMermaid(w http.ResponseWriter, r *http.Request) {
	result, err := h.ServiceMermaid.Handle(r.Context(), chi.URLParam(r, "projectId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeMermaid(w, result)
}

func (h ArchitectureCatalogHandler) GetOperationMermaid(w http.ResponseWriter, r *http.Request) {
	result, err := h.OperationMermaid.Handle(r.Context(), chi.URLParam(r, "projectId"), chi.URLParam(r, "operationId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeMermaid(w, result)
}

func writeMermaid(w http.ResponseWriter, value string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(value))
}
