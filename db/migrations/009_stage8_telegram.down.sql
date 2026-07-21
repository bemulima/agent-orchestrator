DROP TABLE IF EXISTS telegram_poll_state;
DROP TABLE IF EXISTS telegram_callback;
DROP TABLE IF EXISTS telegram_update;
DROP INDEX IF EXISTS telegram_user_chat_unique;
ALTER TABLE telegram_user DROP COLUMN IF EXISTS last_seen_at, DROP COLUMN IF EXISTS updated_at;
