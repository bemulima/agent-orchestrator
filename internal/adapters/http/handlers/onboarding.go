package handlers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	onboardinguc "github.com/bemulima/agent-orchestrator/internal/usecase/onboarding"
)

type prepareOnboardingUseCase interface {
	Handle(context.Context, onboardinguc.PrepareInput) (domain.OnboardingRun, error)
}

type getOnboardingUseCase interface {
	Handle(context.Context, string) (domain.OnboardingRun, error)
}

type approveOnboardingUseCase interface {
	Handle(context.Context, onboardinguc.DecideInput) (domain.OnboardingRun, error)
}

type rejectOnboardingUseCase interface {
	Handle(context.Context, onboardinguc.DecideInput) (domain.OnboardingRun, error)
}

type applyOnboardingUseCase interface {
	Handle(context.Context, onboardinguc.ApplyInput) (onboardinguc.ApplyOutput, error)
}

type OnboardingHandler struct {
	Prepare prepareOnboardingUseCase
	Get     getOnboardingUseCase
	Approve approveOnboardingUseCase
	Reject  rejectOnboardingUseCase
	Apply   applyOnboardingUseCase
}

func (h OnboardingHandler) PrepareProject(w http.ResponseWriter, r *http.Request) {
	input := onboardinguc.PrepareInput{ProjectID: chi.URLParam(r, "projectId")}
	if err := decodeJSON(w, r, &input); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	run, err := h.Prepare.Handle(r.Context(), input)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

func (h OnboardingHandler) GetRun(w http.ResponseWriter, r *http.Request) {
	run, err := h.Get.Handle(r.Context(), chi.URLParam(r, "runId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (h OnboardingHandler) GetDiff(w http.ResponseWriter, r *http.Request) {
	run, err := h.Get.Handle(r.Context(), chi.URLParam(r, "runId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/x-diff; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(run.UnifiedDiff))
}

func (h OnboardingHandler) ApproveRun(w http.ResponseWriter, r *http.Request) {
	input := onboardinguc.DecideInput{RunID: chi.URLParam(r, "runId")}
	if err := decodeJSON(w, r, &input); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	run, err := h.Approve.Handle(r.Context(), input)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (h OnboardingHandler) RejectRun(w http.ResponseWriter, r *http.Request) {
	input := onboardinguc.DecideInput{RunID: chi.URLParam(r, "runId")}
	if err := decodeJSON(w, r, &input); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	run, err := h.Reject.Handle(r.Context(), input)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (h OnboardingHandler) ApplyRun(w http.ResponseWriter, r *http.Request) {
	input := onboardinguc.ApplyInput{RunID: chi.URLParam(r, "runId")}
	if err := decodeJSON(w, r, &input); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	result, err := h.Apply.Handle(r.Context(), input)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
