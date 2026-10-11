#!/usr/bin/env python3
"""Observe actual production Temporal/transport executions with no model API.

The trusted root harness owns Docker, controller faults and owned cleanup. This
script launches an explicitly supplied backend command and exchanges control
files with that harness. It never locates Docker, selects runtime resources,
creates a fixture, changes credentials, or substitutes fake Temporal events.
"""
from __future__ import annotations

import argparse
import datetime
import hashlib
import json
import os
import pathlib
import re
import signal
import subprocess
import sys
import time
from typing import Any

CASES = (
    "success", "activity-cancel", "workflow-cancel", "active-cancel",
    "before-publication", "verification", "partial-publication", "timeout",
    "controller-kill", "backend-kill", "broker-interruption", "lease-expiry",
    "runtime-unknown", "transport-disconnect", "stale-result", "stale-publication", "security-failure",
)
EXTERNAL_ACTIONS = {
    "controller-kill", "backend-kill", "broker-interruption", "lease-expiry",
    "runtime-unknown", "security-failure",
    "transport-disconnect", "stale-publication",
}
CANCEL_CASES = {
    "activity-cancel", "workflow-cancel", "active-cancel",
    "before-publication", "verification", "partial-publication", "stale-result",
    "stale-publication",
}
FAULT_PROOFS = {
    "controller-kill": ("controller_process_absent", "controller_restart_reconciled"),
    "backend-kill": ("backend_process_absent", "lease_expired"),
    "broker-interruption": ("broker_process_absent",),
    "lease-expiry": ("lease_expired", "authenticated_renewal_stopped", "expiry_observed_after_last_renewal"),
    "runtime-unknown": ("runtime_unobservable", "runtime_observable_after_reconciliation"),
    "security-failure": ("policy_rejected",),
    "transport-disconnect": ("transport_listener_unreachable", "lease_expired", "transport_listener_restored"),
    "stale-publication": ("journal_failed_before_late_publish", "late_publication_rejected", "journal_failed_after_late_publish", "no_verified_commit"),
}
FAULT_REASONS = {
    "controller-kill": {"CONTROLLER_RESTARTED", "COORDINATOR_PARENT_LOST"},
    "backend-kill": {"EXECUTION_LEASE_EXPIRED"},
    "broker-interruption": {"BROKER_INTERRUPTED", "BROKER_PROCESS_LOST", "BROKER_EXITED", "SANDBOX_LOST"},
    "lease-expiry": {"EXECUTION_LEASE_EXPIRED", "EXECUTION_LEASE_EXPIRED_OR_INVALIDATED"},
    "runtime-unknown": {"RUNTIME_UNKNOWN", "AGENT_STATE_UNKNOWN", "AGENT_CLEANUP_UNKNOWN", "RUNTIME_STATE_UNKNOWN", "CLEANUP_UNKNOWN", "OWNED_CONFIGURATION_UNKNOWN", "COORDINATOR_PROCESS_CLEANUP_UNKNOWN", "OWNED_RESOURCE_CLEANUP_UNKNOWN"},
    "security-failure": {"SDK_PRIVATE_STATE_BYTES_EXCEEDED", "SDK_PRIVATE_STATE_FILES_EXCEEDED", "COMMAND_POLICY_DENIED", "PROPOSAL_SCOPE_DENIED", "REVIEWER_WRITE_DENIED", "PUBLICATION_SCOPE_DENIED"},
    "transport-disconnect": {"EXECUTION_LEASE_EXPIRED", "RUNTIME_TRANSPORT_UNKNOWN"},
    "stale-publication": {"EXECUTION_CANCELLED", "EXECUTION_LEASE_EXPIRED", "CONTROLLER_RESTARTED"},
}
CASE_PHASES = {"before-publication": "before-publication", "verification": "verification", "partial-publication": "publication-applied-1", "stale-publication": "publication-applied-1"}
PUBLICATION_FENCE_REASONS = {
    "FAILED_EXECUTION_PUBLICATION_DENIED", "PUBLICATION_ATTEMPT_ALREADY_USED",
    "EXECUTION_LEASE_EXPIRED_OR_INVALIDATED",
}
ROLES = ("contract", "layer", "composition", "reviewer")


def checkpoint_phase(case: str, requested: str | None = None) -> str:
    if case not in CASES:
        raise ValueError("unknown certification scenario")
    expected = CASE_PHASES.get(case, "active")
    if requested is not None and requested != expected:
        raise ValueError("checkpoint phase does not match certification scenario")
    return expected


def utc() -> str:
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def acknowledged_before(returned: dict[str, Any] | None, workflow: dict[str, Any] | None) -> bool:
    if not returned or not workflow:
        return False
    try:
        first = datetime.datetime.fromisoformat(returned["time"].replace("Z", "+00:00"))
        last = datetime.datetime.fromisoformat(workflow["time"].replace("Z", "+00:00"))
        return first.tzinfo is not None and last.tzinfo is not None and first <= last
    except (KeyError, TypeError, ValueError):
        return False


def load(path: pathlib.Path, limit: int = 1 << 20) -> Any:
    def object_pairs(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                raise ValueError("duplicate evidence JSON key")
            result[key] = value
        return result
    # Files are trusted evidence, but malformed/UNKNOWN observations must never
    # be silently treated as empty success data.
    if path.is_symlink() or not path.is_file() or path.stat().st_size > limit:
        raise ValueError("invalid evidence file: " + path.name)
    return json.loads(path.read_bytes(), object_pairs_hook=object_pairs,
                      parse_constant=lambda _: (_ for _ in ()).throw(ValueError("nonfinite evidence")))


def save(path: pathlib.Path, value: Any) -> None:
    data = (json.dumps(value, sort_keys=True, indent=2, allow_nan=False) + "\n").encode()
    if len(data) > 1 << 20:
        raise ValueError("evidence size limit")
    temporary = path.with_name("." + path.name + ".tmp")
    fd = os.open(temporary, os.O_CREAT | os.O_WRONLY | os.O_TRUNC, 0o600)
    try:
        with os.fdopen(fd, "wb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def wait_file(path: pathlib.Path, timeout: float, process: subprocess.Popen | None = None) -> Any:
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        if path.exists():
            return load(path)
        if process is not None and process.poll() is not None:
            raise RuntimeError("backend exited before required checkpoint: " + path.name)
        time.sleep(0.1)
    raise TimeoutError("required checkpoint unavailable: " + path.name)


def read_history(path: pathlib.Path) -> list[dict[str, Any]]:
    if path.is_symlink() or not path.is_file() or path.stat().st_size > 8 << 20:
        raise ValueError("actual Temporal history missing or unbounded")
    events = [json.loads(line) for line in path.read_text().splitlines() if line]
    if not events or any(not isinstance(event.get("eventId"), str) for event in events):
        raise ValueError("actual Temporal history malformed")
    identities = [int(event["eventId"]) for event in events]
    if identities != list(range(1, len(identities) + 1)) or events[0].get("eventType") != "EVENT_TYPE_WORKFLOW_EXECUTION_STARTED":
        raise ValueError("actual Temporal history event identities invalid")
    return events


def analyze(output: pathlib.Path, case: str, role: str) -> dict[str, Any]:
    if case not in CASES or role not in ROLES:
        raise ValueError("unknown certification scenario or role")
    events = read_history(output / "history.jsonl")
    start = load(output / "workflow-start.json")
    activity = load(output / "activity-start.json")
    history_binding = load(output / "history-binding.json")
    terminal = load(output / "transport-terminal.json")
    boundary = load(output / "backend-boundary.json")
    actual_context = activity.get("runtime_context", {})
    scheduled = [event for event in events if event.get("activityTaskScheduledEventAttributes", {}).get("activityType", {}).get("name") == "ExecutePlanTask"]
    schedule_ids = {
        event["eventId"] for event in events
        if event.get("activityTaskScheduledEventAttributes", {}).get("activityType", {}).get("name") == "ExecutePlanTask"
    }
    execution_events = [
        event for event in events
        if any(str(attrs.get("scheduledEventId", "")) in schedule_ids
               for key, attrs in event.items()
               if key.endswith("EventAttributes") and isinstance(attrs, dict))
    ]
    execution_types = [event.get("eventType") for event in execution_events]
    failure_infos = []
    canceled_failure_seen = False
    timeout_types = []
    for event in execution_events:
        failure = event.get("activityTaskFailedEventAttributes", {}).get("failure", {})
        for _ in range(16):
            if not isinstance(failure, dict) or not failure:
                break
            if isinstance(failure.get("applicationFailureInfo"), dict):
                failure_infos.append(failure["applicationFailureInfo"])
            if isinstance(failure.get("canceledFailureInfo"), dict):
                canceled_failure_seen = True
            failure = failure.get("cause", {})
        if event.get("eventType") == "EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT":
            timeout_info = event.get("activityTaskTimedOutEventAttributes", {}).get("failure", {}).get("timeoutFailureInfo", {})
            if isinstance(timeout_info.get("timeoutType"), str):
                timeout_types.append(timeout_info["timeoutType"])
    returned = load(output / "runner-return.json") if (output / "runner-return.json").exists() else None
    workflow = load(output / "workflow-result.json") if (output / "workflow-result.json").exists() else None
    records = [json.loads(line) for line in (output / "task-records.jsonl").read_text().splitlines()] if (output / "task-records.jsonl").exists() else []
    completed_task = any(record.get("result", {}).get("status") == "completed" for record in records)
    workflow_output = workflow.get("output", {}) if workflow else {}
    completed_task = completed_task or workflow_output.get("task_status", {}).get("fixture") == "completed"
    cleanup = terminal.get("cleanup", {})
    publication = terminal.get("publication", {})
    observed_context = terminal.get("runtime_context", {})
    backend_authority = terminal.get("backend_authority", {})
    workflow_terminal = any(event.get("eventType") in {
        "EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED", "EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED",
        "EVENT_TYPE_WORKFLOW_EXECUTION_FAILED", "EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT",
        "EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED",
    } for event in events)
    assertions = {
        "actual_execution_activity_scheduled_once": len(schedule_ids) == 1,
        "actual_execution_activity_started": "EVENT_TYPE_ACTIVITY_TASK_STARTED" in execution_types,
        "trusted_activity_context_bound": activity.get("trusted_runtime_context_bound") is True,
        "workflow_identity_matches": actual_context.get("workflow_id") == start.get("workflow_id"),
        "workflow_run_identity_matches": bool(start.get("workflow_run_id")) and actual_context.get("workflow_run_id") == start.get("workflow_run_id"),
        "history_exact_run_identity_matches": history_binding.get("api") == "GetWorkflowHistory" and history_binding.get("workflow_id") == start.get("workflow_id") and history_binding.get("workflow_run_id") == start.get("workflow_run_id"),
        "history_complete_bytes_match": history_binding.get("bytes") == (output / "history.jsonl").stat().st_size,
        "history_content_hash_matches": history_binding.get("sha256") == hashlib.sha256((output / "history.jsonl").read_bytes()).hexdigest(),
        "scheduled_activity_identity_matches": len(scheduled) == 1 and scheduled[0]["activityTaskScheduledEventAttributes"].get("activityId") == actual_context.get("activity_id"),
        "activity_identity_matches": actual_context.get("activity_id") == activity.get("activity_id"),
        "attempt_identity_matches": actual_context.get("activity_attempt") == activity.get("attempt") == 1,
        "controller_bound_actual_temporal_identity": observed_context == actual_context,
        "backend_path_and_conventional_socket_absence": boundary.get("docker_cli_absent") is True and boundary.get("docker_socket_absent") is True,
        "backend_oci_has_no_daemon_authority": backend_authority.get("no_docker_socket_mount") is True and backend_authority.get("no_remote_docker_authority") is True,
        "owned_command_positive_absence": cleanup.get("command_absent") is True,
        "owned_sdk_positive_absence": cleanup.get("sdk_absent") is True,
        "private_state_positive_absence": cleanup.get("private_state_absent") is True,
        "cleanup_observable": cleanup.get("state") == "CLEANED",
        "no_real_model_calls": type(terminal.get("real_model_calls")) is int and terminal["real_model_calls"] == 0,
        "same_role": terminal.get("role") == role,
    }
    result = returned.get("result", {}) if returned else {}
    if returned is not None:
        assertions["returned_runtime_context_matches"] = returned.get("runtime_context") == actual_context
    if case == "success":
        assertions.update({
            "runner_result_present": bool(result.get("result")),
            "runner_error_absent": returned is not None and returned.get("error") == "<nil>",
            "actual_execution_completed": "EVENT_TYPE_ACTIVITY_TASK_COMPLETED" in execution_types,
            "task_completed": completed_task,
            "actual_workflow_completed": workflow_terminal and workflow_output.get("status") == "completed",
            "workflow_result_exact_run_matches": workflow is not None and workflow.get("workflow_id") == start.get("workflow_id") and workflow.get("workflow_run_id") == start.get("workflow_run_id"),
            "publication_complete": publication.get("status") == "COMPLETE",
            "transport_verified": terminal.get("status") == "VERIFIED",
            "returned_execution_identity_matches": result.get("execution_id") == terminal.get("execution_id") and bool(terminal.get("execution_id")),
            "returned_attempt_matches": type(result.get("attempt")) is int and result.get("attempt") == actual_context.get("activity_attempt") == 1,
        })
    else:
        assertions.update({
            "no_runner_result": not result.get("result"),
            "no_task_completed": not completed_task,
            "no_transport_verified": terminal.get("status") != "VERIFIED",
            "publication_invalid": publication.get("status") in ("FAILED", "PENDING"),
            "no_execution_completed": "EVENT_TYPE_ACTIVITY_TASK_COMPLETED" not in execution_types,
        })
    if case in CANCEL_CASES:
        assertions["actual_activity_cancel_requested"] = "EVENT_TYPE_ACTIVITY_TASK_CANCEL_REQUESTED" in execution_types
        cancel_ack = load(output / "activity-context-cancel.json") if (output / "activity-context-cancel.json").exists() else {}
        assertions["activity_context_cancel_acknowledged"] = cancel_ack.get("runtime_context") == actual_context and cancel_ack.get("error") in ("context canceled", "context deadline exceeded")
        assertions["runner_cancel_acknowledged"] = returned is not None and returned.get("cancelled") is True and returned.get("error") != "<nil>"
        assertions["workflow_terminal_observed"] = workflow_terminal and workflow is not None
        assertions["workflow_result_exact_run_matches"] = workflow is not None and workflow.get("workflow_id") == start.get("workflow_id") and workflow.get("workflow_run_id") == start.get("workflow_run_id")
        if case != "workflow-cancel":
            # The pinned Go SDK can report a terminal FAILED event with a typed
            # canceledFailureInfo cause. A generic FAILED event is insufficient.
            assertions["actual_activity_cancel_terminal"] = "EVENT_TYPE_ACTIVITY_TASK_CANCELED" in execution_types or canceled_failure_seen
            assertions["runner_ack_precedes_workflow_terminal"] = acknowledged_before(returned, workflow)
    if case == "workflow-cancel":
        assertions["actual_workflow_cancel_requested"] = any(event.get("eventType") == "EVENT_TYPE_WORKFLOW_EXECUTION_CANCEL_REQUESTED" for event in events)
    if case == "timeout":
        assertions["actual_activity_timed_out"] = "EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT" in execution_types
        assertions["actual_workflow_terminal_after_timeout"] = workflow_terminal
        assertions["timeout_runner_acknowledged"] = returned is not None and returned.get("error") not in (None, "<nil>") and returned.get("cancelled") is True
        timeout_limit = start.get("activity_timeout_seconds")
        assertions["bounded_activity_timeout_recorded"] = type(timeout_limit) is int and 1 <= timeout_limit <= 120
        scheduled_timeout = scheduled[0]["activityTaskScheduledEventAttributes"].get("startToCloseTimeout") if len(scheduled) == 1 else None
        assertions["recorded_timeout_matches_actual_schedule"] = type(timeout_limit) is int and scheduled_timeout == str(timeout_limit) + "s"
        assertions["actual_start_to_close_timeout_type"] = "TIMEOUT_TYPE_START_TO_CLOSE" in timeout_types
    if case in ("partial-publication", "stale-publication"):
        assertions["actual_partial_publication_invalidated"] = publication.get("partial") is True and publication.get("status") == "FAILED"
    if case == "runtime-unknown":
        assertions["unknown_not_reclassified_success"] = terminal.get("unknown_latched") is True
        assertions["reconciliation_positive_observation"] = cleanup.get("reconciled") is True
        action = load(output / "control-completed.json")
        assertions["unknown_did_not_report_cleanup_pass"] = action.get("cleanup_state_while_unobservable") == "UNKNOWN" and action.get("cleanup_proven_while_unobservable") is False
        assertions["unknown_execution_result_not_revived"] = terminal.get("status") == "UNKNOWN"
    if case == "stale-result":
        rejected = load(output / "stale-observation.json")
        signal_events = {int(event["eventId"]) for event in events if event.get("workflowExecutionSignaledEventAttributes", {}).get("signalName") == "plan.task_result"}
        assertions["stale_green_not_closed_workflow"] = rejected.get("workflow_status") == "Running"
        assertions["stale_green_not_completed_task"] = rejected.get("task_status") == "running" and rejected.get("query_error") == "<nil>"
        assertions["stale_signal_observed_in_actual_history"] = rejected.get("signal_event_id") in signal_events
        assertions["stale_observation_exact_run_matches"] = rejected.get("workflow_id") == start.get("workflow_id") and rejected.get("workflow_run_id") == start.get("workflow_run_id")
    if case not in ("success", "security-failure"):
        checkpoint = load(output / "transport-checkpoint.json")
        assertions["case_checkpoint_phase_and_identity_matches"] = checkpoint.get("phase") == CASE_PHASES.get(case, "active") and checkpoint.get("execution_id") == terminal.get("execution_id") and checkpoint.get("runtime_context") == actual_context
    if case in EXTERNAL_ACTIONS:
        action = load(output / "control-completed.json")
        assertions["root_action_performed_for_exact_execution"] = action.get("performed") is True and action.get("action") == case and action.get("execution_id") == terminal.get("execution_id") and action.get("runtime_context") == actual_context
        reason = terminal.get("invalid_reason")
        assertions["typed_failure_matches_observed_fault"] = isinstance(reason, str) and re.fullmatch(r"[A-Z][A-Z0-9_]{0,127}", reason) is not None and action.get("invalid_reason") == reason
        assertions["typed_failure_matches_case"] = reason in FAULT_REASONS[case] if isinstance(reason, str) else False
        for proof in FAULT_PROOFS[case]:
            assertions["fault_observed_" + proof] = action.get(proof) is True
        if case == "stale-publication":
            assertions["late_publication_has_typed_fence_denial"] = action.get("late_publication_error") in PUBLICATION_FENCE_REASONS
    if case == "backend-kill":
        assertions["backend_loss_activity_timed_out"] = "EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT" in execution_types
        observer = load(output / "observer-complete.json")
        assertions["backend_loss_observer_exact_run_matches"] = observer.get("workflow_id") == start.get("workflow_id") and observer.get("workflow_run_id") == start.get("workflow_run_id")
        assertions["backend_loss_observer_cannot_execute_agent"] = observer.get("started_execution") is False and observer.get("mode") == "BOOKKEEPING_ONLY" and observer.get("registered_activity_names") == ["SetPlanRunStatus", "RecordPlanTaskResult"]
        observer_limit = observer.get("observer_timeout_seconds")
        assertions["backend_loss_observer_wait_bounded"] = type(observer_limit) is int and 150 <= observer_limit <= 300
        assertions["backend_loss_observer_persisted_terminal_result"] = workflow is not None and workflow.get("bookkeeping_observer") is True and workflow.get("workflow_id") == start.get("workflow_id") and workflow.get("workflow_run_id") == start.get("workflow_run_id") and workflow.get("error") == "<nil>" and workflow_output.get("status") == "failed" and workflow_output.get("task_status", {}).get("fixture") == "failed"
    if case in EXTERNAL_ACTIONS:
        assertions["actual_activity_terminal_after_fault"] = any(kind in execution_types for kind in ("EVENT_TYPE_ACTIVITY_TASK_FAILED", "EVENT_TYPE_ACTIVITY_TASK_CANCELED", "EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT"))
        assertions["actual_workflow_terminal_after_fault"] = workflow_terminal
        if case != "backend-kill":
            assertions["surviving_backend_acknowledged_failure"] = returned is not None and returned.get("error") not in (None, "<nil>") and not result.get("result")
        if case not in ("backend-kill", "stale-publication"):
            assertions["actual_failure_nonretryable_classification"] = any(info.get("nonRetryable") is True for info in failure_infos)
        if case in ("runtime-unknown", "transport-disconnect"):
            assertions["actual_unknown_failure_classification"] = any(info.get("type") == "UNKNOWN_RUNTIME" for info in failure_infos)
        if case == "security-failure":
            assertions["actual_security_failure_classification"] = any(info.get("type") == "SECURITY_FAILURE" for info in failure_infos)
    return {
        "case": case, "role": role, "observed_at": utc(),
        "workflow_id": start["workflow_id"], "runtime_context": actual_context,
        "execution_id": terminal.get("execution_id"),
        "execution_activity_schedule_ids": sorted(schedule_ids),
        "execution_history_types": execution_types, "assertions": assertions,
        "actual_application_failure_classifications": failure_infos,
        "actual_typed_cancellation_failure": canceled_failure_seen,
        "actual_timeout_types": timeout_types,
        "status": "PASS" if all(assertions.values()) else "FAIL",
        "evidence_kind": "ACTUAL_TEMPORAL_HISTORY_AND_TRUSTED_CONTROLLER_OBSERVATIONS",
    }


def run(args: argparse.Namespace) -> int:
    output = pathlib.Path(args.output).resolve()
    if not output.is_dir():
        raise ValueError("trusted root must create evidence directory")
    expected_phase = checkpoint_phase(args.case, args.checkpoint_phase)
    if args.analyze_only:
        result = analyze(output, args.case, args.role)
        save(output / "transport-temporal-result.json", result)
        print(json.dumps({"case": args.case, "status": result["status"], "assertions": result["assertions"]}))
        return 0 if result["status"] == "PASS" else 1
    if not args.backend_command_json:
        raise ValueError("explicit trusted backend launch argv required")
    argv = load(pathlib.Path(args.backend_command_json))
    if not isinstance(argv, list) or not argv or any(not isinstance(arg, str) or not arg or "\x00" in arg for arg in argv):
        raise ValueError("backend launch argv invalid")
    generated = (
        "cancel", "stale", "control-request.json", "control-completed.json", "workflow-start.json",
        "activity-start.json", "runner-return.json", "workflow-result.json", "history.jsonl", "history-binding.json",
        "transport-terminal.json", "task-records.jsonl", "plan-records.jsonl", "activity-context-cancel.json",
        "backend-boundary.json", "stale-sent.json", "stale-observation.json", "transport-checkpoint.json",
        "transport-temporal-result.json", "transport-temporal-error.json", "observer-complete.json",
    )
    if any((output / name).exists() for name in generated):
        raise ValueError("fresh case control files required")
    environment = {**os.environ, "CODEX_SOL_MAX_RUNS_5H": "20", "CODEX_BUDGET_MODE": "enforce"}
    with (output / "backend-launch.log").open("w") as log:
        process = subprocess.Popen(argv, env=environment, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        try:
            wait_file(output / "workflow-start.json", args.timeout, process)
            wait_file(output / "activity-start.json", args.timeout, process)
            if args.case not in ("success", "security-failure"):
                checkpoint = wait_file(output / "transport-checkpoint.json", args.timeout, process)
                if checkpoint.get("phase") != expected_phase or not checkpoint.get("execution_id"):
                    raise RuntimeError("trusted controller checkpoint does not match requested phase")
            if args.case == "stale-result":
                (output / "stale").touch(mode=0o600)
                wait_file(output / "stale-sent.json", args.timeout, process)
                wait_file(output / "stale-observation.json", args.timeout, process)
            if args.case in CANCEL_CASES:
                (output / "cancel").touch(mode=0o600)
            if args.case in EXTERNAL_ACTIONS:
                save(output / "control-request.json", {"case": args.case, "action": args.case, "requested_at": utc()})
                completed = wait_file(output / "control-completed.json", args.timeout)
                if completed.get("action") != args.case or completed.get("performed") is not True:
                    raise RuntimeError("root-owned action not positively observed")
            process.wait(timeout=args.timeout)
            # Interrupted backend cases need a separate root-owned observer.
            wait_file(output / "transport-terminal.json", args.timeout)
            end = time.monotonic() + args.timeout
            while not (output / "history.jsonl").exists() and time.monotonic() < end:
                time.sleep(0.1)
            result = analyze(output, args.case, args.role)
            save(output / "transport-temporal-result.json", result)
            print(json.dumps({"case": args.case, "status": result["status"], "assertions": result["assertions"]}))
            return 0 if result["status"] == "PASS" else 1
        finally:
            if process.poll() is None:
                # The launch process belongs to this harness; controller/container
                # cleanup remains the trusted root's responsibility and is not PASS.
                os.killpg(process.pid, signal.SIGTERM)
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True)
    parser.add_argument("--case", choices=CASES, required=True)
    parser.add_argument("--role", choices=ROLES, required=True)
    parser.add_argument("--backend-command-json")
    parser.add_argument("--checkpoint-phase", help="Optional exact phase; otherwise derived from the scenario")
    parser.add_argument("--timeout", type=float, default=180)
    parser.add_argument("--analyze-only", action="store_true")
    args = parser.parse_args()
    if not 1 <= args.timeout <= 300:
        parser.error("timeout must be within 1..300 seconds")
    try:
        raise SystemExit(run(args))
    except (OSError, ValueError, RuntimeError, TimeoutError, subprocess.TimeoutExpired) as error:
        output = pathlib.Path(args.output).resolve()
        if output.is_dir():
            save(output / "transport-temporal-error.json", {"status": "FAIL", "error": str(error), "observed_at": utc()})
        print(json.dumps({"status": "FAIL", "error": str(error)}), file=sys.stderr)
        raise SystemExit(1)


if __name__ == "__main__":
    main()
