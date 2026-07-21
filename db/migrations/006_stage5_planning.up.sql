UPDATE command SET status = 'received' WHERE status NOT IN (
    'received', 'planning', 'planned', 'approved', 'running',
    'completed', 'failed', 'cancelled'
);

ALTER TABLE command
    ADD CONSTRAINT command_status_check CHECK (status IN (
        'received', 'planning', 'planned', 'approved', 'running',
        'completed', 'failed', 'cancelled'
    ));

ALTER TABLE plan
    ADD COLUMN approval_id uuid REFERENCES approval(id) ON DELETE SET NULL,
    ADD COLUMN topology_revision_id uuid REFERENCES topology_revision(id) ON DELETE SET NULL,
    ADD COLUMN fingerprint varchar(64),
    ADD COLUMN planner_input jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(planner_input) = 'object'),
    ADD COLUMN planner_output jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(planner_output) = 'object'),
    ADD COLUMN replan_count integer NOT NULL DEFAULT 0 CHECK (replan_count >= 0),
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

CREATE UNIQUE INDEX plan_command_fingerprint_unique
    ON plan (command_id, fingerprint) WHERE fingerprint IS NOT NULL;

ALTER TABLE task
    ADD COLUMN planner_key varchar(128),
    ADD COLUMN risk_level varchar(32) NOT NULL DEFAULT 'low' CHECK (risk_level IN ('low', 'medium', 'high', 'critical')),
    ADD COLUMN requires_migration boolean NOT NULL DEFAULT false,
    ADD COLUMN changes_contracts boolean NOT NULL DEFAULT false,
    ADD COLUMN verification_commands jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(verification_commands) = 'array'),
    ADD COLUMN depth integer NOT NULL DEFAULT 0 CHECK (depth >= 0);

CREATE UNIQUE INDEX task_plan_planner_key_unique
    ON task (plan_id, planner_key) WHERE planner_key IS NOT NULL;

CREATE TABLE plan_run (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id uuid NOT NULL REFERENCES plan(id) ON DELETE CASCADE,
    status varchar(32) NOT NULL CHECK (status IN (
        'pending', 'running', 'paused', 'completed', 'failed', 'cancelled'
    )),
    workflow_id varchar(255) NOT NULL UNIQUE,
    temporal_run_id varchar(255),
    idempotency_key varchar(255) NOT NULL UNIQUE,
    max_parallel_tasks integer NOT NULL CHECK (max_parallel_tasks BETWEEN 1 AND 3),
    error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    paused_at timestamptz,
    completed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (plan_id)
);

CREATE INDEX plan_run_status_updated_idx ON plan_run (status, updated_at DESC);
CREATE INDEX task_dependency_depends_on_idx ON task_dependency (depends_on_task_id, task_id);
