"""Synthetic analyzer regressions; these fixtures are not runtime certification."""
import importlib.util
import hashlib
import json
import pathlib
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name("probe-complete-transport-lifecycle.py")
SPEC = importlib.util.spec_from_file_location("complete_transport_lifecycle", SCRIPT)
PROBE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PROBE)


class EvidenceAnalyzerTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="transport-analyzer-unit-")
        self.addCleanup(self.temporary.cleanup)
        self.output = pathlib.Path(self.temporary.name)
        self.context = {"workflow_id": "synthetic-unit", "workflow_run_id": "synthetic-run", "activity_id": "3", "activity_attempt": 1}
        self.write("workflow-start.json", {"workflow_id": "synthetic-unit", "workflow_run_id": "synthetic-run"})
        self.write("activity-start.json", {"activity_id": "3", "attempt": 1, "runtime_context": self.context, "trusted_runtime_context_bound": True})
        self.write("backend-boundary.json", {"docker_cli_absent": True, "docker_socket_absent": True})
        self.write("activity-context-cancel.json", {"runtime_context": self.context, "error": "context canceled"})
        self.write("runner-return.json", {"time": "2026-10-09T01:00:00Z", "cancelled": True, "error": "context canceled", "result": {}, "runtime_context": self.context})
        self.write("workflow-result.json", {"time": "2026-10-09T01:00:01Z", "workflow_id": "synthetic-unit", "workflow_run_id": "synthetic-run", "output": {"status": "cancelled", "task_status": {"fixture": "cancelled"}}})
        self.write("transport-checkpoint.json", {"phase": "active", "execution_id": "synthetic", "runtime_context": self.context})
        self.terminal = {
            "execution_id": "synthetic", "role": "layer", "status": "CANCELLED",
            "runtime_context": self.context, "real_model_calls": 0,
            "backend_authority": {"no_docker_socket_mount": True, "no_remote_docker_authority": True},
            "cleanup": {"state": "CLEANED", "command_absent": True, "sdk_absent": True, "private_state_absent": True},
            "publication": {"status": "FAILED"},
        }
        self.write("transport-terminal.json", self.terminal)
        self.history = [
            {"eventId": "1", "eventType": "EVENT_TYPE_WORKFLOW_EXECUTION_STARTED", "workflowExecutionStartedEventAttributes": {}},
            {"eventId": "2", "eventType": "EVENT_TYPE_WORKFLOW_TASK_COMPLETED", "workflowTaskCompletedEventAttributes": {}},
            {"eventId": "3", "eventType": "EVENT_TYPE_ACTIVITY_TASK_SCHEDULED", "activityTaskScheduledEventAttributes": {"activityType": {"name": "ExecutePlanTask"}, "activityId": "3"}},
            {"eventId": "4", "eventType": "EVENT_TYPE_ACTIVITY_TASK_STARTED", "activityTaskStartedEventAttributes": {"scheduledEventId": "3"}},
            {"eventId": "5", "eventType": "EVENT_TYPE_ACTIVITY_TASK_CANCEL_REQUESTED", "activityTaskCancelRequestedEventAttributes": {"scheduledEventId": "3"}},
            {"eventId": "6", "eventType": "EVENT_TYPE_ACTIVITY_TASK_CANCELED", "activityTaskCanceledEventAttributes": {"scheduledEventId": "3"}},
            {"eventId": "7", "eventType": "EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED", "workflowExecutionCompletedEventAttributes": {}},
        ]
        self.write_history()

    def write(self, name, value):
        (self.output / name).write_text(json.dumps(value))

    def write_history(self):
        (self.output / "history.jsonl").write_text("\n".join(json.dumps(event) for event in self.history) + "\n")
        self.write("history-binding.json", {"workflow_id": "synthetic-unit", "workflow_run_id": "synthetic-run", "api": "GetWorkflowHistory", "bytes": (self.output / "history.jsonl").stat().st_size, "sha256": hashlib.sha256((self.output / "history.jsonl").read_bytes()).hexdigest()})

    def result(self, case="activity-cancel"):
        return PROBE.analyze(self.output, case, "layer")

    def test_synthetic_complete_cancel_observations(self):
        self.assertEqual(self.result()["status"], "PASS")

    def test_unrelated_activity_cancel_cannot_prove_owned_cancel(self):
        self.history[4]["activityTaskCancelRequestedEventAttributes"]["scheduledEventId"] = "99"
        self.write_history()
        self.assertFalse(self.result()["assertions"]["actual_activity_cancel_requested"])

    def test_unknown_cleanup_cannot_pass(self):
        self.terminal["cleanup"]["state"] = "UNKNOWN"
        self.write("transport-terminal.json", self.terminal)
        self.assertEqual(self.result()["status"], "FAIL")

    def test_false_truthy_absence_does_not_pass(self):
        self.terminal["cleanup"]["command_absent"] = "true"
        self.write("transport-terminal.json", self.terminal)
        self.assertFalse(self.result()["assertions"]["owned_command_positive_absence"])

    def test_other_runtime_context_does_not_pass(self):
        self.terminal["runtime_context"] = {**self.context, "workflow_run_id": "other"}
        self.write("transport-terminal.json", self.terminal)
        self.assertFalse(self.result()["assertions"]["controller_bound_actual_temporal_identity"])

    def test_late_success_cannot_pass_failure_case(self):
        self.write("runner-return.json", {"result": {"result": {"status": "completed"}}})
        self.assertFalse(self.result()["assertions"]["no_runner_result"])

    def test_completed_task_cannot_pass_failure_case(self):
        (self.output / "task-records.jsonl").write_text(json.dumps({"result": {"status": "completed"}}) + "\n")
        self.assertFalse(self.result()["assertions"]["no_task_completed"])

    def test_partial_publication_requires_actual_partial_failure(self):
        self.assertFalse(self.result("partial-publication")["assertions"]["actual_partial_publication_invalidated"])
        self.terminal["publication"]["partial"] = True
        self.write("transport-terminal.json", self.terminal)
        self.assertTrue(self.result("partial-publication")["assertions"]["actual_partial_publication_invalidated"])

    def test_runtime_unknown_requires_reconciled_positive_observation(self):
        self.terminal["unknown_latched"] = True
        self.terminal["invalid_reason"] = "RUNTIME_STATE_UNKNOWN"
        self.write("transport-terminal.json", self.terminal)
        self.write("control-completed.json", {"action": "runtime-unknown", "performed": True, "execution_id": "synthetic", "runtime_context": self.context, "invalid_reason": "RUNTIME_STATE_UNKNOWN", "runtime_unobservable": True, "runtime_observable_after_reconciliation": True})
        self.assertFalse(self.result("runtime-unknown")["assertions"]["reconciliation_positive_observation"])

    def test_history_absence_is_error(self):
        (self.output / "history.jsonl").unlink()
        with self.assertRaises(ValueError):
            self.result()

    def test_history_duplicate_ids_are_error(self):
        self.history.append(self.history[-1])
        self.write_history()
        with self.assertRaises(ValueError):
            self.result()

    def test_repeat_activity_schedule_fails_no_retry_assertion(self):
        self.history.append({"eventId": "8", "eventType": "EVENT_TYPE_ACTIVITY_TASK_SCHEDULED", "activityTaskScheduledEventAttributes": {"activityType": {"name": "ExecutePlanTask"}}})
        self.write_history()
        self.assertFalse(self.result()["assertions"]["actual_execution_activity_scheduled_once"])

    def test_duplicate_json_keys_are_error(self):
        (self.output / "backend-boundary.json").write_text('{"docker_cli_absent":false,"docker_cli_absent":true}')
        with self.assertRaises(ValueError):
            self.result()

    def test_cancel_request_alone_cannot_pass(self):
        (self.output / "runner-return.json").unlink()
        (self.output / "activity-context-cancel.json").unlink()
        self.history = self.history[:5]
        self.write_history()
        self.assertEqual(self.result()["status"], "FAIL")

    def test_history_other_run_cannot_pass(self):
        self.write("history-binding.json", {"workflow_id": "synthetic-unit", "workflow_run_id": "other", "api": "GetWorkflowHistory"})
        self.assertFalse(self.result()["assertions"]["history_exact_run_identity_matches"])

    def test_wrong_scheduled_activity_cannot_pass(self):
        self.history[2]["activityTaskScheduledEventAttributes"]["activityId"] = "other"
        self.write_history()
        self.assertFalse(self.result()["assertions"]["scheduled_activity_identity_matches"])

    def test_success_uses_actual_workflow_output(self):
        self.history = self.history[:4] + [
            {"eventId": "5", "eventType": "EVENT_TYPE_ACTIVITY_TASK_COMPLETED", "activityTaskCompletedEventAttributes": {"scheduledEventId": "3"}},
            {"eventId": "6", "eventType": "EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED", "workflowExecutionCompletedEventAttributes": {}},
        ]
        self.write_history()
        self.write("runner-return.json", {"time": "2026-10-09T01:00:00Z", "error": "<nil>", "result": {"execution_id": "synthetic", "attempt": 1, "result": {"status": "completed"}}, "runtime_context": self.context})
        self.write("workflow-result.json", {"workflow_id": "synthetic-unit", "workflow_run_id": "synthetic-run", "output": {"status": "completed", "task_status": {"fixture": "completed"}}})
        self.terminal["status"] = "VERIFIED"
        self.terminal["publication"]["status"] = "COMPLETE"
        self.write("transport-terminal.json", self.terminal)
        self.assertFalse((self.output / "task-records.jsonl").exists())
        self.assertEqual(self.result("success")["status"], "PASS")

    def test_external_fault_requires_actual_bound_fault_proofs(self):
        self.terminal["invalid_reason"] = "BROKER_EXITED"
        self.write("transport-terminal.json", self.terminal)
        self.write("control-completed.json", {"action": "controller-kill", "performed": True, "execution_id": "synthetic", "runtime_context": self.context, "invalid_reason": "BROKER_EXITED"})
        result = self.result("controller-kill")
        self.assertFalse(result["assertions"]["fault_observed_controller_process_absent"])
        self.assertFalse(result["assertions"]["fault_observed_controller_restart_reconciled"])
        self.assertFalse(result["assertions"]["typed_failure_matches_case"])

    def test_time_order_handles_zero_fraction_and_fractional_seconds(self):
        self.assertFalse(PROBE.acknowledged_before({"time": "2026-10-09T01:00:00.001Z"}, {"time": "2026-10-09T01:00:00Z"}))
        self.assertTrue(PROBE.acknowledged_before({"time": "2026-10-09T01:00:00Z"}, {"time": "2026-10-09T01:00:00.001Z"}))

    def test_return_from_other_context_fails(self):
        self.write("runner-return.json", {"cancelled": True, "error": "context canceled", "result": {}, "runtime_context": {**self.context, "workflow_run_id": "other"}})
        self.assertFalse(self.result()["assertions"]["returned_runtime_context_matches"])

    def test_external_pending_history_cannot_pass(self):
        self.history = self.history[:4]
        self.write_history()
        self.terminal["invalid_reason"] = "CONTROLLER_RESTARTED"
        self.write("transport-terminal.json", self.terminal)
        self.write("control-completed.json", {"action": "controller-kill", "performed": True, "execution_id": "synthetic", "runtime_context": self.context, "invalid_reason": "CONTROLLER_RESTARTED", "controller_process_absent": True, "controller_restart_reconciled": True})
        result = self.result("controller-kill")
        self.assertFalse(result["assertions"]["actual_activity_terminal_after_fault"])
        self.assertFalse(result["assertions"]["actual_workflow_terminal_after_fault"])

    def test_truncated_prefix_history_is_invalid(self):
        self.history = self.history[2:]
        self.write_history()
        with self.assertRaises(ValueError):
            self.result()

    def test_changed_history_byte_length_is_not_accepted(self):
        (self.output / "history.jsonl").write_text((self.output / "history.jsonl").read_text() + "\n")
        self.assertFalse(self.result()["assertions"]["history_complete_bytes_match"])

    def test_same_length_history_alteration_is_not_accepted(self):
        history = self.output / "history.jsonl"
        old = history.read_text()
        new = old.replace('"activityId": "3"', '"activityId": "4"')
        self.assertEqual(len(old), len(new))
        history.write_text(new)
        self.assertFalse(self.result()["assertions"]["history_content_hash_matches"])

    def test_checkpoint_phase_is_derived_for_every_selected_scenario(self):
        for case in PROBE.CASES:
            expected = PROBE.CASE_PHASES.get(case, "active")
            self.assertEqual(PROBE.checkpoint_phase(case), expected)
            self.assertEqual(PROBE.checkpoint_phase(case, expected), expected)
        self.assertEqual(PROBE.checkpoint_phase("before-publication"), "before-publication")
        self.assertEqual(PROBE.checkpoint_phase("partial-publication"), "publication-applied-1")
        with self.assertRaises(ValueError):
            PROBE.checkpoint_phase("before-publication", "active")

    def test_unknown_analysis_case_and_role_are_rejected(self):
        with self.assertRaises(ValueError):
            self.result("unsupported-case")
        with self.assertRaises(ValueError):
            PROBE.analyze(self.output, "activity-cancel", "unknown-role")

    def test_backend_kill_requires_exact_metadata_only_observer(self):
        self.history = self.history[:4] + [
            {"eventId": "5", "eventType": "EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT", "activityTaskTimedOutEventAttributes": {"scheduledEventId": "3", "failure": {"timeoutFailureInfo": {"timeoutType": "TIMEOUT_TYPE_HEARTBEAT"}}}},
            {"eventId": "6", "eventType": "EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED", "workflowExecutionCompletedEventAttributes": {}},
        ]
        self.write_history()
        (self.output / "runner-return.json").unlink()
        self.write("workflow-result.json", {"time": "2026-10-09T01:00:01Z", "workflow_id": "synthetic-unit", "workflow_run_id": "synthetic-run", "bookkeeping_observer": True, "error": "<nil>", "output": {"status": "failed", "task_status": {"fixture": "failed"}}})
        self.terminal["invalid_reason"] = "EXECUTION_LEASE_EXPIRED"
        self.write("transport-terminal.json", self.terminal)
        self.write("control-completed.json", {"action": "backend-kill", "performed": True, "execution_id": "synthetic", "runtime_context": self.context, "invalid_reason": "EXECUTION_LEASE_EXPIRED", "backend_process_absent": True, "lease_expired": True})
        observer = {"workflow_id": "synthetic-unit", "workflow_run_id": "synthetic-run", "started_execution": False, "mode": "BOOKKEEPING_ONLY", "registered_activity_names": ["SetPlanRunStatus", "RecordPlanTaskResult"], "observer_timeout_seconds": 180}
        self.write("observer-complete.json", observer)
        self.assertEqual(self.result("backend-kill")["status"], "PASS")
        self.write("workflow-result.json", {"workflow_id": "synthetic-unit", "workflow_run_id": "synthetic-run", "bookkeeping_observer": True, "error": "context deadline exceeded", "output": {}})
        self.assertFalse(self.result("backend-kill")["assertions"]["backend_loss_observer_persisted_terminal_result"])
        observer["registered_activity_names"].append("ExecutePlanTask")
        self.write("observer-complete.json", observer)
        self.assertFalse(self.result("backend-kill")["assertions"]["backend_loss_observer_cannot_execute_agent"])

    def test_lease_expiry_requires_authenticated_renewal_stop_proofs(self):
        self.set_failed_history("NON_RETRYABLE_POLICY")
        self.terminal["invalid_reason"] = "EXECUTION_LEASE_EXPIRED"
        self.write("transport-terminal.json", self.terminal)
        action = {"action": "lease-expiry", "performed": True, "execution_id": "synthetic", "runtime_context": self.context, "invalid_reason": "EXECUTION_LEASE_EXPIRED", "lease_expired": True, "authenticated_renewal_stopped": True, "expiry_observed_after_last_renewal": True}
        self.write("control-completed.json", action)
        self.assertEqual(self.result("lease-expiry")["status"], "PASS")
        del action["authenticated_renewal_stopped"]
        self.write("control-completed.json", action)
        self.assertFalse(self.result("lease-expiry")["assertions"]["fault_observed_authenticated_renewal_stopped"])
        action["authenticated_renewal_stopped"] = True
        action["expiry_observed_after_last_renewal"] = False
        self.write("control-completed.json", action)
        self.assertFalse(self.result("lease-expiry")["assertions"]["fault_observed_expiry_observed_after_last_renewal"])

    def set_failed_history(self, kind="UNKNOWN_RUNTIME"):
        self.history = self.history[:4] + [
            {"eventId": "5", "eventType": "EVENT_TYPE_ACTIVITY_TASK_FAILED", "activityTaskFailedEventAttributes": {"scheduledEventId": "3", "failure": {"applicationFailureInfo": {"type": kind, "nonRetryable": True}}}},
            {"eventId": "6", "eventType": "EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED", "workflowExecutionCompletedEventAttributes": {}},
        ]
        self.write_history()
        self.write("runner-return.json", {"time": "2026-10-09T01:00:00Z", "cancelled": False, "error": "RUNTIME_TRANSPORT_UNKNOWN", "result": {}, "runtime_context": self.context})
        self.write("workflow-result.json", {"time": "2026-10-09T01:00:01Z", "workflow_id": "synthetic-unit", "workflow_run_id": "synthetic-run", "output": {"status": "failed", "task_status": {"fixture": "failed"}}})

    def test_transport_disconnect_requires_actual_lease_and_restore_proofs(self):
        self.set_failed_history()
        self.terminal["invalid_reason"] = "EXECUTION_LEASE_EXPIRED"
        self.write("transport-terminal.json", self.terminal)
        action = {"action": "transport-disconnect", "performed": True, "execution_id": "synthetic", "runtime_context": self.context, "invalid_reason": "EXECUTION_LEASE_EXPIRED", "transport_listener_unreachable": True, "lease_expired": True, "transport_listener_restored": True}
        self.write("control-completed.json", action)
        self.assertEqual(self.result("transport-disconnect")["status"], "PASS")
        action["lease_expired"] = False
        self.write("control-completed.json", action)
        self.assertFalse(self.result("transport-disconnect")["assertions"]["fault_observed_lease_expired"])

    def test_runtime_unknown_cannot_pass_without_negative_cleanup_observation(self):
        self.set_failed_history()
        self.terminal.update(status="UNKNOWN", invalid_reason="RUNTIME_UNKNOWN", unknown_latched=True)
        self.terminal["cleanup"]["reconciled"] = True
        self.write("transport-terminal.json", self.terminal)
        action = {"action": "runtime-unknown", "performed": True, "execution_id": "synthetic", "runtime_context": self.context, "invalid_reason": "RUNTIME_UNKNOWN", "runtime_unobservable": True, "runtime_observable_after_reconciliation": True, "cleanup_state_while_unobservable": "UNKNOWN", "cleanup_proven_while_unobservable": False}
        self.write("control-completed.json", action)
        self.assertEqual(self.result("runtime-unknown")["status"], "PASS")
        action["cleanup_proven_while_unobservable"] = True
        self.write("control-completed.json", action)
        self.assertFalse(self.result("runtime-unknown")["assertions"]["unknown_did_not_report_cleanup_pass"])

    def test_stale_publication_requires_typed_fence_and_failed_journal(self):
        self.terminal["invalid_reason"] = "EXECUTION_CANCELLED"
        self.terminal["publication"]["partial"] = True
        self.write("transport-terminal.json", self.terminal)
        self.write("transport-checkpoint.json", {"phase": "publication-applied-1", "execution_id": "synthetic", "runtime_context": self.context})
        action = {"action": "stale-publication", "performed": True, "execution_id": "synthetic", "runtime_context": self.context, "invalid_reason": "EXECUTION_CANCELLED", "journal_failed_before_late_publish": True, "late_publication_rejected": True, "journal_failed_after_late_publish": True, "no_verified_commit": True, "late_publication_error": "FAILED_EXECUTION_PUBLICATION_DENIED"}
        self.write("control-completed.json", action)
        self.assertEqual(self.result("stale-publication")["status"], "PASS")
        action["late_publication_error"] = "GENERIC_DENIED"
        self.write("control-completed.json", action)
        self.assertFalse(self.result("stale-publication")["assertions"]["late_publication_has_typed_fence_denial"])

    def test_timeout_requires_terminal_history_and_bounded_recorded_timeout(self):
        self.history[2]["activityTaskScheduledEventAttributes"]["startToCloseTimeout"] = "40s"
        self.history = self.history[:4] + [
            {"eventId": "5", "eventType": "EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT", "activityTaskTimedOutEventAttributes": {"scheduledEventId": "3", "failure": {"timeoutFailureInfo": {"timeoutType": "TIMEOUT_TYPE_START_TO_CLOSE"}}}},
            {"eventId": "6", "eventType": "EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED", "workflowExecutionCompletedEventAttributes": {}},
        ]
        self.write_history()
        self.write("workflow-start.json", {"workflow_id": "synthetic-unit", "workflow_run_id": "synthetic-run", "activity_timeout_seconds": 40})
        self.assertEqual(self.result("timeout")["status"], "PASS")
        self.write("workflow-start.json", {"workflow_id": "synthetic-unit", "workflow_run_id": "synthetic-run", "activity_timeout_seconds": 121})
        self.assertFalse(self.result("timeout")["assertions"]["bounded_activity_timeout_recorded"])

    def test_actual_timeout_must_match_recorded_requested_duration(self):
        self.history[2]["activityTaskScheduledEventAttributes"]["startToCloseTimeout"] = "30s"
        self.history = self.history[:4] + [
            {"eventId": "5", "eventType": "EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT", "activityTaskTimedOutEventAttributes": {"scheduledEventId": "3", "failure": {"timeoutFailureInfo": {"timeoutType": "TIMEOUT_TYPE_HEARTBEAT"}}}},
            {"eventId": "6", "eventType": "EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED", "workflowExecutionCompletedEventAttributes": {}},
        ]
        self.write_history()
        self.write("workflow-start.json", {"workflow_id": "synthetic-unit", "workflow_run_id": "synthetic-run", "activity_timeout_seconds": 40})
        result = self.result("timeout")
        self.assertFalse(result["assertions"]["recorded_timeout_matches_actual_schedule"])
        self.assertFalse(result["assertions"]["actual_start_to_close_timeout_type"])

    def test_explicit_typed_wrapped_cancel_failure_is_terminal_cancellation(self):
        self.history[5] = {"eventId": "6", "eventType": "EVENT_TYPE_ACTIVITY_TASK_FAILED", "activityTaskFailedEventAttributes": {"scheduledEventId": "3", "failure": {"applicationFailureInfo": {"type": "wrapError"}, "cause": {"canceledFailureInfo": {}}}}}
        self.write_history()
        self.assertEqual(self.result()["status"], "PASS")
        self.history[5]["activityTaskFailedEventAttributes"]["failure"]["cause"] = {"applicationFailureInfo": {"type": "SECURITY_FAILURE"}}
        self.write_history()
        self.assertFalse(self.result()["assertions"]["actual_activity_cancel_terminal"])


if __name__ == "__main__":
    unittest.main()
