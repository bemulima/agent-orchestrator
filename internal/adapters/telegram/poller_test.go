package telegram

import (
	"context"
	"testing"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type pollStateStub struct{ offset int64 }

func (*pollStateStub) BeginUpdate(context.Context, domain.TelegramUpdateReceipt) (bool, error) {
	return true, nil
}
func (*pollStateStub) FinishUpdate(context.Context, int64, string) error { return nil }
func (s *pollStateStub) GetPollOffset(context.Context, string) (int64, error) {
	return s.offset, nil
}
func (s *pollStateStub) SavePollOffset(_ context.Context, _ string, offset int64) error {
	s.offset = offset
	return nil
}
func (*pollStateStub) SaveCallback(context.Context, domain.TelegramCallbackGrant) error { return nil }
func (*pollStateStub) ConsumeCallback(context.Context, string, int64, int64, time.Time) (domain.TelegramCallbackGrant, error) {
	return domain.TelegramCallbackGrant{}, nil
}

type pollGatewayStub struct {
	updates []domain.TelegramUpdate
	calls   int
}

func (s *pollGatewayStub) GetUpdates(ctx context.Context, _ int64, _ int) ([]domain.TelegramUpdate, error) {
	s.calls++
	if s.calls == 1 {
		return s.updates, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}
func (*pollGatewayStub) SendMessage(context.Context, domain.TelegramOutgoingMessage) error {
	return nil
}
func (*pollGatewayStub) AnswerCallback(context.Context, string, string, bool) error { return nil }
func (*pollGatewayStub) SetWebhook(context.Context, string, string) error           { return nil }
func (*pollGatewayStub) DeleteWebhook(context.Context, bool) error                  { return nil }

type pollProcessorStub struct {
	ids    []int64
	cancel context.CancelFunc
}

func (s *pollProcessorStub) Handle(_ context.Context, update domain.TelegramUpdate, source string) error {
	if source != "polling" {
		panic("unexpected source")
	}
	s.ids = append(s.ids, update.UpdateID)
	if len(s.ids) == 2 {
		s.cancel()
	}
	return nil
}

func TestPollerProcessesUpdatesInOrderAndPersistsNextOffset(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	state := &pollStateStub{offset: 40}
	gateway := &pollGatewayStub{updates: []domain.TelegramUpdate{{UpdateID: 42}, {UpdateID: 41}}}
	processor := &pollProcessorStub{cancel: cancel}
	err := (Poller{
		Gateway: gateway, Processor: processor, State: state,
		BotToken: testBotToken, Timeout: 1,
	}).Run(ctx)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(processor.ids) != 2 || processor.ids[0] != 41 || processor.ids[1] != 42 || state.offset != 43 {
		t.Fatalf("processed IDs = %#v, offset = %d", processor.ids, state.offset)
	}
}
