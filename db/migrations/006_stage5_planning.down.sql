DROP INDEX IF EXISTS task_dependency_depends_on_idx;
DROP TABLE IF EXISTS plan_run;

DROP INDEX IF EXISTS task_plan_planner_key_unique;
ALTER TABLE task
    DROP COLUMN IF EXISTS depth,
    DROP COLUMN IF EXISTS verification_commands,
    DROP COLUMN IF EXISTS changes_contracts,
    DROP COLUMN IF EXISTS requires_migration,
    DROP COLUMN IF EXISTS risk_level,
    DROP COLUMN IF EXISTS planner_key;

DROP INDEX IF EXISTS plan_command_fingerprint_unique;
ALTER TABLE plan
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS replan_count,
    DROP COLUMN IF EXISTS planner_output,
    DROP COLUMN IF EXISTS planner_input,
    DROP COLUMN IF EXISTS fingerprint,
    DROP COLUMN IF EXISTS topology_revision_id,
    DROP COLUMN IF EXISTS approval_id;

ALTER TABLE command DROP CONSTRAINT IF EXISTS command_status_check;
