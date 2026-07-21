DROP INDEX IF EXISTS artifact_task_created_idx;
DROP INDEX IF EXISTS task_review_attempt_created_idx;
DROP INDEX IF EXISTS task_attempt_status_updated_idx;
DROP INDEX IF EXISTS task_plan_project_unique;
DROP TABLE IF EXISTS task_review;

ALTER TABLE artifact DROP COLUMN IF EXISTS created_at;

ALTER TABLE task_attempt DROP CONSTRAINT IF EXISTS task_attempt_status_check;
ALTER TABLE task_attempt
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS review_count,
    DROP COLUMN IF EXISTS verification_result;
