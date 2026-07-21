package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type telegramProcessorStub struct {
	updates []domain.TelegramUpdate
	sources []string
}

func (s *telegramProcessorStub) Handle(_ context.Context, update domain.TelegramUpdate, source string) error {
	s.updates = append(s.updates, update)
	s.sources = append(s.sources, source)
	return nil
}

func TestTelegramHandler_RequiresSecretJSONAndBoundedBody(t *testing.T) {
	processor := &telegramProcessorStub{}
	handler := TelegramHandler{Processor: processor, Secret: "valid_webhook_secret_123"}
	payload := `{"update_id":42,"message":{"message_id":1,"from":{"id":101},"chat":{"id":-303},"text":"/help"}}`

	request := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/telegram/webhook", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ReceiveWebhook(response, request)
	if response.Code != http.StatusForbidden || len(processor.updates) != 0 {
		t.Fatalf("missing secret response = %d, updates = %d", response.Code, len(processor.updates))
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/integrations/telegram/webhook", strings.NewReader(payload))
	request.Header.Set("Content-Type", "text/plain")
	request.Header.Set("X-Telegram-Bot-Api-Secret-Token", "valid_webhook_secret_123")
	response = httptest.NewRecorder()
	handler.ReceiveWebhook(response, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong content type response = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/integrations/telegram/webhook",
		strings.NewReader(strings.Repeat("x", maxTelegramWebhookBodyBytes+1)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Telegram-Bot-Api-Secret-Token", "valid_webhook_secret_123")
	response = httptest.NewRecorder()
	handler.ReceiveWebhook(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized response = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/integrations/telegram/webhook", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("X-Telegram-Bot-Api-Secret-Token", "valid_webhook_secret_123")
	response = httptest.NewRecorder()
	handler.ReceiveWebhook(response, request)
	if response.Code != http.StatusAccepted || len(processor.updates) != 1 || processor.sources[0] != "webhook" ||
		processor.updates[0].UpdateID != 42 {
		t.Fatalf("valid webhook response = %d, updates = %#v, sources = %#v", response.Code, processor.updates, processor.sources)
	}
}
