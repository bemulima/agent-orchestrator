package repository

import (
	"context"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type TelegramRepository interface {
	BeginUpdate(context.Context, domain.TelegramUpdateReceipt) (bool, error)
	FinishUpdate(context.Context, int64, string) error
	GetPollOffset(context.Context, string) (int64, error)
	SavePollOffset(context.Context, string, int64) error
	SaveCallback(context.Context, domain.TelegramCallbackGrant) error
	ConsumeCallback(context.Context, string, int64, int64, time.Time) (domain.TelegramCallbackGrant, error)
}

type TelegramGateway interface {
	GetUpdates(context.Context, int64, int) ([]domain.TelegramUpdate, error)
	SendMessage(context.Context, domain.TelegramOutgoingMessage) error
	AnswerCallback(context.Context, string, string, bool) error
	SetWebhook(context.Context, string, string) error
	DeleteWebhook(context.Context, bool) error
}

type TelegramUpdateProcessor interface {
	Handle(context.Context, domain.TelegramUpdate, string) error
}
