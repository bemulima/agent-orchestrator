DROP INDEX IF EXISTS plan_command_planner_fingerprint_unique;
ALTER TABLE plan DROP COLUMN IF EXISTS planner_fingerprint;
