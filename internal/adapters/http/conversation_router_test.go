package http

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/adapters/http/handlers"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	conversationuc "github.com/bemulima/agent-orchestrator/internal/usecase/conversation"
	healthuc "github.com/bemulima/agent-orchestrator/internal/usecase/health"
)

type conversationServiceFake struct{}

func (conversationServiceFake) Create(context.Context, conversationuc.CreateInput) (domain.Conversation, error) {
	return domain.Conversation{ID: "conversation-id"}, nil
}
func (conversationServiceFake) List(context.Context, int) ([]domain.Conversation, error) {
	return []domain.Conversation{}, nil
}
func (conversationServiceFake) Get(context.Context, string) (domain.ConversationDetail, error) {
	return domain.ConversationDetail{}, nil
}
func (conversationServiceFake) Send(context.Context, string, conversationuc.SendInput) (domain.ConversationDetail, error) {
	return domain.ConversationDetail{}, nil
}
func (conversationServiceFake) DecideProposal(context.Context, string, domain.ActionProposalStatus) (domain.ActionProposal, error) {
	return domain.ActionProposal{}, nil
}

func TestRouterConversationAPI(t *testing.T) {
	router := NewRouter(RouterDependencies{
		HealthHandler:       handlers.HealthHandler{Readiness: healthuc.CheckReadiness{}},
		ConversationHandler: &handlers.ConversationHandler{Service: conversationServiceFake{}},
	})
	tests := []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/api/v1/conversations", "", http.StatusOK},
		{http.MethodPost, "/api/v1/conversations", `{"title":"Control","scope_type":"workspace"}`, http.StatusCreated},
		{http.MethodGet, "/api/v1/conversations/conversation-id", "", http.StatusOK},
		{http.MethodPost, "/api/v1/conversations/conversation-id/messages", `{"content":"What is running?"}`, http.StatusOK},
		{http.MethodPost, "/api/v1/action-proposals/proposal-id/decision", `{"status":"rejected"}`, http.StatusOK},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(test.method, test.path, bytes.NewBufferString(test.body)))
		if response.Code != test.status {
			t.Fatalf("%s %s = %d, body=%s", test.method, test.path, response.Code, response.Body.String())
		}
	}
}
