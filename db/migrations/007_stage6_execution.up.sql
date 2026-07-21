ALTER TABLE task_attempt
    ADD COLUMN verification_result jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(verification_result) = 'object'),
    ADD COLUMN review_count integer NOT NULL DEFAULT 0 CHECK (review_count >= 0),
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE task_attempt
    ADD CONSTRAINT task_attempt_status_check CHECK (status IN (
        'running', 'verification', 'review', 'changes_requested',
        'completed', 'blocked', 'failed', 'cancelled'
    ));

ALTER TABLE artifact ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();

CREATE TABLE task_review (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_attempt_id uuid NOT NULL REFERENCES task_attempt(id) ON DELETE CASCADE,
    review_number integer NOT NULL CHECK (review_number > 0),
    agent_thread_id varchar(255) NOT NULL UNIQUE,
    status varchar(32) NOT NULL CHECK (status IN ('running', 'approved', 'changes_requested')),
    structured_result jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(structured_result) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (task_attempt_id, review_number)
);

CREATE UNIQUE INDEX task_plan_project_unique ON task (plan_id, project_id);
CREATE INDEX task_attempt_status_updated_idx ON task_attempt (status, updated_at DESC);
CREATE INDEX task_review_attempt_created_idx ON task_review (task_attempt_id, created_at);
CREATE INDEX artifact_task_created_idx ON artifact (task_id, created_at DESC);
