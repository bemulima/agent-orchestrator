DROP INDEX IF EXISTS project_active_name_idx;

ALTER TABLE project
    DROP CONSTRAINT IF EXISTS project_archive_state_check,
    DROP COLUMN IF EXISTS archived_from_status,
    DROP COLUMN IF EXISTS archived_at;
