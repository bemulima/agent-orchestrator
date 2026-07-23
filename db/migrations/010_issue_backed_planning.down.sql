DROP TABLE IF EXISTS work_item;
DROP TABLE IF EXISTS plan_comment;

ALTER TABLE plan
    DROP COLUMN IF EXISTS approved_fingerprint,
    DROP COLUMN IF EXISTS discussion_revision,
    DROP COLUMN IF EXISTS source_kind;

ALTER TABLE plan DROP CONSTRAINT plan_status_check;
ALTER TABLE plan ADD CONSTRAINT plan_status_check CHECK (status IN (
    'draft', 'planned', 'awaiting_approval', 'approved', 'running',
    'paused', 'completed', 'failed', 'cancelled'
));
