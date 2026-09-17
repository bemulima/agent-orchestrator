package handlers

import (
	"context"
	"net/http"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/go-chi/chi/v5"
)

type architectureCurrentUseCase interface {
	Handle(context.Context) (domain.ArchitectureCurrent, error)
}
type architectureServiceUseCase interface {
	Handle(context.Context, string) (domain.ArchitectureServiceDetail, error)
}
type architectureContractsUseCase interface {
	Handle(context.Context, string) ([]domain.ArchitectureContract, error)
}
type ArchitectureHandler struct {
	Current   architectureCurrentUseCase
	Service   architectureServiceUseCase
	Contracts architectureContractsUseCase
}

func (h ArchitectureHandler) GetCurrent(w http.ResponseWriter, r *http.Request) {
	result, err := h.Current.Handle(r.Context())
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h ArchitectureHandler) GetService(w http.ResponseWriter, r *http.Request) {
	result, err := h.Service.Handle(r.Context(), chi.URLParam(r, "projectId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h ArchitectureHandler) GetContracts(w http.ResponseWriter, r *http.Request) {
	result, err := h.Contracts.Handle(r.Context(), chi.URLParam(r, "projectId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contracts": result})
}
