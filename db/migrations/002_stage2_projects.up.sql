ALTER TABLE project
    ADD COLUMN repository_role varchar(32) NOT NULL DEFAULT 'service',
    ADD COLUMN source_identity text,
    ADD COLUMN current_branch varchar(255) NOT NULL DEFAULT '',
    ADD COLUMN head_commit varchar(64) NOT NULL DEFAULT '',
    ADD COLUMN is_dirty boolean NOT NULL DEFAULT false;

UPDATE project
SET source_identity = CASE
    WHEN git_url IS NOT NULL THEN 'git:' || git_url
    ELSE 'local:' || local_path
END
WHERE source_identity IS NULL;

ALTER TABLE project
    ALTER COLUMN source_identity SET NOT NULL,
    ADD CONSTRAINT project_repository_role_check CHECK (repository_role IN (
        'service', 'frontend', 'infrastructure', 'content', 'policy',
        'documentation', 'archive', 'unknown'
    ));

CREATE UNIQUE INDEX project_source_identity_unique ON project (source_identity);

ALTER TABLE service_snapshot
    ADD COLUMN branch varchar(255) NOT NULL DEFAULT '',
    ADD COLUMN is_dirty boolean NOT NULL DEFAULT false;
