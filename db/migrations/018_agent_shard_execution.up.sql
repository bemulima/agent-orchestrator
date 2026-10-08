CREATE TABLE architectural_shard (
    id uuid PRIMARY KEY,
    plan_id uuid NOT NULL REFERENCES plan(id) ON DELETE CASCADE,
    task_id uuid NOT NULL REFERENCES task(id) ON DELETE CASCADE,
    repository_project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    profile_id varchar(128) NOT NULL,
    profile_fingerprint varchar(128) NOT NULL,
    route_id varchar(128) NOT NULL,
    status varchar(64) NOT NULL CHECK (status IN ('PLANNED', 'COMPOSITION_REQUIRED', 'OWNER_REVIEW_REQUIRED')),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (plan_id, task_id, route_id)
);

CREATE INDEX architectural_shard_plan_idx ON architectural_shard (plan_id, repository_project_id);

CREATE TABLE contract_baseline (
    id uuid PRIMARY KEY,
    plan_id uuid NOT NULL REFERENCES plan(id) ON DELETE CASCADE,
    task_id uuid NOT NULL REFERENCES task(id) ON DELETE CASCADE,
    repository_project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    profile_id varchar(128) NOT NULL,
    planning_contract_state varchar(32) NOT NULL CHECK (planning_contract_state IN ('NOT_REQUIRED', 'PLANNED', 'FREEZE_REQUIRED')),
    execution_state varchar(32) NOT NULL CHECK (execution_state IN ('NOT_REQUIRED', 'PENDING', 'MATERIALIZING', 'FROZEN', 'INVALIDATED', 'BLOCKED')),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (plan_id, task_id)
);

CREATE INDEX contract_baseline_plan_idx ON contract_baseline (plan_id, execution_state);

CREATE TABLE fanout_readiness (
    plan_id uuid PRIMARY KEY REFERENCES plan(id) ON DELETE CASCADE,
    state varchar(32) NOT NULL CHECK (state IN ('READY_FOR_FANOUT', 'BLOCKED', 'OWNER_REVIEW_REQUIRED')),
    parallelism_scope varchar(32) NOT NULL CHECK (parallelism_scope = 'same_repository_only'),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    recorded_at timestamptz NOT NULL DEFAULT now()
);
