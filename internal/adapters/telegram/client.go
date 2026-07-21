package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

const (
	defaultBaseURL      = "https://api.telegram.org"
	maxAPIResponseBytes = 1 << 20
	maxMessageBytes     = 4096
	maxCallbackBytes    = 64
)

var botTokenPattern = regexp.MustCompile(`^[A-Za-z0-9:_-]{16,256}$`)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func NewClient(token string, baseURL string, httpClient *http.Client) (*Client, error) {
	token = strings.TrimSpace(token)
	if !botTokenPattern.MatchString(token) {
		return nil, fmt.Errorf("invalid Telegram bot token: %w", domain.ErrValidation)
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultBaseURL
	}
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, fmt.Errorf("invalid Telegram API base URL: %w", domain.ErrValidation)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 65 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	return &Client{baseURL: parsed.String(), token: token, httpClient: httpClient}, nil
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout int) ([]domain.TelegramUpdate, error) {
	if offset < 0 || timeout < 1 || timeout > 50 {
		return nil, fmt.Errorf("invalid Telegram polling parameters: %w", domain.ErrValidation)
	}
	var updates []domain.TelegramUpdate
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset": offset, "limit": 50, "timeout": timeout,
		"allowed_updates": []string{"message", "callback_query"},
	}, &updates)
	if err != nil {
		return nil, err
	}
	for _, update := range updates {
		if update.UpdateID < 0 {
			return nil, fmt.Errorf("Telegram returned an invalid update: %w", domain.ErrValidation)
		}
	}
	return updates, nil
}

func (c *Client) SendMessage(ctx context.Context, message domain.TelegramOutgoingMessage) error {
	if err := validateOutgoingMessage(message); err != nil {
		return err
	}
	var ignored json.RawMessage
	return c.call(ctx, "sendMessage", message, &ignored)
}

func (c *Client) AnswerCallback(ctx context.Context, callbackID, text string, alert bool) error {
	callbackID = strings.TrimSpace(callbackID)
	if callbackID == "" || len(callbackID) > 256 || len(text) > 200 || !utf8.ValidString(text) {
		return fmt.Errorf("invalid Telegram callback answer: %w", domain.ErrValidation)
	}
	var ignored json.RawMessage
	return c.call(ctx, "answerCallbackQuery", map[string]any{
		"callback_query_id": callbackID, "text": text, "show_alert": alert,
	}, &ignored)
}

func (c *Client) SetWebhook(ctx context.Context, webhookURL, secret string) error {
	parsed, err := url.Parse(strings.TrimSpace(webhookURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("invalid Telegram webhook URL: %w", domain.ErrValidation)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{16,256}$`).MatchString(secret) {
		return fmt.Errorf("invalid Telegram webhook secret: %w", domain.ErrValidation)
	}
	var ignored json.RawMessage
	return c.call(ctx, "setWebhook", map[string]any{
		"url": parsed.String(), "secret_token": secret,
		"allowed_updates": []string{"message", "callback_query"},
	}, &ignored)
}

func (c *Client) DeleteWebhook(ctx context.Context, dropPendingUpdates bool) error {
	var ignored json.RawMessage
	return c.call(ctx, "deleteWebhook", map[string]any{"drop_pending_updates": dropPendingUpdates}, &ignored)
}

func (c *Client) call(ctx context.Context, method string, payload any, result any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Telegram request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create Telegram request: %w", domain.ErrValidation)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		// net/http errors include the request URL, which contains the bot token.
		return fmt.Errorf("Telegram API request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxAPIResponseBytes))
		return fmt.Errorf("Telegram API returned HTTP %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, maxAPIResponseBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil || len(raw) > maxAPIResponseBytes {
		return fmt.Errorf("Telegram API response is invalid")
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(raw, &envelope) != nil || !envelope.OK {
		return fmt.Errorf("Telegram API rejected the request")
	}
	if result == nil || len(envelope.Result) == 0 || string(envelope.Result) == "true" {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return fmt.Errorf("Telegram API result is invalid")
	}
	return nil
}

func validateOutgoingMessage(message domain.TelegramOutgoingMessage) error {
	if message.ChatID == 0 || strings.TrimSpace(message.Text) == "" || len(message.Text) > maxMessageBytes ||
		!utf8.ValidString(message.Text) {
		return fmt.Errorf("invalid Telegram message: %w", domain.ErrValidation)
	}
	if message.ReplyMarkup == nil {
		return nil
	}
	if len(message.ReplyMarkup.InlineKeyboard) == 0 || len(message.ReplyMarkup.InlineKeyboard) > 8 {
		return fmt.Errorf("invalid Telegram keyboard: %w", domain.ErrValidation)
	}
	for _, row := range message.ReplyMarkup.InlineKeyboard {
		if len(row) == 0 || len(row) > 8 {
			return fmt.Errorf("invalid Telegram keyboard row: %w", domain.ErrValidation)
		}
		for _, button := range row {
			if strings.TrimSpace(button.Text) == "" || len(button.Text) > 64 ||
				strings.TrimSpace(button.CallbackData) == "" || len(button.CallbackData) > maxCallbackBytes {
				return fmt.Errorf("invalid Telegram keyboard button: %w", domain.ErrValidation)
			}
		}
	}
	return nil
}
