CREATE TABLE shard_fanout_execution (
    plan_id uuid PRIMARY KEY REFERENCES plan(id) ON DELETE CASCADE,
    contract_baseline_id uuid NOT NULL REFERENCES contract_baseline(id) ON DELETE RESTRICT,
    baseline_commit character varying(40) NOT NULL,
    state character varying(40) NOT NULL,
    barrier_state character varying(32) NOT NULL,
    composition_state character varying(32) NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT shard_fanout_execution_baseline_commit_check CHECK (baseline_commit ~ '^[0-9a-f]{40}$'),
    CONSTRAINT shard_fanout_execution_state_check CHECK (state IN (
        'FANOUT_RUNNING', 'BARRIER_BLOCKED', 'BARRIER_READY', 'ASSEMBLED',
        'INTEGRATION_MECHANICALLY_VERIFIED', 'INTEGRATION_CONFLICT',
        'INTEGRATION_VERIFICATION_FAILED', 'INTEGRATION_REJECTED', 'REPLAN_REQUIRED',
        'INTEGRATION_VERIFIED', 'COMPOSITION_BLOCKED'
    )),
    CONSTRAINT shard_fanout_execution_barrier_check CHECK (barrier_state IN (
        'PENDING', 'BARRIER_READY', 'BARRIER_BLOCKED', 'REPLAN_REQUIRED'
    )),
    CONSTRAINT shard_fanout_execution_composition_check CHECK (composition_state IN (
        'PENDING', 'SKIPPED_NOT_REQUIRED', 'COMPOSITION_REQUIRED', 'COMPOSITION_COMPLETED'
    )),
    CONSTRAINT shard_fanout_execution_payload_check CHECK (jsonb_typeof(payload) = 'object')
);

CREATE TABLE shard_attempt (
    id uuid PRIMARY KEY,
    plan_id uuid NOT NULL REFERENCES plan(id) ON DELETE CASCADE,
    task_id uuid NOT NULL REFERENCES task(id) ON DELETE CASCADE,
    shard_id uuid NOT NULL REFERENCES architectural_shard(id) ON DELETE CASCADE,
    attempt_number integer NOT NULL,
    baseline_commit character varying(40) NOT NULL,
    worker_thread_id text,
    model text NOT NULL DEFAULT '',
    reasoning_effort text NOT NULL DEFAULT '',
    status character varying(40) NOT NULL,
    worker_started_at timestamptz NOT NULL,
    worker_finished_at timestamptz,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT shard_attempt_number_check CHECK (attempt_number > 0),
    CONSTRAINT shard_attempt_baseline_commit_check CHECK (baseline_commit ~ '^[0-9a-f]{40}$'),
    CONSTRAINT shard_attempt_status_check CHECK (status IN (
        'RUNNING', 'VERIFIED', 'SHARD_BLOCKED', 'SHARD_FAILED',
        'REPLAN_REQUIRED', 'CONTRACT_CHANGE_REQUIRED'
    )),
    CONSTRAINT shard_attempt_payload_check CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT shard_attempt_shard_number_key UNIQUE (shard_id, attempt_number)
);

CREATE INDEX shard_attempt_plan_status_idx ON shard_attempt(plan_id, status, worker_started_at);
