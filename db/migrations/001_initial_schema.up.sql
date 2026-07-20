CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE project (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name varchar(255) NOT NULL,
    status varchar(64) NOT NULL,
    local_path text,
    git_url text,
    default_branch varchar(255) NOT NULL DEFAULT 'main',
    gitlab_project_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT project_source_check CHECK (local_path IS NOT NULL OR git_url IS NOT NULL)
);

CREATE UNIQUE INDEX project_local_path_unique
    ON project (local_path) WHERE local_path IS NOT NULL;
CREATE UNIQUE INDEX project_git_url_unique
    ON project (git_url) WHERE git_url IS NOT NULL;
CREATE UNIQUE INDEX project_gitlab_id_unique
    ON project (gitlab_project_id) WHERE gitlab_project_id IS NOT NULL;

CREATE TABLE service_snapshot (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    version integer NOT NULL CHECK (version > 0),
    commit_sha varchar(64) NOT NULL,
    service_kind varchar(64) NOT NULL CHECK (service_kind IN (
        'backend_service', 'frontend_application', 'gateway',
        'infrastructure', 'background_worker', 'shared_library',
        'ai_service', 'storage_service', 'unknown'
    )),
    language varchar(128) NOT NULL DEFAULT '',
    framework varchar(128) NOT NULL DEFAULT '',
    purpose text NOT NULL DEFAULT '',
    confidence double precision NOT NULL DEFAULT 0 CHECK (confidence >= 0 AND confidence <= 1),
    discovered_at timestamptz NOT NULL DEFAULT now(),
    raw_report jsonb NOT NULL DEFAULT '{}'::jsonb,
    status varchar(64) NOT NULL,
    UNIQUE (project_id, version)
);

CREATE TABLE service_capability (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    code varchar(255) NOT NULL,
    name varchar(255) NOT NULL,
    description text NOT NULL DEFAULT '',
    confidence double precision NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    source text NOT NULL,
    UNIQUE (project_id, code, source)
);

CREATE TABLE service_ownership (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    resource_type varchar(64) NOT NULL,
    resource_name varchar(255) NOT NULL,
    confidence double precision NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    source text NOT NULL,
    UNIQUE (project_id, resource_type, resource_name, source)
);

CREATE TABLE contract (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    code varchar(255) NOT NULL,
    type varchar(32) NOT NULL CHECK (type IN (
        'http', 'event', 'database', 'graphql', 'grpc', 'file', 'environment'
    )),
    version varchar(128) NOT NULL,
    direction varchar(32) NOT NULL,
    definition jsonb NOT NULL,
    source_path text NOT NULL,
    checksum varchar(128) NOT NULL,
    discovered_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, code, type, version, direction)
);

CREATE TABLE service_relation (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source_project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    target_project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    relation_type varchar(64) NOT NULL CHECK (relation_type IN (
        'depends_on', 'exposes', 'consumes', 'publishes', 'subscribes',
        'routes_to', 'authenticates_through', 'stores_in', 'deploys', 'owns'
    )),
    contract_code varchar(255),
    confidence double precision NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    source text NOT NULL
);

CREATE UNIQUE INDEX service_relation_identity_unique
    ON service_relation (
        source_project_id,
        target_project_id,
        relation_type,
        COALESCE(contract_code, ''),
        source
    );

CREATE TABLE command (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source varchar(32) NOT NULL CHECK (source IN ('cli', 'telegram', 'api')),
    source_user_id varchar(255),
    text text NOT NULL,
    status varchar(64) NOT NULL,
    idempotency_key varchar(255) NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE plan (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    command_id uuid NOT NULL REFERENCES command(id) ON DELETE CASCADE,
    status varchar(64) NOT NULL CHECK (status IN (
        'draft', 'planned', 'awaiting_approval', 'approved', 'running',
        'paused', 'completed', 'failed', 'cancelled'
    )),
    version integer NOT NULL CHECK (version > 0),
    summary text NOT NULL,
    risk_level varchar(32) NOT NULL,
    requires_approval boolean NOT NULL DEFAULT true CHECK (requires_approval),
    created_at timestamptz NOT NULL DEFAULT now(),
    approved_at timestamptz,
    UNIQUE (command_id, version)
);

CREATE TABLE task (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id uuid NOT NULL REFERENCES plan(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE RESTRICT,
    role varchar(64) NOT NULL,
    title varchar(255) NOT NULL,
    description text NOT NULL,
    status varchar(64) NOT NULL CHECK (status IN (
        'draft', 'planned', 'ready', 'running', 'blocked', 'verification',
        'changes_requested', 'completed', 'failed', 'cancelled'
    )),
    acceptance_criteria jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(acceptance_criteria) = 'array'),
    write_scope jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(write_scope) = 'array'),
    model_profile varchar(32) NOT NULL CHECK (model_profile IN ('fast', 'standard', 'deep', 'review')),
    priority integer NOT NULL DEFAULT 0,
    idempotency_key varchar(255) NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    completed_at timestamptz
);

CREATE TABLE task_dependency (
    task_id uuid NOT NULL REFERENCES task(id) ON DELETE CASCADE,
    depends_on_task_id uuid NOT NULL REFERENCES task(id) ON DELETE CASCADE,
    dependency_type varchar(64) NOT NULL,
    PRIMARY KEY (task_id, depends_on_task_id),
    CONSTRAINT task_dependency_not_self CHECK (task_id <> depends_on_task_id)
);

CREATE TABLE task_attempt (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id uuid NOT NULL REFERENCES task(id) ON DELETE CASCADE,
    attempt_number integer NOT NULL CHECK (attempt_number > 0),
    agent_thread_id varchar(255),
    workflow_id varchar(255) NOT NULL UNIQUE,
    worktree_path text NOT NULL,
    branch_name varchar(255) NOT NULL,
    commit_sha varchar(64),
    status varchar(64) NOT NULL,
    structured_result jsonb NOT NULL DEFAULT '{}'::jsonb,
    error text,
    started_at timestamptz NOT NULL DEFAULT now(),
    heartbeat_at timestamptz,
    finished_at timestamptz,
    UNIQUE (task_id, attempt_number)
);

CREATE TABLE artifact (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id uuid NOT NULL REFERENCES task(id) ON DELETE CASCADE,
    type varchar(64) NOT NULL,
    name varchar(255) NOT NULL,
    uri text NOT NULL,
    checksum varchar(128) NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (task_id, type, name, checksum)
);

CREATE TABLE approval (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_type varchar(64) NOT NULL,
    resource_id uuid NOT NULL,
    action varchar(64) NOT NULL,
    status varchar(32) NOT NULL CHECK (status IN (
        'pending', 'approved', 'rejected', 'expired', 'cancelled'
    )),
    requested_at timestamptz NOT NULL DEFAULT now(),
    decided_at timestamptz,
    decided_by varchar(255),
    comment text
);

CREATE UNIQUE INDEX approval_one_pending_action
    ON approval (resource_type, resource_id, action) WHERE status = 'pending';

CREATE TABLE gitlab_link (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_type varchar(64) NOT NULL,
    resource_id uuid NOT NULL,
    gitlab_project_id bigint NOT NULL,
    issue_iid bigint,
    merge_request_iid bigint,
    url text NOT NULL,
    UNIQUE (resource_type, resource_id, gitlab_project_id)
);

CREATE TABLE telegram_user (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    telegram_user_id bigint NOT NULL UNIQUE,
    telegram_chat_id bigint NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_event (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_type varchar(64) NOT NULL,
    actor_id varchar(255),
    action varchar(128) NOT NULL,
    resource_type varchar(64) NOT NULL,
    resource_id uuid NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX service_snapshot_project_discovered_idx
    ON service_snapshot (project_id, discovered_at DESC);
CREATE INDEX service_capability_code_idx ON service_capability (code);
CREATE INDEX service_ownership_resource_idx ON service_ownership (resource_type, resource_name);
CREATE INDEX service_relation_source_idx ON service_relation (source_project_id);
CREATE INDEX service_relation_target_idx ON service_relation (target_project_id);
CREATE INDEX contract_code_idx ON contract (code, type);
CREATE INDEX task_plan_status_idx ON task (plan_id, status);
CREATE INDEX task_attempt_task_status_idx ON task_attempt (task_id, status);
CREATE INDEX approval_resource_idx ON approval (resource_type, resource_id, status);
CREATE INDEX audit_event_resource_idx ON audit_event (resource_type, resource_id, created_at DESC);
CREATE INDEX audit_event_created_idx ON audit_event (created_at DESC);
