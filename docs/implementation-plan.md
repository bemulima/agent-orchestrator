# Course development orchestrator implementation plan

## Goal

Deliver an internal, single-owner orchestrator that can discover a service
landscape, prepare approved multi-repository changes, execute a bounded DAG of
Codex tasks through Temporal, verify the result, and report through CLI,
Telegram, and self-hosted GitLab. Automatic merge and deploy are always out of
scope.

## Invariants used in every stage

- Discovery is read-only until an explicit approval is persisted.
- Repository paths must resolve below configured allowlisted roots.
- One task owns one repository, worktree, branch, and Codex thread.
- Every write has an idempotency key and every material transition emits an
  audit event.
- Agent output is JSON Schema validated and then independently checked against
  Git, write scope, tests, lint, migrations, and contracts.
- Plans are acyclic, always require approval in the MVP, and run at most three
  tasks concurrently by default.
- No secret, `.env` value, access token, or unredacted agent prompt is logged or
  sent to an agent.

## Stage 1 — platform bootstrap

Scope:

- Go module and reference-compatible layered layout;
- environment configuration, model profiles, and safety limits;
- PostgreSQL pool and complete initial schema for the core entities;
- reversible, tracked SQL migrations;
- Temporal server, UI, worker, and a deterministic system probe workflow;
- CLI process modes, HTTP liveness/readiness, structured logging, and graceful
  shutdown;
- Docker image, Docker Compose, Make targets, unit tests, workflow tests, and a
  PostgreSQL integration smoke test.

Acceptance:

- `make verify` passes;
- `docker compose config` is valid;
- `make up` starts PostgreSQL, Temporal, API, worker, and Temporal UI;
- `make migrate` is idempotent and `GET /ready` reports dependency state;
- `make workflow-probe` completes through the worker.

## Stage 2 — projects and read-only discovery

Scope:

- idempotent local-path and Git URL connection;
- canonical path validation against `REPOSITORY_ALLOWED_ROOTS`;
- collision-safe managed clones under `REPOSITORY_STORAGE_PATH`;
- project repository/use cases, HTTP and CLI operations;
- bounded file inventory with explicit exclusions and no `.env` reads;
- detectors for stack, service kind, capabilities, ownership, HTTP/events,
  database, gateway, frontend, infrastructure, commands, prompts, and
  conflicts;
- evidence-rich discovery report and fixtures for Go, Next.js, gateway,
  infrastructure, prompts, existing `.ai`, conflicts, and unknown projects.

Acceptance:

- connecting the same source twice returns the same project;
- paths outside allowed roots and non-Git directories are rejected;
- every discovered fact includes confidence, source path, and explanation;
- fixture-based discovery and HTTP/repository/idempotency tests pass.

## Stage 3 — onboarding proposals and approved writes

Scope:

- generate only evidence-backed `.ai/*` and an `AGENTS.md` merge proposal;
- preserve existing rules and prompts, surfacing duplication/conflicts;
- store proposed files and unified diff without touching the source checkout;
- approval/rejection state machine and dry-run;
- approved worktree, `ai/onboard-{service}` branch, checks, commit, optional
  push, and GitLab merge request.

Acceptance:

- source files are unchanged before approval;
- repeated prepare/apply calls are idempotent;
- existing `.ai` and prompt fixtures retain user-authored instructions;
- apply writes only the approved proposal inside a dedicated worktree.

## Stage 4 — topology and contract drift

Scope:

- persist capabilities, ownership, relations, and versioned contracts;
- rebuild/query platform topology and impact paths;
- correlate producer/consumer descriptions and persist contract drift;
- expose topology, dependencies, consumers, contracts, and drift via API/CLI.

Acceptance:

- capability owners and transitive impacts are deterministic;
- producer/consumer fixture differences yield severity-ranked drift;
- topology rebuild is idempotent and leaves no stale relations.

## Stage 5 — commands, planning, DAG, and Temporal plan workflow

Scope:

- natural-language command capture from API/CLI;
- structured planner input/output and plan persistence;
- one-repository tasks with acceptance criteria, write scopes, risks,
  migration/contract flags, priorities, and dependencies;
- DAG validation, approval, pause/resume/cancel, bounded parallel execution,
  heartbeat, retries, and restart recovery in Temporal.

Acceptance:

- cyclic, incomplete, or over-parallelized plans cannot start;
- repeated approval/start calls do not duplicate workflows or tasks;
- Temporal tests cover recovery, retry, pause, cancel, and dependency order.

## Stage 6 — Codex execution, verification, and review

Scope:

- TypeScript runner using `@openai/codex-sdk` and configurable model profiles;
- per-task Codex thread creation/resume and immediate thread ID persistence;
- JSON Schema validation for coder and reviewer results;
- bounded dependent-task handoff through the orchestrator;
- independent Git/write-scope/check/contract/migration verification;
- separate reviewer invocation and changes-requested loop.

Acceptance:

- a fixture task produces a real diff and validated structured result;
- claims not supported by Git/check output fail verification;
- reviewer never reuses the coder thread;
- attempt, review, replan, depth, and parallel limits are enforced.

## Stage 7 — self-hosted GitLab

Scope:

- configurable REST client for any GitLab base URL;
- plan/child issues, labels, checklist, links, comments, branches, and merge
  requests;
- signed webhook processing and status synchronization;
- fake adapter, dry-run adapter, and idempotency mapping via `GitLabLink`.

Acceptance:

- retries do not duplicate issues, branches, or merge requests;
- webhook secrets and state transitions are validated;
- merge and deploy operations do not exist in the adapter.

## Stage 8 — Telegram owner interface

Scope:

- long polling by default and optional signed webhook;
- allowlisted user/chat checks and all specified slash commands;
- natural text routed to the same application operations as HTTP/CLI;
- resource-bound, expiring inline approvals with replay protection;
- concise status/failure messages and GitLab links for large outputs.

Acceptance:

- unauthorized user/chat, stale callback, replay, and text-only dangerous
  approvals are rejected;
- fake adapter tests cover every command and approval callback;
- tokens, secrets, full prompts, large logs, and large diffs never appear in
  Telegram payloads.

## Final MVP verification

Run a clean end-to-end fixture scenario: bootstrap, migrate, connect, discover,
prepare onboarding, approve/apply, rebuild topology, create/approve a plan,
execute one Codex task, verify and review it, observe it in Telegram, create
GitLab issue/MR in integration mode, restart the stack during execution, and
confirm Temporal resumes without duplication.
