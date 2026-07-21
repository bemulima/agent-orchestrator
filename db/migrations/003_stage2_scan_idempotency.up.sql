ALTER TABLE service_snapshot
    ADD COLUMN IF NOT EXISTS content_checksum varchar(64) NOT NULL DEFAULT '';
