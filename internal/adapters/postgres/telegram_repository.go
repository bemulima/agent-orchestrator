package postgres

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

var sha256HexPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type TelegramRepoPG struct {
	Pool *pgxpool.Pool
}

func (r TelegramRepoPG) BeginUpdate(ctx context.Context, receipt domain.TelegramUpdateReceipt) (bool, error) {
	if receipt.UpdateID < 0 || !sha256HexPattern.MatchString(receipt.Checksum) ||
		(receipt.Source != "polling" && receipt.Source != "webhook") {
		return false, fmt.Errorf("invalid Telegram update receipt: %w", domain.ErrValidation)
	}
	if receipt.Status == "" {
		receipt.Status = domain.TelegramUpdateStatusReceived
	}
	if receipt.ReceivedAt.IsZero() {
		receipt.ReceivedAt = time.Now().UTC()
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin Telegram update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var updateID int64
	err = tx.QueryRow(ctx, `
INSERT INTO telegram_update (
    update_id, source, payload_checksum, telegram_user_id, telegram_chat_id, status, received_at
) VALUES ($1, $2, $3, $4, $5, 'received', $6)
ON CONFLICT (update_id) DO UPDATE SET
    source = EXCLUDED.source,
    telegram_user_id = EXCLUDED.telegram_user_id,
    telegram_chat_id = EXCLUDED.telegram_chat_id,
    status = 'received',
    received_at = EXCLUDED.received_at,
    processed_at = NULL
WHERE telegram_update.status = 'failed'
  AND telegram_update.payload_checksum = EXCLUDED.payload_checksum
RETURNING update_id`, receipt.UpdateID, receipt.Source, receipt.Checksum,
		receipt.TelegramUserID, receipt.TelegramChatID, receipt.ReceivedAt).Scan(&updateID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim Telegram update: %w", err)
	}
	if receipt.TelegramUserID != nil && receipt.TelegramChatID != nil {
		if _, err := tx.Exec(ctx, `
INSERT INTO telegram_user (telegram_user_id, telegram_chat_id, enabled, last_seen_at)
VALUES ($1, $2, true, $3)
ON CONFLICT (telegram_user_id) DO UPDATE SET
    telegram_chat_id = EXCLUDED.telegram_chat_id,
    enabled = true,
    last_seen_at = EXCLUDED.last_seen_at,
    updated_at = now()`, *receipt.TelegramUserID, *receipt.TelegramChatID, receipt.ReceivedAt); err != nil {
			return false, fmt.Errorf("record Telegram user: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit Telegram update: %w", err)
	}
	return true, nil
}

func (r TelegramRepoPG) FinishUpdate(ctx context.Context, updateID int64, status string) error {
	if status != domain.TelegramUpdateStatusProcessed && status != domain.TelegramUpdateStatusIgnored &&
		status != domain.TelegramUpdateStatusFailed {
		return fmt.Errorf("invalid Telegram update status: %w", domain.ErrValidation)
	}
	result, err := r.Pool.Exec(ctx, `
UPDATE telegram_update SET status = $2, processed_at = now() WHERE update_id = $1`, updateID, status)
	if err != nil {
		return fmt.Errorf("finish Telegram update: %w", err)
	}
	if result.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r TelegramRepoPG) GetPollOffset(ctx context.Context, botKey string) (int64, error) {
	if !sha256HexPattern.MatchString(strings.TrimSpace(botKey)) {
		return 0, fmt.Errorf("invalid Telegram bot key: %w", domain.ErrValidation)
	}
	var offset int64
	err := r.Pool.QueryRow(ctx, `SELECT next_offset FROM telegram_poll_state WHERE bot_key = $1`, botKey).Scan(&offset)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get Telegram poll offset: %w", err)
	}
	return offset, nil
}

func (r TelegramRepoPG) SavePollOffset(ctx context.Context, botKey string, offset int64) error {
	if !sha256HexPattern.MatchString(strings.TrimSpace(botKey)) || offset < 0 {
		return fmt.Errorf("invalid Telegram poll offset: %w", domain.ErrValidation)
	}
	_, err := r.Pool.Exec(ctx, `
INSERT INTO telegram_poll_state (bot_key, next_offset)
VALUES ($1, $2)
ON CONFLICT (bot_key) DO UPDATE SET
    next_offset = GREATEST(telegram_poll_state.next_offset, EXCLUDED.next_offset),
    updated_at = now()`, botKey, offset)
	if err != nil {
		return fmt.Errorf("save Telegram poll offset: %w", err)
	}
	return nil
}

func (r TelegramRepoPG) SaveCallback(ctx context.Context, grant domain.TelegramCallbackGrant) error {
	if _, err := uuid.Parse(grant.ResourceID); err != nil ||
		!sha256HexPattern.MatchString(grant.TokenHash) || grant.TelegramUserID <= 0 || grant.TelegramChatID == 0 ||
		!validTelegramCallbackAction(grant.Action) || !validTelegramResourceType(grant.ResourceType) {
		return fmt.Errorf("invalid Telegram callback grant: %w", domain.ErrValidation)
	}
	if grant.CreatedAt.IsZero() {
		grant.CreatedAt = time.Now().UTC()
	}
	if !grant.ExpiresAt.After(grant.CreatedAt) {
		return fmt.Errorf("Telegram callback expiry must be in the future: %w", domain.ErrValidation)
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin Telegram callback: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
UPDATE telegram_callback SET status = 'cancelled'
WHERE status = 'pending' AND action = $1 AND resource_type = $2 AND resource_id = $3
  AND telegram_user_id = $4 AND telegram_chat_id = $5`, grant.Action, grant.ResourceType,
		grant.ResourceID, grant.TelegramUserID, grant.TelegramChatID); err != nil {
		return fmt.Errorf("cancel prior Telegram callback: %w", err)
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO telegram_callback (
    token_hash, action, resource_type, resource_id, telegram_user_id,
    telegram_chat_id, status, expires_at, created_at
) VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, $8)
RETURNING id`, grant.TokenHash, grant.Action, grant.ResourceType, grant.ResourceID,
		grant.TelegramUserID, grant.TelegramChatID, grant.ExpiresAt, grant.CreatedAt).Scan(&grant.ID); err != nil {
		return fmt.Errorf("save Telegram callback: %w", err)
	}
	if err := insertTelegramAuditTx(ctx, tx, grant, "telegram.callback_issued"); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Telegram callback: %w", err)
	}
	return nil
}

func (r TelegramRepoPG) ConsumeCallback(
	ctx context.Context,
	tokenHash string,
	userID, chatID int64,
	now time.Time,
) (domain.TelegramCallbackGrant, error) {
	if !sha256HexPattern.MatchString(tokenHash) || userID <= 0 || chatID == 0 {
		return domain.TelegramCallbackGrant{}, fmt.Errorf("invalid Telegram callback identity: %w", domain.ErrValidation)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.TelegramCallbackGrant{}, fmt.Errorf("begin Telegram callback consumption: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	grant, err := scanTelegramCallback(tx.QueryRow(ctx, `
SELECT id, token_hash, action, resource_type, resource_id, telegram_user_id,
       telegram_chat_id, status, expires_at, consumed_at, created_at
FROM telegram_callback WHERE token_hash = $1 FOR UPDATE`, tokenHash))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.TelegramCallbackGrant{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.TelegramCallbackGrant{}, fmt.Errorf("load Telegram callback: %w", err)
	}
	if grant.TelegramUserID != userID || grant.TelegramChatID != chatID {
		return domain.TelegramCallbackGrant{}, domain.ErrForbidden
	}
	if grant.Status != domain.TelegramCallbackStatusPending {
		return domain.TelegramCallbackGrant{}, fmt.Errorf("Telegram callback was already used: %w", domain.ErrConflict)
	}
	if !now.Before(grant.ExpiresAt) {
		if _, err := tx.Exec(ctx, `UPDATE telegram_callback SET status = 'expired' WHERE id = $1`, grant.ID); err != nil {
			return domain.TelegramCallbackGrant{}, fmt.Errorf("expire Telegram callback: %w", err)
		}
		if err := insertTelegramAuditTx(ctx, tx, grant, "telegram.callback_expired"); err != nil {
			return domain.TelegramCallbackGrant{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.TelegramCallbackGrant{}, fmt.Errorf("commit expired Telegram callback: %w", err)
		}
		return domain.TelegramCallbackGrant{}, fmt.Errorf("Telegram callback expired: %w", domain.ErrInvalidStatus)
	}
	grant.Status = domain.TelegramCallbackStatusConsumed
	grant.ConsumedAt = &now
	if _, err := tx.Exec(ctx, `
UPDATE telegram_callback SET status = 'consumed', consumed_at = $2 WHERE id = $1`, grant.ID, now); err != nil {
		return domain.TelegramCallbackGrant{}, fmt.Errorf("consume Telegram callback: %w", err)
	}
	if err := insertTelegramAuditTx(ctx, tx, grant, "telegram.callback_consumed"); err != nil {
		return domain.TelegramCallbackGrant{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.TelegramCallbackGrant{}, fmt.Errorf("commit Telegram callback consumption: %w", err)
	}
	return grant, nil
}

func validTelegramCallbackAction(value string) bool {
	switch value {
	case "approve", "reject", "show_tasks", "change", "pause", "resume", "retry", "cancel":
		return true
	default:
		return false
	}
}

func validTelegramResourceType(value string) bool {
	return value == "plan" || value == "onboarding_run" || value == "run" || value == "task"
}

func insertTelegramAuditTx(ctx context.Context, tx pgx.Tx, grant domain.TelegramCallbackGrant, action string) error {
	_, err := tx.Exec(ctx, `
INSERT INTO audit_event (actor_type, actor_id, action, resource_type, resource_id, payload)
VALUES ('telegram', $1, $2, $3, $4, jsonb_build_object(
    'callback_id', $5::text, 'callback_action', $6::text, 'chat_id', $7::bigint
))`, fmt.Sprintf("%d", grant.TelegramUserID), action, grant.ResourceType, grant.ResourceID,
		grant.ID, grant.Action, grant.TelegramChatID)
	if err != nil {
		return fmt.Errorf("insert Telegram audit event: %w", err)
	}
	return nil
}

func scanTelegramCallback(row rowScanner) (domain.TelegramCallbackGrant, error) {
	var value domain.TelegramCallbackGrant
	err := row.Scan(&value.ID, &value.TokenHash, &value.Action, &value.ResourceType,
		&value.ResourceID, &value.TelegramUserID, &value.TelegramChatID,
		&value.Status, &value.ExpiresAt, &value.ConsumedAt, &value.CreatedAt)
	return value, err
}
