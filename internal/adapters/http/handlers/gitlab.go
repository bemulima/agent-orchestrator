package handlers

import (
	"context"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	gitlabuc "github.com/bemulima/agent-orchestrator/internal/usecase/gitlab"
)

const maxGitLabWebhookBodyBytes = 1 << 20

type syncGitLabUseCase interface {
	Handle(context.Context, string) (domain.GitLabSyncResult, error)
}

type listGitLabLinksUseCase interface {
	Handle(context.Context, string) ([]domain.GitLabLink, error)
}

type processGitLabWebhookUseCase interface {
	Handle(context.Context, gitlabuc.WebhookInput) (domain.GitLabWebhookResult, error)
}

type GitLabHandler struct {
	Sync    syncGitLabUseCase
	Links   listGitLabLinksUseCase
	Webhook processGitLabWebhookUseCase
}

func (h GitLabHandler) SyncPlan(w http.ResponseWriter, r *http.Request) {
	result, err := h.Sync.Handle(r.Context(), chi.URLParam(r, "planId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h GitLabHandler) ListPlanLinks(w http.ResponseWriter, r *http.Request) {
	planID := chi.URLParam(r, "planId")
	links, err := h.Links.Handle(r.Context(), planID)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan_id": planID, "links": links})
}

func (h GitLabHandler) ReceiveWebhook(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxGitLabWebhookBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_webhook", "webhook body is invalid or too large")
		return
	}
	eventUUID := strings.TrimSpace(r.Header.Get("X-Gitlab-Event-UUID"))
	messageID := strings.TrimSpace(r.Header.Get("webhook-id"))
	if messageID != "" {
		eventUUID = messageID
	}
	if eventUUID == "" {
		eventUUID = strings.TrimSpace(r.Header.Get("X-Gitlab-Webhook-UUID"))
	}
	result, err := h.Webhook.Handle(r.Context(), gitlabuc.WebhookInput{
		Token: r.Header.Get("X-Gitlab-Token"), MessageID: messageID,
		Timestamp: r.Header.Get("webhook-timestamp"), Signature: r.Header.Get("webhook-signature"),
		EventUUID: eventUUID,
		EventType: r.Header.Get("X-Gitlab-Event"), Body: body,
	})
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}
