package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type ConversationRepoPG struct{ Pool *pgxpool.Pool }

func (r ConversationRepoPG) CreateConversation(ctx context.Context, value domain.Conversation) (domain.Conversation, error) {
	value.Title = strings.TrimSpace(value.Title)
	if value.Title == "" || !validConversationScope(value.ScopeType, value.ScopeID) {
		return domain.Conversation{}, fmt.Errorf("title and valid conversation scope are required: %w", domain.ErrValidation)
	}
	result, err := scanConversation(r.Pool.QueryRow(ctx, `
INSERT INTO conversation (title, scope_type, scope_id)
VALUES ($1, $2, $3)
RETURNING `+conversationColumns+`, 0`, value.Title, value.ScopeType, value.ScopeID))
	if err != nil {
		return domain.Conversation{}, mapPlanningError(err)
	}
	return result, nil
}

func (r ConversationRepoPG) ListConversations(ctx context.Context, limit int) ([]domain.Conversation, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("conversation limit must be between 1 and 100: %w", domain.ErrValidation)
	}
	rows, err := r.Pool.Query(ctx, `
SELECT `+conversationColumns+`, (SELECT count(*) FROM conversation_message m WHERE m.conversation_id = c.id)
FROM conversation c ORDER BY updated_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.Conversation, 0, limit)
	for rows.Next() {
		item, scanErr := scanConversation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r ConversationRepoPG) GetConversation(ctx context.Context, id string) (domain.ConversationDetail, error) {
	conversation, err := scanConversation(r.Pool.QueryRow(ctx, `
SELECT `+conversationColumns+`, (SELECT count(*) FROM conversation_message m WHERE m.conversation_id = c.id)
FROM conversation c WHERE id = $1`, id))
	if err != nil {
		return domain.ConversationDetail{}, mapPlanningError(err)
	}
	result := domain.ConversationDetail{
		Conversation: conversation,
		Messages:     []domain.ConversationMessage{},
		Proposals:    []domain.ActionProposal{},
	}
	rows, err := r.Pool.Query(ctx, `
SELECT `+conversationMessageColumns+` FROM conversation_message
WHERE conversation_id = $1 ORDER BY created_at, id LIMIT 400`, id)
	if err != nil {
		return domain.ConversationDetail{}, err
	}
	for rows.Next() {
		message, scanErr := scanConversationMessage(rows)
		if scanErr != nil {
			rows.Close()
			return domain.ConversationDetail{}, scanErr
		}
		result.Messages = append(result.Messages, message)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.ConversationDetail{}, err
	}
	rows.Close()
	proposalRows, err := r.Pool.Query(ctx, `
SELECT `+actionProposalColumns+` FROM action_proposal
WHERE conversation_id = $1 ORDER BY created_at, id LIMIT 200`, id)
	if err != nil {
		return domain.ConversationDetail{}, err
	}
	defer proposalRows.Close()
	for proposalRows.Next() {
		proposal, scanErr := scanActionProposal(proposalRows)
		if scanErr != nil {
			return domain.ConversationDetail{}, scanErr
		}
		result.Proposals = append(result.Proposals, proposal)
	}
	return result, proposalRows.Err()
}

func (r ConversationRepoPG) BeginConversationTurn(ctx context.Context, conversationID, content string) (domain.ConversationMessage, domain.ConversationMessage, error) {
	content = strings.TrimSpace(content)
	if content == "" || len(content) > 12000 {
		return domain.ConversationMessage{}, domain.ConversationMessage{}, fmt.Errorf("bounded owner message is required: %w", domain.ErrValidation)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.ConversationMessage{}, domain.ConversationMessage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT id FROM conversation WHERE id = $1 FOR UPDATE`, conversationID); err != nil {
		return domain.ConversationMessage{}, domain.ConversationMessage{}, mapPlanningError(err)
	}
	owner, err := scanConversationMessage(tx.QueryRow(ctx, `
INSERT INTO conversation_message (conversation_id, role, status, content, completed_at)
VALUES ($1, 'owner', 'completed', $2, now()) RETURNING `+conversationMessageColumns, conversationID, content))
	if err != nil {
		return domain.ConversationMessage{}, domain.ConversationMessage{}, mapPlanningError(err)
	}
	assistant, err := scanConversationMessage(tx.QueryRow(ctx, `
INSERT INTO conversation_message (conversation_id, role, status, content)
VALUES ($1, 'assistant', 'pending', '') RETURNING `+conversationMessageColumns, conversationID))
	if err != nil {
		return domain.ConversationMessage{}, domain.ConversationMessage{}, mapPlanningError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE conversation SET updated_at = now() WHERE id = $1`, conversationID); err != nil {
		return domain.ConversationMessage{}, domain.ConversationMessage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ConversationMessage{}, domain.ConversationMessage{}, err
	}
	return owner, assistant, nil
}

func (r ConversationRepoPG) AttachConversationThread(ctx context.Context, conversationID, threadID string) (domain.Conversation, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return domain.Conversation{}, fmt.Errorf("agent thread is required: %w", domain.ErrValidation)
	}
	value, err := scanConversation(r.Pool.QueryRow(ctx, `
UPDATE conversation SET agent_thread_id = $2, updated_at = now()
WHERE id = $1 AND (agent_thread_id IS NULL OR agent_thread_id = $2)
RETURNING `+conversationColumns+`, (SELECT count(*) FROM conversation_message m WHERE m.conversation_id = conversation.id)`, conversationID, threadID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Conversation{}, fmt.Errorf("conversation thread cannot be replaced: %w", domain.ErrConflict)
	}
	return value, mapPlanningError(err)
}

func (r ConversationRepoPG) CompleteConversationTurn(ctx context.Context, messageID, content string, references []domain.ResourceReference, proposals []domain.ActionProposal) (domain.ConversationDetail, error) {
	content = strings.TrimSpace(content)
	if content == "" || len(content) > 50000 || len(proposals) > 12 {
		return domain.ConversationDetail{}, fmt.Errorf("bounded assistant response is required: %w", domain.ErrValidation)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.ConversationDetail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rawReferences, err := json.Marshal(references)
	if err != nil {
		return domain.ConversationDetail{}, fmt.Errorf("encode conversation references: %w", err)
	}
	var conversationID string
	if err := tx.QueryRow(ctx, `
UPDATE conversation_message SET status = 'completed', content = $2, resource_references = $3, completed_at = now()
WHERE id = $1 AND role = 'assistant' AND status = 'pending'
RETURNING conversation_id`, messageID, content, rawReferences).Scan(&conversationID); err != nil {
		return domain.ConversationDetail{}, mapPlanningError(err)
	}
	for _, proposal := range proposals {
		if _, err := tx.Exec(ctx, `
INSERT INTO action_proposal (
    conversation_id, message_id, action, resource_type, resource_id,
    title, description, risk_level, fingerprint
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, conversationID, messageID,
			proposal.Action, proposal.ResourceType, proposal.ResourceID, proposal.Title,
			proposal.Description, proposal.RiskLevel, proposal.Fingerprint); err != nil {
			return domain.ConversationDetail{}, mapPlanningError(err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE conversation SET updated_at = now() WHERE id = $1`, conversationID); err != nil {
		return domain.ConversationDetail{}, err
	}
	if err := insertResourceAuditTx(ctx, tx, "conversation", "conversation.turn.completed", conversationID, map[string]any{"proposal_count": len(proposals)}); err != nil {
		return domain.ConversationDetail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ConversationDetail{}, err
	}
	return r.GetConversation(ctx, conversationID)
}

func (r ConversationRepoPG) FailConversationTurn(ctx context.Context, messageID, message string) error {
	message = strings.TrimSpace(message)
	if len(message) > 2000 {
		message = message[:2000]
	}
	command, err := r.Pool.Exec(ctx, `
UPDATE conversation_message SET status = 'failed', error = $2, completed_at = now()
WHERE id = $1 AND role = 'assistant' AND status = 'pending'`, messageID, message)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("pending assistant message not found: %w", domain.ErrInvalidStatus)
	}
	return nil
}

func (r ConversationRepoPG) DecideActionProposal(ctx context.Context, id string, status domain.ActionProposalStatus) (domain.ActionProposal, error) {
	if status != domain.ActionProposalConfirmed && status != domain.ActionProposalRejected {
		return domain.ActionProposal{}, fmt.Errorf("invalid proposal decision: %w", domain.ErrValidation)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.ActionProposal{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanActionProposal(tx.QueryRow(ctx, `SELECT `+actionProposalColumns+` FROM action_proposal WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return domain.ActionProposal{}, mapPlanningError(err)
	}
	if current.Status == status {
		if err := tx.Commit(ctx); err != nil {
			return domain.ActionProposal{}, err
		}
		return current, nil
	}
	if current.Status != domain.ActionProposalPending {
		return domain.ActionProposal{}, fmt.Errorf("proposal is already decided: %w", domain.ErrConflict)
	}
	current, err = scanActionProposal(tx.QueryRow(ctx, `
UPDATE action_proposal SET status = $2, decided_at = now() WHERE id = $1
RETURNING `+actionProposalColumns, id, status))
	if err != nil {
		return domain.ActionProposal{}, err
	}
	if err := insertResourceAuditTx(ctx, tx, "action_proposal", "action_proposal."+string(status), current.ID, map[string]any{
		"conversation_id": current.ConversationID, "action": current.Action,
		"resource_type": current.ResourceType, "resource_id": current.ResourceID,
	}); err != nil {
		return domain.ActionProposal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ActionProposal{}, err
	}
	return current, nil
}

func validConversationScope(scope domain.ConversationScope, id *string) bool {
	if scope == domain.ConversationScopeWorkspace {
		return id == nil
	}
	return id != nil && strings.TrimSpace(*id) != "" && (scope == domain.ConversationScopeProject || scope == domain.ConversationScopePlan || scope == domain.ConversationScopeRun || scope == domain.ConversationScopeTask)
}

const conversationColumns = `id, title, scope_type, scope_id, agent_thread_id, created_at, updated_at`
const conversationMessageColumns = `id, conversation_id, role, status, content, resource_references, error, created_at, completed_at`
const actionProposalColumns = `id, conversation_id, message_id, action, resource_type, resource_id, title, description, risk_level, fingerprint, status, created_at, decided_at`

func scanConversation(row rowScanner) (domain.Conversation, error) {
	var value domain.Conversation
	err := row.Scan(&value.ID, &value.Title, &value.ScopeType, &value.ScopeID, &value.AgentThreadID, &value.CreatedAt, &value.UpdatedAt, &value.MessageCount)
	return value, err
}

func scanConversationMessage(row rowScanner) (domain.ConversationMessage, error) {
	var value domain.ConversationMessage
	var references []byte
	err := row.Scan(&value.ID, &value.ConversationID, &value.Role, &value.Status, &value.Content, &references, &value.Error, &value.CreatedAt, &value.CompletedAt)
	if err == nil {
		err = json.Unmarshal(references, &value.References)
	}
	return value, err
}

func scanActionProposal(row rowScanner) (domain.ActionProposal, error) {
	var value domain.ActionProposal
	err := row.Scan(&value.ID, &value.ConversationID, &value.MessageID, &value.Action, &value.ResourceType, &value.ResourceID,
		&value.Title, &value.Description, &value.RiskLevel, &value.Fingerprint, &value.Status, &value.CreatedAt, &value.DecidedAt)
	return value, err
}
