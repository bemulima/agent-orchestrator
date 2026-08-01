ALTER TABLE plan DROP CONSTRAINT IF EXISTS plan_cannot_supersede_itself;
DROP INDEX IF EXISTS plan_single_successor_unique;
ALTER TABLE plan DROP COLUMN IF EXISTS supersedes_plan_id;
