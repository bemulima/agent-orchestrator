DROP TABLE IF EXISTS gitlab_webhook_event;
DROP INDEX IF EXISTS gitlab_link_resource_idx;
DROP INDEX IF EXISTS gitlab_link_merge_request_unique;
DROP INDEX IF EXISTS gitlab_link_issue_unique;

ALTER TABLE gitlab_link
    DROP CONSTRAINT IF EXISTS gitlab_link_external_state_check,
    DROP CONSTRAINT IF EXISTS gitlab_link_issue_state_check,
    DROP CONSTRAINT IF EXISTS gitlab_link_merge_request_state_check,
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS created_at,
    DROP COLUMN IF EXISTS last_synced_at,
    DROP COLUMN IF EXISTS last_event_uuid,
    DROP COLUMN IF EXISTS pipeline_status,
    DROP COLUMN IF EXISTS merge_request_state,
    DROP COLUMN IF EXISTS issue_state,
    DROP COLUMN IF EXISTS external_state;
