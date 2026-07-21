ALTER TABLE gitlab_link
    ADD COLUMN external_state varchar(32) NOT NULL DEFAULT 'unknown',
    ADD COLUMN issue_state varchar(32) NOT NULL DEFAULT 'unknown',
    ADD COLUMN merge_request_state varchar(32) NOT NULL DEFAULT 'unknown',
    ADD COLUMN pipeline_status varchar(32) NOT NULL DEFAULT '',
    ADD COLUMN last_event_uuid varchar(128),
    ADD COLUMN last_synced_at timestamptz,
    ADD COLUMN created_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
    ADD CONSTRAINT gitlab_link_external_state_check CHECK (
        external_state IN ('unknown', 'opened', 'closed', 'merged', 'locked')
    ),
    ADD CONSTRAINT gitlab_link_issue_state_check CHECK (
        issue_state IN ('unknown', 'opened', 'closed')
    ),
    ADD CONSTRAINT gitlab_link_merge_request_state_check CHECK (
        merge_request_state IN ('unknown', 'opened', 'closed', 'merged', 'locked')
    );

CREATE UNIQUE INDEX gitlab_link_issue_unique
    ON gitlab_link (gitlab_project_id, issue_iid)
    WHERE issue_iid IS NOT NULL;

CREATE UNIQUE INDEX gitlab_link_merge_request_unique
    ON gitlab_link (gitlab_project_id, merge_request_iid)
    WHERE merge_request_iid IS NOT NULL;

CREATE INDEX gitlab_link_resource_idx
    ON gitlab_link (resource_type, resource_id);

CREATE TABLE gitlab_webhook_event (
    event_uuid varchar(128) PRIMARY KEY,
    event_type varchar(128) NOT NULL,
    object_kind varchar(32) NOT NULL CHECK (object_kind IN ('issue', 'merge_request', 'pipeline')),
    gitlab_project_id bigint NOT NULL,
    object_iid bigint NOT NULL,
    payload_checksum varchar(64) NOT NULL,
    status varchar(32) NOT NULL CHECK (status IN ('processed', 'ignored')),
    gitlab_link_id uuid REFERENCES gitlab_link(id) ON DELETE SET NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX gitlab_webhook_event_received_idx
    ON gitlab_webhook_event (received_at DESC);
