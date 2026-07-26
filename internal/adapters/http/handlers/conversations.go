package handlers

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	conversationuc "github.com/bemulima/agent-orchestrator/internal/usecase/conversation"
)

type conversationService interface {
	Create(context.Context, conversationuc.CreateInput) (domain.Conversation, error)
	List(context.Context, int) ([]domain.Conversation, error)
	Get(context.Context, string) (domain.ConversationDetail, error)
	Send(context.Context, string, conversationuc.SendInput) (domain.ConversationDetail, error)
	DecideProposal(context.Context, string, domain.ActionProposalStatus) (domain.ActionProposal, error)
}

type ConversationHandler struct{ Service conversationService }

func (h ConversationHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input conversationuc.CreateInput
	if err := decodeJSON(w, r, &input); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	result, err := h.Service.Create(r.Context(), input)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (h ConversationHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			WriteDomainError(w, domain.ErrValidation)
			return
		}
		limit = value
	}
	result, err := h.Service.List(r.Context(), limit)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": result})
}

func (h ConversationHandler) Get(w http.ResponseWriter, r *http.Request) {
	result, err := h.Service.Get(r.Context(), chi.URLParam(r, "conversationId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h ConversationHandler) Send(w http.ResponseWriter, r *http.Request) {
	var input conversationuc.SendInput
	if err := decodeJSON(w, r, &input); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	result, err := h.Service.Send(r.Context(), chi.URLParam(r, "conversationId"), input)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h ConversationHandler) DecideProposal(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status domain.ActionProposalStatus `json:"status"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	result, err := h.Service.DecideProposal(r.Context(), chi.URLParam(r, "proposalId"), input.Status)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
