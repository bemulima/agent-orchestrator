-- Refuse rollback when version history exists; never delete historical evidence.
ALTER TABLE contract_baseline ADD CONSTRAINT contract_baseline_plan_id_task_id_key UNIQUE (plan_id, task_id);
DROP INDEX contract_baseline_current_task_idx;
DROP INDEX contract_baseline_latest_task_idx;
ALTER TABLE contract_baseline DROP COLUMN generation;
