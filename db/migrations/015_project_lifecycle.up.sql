ALTER TABLE project
    ADD COLUMN archived_at timestamptz,
    ADD COLUMN archived_from_status varchar(64),
    ADD CONSTRAINT project_archive_state_check CHECK (
        (
            status = 'archived'
            AND archived_at IS NOT NULL
            AND archived_from_status IS NOT NULL
            AND archived_from_status <> 'archived'
        ) OR (
            status <> 'archived'
            AND archived_at IS NULL
            AND archived_from_status IS NULL
        )
    );

CREATE INDEX project_active_name_idx
    ON project (name, id)
    WHERE status <> 'archived';
