ALTER TABLE telegram_user
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN last_seen_at timestamptz;

CREATE UNIQUE INDEX telegram_user_chat_unique
    ON telegram_user (telegram_user_id, telegram_chat_id);

CREATE TABLE telegram_update (
    update_id bigint PRIMARY KEY,
    source varchar(16) NOT NULL CHECK (source IN ('polling', 'webhook')),
    payload_checksum varchar(64) NOT NULL CHECK (length(payload_checksum) = 64),
    telegram_user_id bigint,
    telegram_chat_id bigint,
    status varchar(16) NOT NULL CHECK (status IN ('received', 'processed', 'ignored', 'failed')),
    received_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz
);

CREATE INDEX telegram_update_received_idx ON telegram_update (received_at DESC);

CREATE TABLE telegram_callback (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash varchar(64) NOT NULL UNIQUE CHECK (length(token_hash) = 64),
    action varchar(32) NOT NULL CHECK (action IN (
        'approve', 'reject', 'show_tasks', 'change',
        'pause', 'resume', 'retry', 'cancel'
    )),
    resource_type varchar(32) NOT NULL CHECK (resource_type IN ('plan', 'onboarding_run', 'run', 'task')),
    resource_id uuid NOT NULL,
    telegram_user_id bigint NOT NULL,
    telegram_chat_id bigint NOT NULL,
    status varchar(16) NOT NULL CHECK (status IN ('pending', 'consumed', 'expired', 'cancelled')),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT telegram_callback_expiry_check CHECK (expires_at > created_at)
);

CREATE INDEX telegram_callback_resource_idx
    ON telegram_callback (resource_type, resource_id, created_at DESC);
CREATE INDEX telegram_callback_expiry_idx
    ON telegram_callback (status, expires_at) WHERE status = 'pending';

CREATE TABLE telegram_poll_state (
    bot_key varchar(64) PRIMARY KEY,
    next_offset bigint NOT NULL CHECK (next_offset >= 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);
