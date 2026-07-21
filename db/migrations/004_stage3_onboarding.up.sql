CREATE TABLE onboarding_run (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    snapshot_id uuid NOT NULL REFERENCES service_snapshot(id) ON DELETE CASCADE,
    approval_id uuid REFERENCES approval(id) ON DELETE SET NULL,
    status varchar(64) NOT NULL CHECK (status IN (
        'proposal_ready', 'awaiting_approval', 'applying', 'merge_request_created', 'completed',
        'failed', 'cancelled', 'changes_requested'
    )),
    dry_run boolean NOT NULL DEFAULT false,
    base_commit varchar(64) NOT NULL,
    base_branch varchar(255) NOT NULL DEFAULT '',
    proposal_checksum varchar(64) NOT NULL,
    proposal jsonb NOT NULL CHECK (jsonb_typeof(proposal) = 'object'),
    unified_diff text NOT NULL,
    worktree_path text,
    branch_name varchar(255),
    commit_sha varchar(64),
    merge_request_url text,
    checks jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(checks) = 'array'),
    error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    applied_at timestamptz,
    UNIQUE (project_id, snapshot_id, proposal_checksum, dry_run)
);

CREATE INDEX onboarding_run_project_created_idx
    ON onboarding_run (project_id, created_at DESC);
CREATE INDEX onboarding_run_status_idx ON onboarding_run (status, updated_at);
