package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	projectuc "github.com/bemulima/agent-orchestrator/internal/usecase/project"
)

const maxProjectRequestBytes = 16 << 10

type connectProjectUseCase interface {
	Handle(context.Context, projectuc.ConnectInput) (projectuc.ConnectResult, error)
}

type getProjectUseCase interface {
	Handle(context.Context, string) (domain.Project, error)
}

type listProjectsUseCase interface {
	Handle(context.Context) ([]domain.Project, error)
}

type scanProjectUseCase interface {
	Handle(context.Context, string) (projectuc.ScanResult, error)
}

type latestDiscoveryUseCase interface {
	Handle(context.Context, string) (projectuc.LatestDiscoveryResult, error)
}

type ProjectHandler struct {
	Connect      connectProjectUseCase
	Get          getProjectUseCase
	List         listProjectsUseCase
	Scan         scanProjectUseCase
	LatestReport latestDiscoveryUseCase
}

func (h ProjectHandler) ConnectProject(w http.ResponseWriter, r *http.Request) {
	var input projectuc.ConnectInput
	if err := decodeJSON(w, r, &input); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	result, err := h.Connect.Handle(r.Context(), input)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h ProjectHandler) ListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := h.List.Handle(r.Context())
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

func (h ProjectHandler) GetProject(w http.ResponseWriter, r *http.Request) {
	project, err := h.Get.Handle(r.Context(), chi.URLParam(r, "projectId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (h ProjectHandler) ScanProject(w http.ResponseWriter, r *http.Request) {
	result, err := h.Scan.Handle(r.Context(), chi.URLParam(r, "projectId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h ProjectHandler) LatestDiscoveryReport(w http.ResponseWriter, r *http.Request) {
	result, err := h.LatestReport.Handle(r.Context(), chi.URLParam(r, "projectId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	return decodeJSONValue(w, r, target, false)
}

func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, target any) error {
	return decodeJSONValue(w, r, target, true)
}

func decodeJSONValue(w http.ResponseWriter, r *http.Request, target any, optional bool) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxProjectRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if optional && errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("decode request: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return fmt.Errorf("decode request: multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode request trailing data: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
