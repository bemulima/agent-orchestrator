ALTER TABLE shard_fanout_execution
    DROP CONSTRAINT shard_fanout_execution_state_check,
    DROP CONSTRAINT shard_fanout_execution_composition_check;

ALTER TABLE shard_fanout_execution
    ADD CONSTRAINT shard_fanout_execution_state_check CHECK (state IN (
        'FANOUT_RUNNING', 'BARRIER_BLOCKED', 'BARRIER_READY', 'ASSEMBLED',
        'INTEGRATION_MECHANICALLY_VERIFIED', 'INTEGRATION_CONFLICT',
        'INTEGRATION_VERIFICATION_FAILED', 'INTEGRATION_REJECTED', 'REPLAN_REQUIRED',
        'INTEGRATION_VERIFIED', 'COMPOSITION_BLOCKED'
    )),
    ADD CONSTRAINT shard_fanout_execution_composition_check CHECK (composition_state IN (
        'PENDING', 'SKIPPED_NOT_REQUIRED', 'COMPOSITION_REQUIRED', 'COMPOSITION_COMPLETED'
    ));
