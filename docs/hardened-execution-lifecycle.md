# Hardened execution lifecycle

This certification slice preserves production admission DENIED and readiness NO.
It does not authorize a release, a model canary or product execution.

## Authoritative states and result acceptance

The runner audit uses existing `domain.TaskAttemptStatus` terminology:
`created → admitted → running → verification → completed`. Terminal abnormal
states are `failed`, `cancelled`, `timed_out`, `sandbox_lost` and
`infrastructure_unknown`. Task/Shard VERIFIED still requires the orchestrator's
independent GREEN, scope/frozen-input checks and managed commit verification.
The runner audit is not an additional task scheduler or a DB status migration.
No new attempt statuses are written into DB constraints by this audit.

Each ProcessRunner invocation has a fresh trusted execution UUID and an accepted
attempt number, echoed by the trusted runner envelope. Model result JSON cannot
choose these fields. Wrong execution/attempt, cancelled context, process error,
invalid publication journal or unproven cleanup rejects the result. Executable
Plan workflows accept their activity future's result; external GREEN signals
cannot replace it. Commit boundaries reload the newest active durable Task/Shard/Composition attempt
and reject a superseded/invalidated attempt before creating a
verified shard/composition commit. SDK success is not task approval.

## Cancellation and timeout

The production PlanRunner cancel signal and client workflow cancellation both
cancel active activities and join their futures before terminal cancellation.
Activities heartbeat during execution; real cancellation delivery therefore
occurs on heartbeat and is not instantaneous. A disconnected workflow context
permits teardown acknowledgment and cancellation persistence after parent cancel.

ProcessRunner first signals the trusted Node runner with SIGTERM, allowing OCI
and SDK teardown, with bounded process group SIGKILL escalation after 45 seconds.
Node watches its trusted parent and aborts on parent loss. Closed orchestrator
output pipes trigger teardown instead of unhandled EPIPE termination. The broker watches
runner death; the standalone publisher also watches runner death. Result emission
occurs only after successful cleanup. Failure persistence uses bounded contexts
independent of the cancelled execution context.

Plan execution defaults remain StartToClose 2h, ScheduleToClose 6h, heartbeat45s;
a trusted schedule can tighten StartToClose. ProcessRunner has a 20m bound;
broker commands have60s, private execution1200s. Certification exercises outer
6s Temporal timeout and inner60s command timeout independently with real OCI.
SDK, staging/build cache and descendants must disappear for observable cleanup.

## Retry classification

Activity taxonomy: RETRYABLE_INFRASTRUCTURE for explicitly transient errors;
NON_RETRYABLE_POLICY, CANCELLED, SECURITY_FAILURE, UNKNOWN_RUNTIME and TIMED_OUT
for terminal paths. Execution activities have MaximumAttempts1: no automatic
execution retry. Existing owner-directed task recovery remains explicit and
allocates a fresh runner identity. Metadata activity retries are unchanged.

The pinned Go Temporal SDK1.35.0 records the exercised cancelled activity as
ActivityTaskFailed with a nested canceled failure and RETRY_STATE_CANCEL_REQUESTED
(`unexpected activity cancel error`), rather than ActivityTaskCanceled. Actual
cancel request, cancelled activity context, teardown and terminal workflow are
all retained in history/evidence. Certification does not claim a nonexistent
ActivityTaskCanceled event or change SDK dependencies to alter this serialization.

## Publication safety

The existing fsynced PENDING/FAILED journal permanently rejects reuse. Cancellation
of async trusted publication can invalidate COMPLETE before any result is emitted.
After the first file replacement, cancellation retains partial disposable content
and FAILED/PENDING invalidation; it does not introduce repository-wide rollback.
A publisher whose runner dies invalidates and removes its private control state.

## UNKNOWN and reconciliation

Docker daemon errors/timeouts never establish absence. Concurrent removal is
observed with bounded polling; only trusted `no such object/container` proves
absence. An UNKNOWN attempt retains its control root and durable audit.
Command and SDK OCI resources carry trusted `cdo.execution_id` and resource labels.
`cdo_reconcile.py` accepts only an UNKNOWN audit, validates identity/resource/root,
removes precisely matching disposable resources, verifies an empty positive list,
then removes mutable control state. The terminal UNKNOWN status is retained with
reconciliation evidence; it never becomes success. The immutable Go bundle stays.

Fault injection replaces only the test execution's Docker client transport and
pauses its own disposable command container; it does not stop/reset the daemon.
Trusted observer uses the healthy real daemon, then reconciles both retained OCI
resources. No product services or volumes are changed.

## Evidence and commands

Approved commands are declared in `.ai/commands.yaml`: lifecycle_temporal_certification,
lifecycle_process_isolation and lifecycle_reconcile. Trusted certification gates
are active only in explicit hardened-certification mode and are never mounted or
exposed to the SDK/model. They deterministically hold verification/publication
race windows; interruption and cancellation still use actual Temporal/ProcessRunner.

The integration driver registers production PlanWorkflow and PlanActivities,
with disposable repository status adapters and a fixture executor invoking the
actual ProcessRunner. This exercises cancellation boundaries without approved
product Plans, real worktrees or commits. Both actual SDK and OCI run; the Responses
provider is deterministic and credential-free. No model API is called.

Full report, per-run history JSONL, exact event/attempt timestamps, trusted OCI
observations, rejected pre-fix evidence, tests and hashes are retained under
`.cache/sandbox-certification/lifecycle-temporal` and
`.cache/sandbox-certification/LIFECYCLE_TEMPORAL_CERTIFICATION_RESULT.md`.
