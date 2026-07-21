# Implementation progress

Last updated: 2026-07-21

## Current status

Stages 1–7 are complete and verified. The Docker Compose stack is currently
running with PostgreSQL, Temporal, Temporal UI, the HTTP API, and the worker.
No user repository has been connected. Stage 6 verification used only
in-memory fakes, disposable Git repositories, and disposable PostgreSQL
records. No live Codex request was made without an explicitly configured key.
Stage 7 used fake/dry-run GitLab adapters, a local HTTP server, and disposable
PostgreSQL rows; no real GitLab project, issue, branch, or MR was changed.
Stage 8 has not been implemented and is not represented by placeholders.

## Completed

- Read the complete product specification.
- Inspected the reference repository instructions, documentation, module
  dependencies, composition root, configuration, domain/repository contracts,
  use cases, HTTP router/handlers, pgx adapters, migrations, tests, Docker,
  Compose, Makefile, Taskfile, logging, and error handling.
- Recorded the eight implementation stages and cross-cutting safety
  invariants.
- Created the Go module with the reference-compatible `cmd`, `domain`,
  `usecase`, and `adapters` dependency direction and explicit DI.
- Added environment configuration with absolute repository paths, filesystem
  root rejection, execution limits, redacted diagnostics, integration secrets,
  and configurable `fast`, `standard`, `deep`, and `review` model profiles.
- Added the minimum requested domain entities and a reversible PostgreSQL
  migration with foreign keys, status/value checks, JSONB fields, indexes, and
  idempotency constraints.
- Added tracked, transactional `migrate`/`migrate-down` scripts.
- Added `GET /health`, `GET /ready`, a common JSON error envelope, request IDs,
  structured zap request logs, dependency checks, and graceful shutdown.
- Added a Temporal worker, deterministic system probe workflow, probe activity,
  retry policy, structured zap adapter, and CLI probe command.
- Added a non-root multi-stage Docker image and Compose services for PostgreSQL,
  Temporal Server, Temporal UI, API, and worker.
- Added Make targets for bootstrap, lifecycle, migrations, Temporal UI, local
  API/worker/probe, configuration validation, formatting, lint, unit tests,
  integration tests, and verification.
- Verified a clean first start after deleting only the disposable test volumes;
  Temporal auto-setup created its own databases successfully.
- Added a reversible Stage 2 schema for repository roles, normalized source
  identity, branch/commit/dirty state, and idempotent discovery fingerprints.
- Implemented the pgx `ProjectRepository`, transactional audit events,
  idempotent project upsert, immutable versioned snapshots, latest-report
  lookup, and unchanged-snapshot reuse.
- Implemented allowlisted local Git resolution with canonical/symlink checks,
  worktree deduplication through Git common-dir identity, remote/default-branch
  detection, and clean/dirty state capture.
- Implemented validated HTTPS/HTTP/SSH/scp Git URLs, credential rejection,
  normalized cross-protocol identity, collision-safe managed clone paths, and
  no-overwrite/idempotent clone behavior.
- Added repository roles separate from runtime service kinds: service,
  frontend, infrastructure, content, policy, documentation, archive, and
  unknown.
- Implemented bounded read-only discovery with file/byte/depth limits,
  explicit cache/build/dependency exclusions, no private `.env` reads, key and
  certificate exclusions, and sanitized environment-example key extraction.
- Added evidence-rich detectors for stack, runtime service kind, purpose,
  capabilities, ownership, HTTP/event/database contracts, gateway/frontend/
  infrastructure relations, repository commands, prompts, existing `.ai`, and
  conflicts.
- Added project connect/list/show/scan/report operations through CLI, Make, and
  the five Stage 2 `/api/v1/projects` endpoints.
- Added fixtures for Go, Next.js, gateway, infrastructure, prompts, existing
  `.ai`, conflicts, and unknown repositories, plus disposable Git and
  PostgreSQL integration coverage.
- Added the reversible `004_stage3_onboarding` schema with durable proposal,
  diff, approval, status, worktree, commit, checks, error, and audit metadata.
- Implemented an idempotent PostgreSQL onboarding state machine for proposal
  preparation, approval/rejection, approval-gated apply, completion, and
  failure.
- Implemented deterministic, size-bounded proposal generation for
  evidence-backed `AGENTS.md` and `.ai/**` files, with portable repository
  metadata and exact discovery provenance.
- Preserved existing user-authored Markdown and YAML values, recorded merge
  conflicts without overwriting them, linked prompt/instruction paths without
  copying content, and rejected symlinked targets.
- Added proposal/file checksums and unified diffs while keeping the connected
  checkout unchanged before approval.
- Implemented dry-run validation and real apply in a deterministic isolated
  worktree/`ai/onboard-*` branch. Apply uses atomic file replacement, stages
  only the approved scope, runs Git diff checks, commits with configurable
  identity, and verifies the source checkout remains clean at the base commit.
- Added the minimal Stage 3 GitLab publisher: host validation, bounded API
  responses, approval-gated branch push, idempotent open-MR reuse/creation,
  persisted `GitLabLink`, `merge_request_created` transition, and external
  write suppression while `GITLAB_DRY_RUN=true` (the default).
- Added the six Stage 3 HTTP endpoints plus `project-onboard`, `project-diff`,
  `project-approve`, `project-reject`, and `project-apply` CLI/Make commands.
- Added generator, existing-rule conflict, symlink, approval gate, worktree
  idempotency/isolation, exact approved-file scope, GitLab dry-run/MR
  idempotency, HTTP contract, migration, and PostgreSQL state-machine tests.
- Added the reversible `005_stage4_topology` schema with revision fingerprints,
  materialized services, snapshot provenance, indexes, severity-constrained
  contract drift, and audit events.
- Extended discovery with explicit HTTP producer/consumer and event
  publisher/subscriber evidence while preserving bounded read-only behavior.
- Implemented the deterministic topology builder for runtime services,
  purpose/stack, capabilities, ownership, versioned contracts, gateway,
  frontend and infrastructure relations, and canonical project aliases.
- Correlated producers and consumers across HTTP paths and event subjects,
  persisting missing-producer, version-mismatch, and multiple-producer drift
  with ranked severity, machine-readable differences, and suggested actions.
- Implemented transactional PostgreSQL replacement under an advisory lock.
  Unchanged fingerprints reuse the current revision; changed rebuilds remove
  all stale materialized rows atomically and emit one audit event.
- Added direct dependency/consumer queries and deterministic transitive impact
  traversal, with project lookup by UUID or unique name.
- Added Stage 4 CLI/Make commands (`topology`, `contracts`, `contract-drift`,
  `dependencies`, `consumers`) and eight topology/project HTTP routes with
  search/filter parameters.
- Added deterministic builder/drift/impact unit tests, HTTP contract tests,
  discovery contract assertions, and a real PostgreSQL topology idempotency
  integration test.
- Added the reversible `006_stage5_planning` schema for structured planner
  input/output, plan fingerprints, approvals, task execution metadata, and
  durable plan runs.
- Implemented idempotent natural-language command capture and a deterministic
  evidence planner over the latest topology revision. It selects explicit or
  matched projects, expands direct relations, creates one task per repository,
  and persists risks, acceptance criteria, write scopes, verification commands,
  migration/contract flags, priorities, and dependencies.
- Added DAG validation for project existence, completeness, duplicate task
  ownership, dependency references, cycles, maximum depth, model profiles,
  write scopes, and bounded parallel waves.
- Implemented the approval-gated PostgreSQL planning state machine. Repeated
  command/plan/approval/run calls reuse the same records, terminal transitions
  are guarded, and all material changes emit audit events.
- Added the deterministic Temporal plan workflow with dependency-aware bounded
  dispatch, activity heartbeat/retry, pause/resume/cancel and task-result
  signals, workflow state queries, and worker restart recovery.
- Added Stage 5 command/plan/run/task use cases, the fourteen HTTP routes, CLI
  commands, and Make wrappers. Stage 5 dispatch intentionally ends at task
  `ready`; actual Codex execution and verification remain Stage 6.
- Added planner/validator, workflow, HTTP routing, migration, and PostgreSQL
  state-machine/idempotency coverage.
- Added the pinned TypeScript `@openai/codex-sdk` runner with bounded JSONL,
  streaming thread persistence, new/resumed threads, coder workspace-write,
  reviewer read-only mode, disabled network/approvals, structured output, and
  an explicit secret-free subprocess environment.
- Added embedded strict JSON Schemas and semantic validation for coder and
  reviewer results, including bounded paths, blocker handoffs, and review
  consistency.
- Implemented deterministic `ai/task-*` worktrees/branches, clean immutable
  source-base checks, actual Git inspection, allowlisted verification commands,
  bounded artifact reads, verified staging, commit idempotency, and source
  immutability checks.
- Implemented independent verification of claimed files, write scopes,
  non-empty diffs, command evidence, failed/unsupported claims, migration
  pairs, contract paths, and artifact checksums.
- Added the Stage 6 executor with immediate coder/reviewer thread persistence,
  same-thread coder retry, fresh reviewer threads, review feedback loops,
  approved-only commits, artifacts, and bounded required-task handoff.
- Extended Temporal with long heartbeat execution activities, automatic task
  outcomes, dynamic required-task dependencies, owner retry signals, paused
  changes-requested/manual blockers, and attempt/review/replan/depth limits.
- Added migration `007_stage6_execution`, task attempt/review/artifact pgx
  persistence, three task execution API routes, `task-log`/`task-retry` CLI and
  Make targets, and production worker wiring.
- Added the reversible `008_stage7_gitlab` schema with separate issue/MR
  state, pipeline state, delivery identifiers, sync timestamps, partial
  uniqueness constraints, and payload-checksum-only webhook history.
- Implemented a bounded REST client for arbitrary self-hosted GitLab base
  paths with redirect refusal, response limits, encoded project references,
  issue/MR recovery, user-label preservation, notes, related-issue links, and
  task-branch publication.
- Added separate deterministic dry-run and in-memory fake GitLab adapters.
  Dry-run performs no HTTP, Git push, or link persistence; the fake exposes
  create counters for retry/idempotency assertions.
- Added approved-plan synchronization to one control-project plan issue and
  per-project task issues, with labels, checklists, links, marker-keyed status
  comments, and completed-task merge requests from verified `ai/task-*`
  attempts only.
- Added signed GitLab 19+ webhook verification using Standard Webhooks
  HMAC-SHA256, constant-time multi-signature matching, a five-minute replay
  window, stable delivery deduplication, and legacy `X-Gitlab-Token` fallback.
- Added transactional webhook state validation and synchronization for issue,
  merge-request, and related pipeline events. External state remains a
  projection and cannot complete an internal task or trigger merge/deploy.
- Added Stage 7 plan sync/link/webhook HTTP routes, `gitlab-sync` and
  `gitlab-links` CLI/Make commands, redacted configuration flags, and explicit
  `GITLAB_CONTROL_PROJECT`/webhook signing configuration.

## Files changed

- Root: `.dockerignore`, `.env.dist`, `.gitignore`, `AGENTS.md`, `Makefile`,
  `README.md`, `docker-compose.yml`, `go.mod`, `go.sum`.
- Entrypoint: `cmd/course-dev-orchestrator/main.go`.
- Domain/config: `internal/config/*`, `internal/domain/*`.
- Application/transport: `internal/usecase/health/*`,
  `internal/usecase/project/*`, `internal/usecase/planning/*`,
  `internal/usecase/gitlab/*`, `internal/adapters/http/*`.
- Infrastructure: `internal/adapters/git/*`, `internal/adapters/gitlab/*`,
  `internal/adapters/postgres/*`, `internal/discovery/*`,
  `internal/planning/*`, `internal/adapters/temporal/*`, `internal/activities/*`,
  `internal/workflow/*`.
- Database/runtime: `db/migrations/*`, `scripts/migrate*.sh`,
  `docker/Dockerfile`.
- Tests: package-local `*_test.go` files and
  `test/integration/postgres_schema_test.go`, plus discovery/onboarding
  fixtures and disposable Git worktrees.
- Documentation: `docs/architecture-conventions.md`,
  `docs/implementation-plan.md`, `docs/progress.md`.

## Tests

- `make verify` — passed (`gofmt` check, `go vet`, unit/workflow/HTTP/config
  tests, `docker compose config`).
- `go test -race ./...` — passed.
- `make migrate` twice — passed; second run skipped the applied migration.
- `make migrate-down && make migrate` — passed.
- `make test-integration` — passed; core tables and project/command
  idempotency constraints verified against PostgreSQL 16.
- Clean `docker compose up -d --build` — passed; all long-running services are
  running and dependency health checks pass.
- `GET /health` and `GET /ready` — returned HTTP 200 with `status=ok`.
- `make workflow-probe` — passed through the real Temporal worker and returned
  structured `status=ok` output.
- Reversible `002` and `003` Stage 2 migrations — rolled back and reapplied
  successfully.
- Stage 2 `make test-integration` — passed without Go test cache; pgx project
  upsert, snapshot versioning/reuse, report JSON, and schema constraints were
  exercised against PostgreSQL 16.
- Disposable command E2E — `project-connect`, `project-list`, `project-show`,
  `project-scan`, and `project-report` passed; repeated scan reused the same
  snapshot and all fixture DB/filesystem state was removed afterward.
- Stage 2 `go test -race ./...` and `make verify` — passed.
- Rebuilt Docker Compose stack — all services running; `/health`, `/ready`,
  `/api/v1/projects`, and the Temporal workflow probe passed.
- Stage 3 focused unit tests — passed for deterministic proposal generation,
  preservation/conflicts, symlink rejection, approval gating, dry-run, isolated
  worktree apply/idempotency, HTTP routes, and source immutability.
- Reversible `004_stage3_onboarding` migration — rolled back, verified absent,
  reapplied, and then skipped idempotently.
- Stage 3 `make test-integration` — passed without Go test cache; approval and
  apply transitions were exercised against PostgreSQL 16.
- Stage 3 `make verify` and `go test -race ./...` — passed.
- Disposable Stage 3 CLI E2E — connect/discover, prepare, diff, dry-run,
  approve, apply, and repeated apply passed against PostgreSQL and a temporary
  Git repository; the source checkout stayed clean at its original HEAD and
  all temporary worktree, branch, database, and filesystem state was removed.
- Rebuilt the Stage 3 Docker Compose images — API/PostgreSQL/Temporal health,
  empty project catalog, worker workflow probe, and all service states passed.
- Stage 4 focused unit/HTTP tests — passed for deterministic fingerprints,
  runtime-role exclusion, HTTP/event version drift, missing producers,
  gateway/frontend/infrastructure relations, direct queries, and transitive
  impact.
- Reversible `005_stage4_topology` migration — applied, rolled back, reapplied,
  and then skipped idempotently.
- Stage 4 `make test-integration` — passed without Go test cache; catalog
  replacement, fingerprint reuse, changed revisions, provenance, contracts,
  relations, drift, and empty-stack normalization were exercised against
  PostgreSQL 16.
- Stage 4 `make verify` and `go test -race ./...` — passed.
- Empty-catalog Stage 4 CLI/API smoke — `topology`, `contracts`,
  `contract-drift`, rebuild, query filters, `/health`, and `/ready` passed; the
  temporary topology revision/audit event was removed afterward.
- Rebuilt the Stage 4 Docker Compose images — all services are running,
  API/PostgreSQL/Temporal health passes, and the real worker workflow probe
  completed with structured `status=ok` output.
- Stage 5 planner/validator tests — deterministic multi-project DAGs,
  dependency order, explicit-project validation, and rejection of incomplete,
  cyclic, or over-parallelized plans passed.
- Stage 5 Temporal tests — bounded dependency dispatch, pause/resume/cancel,
  and transient activity retry passed.
- Reversible `006_stage5_planning` migration — applied, rolled back, reapplied,
  and then skipped idempotently.
- Stage 5 `make test-integration` — passed; command/plan reuse, approval gate,
  repeated approval/run, run transitions, task results, audit state, and schema
  constraints were exercised against PostgreSQL 16.
- Stage 5 `make verify` and `go test -race ./...` — passed on the final planning
  and workflow implementation.
- Disposable Stage 5 CLI/Temporal E2E — connect/discover, topology rebuild,
  repeated planning, repeated approval/start, task dispatch to `ready`, pause,
  worker restart, resume, and cancel passed. The temporary Git repository and
  all database records were removed afterward.
- Rebuilt Stage 5 services — API/PostgreSQL/Temporal readiness and the real
  worker workflow probe passed after the restart/recovery scenario.
- Stage 6 schema/semantic/runner/worktree/verifier/executor unit tests — passed.
- Stage 6 Temporal tests — automatic execution, owner retry after review
  changes, and dependent-task handoff/resume passed alongside Stage 5 signal
  compatibility tests.
- Disposable Stage 6 fixture E2E — a structured coder result produced a real
  isolated Git diff, independent checks and a separate reviewer approved it,
  the worktree committed, and the source checkout stayed clean at its base.
- Stage 6 `make test-integration` — passed; coder thread persistence, reviewer
  separation, review result, verification report, completion, and artifacts
  were exercised against PostgreSQL 16.
- The pinned SDK runner unit tests and production Docker image build passed.
- Reversible `007_stage6_execution` migration — applied, rolled back with the
  review table/attempt columns verified absent, reapplied, and then skipped
  idempotently.
- Stage 6 `make verify` and `go test -race ./...` — passed on the final
  executor, runner, workflow, API, and documentation state.
- Rebuilt Stage 6 services — all containers are running, API/PostgreSQL/
  Temporal readiness is healthy, and the real worker workflow probe completed
  with structured `status=ok` output.
- Stage 7 REST/fake/dry-run tests — passed for encoded self-hosted paths,
  bounded responses, issue/note/link/MR reuse, branch idempotency,
  deterministic no-write previews, approval gating, and preservation of
  non-managed labels.
- Stage 7 webhook tests — passed for HMAC-SHA256 signatures, timestamp replay
  rejection, tampered bodies, legacy tokens, event/header matching, body
  limits, duplicate delivery IDs, state transitions, ignored unknown links,
  and separate issue/MR/pipeline state.
- Reversible `008_stage7_gitlab` migration — applied, rolled back, reapplied,
  and exercised by the PostgreSQL integration suite.
- Stage 7 `make test-integration` and `make verify` — passed on the GitLab
  repository, use cases, HTTP routes, CLI wiring, runner, and Compose config.
- Stage 7 `go test -race ./...` — passed across all Go packages.
- Rebuilt Stage 7 services — API/PostgreSQL/Temporal readiness is healthy and
  the real worker workflow probe completed with structured `status=ok` output.

## Remaining work

- Stage 8 remains as specified in `docs/implementation-plan.md`: the Telegram
  owner interface.

## Exact next task

Implement Stage 8 Telegram polling/webhook owner interface while preserving
allowlists, expiring resource-bound approvals, replay protection, concise
payloads, and the no-secret/no-large-output boundary.
