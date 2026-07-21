package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type Poller struct {
	Gateway   repository.TelegramGateway
	Processor repository.TelegramUpdateProcessor
	State     repository.TelegramRepository
	BotToken  string
	Timeout   int
}

func (p Poller) Run(ctx context.Context) error {
	if p.Gateway == nil || p.Processor == nil || p.State == nil || p.Timeout < 1 || p.Timeout > 50 {
		return fmt.Errorf("incomplete Telegram poller configuration")
	}
	hash := sha256.Sum256([]byte(p.BotToken))
	botKey := hex.EncodeToString(hash[:])
	offset, err := p.State.GetPollOffset(ctx, botKey)
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		updates, err := p.Gateway.GetUpdates(ctx, offset, p.Timeout)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("poll Telegram updates: %w", err)
		}
		sort.Slice(updates, func(i, j int) bool { return updates[i].UpdateID < updates[j].UpdateID })
		for _, update := range updates {
			if update.UpdateID < offset {
				continue
			}
			if err := p.Processor.Handle(ctx, update, "polling"); err != nil {
				return fmt.Errorf("process Telegram update %d: %w", update.UpdateID, err)
			}
			offset = update.UpdateID + 1
			if err := p.State.SavePollOffset(ctx, botKey, offset); err != nil {
				return err
			}
		}
	}
}
