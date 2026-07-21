ALTER TABLE service_snapshot
    DROP COLUMN IF EXISTS is_dirty,
    DROP COLUMN IF EXISTS branch;

DROP INDEX IF EXISTS project_source_identity_unique;

ALTER TABLE project
    DROP CONSTRAINT IF EXISTS project_repository_role_check,
    DROP COLUMN IF EXISTS is_dirty,
    DROP COLUMN IF EXISTS head_commit,
    DROP COLUMN IF EXISTS current_branch,
    DROP COLUMN IF EXISTS source_identity,
    DROP COLUMN IF EXISTS repository_role;
