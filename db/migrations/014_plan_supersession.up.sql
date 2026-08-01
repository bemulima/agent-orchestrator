ALTER TABLE plan
    ADD COLUMN supersedes_plan_id uuid REFERENCES plan(id) ON DELETE RESTRICT;

CREATE UNIQUE INDEX plan_single_successor_unique
    ON plan (supersedes_plan_id)
    WHERE supersedes_plan_id IS NOT NULL;

ALTER TABLE plan
    ADD CONSTRAINT plan_cannot_supersede_itself
    CHECK (supersedes_plan_id IS NULL OR supersedes_plan_id <> id);

COMMENT ON COLUMN plan.supersedes_plan_id IS
    'Explicit owner-selected predecessor. A successor is the only plan that remains actionable; existing rows are never linked by inference.';
