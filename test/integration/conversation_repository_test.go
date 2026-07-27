//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"

	pgadapter "github.com/bemulima/agent-orchestrator/internal/adapters/postgres"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestConversationRepositoryPersistsTurnThreadAndDecision(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	ctx := context.Background()
	repository := pgadapter.ConversationRepoPG{Pool: pool}
	conversation, err := repository.CreateConversation(ctx, domain.Conversation{Title: "Integration control", ScopeType: domain.ConversationScopeWorkspace})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_event WHERE payload->>'conversation_id' = $1 OR resource_id = $1`, conversation.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM conversation WHERE id = $1`, conversation.ID)
	}()
	empty, err := repository.GetConversation(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Messages == nil || empty.Proposals == nil || len(empty.Messages) != 0 || len(empty.Proposals) != 0 {
		t.Fatalf("empty conversation collections = messages:%#v proposals:%#v", empty.Messages, empty.Proposals)
	}
	_, pending, err := repository.BeginConversationTurn(ctx, conversation.ID, "Что выполняется?")
	if err != nil {
		t.Fatal(err)
	}
	threadID := "operator-" + uuid.NewString()
	if attached, err := repository.AttachConversationThread(ctx, conversation.ID, threadID); err != nil || attached.AgentThreadID == nil {
		t.Fatalf("AttachConversationThread() = %#v, %v", attached, err)
	}
	resourceID := uuid.NewString()
	detail, err := repository.CompleteConversationTurn(ctx, pending.ID, "Активных задач нет.", []domain.ResourceReference{}, []domain.ActionProposal{{
		Action: "pause", ResourceType: "run", ResourceID: resourceID, Title: "Pause", Description: "Owner gate", RiskLevel: domain.RiskLevelMedium,
	}})
	if err != nil || len(detail.Messages) != 2 || len(detail.Proposals) != 1 {
		t.Fatalf("CompleteConversationTurn() = %#v, %v", detail, err)
	}
	proposal, err := repository.DecideActionProposal(ctx, detail.Proposals[0].ID, domain.ActionProposalRejected)
	if err != nil || proposal.Status != domain.ActionProposalRejected {
		t.Fatalf("DecideActionProposal() = %#v, %v", proposal, err)
	}
}
