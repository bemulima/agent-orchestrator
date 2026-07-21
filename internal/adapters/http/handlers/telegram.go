package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

const maxTelegramWebhookBodyBytes = 256 << 10

type processTelegramUpdate interface {
	Handle(context.Context, domain.TelegramUpdate, string) error
}

type TelegramHandler struct {
	Processor processTelegramUpdate
	Secret    string
}

func (h TelegramHandler) ReceiveWebhook(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
	provided := strings.TrimSpace(r.Header.Get("X-Telegram-Bot-Api-Secret-Token"))
	expected := strings.TrimSpace(h.Secret)
	if expected == "" || len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		WriteError(w, http.StatusForbidden, "forbidden", "operation is forbidden")
		return
	}
	if h.Processor == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured", "Telegram integration is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTelegramWebhookBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil || len(body) == 0 || !json.Valid(body) {
		WriteError(w, http.StatusBadRequest, "invalid_webhook", "webhook body is invalid or too large")
		return
	}
	var update domain.TelegramUpdate
	if json.Unmarshal(body, &update) != nil {
		WriteError(w, http.StatusBadRequest, "invalid_webhook", "webhook body is invalid")
		return
	}
	if err := h.Processor.Handle(r.Context(), update, "webhook"); err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}
