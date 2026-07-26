CREATE TABLE conversation (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title varchar(255) NOT NULL CHECK (length(btrim(title)) > 0),
    scope_type varchar(32) NOT NULL CHECK (scope_type IN ('workspace', 'project', 'plan', 'run', 'task')),
    scope_id uuid,
    agent_thread_id varchar(255),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT conversation_scope_check CHECK (
        (scope_type = 'workspace' AND scope_id IS NULL) OR
        (scope_type <> 'workspace' AND scope_id IS NOT NULL)
    )
);

CREATE UNIQUE INDEX conversation_agent_thread_unique
    ON conversation (agent_thread_id) WHERE agent_thread_id IS NOT NULL;
CREATE INDEX conversation_updated_idx ON conversation (updated_at DESC, id DESC);

CREATE TABLE conversation_message (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id uuid NOT NULL REFERENCES conversation(id) ON DELETE CASCADE,
    role varchar(16) NOT NULL CHECK (role IN ('owner', 'assistant', 'system')),
    status varchar(16) NOT NULL CHECK (status IN ('pending', 'completed', 'failed')),
    content text NOT NULL DEFAULT '' CHECK (length(content) <= 50000),
    resource_references jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(resource_references) = 'array'),
    error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

CREATE UNIQUE INDEX conversation_single_pending_assistant
    ON conversation_message (conversation_id)
    WHERE role = 'assistant' AND status = 'pending';
CREATE INDEX conversation_message_timeline_idx
    ON conversation_message (conversation_id, created_at, id);

CREATE TABLE action_proposal (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id uuid NOT NULL REFERENCES conversation(id) ON DELETE CASCADE,
    message_id uuid NOT NULL REFERENCES conversation_message(id) ON DELETE CASCADE,
    action varchar(64) NOT NULL,
    resource_type varchar(32) NOT NULL CHECK (resource_type IN ('plan', 'run', 'task')),
    resource_id uuid NOT NULL,
    title varchar(255) NOT NULL,
    description text NOT NULL CHECK (length(description) <= 5000),
    risk_level varchar(16) NOT NULL CHECK (risk_level IN ('low', 'medium', 'high', 'critical')),
    fingerprint varchar(128),
    status varchar(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'confirmed', 'rejected')),
    created_at timestamptz NOT NULL DEFAULT now(),
    decided_at timestamptz
);

CREATE INDEX action_proposal_conversation_idx
    ON action_proposal (conversation_id, created_at, id);
CREATE INDEX action_proposal_pending_idx
    ON action_proposal (status, created_at) WHERE status = 'pending';
