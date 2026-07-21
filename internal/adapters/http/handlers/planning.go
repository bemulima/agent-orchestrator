package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	planninguc "github.com/bemulima/agent-orchestrator/internal/usecase/planning"
)

type createCommandUseCase interface {
	Handle(context.Context, planninguc.CreateCommandInput) (domain.Command, error)
}

type getCommandUseCase interface {
	Handle(context.Context, string) (domain.Command, error)
}

type createPlanUseCase interface {
	Handle(context.Context, string, domain.PlanRequest) (domain.PlanBundle, error)
}

type getPlanUseCase interface {
	Handle(context.Context, string) (domain.PlanBundle, error)
}

type decidePlanUseCase interface {
	Handle(context.Context, planninguc.DecidePlanInput) (domain.PlanBundle, error)
}

type startPlanUseCase interface {
	Handle(context.Context, string) (domain.PlanRun, error)
}

type getRunUseCase interface {
	Handle(context.Context, string) (domain.PlanRun, error)
}

type controlRunUseCase interface {
	Handle(context.Context, planninguc.ControlRunInput) (domain.PlanRun, error)
}

type getTaskUseCase interface {
	Handle(context.Context, string) (domain.Task, error)
}

type cancelTaskUseCase interface {
	Handle(context.Context, string) (domain.Task, error)
}

type getAttemptsUseCase interface {
	Handle(context.Context, string) ([]domain.TaskAttempt, error)
}

type getArtifactsUseCase interface {
	Handle(context.Context, string) ([]domain.Artifact, error)
}

type retryTaskUseCase interface {
	Handle(context.Context, string) (domain.Task, error)
}

type PlanningHandler struct {
	CreateCommand createCommandUseCase
	GetCommand    getCommandUseCase
	CreatePlan    createPlanUseCase
	GetPlan       getPlanUseCase
	ApprovePlan   decidePlanUseCase
	RejectPlan    decidePlanUseCase
	StartPlan     startPlanUseCase
	GetRun        getRunUseCase
	ControlRun    controlRunUseCase
	GetTask       getTaskUseCase
	CancelTask    cancelTaskUseCase
	GetAttempts   getAttemptsUseCase
	GetArtifacts  getArtifactsUseCase
	RetryTask     retryTaskUseCase
}

type createCommandRequest struct {
	Text           string  `json:"text"`
	SourceUserID   *string `json:"source_user_id,omitempty"`
	IdempotencyKey string  `json:"idempotency_key,omitempty"`
}

func (h PlanningHandler) CreateCommandRequest(w http.ResponseWriter, r *http.Request) {
	var request createCommandRequest
	if err := decodeJSON(w, r, &request); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if request.IdempotencyKey == "" {
		request.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	command, err := h.CreateCommand.Handle(r.Context(), planninguc.CreateCommandInput{
		Source: domain.CommandSourceAPI, SourceUserID: request.SourceUserID,
		Text: request.Text, IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, command)
}

func (h PlanningHandler) GetCommandRequest(w http.ResponseWriter, r *http.Request) {
	command, err := h.GetCommand.Handle(r.Context(), chi.URLParam(r, "commandId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, command)
}

func (h PlanningHandler) PlanCommand(w http.ResponseWriter, r *http.Request) {
	var request domain.PlanRequest
	if err := decodeOptionalJSON(w, r, &request); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	plan, err := h.CreatePlan.Handle(r.Context(), chi.URLParam(r, "commandId"), request)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, plan)
}

func (h PlanningHandler) GetPlanRequest(w http.ResponseWriter, r *http.Request) {
	plan, err := h.GetPlan.Handle(r.Context(), chi.URLParam(r, "planId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (h PlanningHandler) GetPlanTasks(w http.ResponseWriter, r *http.Request) {
	plan, err := h.GetPlan.Handle(r.Context(), chi.URLParam(r, "planId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"plan_id": plan.Plan.ID, "tasks": plan.Tasks, "dependencies": plan.Dependencies,
	})
}

func (h PlanningHandler) ApprovePlanRequest(w http.ResponseWriter, r *http.Request) {
	h.decidePlan(w, r, h.ApprovePlan)
}

func (h PlanningHandler) RejectPlanRequest(w http.ResponseWriter, r *http.Request) {
	h.decidePlan(w, r, h.RejectPlan)
}

func (h PlanningHandler) decidePlan(w http.ResponseWriter, r *http.Request, useCase decidePlanUseCase) {
	var input planninguc.DecidePlanInput
	if err := decodeOptionalJSON(w, r, &input); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	input.PlanID = chi.URLParam(r, "planId")
	plan, err := useCase.Handle(r.Context(), input)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (h PlanningHandler) RunPlan(w http.ResponseWriter, r *http.Request) {
	run, err := h.StartPlan.Handle(r.Context(), chi.URLParam(r, "planId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (h PlanningHandler) GetRunRequest(w http.ResponseWriter, r *http.Request) {
	run, err := h.GetRun.Handle(r.Context(), chi.URLParam(r, "runId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (h PlanningHandler) PauseRun(w http.ResponseWriter, r *http.Request) {
	h.controlRun(w, r, domain.RunControlPause)
}

func (h PlanningHandler) ResumeRun(w http.ResponseWriter, r *http.Request) {
	h.controlRun(w, r, domain.RunControlResume)
}

func (h PlanningHandler) CancelRun(w http.ResponseWriter, r *http.Request) {
	h.controlRun(w, r, domain.RunControlCancel)
}

func (h PlanningHandler) controlRun(w http.ResponseWriter, r *http.Request, action domain.RunControlAction) {
	run, err := h.ControlRun.Handle(r.Context(), planninguc.ControlRunInput{
		RunID: chi.URLParam(r, "runId"), Action: action,
	})
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (h PlanningHandler) GetTaskRequest(w http.ResponseWriter, r *http.Request) {
	task, err := h.GetTask.Handle(r.Context(), chi.URLParam(r, "taskId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h PlanningHandler) CancelTaskRequest(w http.ResponseWriter, r *http.Request) {
	task, err := h.CancelTask.Handle(r.Context(), chi.URLParam(r, "taskId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}

func (h PlanningHandler) GetTaskAttempts(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	attempts, err := h.GetAttempts.Handle(r.Context(), taskID)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task_id": taskID, "attempts": attempts})
}

func (h PlanningHandler) GetTaskArtifacts(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	artifacts, err := h.GetArtifacts.Handle(r.Context(), taskID)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task_id": taskID, "artifacts": artifacts})
}

func (h PlanningHandler) RetryTaskRequest(w http.ResponseWriter, r *http.Request) {
	task, err := h.RetryTask.Handle(r.Context(), chi.URLParam(r, "taskId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}
