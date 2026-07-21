package domain

import "time"

const (
	TelegramUpdateStatusReceived  = "received"
	TelegramUpdateStatusProcessed = "processed"
	TelegramUpdateStatusIgnored   = "ignored"
	TelegramUpdateStatusFailed    = "failed"

	TelegramCallbackStatusPending   = "pending"
	TelegramCallbackStatusConsumed  = "consumed"
	TelegramCallbackStatusExpired   = "expired"
	TelegramCallbackStatusCancelled = "cancelled"
)

type TelegramActor struct {
	ID       int64  `json:"id"`
	IsBot    bool   `json:"is_bot"`
	Username string `json:"username,omitempty"`
}

type TelegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type,omitempty"`
}

type TelegramMessage struct {
	MessageID int64         `json:"message_id"`
	From      TelegramActor `json:"from"`
	Chat      TelegramChat  `json:"chat"`
	Text      string        `json:"text,omitempty"`
}

type TelegramCallbackQuery struct {
	ID      string           `json:"id"`
	From    TelegramActor    `json:"from"`
	Message *TelegramMessage `json:"message,omitempty"`
	Data    string           `json:"data,omitempty"`
}

type TelegramUpdate struct {
	UpdateID      int64                  `json:"update_id"`
	Message       *TelegramMessage       `json:"message,omitempty"`
	CallbackQuery *TelegramCallbackQuery `json:"callback_query,omitempty"`
}

type TelegramInlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type TelegramInlineKeyboard struct {
	InlineKeyboard [][]TelegramInlineButton `json:"inline_keyboard"`
}

type TelegramOutgoingMessage struct {
	ChatID      int64                   `json:"chat_id"`
	Text        string                  `json:"text"`
	ReplyMarkup *TelegramInlineKeyboard `json:"reply_markup,omitempty"`
}

type TelegramUpdateReceipt struct {
	UpdateID       int64
	Source         string
	Checksum       string
	TelegramUserID *int64
	TelegramChatID *int64
	Status         string
	ReceivedAt     time.Time
}

type TelegramCallbackGrant struct {
	ID             string
	TokenHash      string
	Action         string
	ResourceType   string
	ResourceID     string
	TelegramUserID int64
	TelegramChatID int64
	Status         string
	ExpiresAt      time.Time
	ConsumedAt     *time.Time
	CreatedAt      time.Time
}
