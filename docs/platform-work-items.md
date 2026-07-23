# Reviewed platform work items

Last reviewed: 2026-07-23

This list is derived from the merged, evidence-backed semantic reports and the
schema-v17 materialized topology. It is a review queue, not an authorization to
change the named repositories. Items that require a platform policy decision
must be resolved by the owner before a coding plan is approved.

## P0 — security boundaries

### Define the validator authentication boundary

- Scope: `ms-gateway`, the validator services, `ms-go-course`, `ms-go-rbac`,
  `ms-go-statistic`, and both frontends.
- Evidence: many validators expose their validation handler without local
  authentication; several services trust `X-User-ID`/`X-User-Role`; the gateway
  can map a missing bearer token to guest traffic.
- Owner decision: choose whether authentication is enforced only by the
  gateway, by every service, or by a documented combination of both.
- Done when: the trust boundary, required headers/tokens, guest behavior, and
  internal-route policy are documented and then represented by exact topology
  relations and tests.

### Enforce workspace path containment

- Primary scope: `ms-ts-browser-runtime-validator` and every validator that
  accepts `workspace.root_path`, entrypoints, or caller-provided file paths.
- Evidence: the browser validator joins caller-provided paths to a temporary
  work directory without an explicit documented containment check; the Git and
  HTTP-runtime validators accept external workspace roots.
- Required behavior: canonicalize the workspace root and every derived target,
  reject absolute paths, `..` traversal, symlink escapes, and writes outside the
  task workspace.
- Done when: focused traversal/symlink tests pass and the invariant is recorded
  in the affected `.ai` contract/rule manifests.

### Define and enforce command-execution isolation

- Scope: `ms-go-linux-validator`, `ms-go-http-runtime-validator`, and
  `ms-node-validator`.
- Evidence: caller-selected commands/arguments reach process execution;
  `Stage.timeoutSeconds` or runtime timeout settings are not consistently wired;
  repository text does not establish an isolation boundary.
- Owner decision: select the sandbox boundary, filesystem/network policy,
  resource limits, timeout semantics, and cancellation behavior.
- Done when: the selected boundary is implemented, timeout/cancellation tests
  pass, and commands cannot escape the approved workspace.

### Secure shared NATS infrastructure

- Scope: `ms-infra-messaging` and all publishers/subscribers.
- Evidence: the checked-in NATS configuration enables client/monitoring ports
  and JetStream without a documented production authentication/authorization
  policy; several services do not identify the broker owner explicitly.
- Owner decision: credentials/operator model, account/subject permissions,
  secret distribution, monitoring exposure, and local-versus-production
  configuration split.

## P1 — delivery and topology quality

### Establish a CI baseline

- Scope: repositories whose semantic report says no checked-in CI workflow or
  required-check policy exists.
- Minimum candidate checks: the repository-approved test/lint commands,
  `git diff --check`, dependency lock consistency, and container build where a
  Dockerfile is authoritative.
- Constraint: commands marked lifecycle, UI/watch, external-runtime, or
  state-changing remain approval-gated and must not become automatic CI steps.

### Document deployment ownership

- Scope: validators with only a Dockerfile/local Compose file, both frontends,
  `ms-infra-messaging`, and services relying on the external `ms-net` network.
- Done when: each runtime service names its deployment repository/system,
  environment-specific configuration source, network provisioning owner,
  health/readiness checks, and rollback responsibility.

### Triage contract drift

- Trusted revision at review: `a16d1cd2-33fa-4027-90eb-945d1a62a895`, with
  fingerprint
  `839401093020ddc53ac0f30b1aae20079f335ae0b0282185bb1a7c5ab62a2f91`;
  use `course-dev-orchestrator topology` to read the current revision ID.
- Current result after hardening: 92 drift rows — 45 critical missing producers
  and 47 warnings caused by multiple matching producers; 84 HTTP and 8 event.
- Order: resolve frontend/gateway prefix ownership first, then validator
  callers, then event producers. Do not invent producer relations merely to
  remove a warning.

### Complete polyglot execution profiles

- Scope: `course-dev-orchestrator` planning and verification.
- Evidence: current automatic verification allowlist supports Go and npm only;
  generic backend write scope is Go-oriented even though connected services
  also use Python, PHP, and TypeScript.
- Required behavior: derive per-project write scopes and verification commands
  from reviewed `.ai/commands.yaml`, retain approval requirements, and add
  Python/PHP/Node fixture execution tests before using those stacks in a live
  coding plan.

## P2 — repository-specific reconciliation

- Reconcile stale README/runtime differences recorded for OAuth, Tarantool,
  health paths, validator ports, NATS transports, and legacy routes.
- Decide whether `go-ms-ai-summary` is an implementation target or an
  instruction-only placeholder; it must remain outside trusted runtime
  topology until production evidence exists.
- Resolve the differing English/Russian `knowledge-tree`
  `02-generate-lesson.md` instructions before agents generate lesson content.

## Review and execution order

1. Owner decides the authentication and sandbox/path policies.
2. Create one resource-scoped plan per policy area; do not mix security and CI
   changes in one DAG.
3. Pilot the change on one service, run an independent reviewer, and inspect
   the resulting branch before expanding the wave.
4. Rescan merged defaults and rebuild topology after every merged wave.
