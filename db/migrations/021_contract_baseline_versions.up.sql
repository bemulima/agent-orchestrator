-- Preserve retired versions while allowing one current version per approved Task.
ALTER TABLE contract_baseline DROP CONSTRAINT contract_baseline_plan_id_task_id_key;
ALTER TABLE contract_baseline ADD COLUMN generation bigint GENERATED ALWAYS AS IDENTITY;
CREATE UNIQUE INDEX contract_baseline_current_task_idx
    ON contract_baseline (plan_id, task_id) WHERE execution_state <> 'INVALIDATED';
CREATE INDEX contract_baseline_latest_task_idx
    ON contract_baseline (plan_id, task_id, generation DESC);
