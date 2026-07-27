CREATE TABLE agent_run_usage (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    role varchar(32) NOT NULL CHECK (role IN (
        'coder', 'reviewer', 'analyst', 'planner',
        'issue-manager', 'pull-request-manager', 'operator'
    )),
    model varchar(255) NOT NULL,
    reasoning_effort varchar(16) NOT NULL CHECK (reasoning_effort IN ('minimal', 'low', 'medium', 'high', 'xhigh')),
    thread_id varchar(255),
    resource_type varchar(32),
    resource_id uuid,
    route_reason varchar(512) NOT NULL,
    status varchar(16) NOT NULL CHECK (status IN ('running', 'completed', 'failed', 'denied')),
    input_tokens bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    cached_input_tokens bigint NOT NULL DEFAULT 0 CHECK (cached_input_tokens >= 0),
    output_tokens bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    reasoning_output_tokens bigint NOT NULL DEFAULT 0 CHECK (reasoning_output_tokens >= 0),
    duration_ms bigint NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    started_at timestamptz NOT NULL,
    completed_at timestamptz CHECK (completed_at IS NULL OR completed_at >= started_at),
    CHECK ((status = 'running') = (completed_at IS NULL)),
    CHECK ((resource_type IS NULL) = (resource_id IS NULL))
);

CREATE INDEX agent_run_usage_started_idx ON agent_run_usage (started_at DESC);
CREATE INDEX agent_run_usage_model_started_idx ON agent_run_usage (model, started_at DESC);
CREATE INDEX agent_run_usage_role_started_idx ON agent_run_usage (role, started_at DESC);
CREATE INDEX agent_run_usage_resource_idx ON agent_run_usage (resource_type, resource_id, started_at DESC)
    WHERE resource_id IS NOT NULL;
