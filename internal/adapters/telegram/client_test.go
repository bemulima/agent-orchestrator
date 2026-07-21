package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

const testBotToken = "123456:abcdefghijklmnopqrstuvwxyzABCDE"

func TestClient_UsesBoundedBotAPIRequests(t *testing.T) {
	requests := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		body["path"] = r.URL.Path
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			_, _ = io.WriteString(w, `{"ok":true,"result":[{"update_id":42,"message":{"message_id":7,"from":{"id":101,"is_bot":false},"chat":{"id":-303},"text":"/help"}}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
	}))
	defer server.Close()
	client, err := NewClient(testBotToken, server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	updates, err := client.GetUpdates(context.Background(), 41, 30)
	if err != nil || len(updates) != 1 || updates[0].UpdateID != 42 {
		t.Fatalf("GetUpdates() = %#v, %v", updates, err)
	}
	request := <-requests
	if request["offset"] != float64(41) || request["timeout"] != float64(30) ||
		!strings.HasSuffix(request["path"].(string), "/getUpdates") {
		t.Fatalf("getUpdates request = %#v", request)
	}
	message := domain.TelegramOutgoingMessage{ChatID: -303, Text: "ready", ReplyMarkup: &domain.TelegramInlineKeyboard{
		InlineKeyboard: [][]domain.TelegramInlineButton{{{Text: "Подтвердить", CallbackData: "tg:opaque"}}},
	}}
	if err := client.SendMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if request := <-requests; request["chat_id"] != float64(-303) || request["text"] != "ready" {
		t.Fatalf("sendMessage request = %#v", request)
	}
	if err := client.AnswerCallback(context.Background(), "callback-id", "Готово.", false); err != nil {
		t.Fatal(err)
	}
	if request := <-requests; request["callback_query_id"] != "callback-id" {
		t.Fatalf("answerCallbackQuery request = %#v", request)
	}
}

func TestClient_SetWebhookUsesOfficialSecretHeaderConfiguration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["url"] != "https://orchestrator.example.test/api/v1/integrations/telegram/webhook" ||
			body["secret_token"] != "valid_webhook_secret_123" {
			t.Errorf("setWebhook body = %#v", body)
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
	}))
	defer server.Close()
	client, err := NewClient(testBotToken, server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetWebhook(context.Background(),
		"https://orchestrator.example.test/api/v1/integrations/telegram/webhook", "valid_webhook_secret_123"); err != nil {
		t.Fatal(err)
	}
}

func TestClient_RejectsOversizedPayloadsAndNeverLeaksTokenInErrors(t *testing.T) {
	client, err := NewClient(testBotToken, "https://api.telegram.invalid", &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network failure")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SendMessage(context.Background(), domain.TelegramOutgoingMessage{
		ChatID: 1, Text: strings.Repeat("x", maxMessageBytes+1),
	}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("oversized SendMessage error = %v", err)
	}
	err = client.SendMessage(context.Background(), domain.TelegramOutgoingMessage{ChatID: 1, Text: "hello"})
	if err == nil || strings.Contains(err.Error(), testBotToken) || strings.Contains(err.Error(), "api.telegram.invalid") {
		t.Fatalf("request error leaked URL/token: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
