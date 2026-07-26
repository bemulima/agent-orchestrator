package repository

import (
	"context"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type ConversationRepository interface {
	CreateConversation(context.Context, domain.Conversation) (domain.Conversation, error)
	ListConversations(context.Context, int) ([]domain.Conversation, error)
	GetConversation(context.Context, string) (domain.ConversationDetail, error)
	BeginConversationTurn(context.Context, string, string) (domain.ConversationMessage, domain.ConversationMessage, error)
	AttachConversationThread(context.Context, string, string) (domain.Conversation, error)
	CompleteConversationTurn(context.Context, string, string, []domain.ResourceReference, []domain.ActionProposal) (domain.ConversationDetail, error)
	FailConversationTurn(context.Context, string, string) error
	DecideActionProposal(context.Context, string, domain.ActionProposalStatus) (domain.ActionProposal, error)
}
