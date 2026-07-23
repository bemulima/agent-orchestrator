ALTER TABLE plan ADD COLUMN planner_fingerprint varchar(64);
UPDATE plan SET planner_fingerprint = fingerprint WHERE planner_fingerprint IS NULL;
ALTER TABLE plan ALTER COLUMN planner_fingerprint SET NOT NULL;

CREATE UNIQUE INDEX plan_command_planner_fingerprint_unique
    ON plan (command_id, planner_fingerprint);

COMMENT ON COLUMN plan.planner_fingerprint IS
    'Stable DAG fingerprint used for planner idempotency; plan.fingerprint also binds owner-reviewed issue proposals.';
