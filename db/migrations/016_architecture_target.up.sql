-- TARGET proposals are intentionally separate from architecture CURRENT. The
-- payload is structured JSONB generated from typed Go values, not raw YAML or
-- source contents. Every row binds one immutable CURRENT catalog fingerprint.
CREATE TABLE architecture_target_proposal (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    current_fingerprint varchar(64) NOT NULL CHECK (current_fingerprint ~ '^[a-f0-9]{64}$'),
    fingerprint varchar(64) NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
    status varchar(32) NOT NULL CHECK (status IN (
        'draft', 'submitted', 'approved', 'rejected', 'changes_requested', 'superseded'
    )),
    revision integer NOT NULL CHECK (revision > 0),
    idempotency_key varchar(255) NOT NULL UNIQUE,
    supersedes_proposal_id uuid REFERENCES architecture_target_proposal(id) ON DELETE RESTRICT,
    changes jsonb NOT NULL CHECK (jsonb_typeof(changes) = 'array'),
    diff jsonb NOT NULL CHECK (jsonb_typeof(diff) = 'object'),
    impact jsonb NOT NULL CHECK (jsonb_typeof(impact) = 'object'),
    submitted_at timestamptz,
    decided_at timestamptz,
    superseded_at timestamptz,
    decided_by varchar(255),
    decision_comment text NOT NULL DEFAULT '',
    changes_requested_by varchar(255),
    changes_requested_at timestamptz,
    changes_request_comment text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT architecture_target_fingerprint_per_current_unique UNIQUE (current_fingerprint, fingerprint),
    CONSTRAINT architecture_target_submission_state_check CHECK (
        (status IN ('submitted', 'approved', 'rejected', 'changes_requested', 'superseded') AND submitted_at IS NOT NULL)
        OR (status = 'draft' AND submitted_at IS NULL)
    ),
    CONSTRAINT architecture_target_decision_state_check CHECK (
        (status IN ('approved', 'rejected') AND decided_at IS NOT NULL AND decided_by IS NOT NULL)
        OR (status NOT IN ('approved', 'rejected') AND decided_at IS NULL)
    ),
    CONSTRAINT architecture_target_changes_requested_state_check CHECK (
        (status = 'changes_requested' AND changes_requested_at IS NOT NULL AND changes_requested_by IS NOT NULL AND changes_request_comment <> '')
        OR status <> 'changes_requested'
    )
);

CREATE INDEX architecture_target_current_status_idx
    ON architecture_target_proposal (current_fingerprint, status, updated_at DESC, id DESC);
CREATE INDEX architecture_target_status_updated_idx
    ON architecture_target_proposal (status, updated_at DESC, id DESC);
CREATE INDEX architecture_target_supersedes_idx
    ON architecture_target_proposal (supersedes_proposal_id)
    WHERE supersedes_proposal_id IS NOT NULL;
CREATE UNIQUE INDEX architecture_target_one_successor_unique
    ON architecture_target_proposal (supersedes_proposal_id)
    WHERE supersedes_proposal_id IS NOT NULL;
CREATE INDEX architecture_target_changes_gin_idx
    ON architecture_target_proposal USING gin (changes jsonb_path_ops);
CREATE INDEX architecture_target_diff_gin_idx
    ON architecture_target_proposal USING gin (diff jsonb_path_ops);
CREATE INDEX architecture_target_impact_gin_idx
    ON architecture_target_proposal USING gin (impact jsonb_path_ops);
