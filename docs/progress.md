# Implementation progress

Last updated: 2026-09-24

## Current status

Stages 1–9 and the final cross-stage MVP rehearsal are complete and verified.
Stage 10 is also implemented and locally verified: a dedicated Next.js owner
UI now provides dashboard, project, plan/DAG, run, task attempt/artifact, and
approval views over bounded Go read models. The Compose stack exposes it only
on `127.0.0.1:3010` by default. Backend-derived allowed actions reuse the
existing fingerprint and state-machine gates. Audit-backed SSE provides live
cache invalidation, with polling fallback; the initial stream establishes a
baseline instead of replaying historical events.
Stages 11, 12, and 13 are implemented and locally verified. The UI now supports project
connection, explicit-project plan authoring, immutable plan revisions, and a
persistent conversational control center. Operator conversations resume one
read-only Codex thread, cite only verified resources, and can propose only
actions currently exposed by backend `allowed_actions`; existing confirmation,
fingerprint, and state-machine gates still execute every mutation.
Coder turns now stay on GPT-5.3-Codex-Spark, routine review uses Terra, status
lookups and manager fallback use Luna, and Sol is reserved for critical review
or explicitly difficult analysis. SDK token usage is persisted without prompts
and visible in `/usage`; deterministic issue/PR templates spend no model quota.
The Docker Compose stack is currently
running with PostgreSQL, Temporal, Temporal UI, the HTTP API, worker, and owner
UI.

The D2 development storage pass adds explicit `json-file` rotation to all six
Compose services (`20m` per file, three files). Compose validation passed; the
six already-running containers were left untouched and keep their prior
logging configuration until a normal Compose recreation.
Project lifecycle and agent-policy extraction are now implemented locally.
Projects can be archived without deleting snapshots or history and restored to
their exact pre-archive status. Active queries exclude archived sources from
new planning, topology, discovery, and onboarding, while the owner catalog can
still display and restore them. Canonical shared agent policy is versioned in
the orchestrator and generated separately from repository-specific business,
architecture, contract, command, and documentation evidence.
All 38 requested repositories now resolve through clean orchestrator-managed
clones of their merged remote default branches. The catalog contains 31
services, two frontends, one infrastructure repository, and one repository in
each of the policy, documentation, content, and archive roles. The user's 13
original primary checkouts were neither modified nor rescanned in place. The
trusted schema-v17 topology contains 34 runtime services, 1,030 capabilities,
216 ownership records, 695 machine-readable contracts, 111 explicit relations,
and 92 reported contract drifts. Five consecutive rebuilds reused revision
`a16d1cd2-33fa-4027-90eb-945d1a62a895` and fingerprint
`839401093020ddc53ac0f30b1aae20079f335ae0b0282185bb1a7c5ab62a2f91`.

Local Codex CLI execution now uses the existing ChatGPT login by default; no
`CODEX_API_KEY` is required. Evidence-backed semantic enrichment is implemented
as a proposal-only analyst pass: it cannot modify a connected checkout or
affect topology until the owner reviews, approves, applies, and rescans its
proposal. A live pilot for `ms-go-http-runtime-validator` produced run
`95482dd4-4a59-48b6-8a51-61e99ec4e662` with 31 verified semantic facts, seven
open questions, and two approved Taskfile commands. The owner approved it on
2026-07-23; dry-run and real apply passed in the isolated
`ai/onboard-ms-go-http-runtime-validator-95482dd44a59` worktree, commit
`5b3c99391d2379b1e9ddeb40f772a371abaf0e85` was pushed, and GitHub draft PR #6
was opened. All 38 reviewed onboarding PRs in `docs/onboarding-prs.md` and
orchestrator PR #1 were explicitly approved and merged on 2026-07-23. Legacy
plan `383dede3-2393-47af-b3db-e6c52bbfa4e8` was created before the issue-backed
workflow and must not run: it has neither manager-agent issue proposals nor an
approval fingerprint that binds them. No coding task, branch, push, merge, or
deployment has been started by it.
Stage 9 adds question/idea or existing-issue sources, plan discussion and
submission, dedicated issue/PR manager agents, exact content-bound approval,
risk-based Codex model/reasoning profiles, concurrent-plan limits, and a local
fake work-item gateway. Migrations `010` and `011`, PostgreSQL integration
tests, unit/HTTP/workflow tests, TypeScript runner tests, and `make verify`
passed. No real GitHub/GitLab issue, branch, PR, or MR was created.
The first live Stage 9 execution pilot is plan
`d83dc80c-4df7-4274-9b70-3b00a9683a1c`, version 4. It fixes one shared path
containment policy first, then schedules independent Git, HTTP-runtime, and
browser-runtime validator tasks with three parallel agents. A local
ChatGPT-auth `issue-manage-agent` prepared four complete Russian fake issue
proposals. The owner submitted and approved exact fingerprint
`2a0e81e70411b63a7e55295e4d11f6af10e550fd38445b53d7f81b4f9b1faeea`;
the fake gateway published four local-only issue records and the Temporal run
started through ChatGPT-auth Codex CLI. The prerequisite policy task produced
an isolated untracked contract and received independent reviewer feedback, but
did not complete. The run is terminal `failed`; none of the three downstream
tasks ran. No real issue/PR, push, merge, or deployment occurred.
Stage 7 used fake/dry-run GitLab adapters, a local HTTP server, and disposable
PostgreSQL rows; no real GitLab project, issue, branch, or MR was changed.
Stage 8 used a fake Bot API adapter, signed local webhook requests, and
disposable PostgreSQL rows; no real Telegram bot, user, or chat was contacted.

## Completed

- Completed S1 Architecture Manifest v1 foundation for the next Architecture
  Control Center rollout. It introduces validated evidence-backed service and
  operation manifests; five operation kinds (HTTP, NATS request/reply, NATS
  event subscriber, worker, scheduled); immutable snapshot persistence without
  raw YAML; deterministic service/operation Mermaid; a separate CURRENT
  catalog with explicit completeness counters; read-only platform/service/
  operation and Mermaid API endpoints; and the owner UI drill-down while
  preserving the existing XYFlow topology map. At this S1 milestone, S2
  remained the `ms-go-teacher-agent` reference implementation, S3 remained the
  mandatory full backend rollout, and TARGET/proposal editing had not yet been
  added.

- Completed S3 full CURRENT rollout and platform-wide completeness remediation
  on 2026-09-22. All 37 runtime backend services under `microservices/*` now
  provide a validated `.ai/architecture/service.yaml` and generated
  `service.mmd`; all 866 statically discovered operations provide a validated
  manifest and generated Mermaid (812 HTTP, 2 NATS request/reply, 31 event
  subscribers, and 21 scheduled operations). The final schema-v21 audit
  reports `validation_errors=0`, `operations_missing_manifests=0`, and
  `operations_blocked=0`. It also records 26 evidence-bounded `unknown` areas
  instead of inventing static facts. At this S3 milestone, S4 CURRENT
  verification was still required before TARGET, proposal, approval, or
  work-item implementation.

- Completed S4 global CURRENT verification on 2026-09-22. The new read-only
  `architecture-verify-current` command performs a fresh bounded scan,
  verifies Git/source stability across that scan, validates manifests and
  generated Mermaid, and reports per-service evidence without treating the
  intentionally untracked generated architecture artifacts as stale source.
  The final run is `verified` for 37/37 services: 866/866 operations (812
  HTTP, 2 NATS request/reply, 31 event subscribers, 21 scheduled), 37 service
  Mermaid, and 891 operation Mermaid; no missing, blocked, or validation
  failures. Five discovery-conflict classes and 26 unprovable manifest-to-code
  identities remain explicit attention evidence, not invented graph edges.
  CURRENT now also derives deterministic manifest-only cross-service relations
  and global Mermaid, exposed read-only in the API and Control Center UI with
  search/filter, drill-down/deep links, graph navigation, Mermaid preview and
  export.

- Completed S5–S8 Architecture Control Center lifecycle on 2026-09-22. TARGET
  is a separate fingerprint-bound, evidence-preserving proposal model with
  deterministic diff and graph impact; S6 requires an explicit revision- and
  fingerprint-bound approve/reject/request-changes decision. An approved
  target may only prepare existing local command, issue-draft, or Project Plan
  artifacts—there is no external publication or execution side effect. The
  final pure verification API compares a fresh CURRENT with the approved
  TARGET and returns `MATCHED`, `PARTIALLY_IMPLEMENTED`, `DRIFT`,
  `NOT_IMPLEMENTED`, or `VERIFICATION_PENDING` per change. The owner
  `/architecture` UI now exposes the complete PLATFORM → SERVICE → OPERATION
  read-only drill-down and TARGET editing, decision, planning, and verification
  workflow.

- Implemented Phase 1 Architecture Control Center as a read-only CURRENT projection over the persisted topology catalog. Added `/api/v1/architecture/current`, per-service and per-service-contract endpoints, deterministic Mermaid renderers, onboarding-proposal-only `.ai/architecture/*.mmd` artifacts, canonical agent guidance, and the owner `/architecture` map. No TARGET state, source-code mutation, plan execution, merge, or deploy path was added.

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
  `docs/implementation-plan.md`, `docs/progress.md`, and
  `docs/repository-onboarding-runbook.md`.

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
- Added the bounded Telegram Bot API adapter, long polling with durable
  highest-update-plus-one offsets, explicit webhook registration/removal, and
  the signed webhook HTTP endpoint.
- Added allowlisted user/chat authorization and all Stage 8 commands:
  `/start`, `/help`, `/projects`, `/connect`, `/analyze`, `/topology`, `/plan`,
  `/status`, `/approve`, `/reject`, `/pause`, `/resume`, `/retry`, `/cancel`,
  and `/issues`, plus natural Russian/English routing through the existing
  application operations.
- Added opaque inline callbacks for approve/reject/show/change and run/task
  controls. Grants are bound to user, chat, action, resource type, and UUID,
  expire after a bounded TTL, persist only SHA-256 token hashes, and are
  consumed atomically with replay and cross-user rejection.
- Added bounded/sanitized Telegram rendering: no raw update body, command text,
  full prompt, `.env`, log, diff, bot token, or adapter error is persisted or
  returned; large results use concise summaries and bounded GitLab links.
- Stage 8 fake tests cover all 15 commands and every callback action, including
  unauthorized user/chat, text-only mutation attempts, stale grants, repeated
  clicks, resource/user binding, callback acknowledgement, Bot API body limits,
  webhook secret validation, and token-free network errors.
- Reversible `009_stage8_telegram` migration — applied, rolled back with all
  Stage 8 tables verified absent, reapplied, and exercised by the PostgreSQL
  integration suite for update deduplication, monotonic polling offsets,
  callback expiry, binding, atomic consumption, and replay protection.
- Stage 8 `make test-integration`, `make verify`, and `go test -race ./...` —
  passed. The production Docker image rebuilt successfully; API/PostgreSQL/
  Temporal readiness and the real worker workflow probe passed after restart.
- The final database audit still reports zero projects, commands, Telegram
  updates, and Telegram callbacks; no user project or external integration was
  touched during Stage 8.
- Added the opt-in `make mvp-rehearsal` target. It refuses a non-empty project
  database and composes real PostgreSQL discovery, onboarding, topology,
  planning, execution repositories, and the real Temporal workflow around a
  temporary Git fixture and fake Codex/GitLab/Telegram boundaries.
- Final MVP rehearsal — passed twice, including a run that restarted
  PostgreSQL, Temporal, API, and the normal worker during an active coder
  activity. A replacement fixture worker resumed the durable coder thread and
  completed independent verification/review.
- Final duplicate assertions — one plan run, one task attempt, one review, one
  task commit, two GitLab links, one fake branch, and one fake MR. Repeated plan
  start, Telegram approval callback, onboarding apply, and GitLab sync reused
  their persisted/external resources.
- Final cleanup assertions and direct database audit — zero projects, commands,
  Telegram updates/callbacks, and GitLab links after rehearsal. The temporary
  repository and worktrees were removed automatically.
- Completed a read-only operational inventory of the requested local project
  landscape. It identified 36 primary microservices Git roots, 13 linked issue
  worktrees that must not be registered separately, the nested
  `infra/messaging` repository, branch/dirty blockers, and the correct
  non-runtime roles for `journal`, `prompts`, and `wiki`.
- Added a configurable `PROJECTS_HOST_ROOT` bind mount to both Compose API and
  worker services. Local projects now have one stable container namespace at
  `/projects` for discovery and eventual approved worktree execution.
- Added `docs/repository-onboarding-runbook.md` with the reviewed candidate
  groups, exclusions, connection order, three-repository pilot, and explicit
  container commands.
- Executed the owner-approved read-only discovery pilot for one Go validator,
  one TypeScript validator, and the nested messaging infrastructure repository.
  All three resolved to clean `main` checkouts under `/projects`, produced
  bounded non-truncated reports, and left their source HEAD/status unchanged.
- The pilot exposed and fixed name-based project lookup passing names into a
  PostgreSQL UUID query, plus generic request values being misclassified as
  NATS subjects. Regression tests cover both defects.
- Completed the owner-approved second read-only wave for
  `ms-go-cache-search-validator`, `ms-go-docker-validator`,
  `ms-go-git-validator`, `ms-go-linux-validator`,
  `ms-go-php-framework-validator`, `ms-go-statistic`, `ms-py-validator`, and
  `ms-ts-browser-runtime-validator`.
- Discovery report schema v4 now recognizes Python/PHP runtime evidence,
  classifies Python service repositories correctly, and extracts contracts
  from Go `net/http` `HandleFunc` registrations and Python
  `BaseHTTPRequestHandler` methods. Regression tests cover both route styles.
- Regenerated all eleven reports at unchanged commits. Every report is
  bounded and non-truncated; repeated scans reused snapshot version 4 for the
  original pilot and version 3 for the second wave.
- Rebuilt the eleven-project topology repeatedly. The same revision and
  fingerprint were reused; it contains 11 services, 31 capabilities, one
  ownership record, 25 contracts, no relations, and no contract drift.
- Rechecked every connected source checkout after discovery. All remain on
  clean `main`, and their stored/current HEADs match. Database audit reports
  zero onboarding runs, commands, GitLab links/events, Telegram updates,
  plans, and plan runs.
- Second-wave final verification — `make test-integration`, `make verify`, and
  `go test -race ./...` passed. The rebuilt stack returned healthy liveness,
  PostgreSQL/Temporal readiness, and a successful real Temporal workflow
  probe.
- Completed a fresh read-only branch-hygiene audit of all 21 remaining
  runtime-primary repositories. Live GitHub default/branch refs were queried
  without `fetch`; none is simultaneously clean, on the remote default branch,
  and current. No target checkout or Git ref was changed.
- Rechecked the deferred non-runtime/frontend/content group against live
  remotes. `prompts` and `journal` are the only immediately eligible next
  repositories: both are clean and exactly match `main`. `wiki`, both
  frontends, and `knowledge-tree` remain deferred for branch or dirty-state
  resolution.
- Connected local `prompts` and `journal` read-only as `policy` and `archive`.
  Their canonical Git identities produce project names `ms-course-promts` and
  `ms-course-journal`; both remain on clean unchanged `main` checkouts.
- Discovery report schema v6 suppresses capabilities, contracts,
  infrastructure, ownership, and relations for non-runtime repository roles.
  It also records every policy Markdown document as a checksum-only
  instruction fact without treating ordinary archive Markdown as policy.
- Regenerated all 13 discovery reports at schema v6 and verified repeated scan
  reuse. `ms-course-promts` contains 19 policy instruction facts and zero
  runtime facts; `ms-course-journal` contains only classification/purpose and
  zero runtime facts.
- Rebuilt topology twice with stable revision/fingerprint. The revision covers
  13 projects but still materializes 11 services, 31 capabilities, one
  ownership record, 25 contracts, no relations, and no contract drift. Direct
  database checks found no non-runtime rows in any topology table.
- The post-wave database audit reports zero onboarding runs, commands, GitLab
  links/events, Telegram updates, plans, and plan runs.
- Non-runtime-wave final verification — `make test-integration`, `make verify`,
  and `go test -race ./...` passed. The rebuilt stack returned healthy
  liveness, PostgreSQL/Temporal readiness, and a successful real Temporal
  workflow probe.
- Connected all 38 requested repositories using 25 managed remote-default
  clones plus 13 reviewed clean local checkouts, without modifying primary
  user checkouts, and rebuilt the 34-service platform landscape used by
  planning/execution context.
- Switched live Codex execution to the existing local ChatGPT login and added
  per-role model profiles without requiring `CODEX_API_KEY`.
- Added proposal-only semantic enrichment with analyst JSON Schema, exact
  source-quote validation, rejected-fact isolation, business rules/processes,
  entities, relations, and evidence-backed commands.
- Added scan-time revalidation of approved semantic quotes and discovery schema
  v7 so altered or stale reports fail closed before topology ingestion.
- Installed and verified `bubblewrap` inside the runtime containers; Compose
  allows its unprivileged namespace while dropping all capabilities and
  enforcing `no-new-privileges`.
- Completed the live `ms-go-http-runtime-validator` semantic pilot. Four
  superseded experimental proposals were cancelled, the final proposal passed
  dry-run checks, and the managed clone remained clean at its original HEAD.
- Applied and published the owner-approved HTTP runtime validator pilot without
  modifying its primary checkout. All proposal checksum, generated-format,
  write-scope, worktree-isolation, commit, and source-immutability checks passed.
- Rejected the first `ms-go-validation-orchestrator` semantic proposal during
  evidence review because deterministic discovery treated SQL embedded in
  `docs/examples/*.json` as owned tables and a route in `_test.go` as a
  production endpoint.
- Advanced discovery report schema to v9. Runtime topology evidence now ignores
  test, fixture, and example paths, while database ownership requires a
  checked-in production SQL file. Compose detection accepts only YAML manifests
  and no longer misclassifies Go files such as `compose.go`. Semantic analysts now receive the exact
  connected-project name catalog, and relation facts targeting networks,
  containers, URLs, or other non-project values are isolated as rejected facts.
- Added regression coverage for example/test evidence suppression and
  non-catalog semantic relation targets. Focused tests and `make verify` pass.
- Rejected the first `ms-go-sandbox` semantic proposal because its command
  manifest exposed cleanup and Docker lifecycle commands without an explicit
  approval boundary. Generated command catalogs now classify each entry as
  verification, lifecycle, external-runtime, or state-change and set
  `requires_approval` accordingly. Agent and test workflows run only
  non-approval verification commands; all other commands require an owner gate.
- Rejected the first `ms-go-course` semantic proposal because seed-import and
  integration commands were initially classified by their test-like names.
  Command risk precedence now treats migration, create, import, insert, seed,
  and integration operations as approval-required before considering test or
  validation keywords.
- Rejected the first `ms-gateway` semantic proposal because an E2E command was
  quoted without its required working directory and did not exist relative to
  the repository root. Semantic command validation now rejects missing `./...`
  executable paths instead of placing non-runnable commands in agent manifests.
- Completed reviewed, dry-run-validated onboarding applies for the platform
  anchors `ms-go-validation-orchestrator`, `ms-go-sandbox`, `ms-go-course`, and
  `ms-gateway`. Every apply used an isolated `ai/onboard-*` worktree, restricted
  writes to `AGENTS.md` and `.ai/**`, committed the exact approved proposal, and
  left the managed source checkout unchanged.
- Rejected the first `course-wiki` semantic proposal because documentation-only
  evidence was represented as runtime ownership, contracts, infrastructure, and
  topology relations. Semantic validation now fail-closes those categories for
  content, policy, documentation, and archive roles while retaining purpose,
  business rules, business processes, entities, and repository commands.
- Regenerated and applied the corrected `course-wiki` proposal with 43 admitted
  knowledge facts: 29 business rules, five business processes, eight entities,
  and one purpose statement. Two unverifiable quotes were isolated and six
  cross-document ambiguities remain explicit open questions; no runtime
  ownership, contract, relation, capability, or infrastructure fact was admitted.
- Published the five reviewed platform-anchor trees after exact tree-hash
  comparison as GitHub draft PRs: validation orchestrator PR #21, sandbox PR
  #13, course PR #74, gateway PR #29, and course-wiki PR #11. No PR was merged.
- Rejected the first `ms-go-auth` proposal because an E2E-only path reversed a
  gateway relation and a Taskfile comment promoted a local shared-Postgres setup
  into runtime topology. Semantic runtime categories now reject evidence from
  tests, fixtures, examples, and testdata; relations reject operational manifest
  sources; gateway/frontend relation types are constrained to matching source
  repository kinds.
- Two subsequent `ms-go-auth` analyses reached a child-runner failure after
  emitting only the thread frame. The Go adapter previously returned only an
  unhelpful incomplete-protocol error and discarded the runner's structured
  stderr event. It now surfaces the bounded JSON error message while continuing
  to ignore arbitrary stderr, with regression coverage for the partial protocol.
- The surfaced cause was a TLS unexpected-EOF after the SDK exhausted its five
  stream reconnect attempts. Partial protocol reads now return the captured
  thread ID, transport-like runner messages are typed as transient failures,
  and semantic enrichment performs one bounded same-thread resume instead of
  discarding completed analysis work or retrying indefinitely.
- Rejected the first `ms-go-rbac` proposal because its documented test command
  set `GOCACHE=../.gocache`, which would write outside the isolated worktree and
  contradicted repository instructions. Commands containing parent-directory
  traversal now require approval and are excluded from automatic test workflows.
- Rejected the first `ms-go-user` proposal because semantic GORM-model facts
  duplicated four table ownership records already discovered from production
  migrations. Semantic `ownership/database_table` facts now require checked-in
  `.sql` evidence; code models remain valid evidence for domain entities only.
- Rejected the first `ms-go-student` proposal because a sandbox caller allowlist
  was represented backwards as authentication delegation. The
  `authenticates_through` relation now requires direct authentication/JWT/token
  evidence. Command risk matching no longer finds destructive `rm` inside words
  such as `performance`; formatting commands are explicitly state-changing.
- Rejected the second `ms-go-student` proposal because the same inbound
  `AllowedServices` evidence was renamed to `depends_on`. Caller allowlists are
  now rejected as evidence for every semantic relation type; outbound
  dependencies require outbound-client or explicit architecture evidence.
- Completed reviewed onboarding applies for the identity/core wave:
  `ms-go-auth`, `ms-go-rbac`, `ms-go-user`, `ms-go-student`,
  `ms-go-filestorage`, and `ms-go-statistic`. Every proposal passed dry-run,
  generated-format, exact write-scope, isolated-worktree, commit, and
  source-checkout immutability checks.
- Published the identity/core trees after exact tree-hash comparison as GitHub
  draft PRs: auth #4, RBAC #8, user #11, student #30, filestorage #6, and
  statistic #6. No PR was merged.
- Rejected the first `go-ms-ai-summary` proposal because the three-file checkout
  contains copied/generic architecture instructions for different services but
  no production code or manifests. Deterministic discovery now suppresses
  runtime extraction from `docs/*.md`; semantic runtime categories reject
  `AGENTS.md` and `prompts/**` evidence while retaining commands and working
  rules. Discovery report schema advanced to v10 so every connected checkout is
  rescanned under the documentation boundary before topology is rebuilt.
- Rejected the second `go-ms-ai-summary` proposal because copied commands in
  `AGENTS.md` had no corresponding Go module, Makefile, Taskfile, or Compose
  manifest. Discovery schema v11 now leaves name-only runtime placeholders as
  `service_kind: unknown`, and semantic commands sourced only from README or
  AGENTS require the matching repository manifest. The corrected proposal has
  no runtime topology, executable commands, backend coder, or feature workflow;
  its missing source, contracts, schema, configuration, and deployment remain
  explicit open questions.
- Rejected the first `ms-go-pet-project-orchestrator` proposal because a README
  statement about downstream services forwarding a contract was represented as
  a direct dependency on `ms-go-validation-orchestrator`. Indirect downstream
  mentions now fail the relation-evidence gate. The corrected proposal records
  only the production-wired `ms-go-ai-prompt` and `ms-go-student` dependencies,
  SQL-backed ownership, approval-gated state changes, and `task test` as the
  automatic verification command.
- Completed reviewed, dry-run-validated onboarding applies for the
  AI/orchestration wave: `ms-go-ai-prompt`, `go-ms-ai-summary`, and
  `ms-go-pet-project-orchestrator`. Every apply passed exact write-scope,
  isolated-worktree, commit, and source-checkout immutability checks.
- Published the AI/orchestration trees after exact tree-hash comparison as
  GitHub draft PRs: AI prompt #5, AI summary #3, and practice-task orchestrator
  #8. No PR was merged.
- Completed reviewed onboarding applies for the platform-knowledge wave:
  `ms-infra-messaging`, `ms-course-promts`, `ms-course-journal`, and
  `knowledge-tree`. Infrastructure stream definitions remain distinct from
  unknown publisher/consumer ownership; policy, archive, and content facts do
  not create runtime topology.
- The `knowledge-tree` proposal preserves the differing EN/RU
  `02-generate-lesson.md` checksums as an unresolved conflict. Export, update,
  dev, and other mutating scripts require approval; only safe check/test/
  validation commands enter its automatic verification workflow.
- Published the four platform-knowledge trees after exact tree-hash comparison
  as GitHub draft PRs: messaging #3, shared policy #5, journal #1, and
  knowledge-tree #224. No PR was merged.
- Rejected the first `nextjs` frontend proposal because deterministic Nginx
  detection represented the frontend reverse proxy as an owner of
  `gateway_routes_to` relations. Discovery schema v12 suppresses gateway-owned
  relations for frontend-role repositories while retaining endpoint-level
  consumer evidence and unresolved backend ownership questions.
- Rejected the second `nextjs` proposal because i18n keys such as
  `actions.publish` and `toast.publishSuccess` were represented as event-bus
  subjects. Discovery schema v13 requires explicit subject/NATS context or a
  real publish/subscribe call; the corrected frontend report contains no false
  event facts or `events.yaml`.
- Rejected the first reviewed frontend command manifests because npm lifecycle
  hooks and interactive Vitest UI were treated as automatic verification.
  `pre*`/`post*` hooks and UI modes now require approval, covering admin Monaco
  asset synchronization and both frontend test UIs.
- Completed and published corrected frontend onboarding drafts: student
  frontend PR #52 and admin frontend PR #77. Student PR #51 was closed as
  superseded. Both corrected trees passed exact tree-hash, isolated-worktree,
  write-scope, commit, and source-immutability checks; no PR was merged.
- Completed reviewed data/runtime onboarding applies for
  `ms-go-cache-search-validator`, `ms-go-db-validator`, `ms-go-tarantool`, and
  `ms-go-image-processor`. Ephemeral validator engines are infrastructure, Lua
  Tarantool spaces are database contracts rather than SQL table ownership, and
  image-variant ownership is backed by a checked-in SQL migration.
- The DB validator uses the clean orchestrator-managed remote clone; the 1,793
  untracked files in the user's separate primary checkout were neither scanned
  nor modified. Tarantool's stale NATS/cache documentation and image
  processor's `ms-filestorage` versus `ms-go-filestorage` alias remain explicit
  open questions.
- Published the four data/runtime trees after exact tree-hash comparison as
  GitHub draft PRs: cache/search #5, DB validator #5, Tarantool #5, and image
  processor #5. No PR was merged.
- Completed reviewed onboarding applies for the Go validator wave:
  `ms-go-code-validator`, `ms-go-docker-validator`, `ms-go-git-validator`,
  `ms-go-linux-validator`, `ms-go-php-framework-validator`, and
  `ms-go-php-validator`. Each proposal separates validator behavior from the
  user workspace/runtime it inspects and leaves unidentified callers,
  deployment controls, and persistence boundaries as open questions.
- The Linux validator report explicitly records that caller-controlled commands
  reach `os/exec` while no deployment isolation boundary is documented. The
  PHP validator report preserves stale route/event and unenforced configuration
  discrepancies rather than turning them into active contracts.
- Published the six Go-validator trees after exact tree-hash comparison as
  GitHub draft PRs: code #11, Docker #6, Git #6, Linux #5, PHP framework #6,
  and PHP #8. No PR was merged.
- Completed the final polyglot wave for Node, Python, browser runtime, CSS,
  HTML, Next.js, and React validators. Every proposal was individually
  reviewed, dry-run validated, applied in an isolated worktree, tree-hash
  checked, and published as a draft PR.
- Advanced discovery through schemas v14-v16. Package-script facts must match
  exact manifest scripts, watch/UI/lifecycle commands require approval,
  unexpanded `{{...}}` command templates are rejected, and Compose-backed
  Nginx `*.conf.template` repositories are classified as gateways before the
  generic infrastructure fallback.
- Added command-manifest deduplication by executable command, retaining the
  strongest evidence and removing deterministic/semantic aliases from test
  workflows.
- Replaced stale or noisy onboarding drafts after the final audit: gateway #30
  supersedes #29, filestorage #7 supersedes #6, RBAC #9 supersedes #8, user #12
  supersedes #11, course #75 supersedes #74, browser runtime #9 supersedes #8,
  CSS #12 supersedes #11, and Next.js validator #7 supersedes #6.
- Audited the latest command manifests for all 38 projects. The audit found
  zero duplicated run commands, zero unexpanded command templates, and zero
  watch/UI/lifecycle commands incorrectly marked for automatic execution.
- Confirmed exactly one current onboarding draft per connected project and
  recorded the resource-level merge set in `docs/onboarding-prs.md`.
- Merged the exact 38 approved onboarding PRs and `agent-orchestrator` PR #1,
  then independently confirmed that all 39 pull requests are closed and merged.
- Moved the remaining 13 catalog entries to separate orchestrator-managed
  clones while preserving the user's primary checkouts and their HEAD/dirty
  state.
- Advanced discovery to schema v17, rejected prose-shaped semantic contracts,
  and rescanned all 38 clean merged default branches without truncation.
- Made topology sorting total and deterministic, added independent contract
  shape validation, and confirmed five stable rebuilds with the same revision,
  fingerprint, and counts.
- Added exact explicit project scope to planning and name-based topology CLI
  lookup, with regression coverage for both behaviors.
- Converted the reviewed platform gaps into the prioritized queue in
  `docs/platform-work-items.md`.
- Created the first real three-project concurrent coding plan
  `383dede3-2393-47af-b3db-e6c52bbfa4e8`; it is intentionally paused in
  `awaiting_approval` before any project mutation.
- Added reversible issue-backed planning migrations with discussion comments,
  immutable issue/PR work items, source kind, planner fingerprint, and an
  approval fingerprint that binds the complete canonical issue set.
- Added dedicated read-only `issue-manage-agent` and
  `pull-request-manage-agent` roles with strict Russian JSON schemas. Issues
  require context, goal, repository responsibility, scope, acceptance,
  dependencies, risks, labels, milestone, and assignees. PRs require the linked
  issue, completed work, components, checks, contracts/migrations, risks,
  result verification, labels, milestone, assignees, and reviewers.
- Added explicit discussion, issue prepare/publish, submit,
  exact-fingerprint approval, PR prepare/publish HTTP and CLI operations.
- Added the bounded GitHub REST gateway with idempotency markers, complete
  metadata, reviewer assignment, redirect refusal, response limits, and
  non-force `ai/task-*` push validation. The default fake gateway performs no
  network or Git push and supports a complete local lifecycle.
- Removed automatic required-task persistence and dynamic Temporal DAG
  injection. Newly discovered prerequisites persist as blockers and pause the
  run until a new plan version is discussed and approved.
- Added a global Temporal activity limit across simultaneous plan workflows in
  addition to each plan's parallelism limit.
- Configured local ChatGPT-auth Codex profiles by complexity: fast uses
  `gpt-5.6-luna`/low; standard uses `gpt-5.6-terra`/medium; deep and review use
  `gpt-5.6-sol`/high. All values remain environment-overridable; no API key is
  required.
- Disabled real legacy GitLab synchronization because it bypasses dedicated
  manager-agent proposals; its fake/dry-run preview remains available.
- Applied migrations `010` and `011` to the local PostgreSQL instance and
  passed the full issue proposal → submit → exact approval → publication → run
  gate integration state machine. `make verify` passed on the final tree.
- Added a read-only semantic planner-agent over the deterministic project
  selection boundary. It produces Russian task detail and dependency intent,
  while Go validation still owns exact project scope, DAG validity, depth,
  parallel width, model profiles, write scope, and verification commands.
- Verification commands are now selected from each connected repository's
  bounded `.ai/commands.yaml`. Only canonical non-approval verification
  commands are admitted; lifecycle, state-changing, and external-runtime
  commands remain excluded.
- Made topology materialization fail closed when stored revision counts do not
  match their rows and repair an incomplete matching revision transactionally.
  The live schema-v17 topology was restored to its expected 34 services, 1,030
  capabilities, 216 ownership records, 695 contracts, 111 relations, and 92
  drifts under the same deterministic fingerprint.
- Isolated PostgreSQL integration tests in a disposable database whose name
  must end in `_test`. The harness creates, migrates, tests, and drops only that
  database; integration code rejects the live work database.
- Verified the local ChatGPT login against the configured Codex aliases:
  `gpt-5.6-luna` for fast, `gpt-5.6-terra` for standard, and `gpt-5.6-sol` for
  deep/review, with no API key.
- Fixed strict nullable `existing_issue` output schema compatibility for the
  issue manager and added recursive coverage requiring every declared object
  property in manager schemas to be listed in `required`.
- Routed Make planning, execution, and work-item commands through the Compose
  CLI so persisted `/data` managed clones are resolvable. Plan files can now be
  streamed through size-bounded stdin instead of requiring a container mount.
- Prepared four fake issue proposals for the containment pilot. Each has a
  Russian title and required body sections, labels, milestone, assignee,
  complexity, model profile, exact task mapping, and the immutable plan
  fingerprint. No external issue or PR was created.
- Submitted and approved containment plan
  `d83dc80c-4df7-4274-9b70-3b00a9683a1c` only for its exact fingerprint, then
  published all four work items through the fake gateway and executed the
  prerequisite through local ChatGPT-auth Codex CLI.
- The live run exposed and fixed three execution defects: an invalid strict
  reviewer schema, reuse of a failed task execution identity across Temporal
  retries, and resuming a coder thread after reviewer feedback until its
  context was exhausted. Technical plan retry now uses `-retry-N` workflow
  identities, preserves completed prerequisites, and starts a fresh audited
  coder revision thread after `changes_requested`.
- Runner failures now close one task attempt without blindly repeating the
  same Codex call as a Temporal activity. Invalid coder/reviewer JSON is
  persisted for diagnosis, and recursive schema tests catch missing strict
  `required` fields before execution.
- Coder and reviewer context now includes all approved plan tasks. Prompts
  distinguish a strict prerequisite from later rollout work and require
  `status=blocked` whenever `required_tasks` is non-empty.
- The policy reviewer identified unresolved atomic no-follow mutation and
  symlink semantics plus possible rollout work in additional repositories.
  The last coder result was rejected because it combined `completed` with
  non-empty `required_tasks`. The policy task and plan failed safely; Git,
  HTTP-runtime, and browser-runtime tasks remained unexecuted.
- Re-ran `make verify`, race tests for the affected Go packages, and the full
  disposable PostgreSQL integration suite after the execution fixes.
- Approved and executed the fake-backed containment plan
  `a6f748b2-d619-4536-a308-219cfd364eb6` with exact fingerprint
  `90edabd3326f0fd0d122c866d2fe67159895a90f207db4a22e33eb5747e60bd6`.
  The live Temporal run respected the three-agent limit, dispatched independent
  tasks in parallel, preserved completed prerequisites across technical plan
  retries, and kept dependent Node/sandbox tasks gated.
- Completed six independently verified and reviewed task branches with local
  commits only: policy, cache/search, DB, Git, Linux, and browser-runtime. The
  browser task exercised two rejected review rounds, a persisted task retry,
  deterministic npm dependency preparation, independent verification, and a
  final second-review approval before commit `6a3924f3b76fc7b91623ea80dc7e9ed6503f6233`.
- Fixed live execution infrastructure discovered by the run: PostgreSQL
  attempt failure typing, Go/Node/Python worker runtimes, PID 1 reaping,
  persistent Go/npm caches, verifier-only sandbox result promotion, persisted
  retry feedback, and deterministic `npm ci --ignore-scripts` preparation for
  lockfile-backed Node worktrees. The task-attempt operator limit is bounded at
  eight while the default remains three.
- The HTTP-runtime task stopped fail closed after three task attempts and two
  reviews per attempt. Its final reviewer found that indirect runtime imports
  cannot be confined safely by path rewriting alone; an execution sandbox is a
  prerequisite architecture concern. No HTTP commit was created. Node and
  sandbox tasks were not dispatched because the approved DAG remains blocked
  by HTTP.
- Confirmed all nine managed source checkouts still match their planned HEAD
  and are clean, all six task commits are absent from remote-tracking refs,
  there are zero PR work items, and every issue URL is under
  `github.example.test`. GitHub/GitLab tokens remain empty, dry-run is enabled,
  and no push, merge, deployment, or external work-item write occurred.
- Passed all Go package tests, all eight Codex-runner tests, and the disposable
  PostgreSQL migration/integration suite after the live run fixes.

- Added Stage 10 UI read models and pgx queries for dashboard counts/attention,
  plans, runs, tasks, approvals, and audit activity with opaque keyset cursors.
- Added backend-derived allowed actions so the browser does not duplicate plan,
  run, or task state machines.
- Added the audit-backed SSE endpoint with heartbeat, `Last-Event-ID` recovery,
  initial-baseline suppression, CORS for the read-only stream, and logging
  middleware support for `http.Flusher`.
- Added the Next.js owner UI under `web` with TanStack Query/Table, React Flow,
  Zod response validation, responsive desktop/mobile layout, dark mode, and
  confirmation-gated calls to the existing mutation endpoints.
- Added overview, projects, project detail, plans, plan DAG, runs, run detail,
  task attempts/artifacts, and approvals screens against the live 38-project
  database.
- Added a non-root, capability-dropped, read-only Compose UI service bound to
  `127.0.0.1:3010`, plus bootstrap, development, typecheck, build, unit, and
  Playwright Make targets.
- Verified focused Go unit/HTTP tests, real live-database read endpoints,
  frontend typecheck/unit/production build, four desktop Playwright flows, a
  375px mobile flow, zero npm audit vulnerabilities, Docker image builds, and
  Compose API/UI health. No orchestrator resource was mutated by UI tests.
- Published the completed Stage 9/10 checkpoint as commit `5aa09e8` on branch
  `agent/owner-ui-and-planning-reliability` and opened draft GitHub PR #4 after
  explicit owner authorization.
- Added Stage 11 project connection and plan creation wizards. Owners can now
  connect an allowlisted path or Git URL, inspect discovery output, describe a
  goal, explicitly select projects, and open the generated discussion plan.
- Added immutable plan revisions through
  `POST /api/v1/plans/{planId}/revisions`. Only a discussion plan may create a
  successor; the correction becomes part of planner input, the same command
  and project set are retained by default, and repository fingerprint/version
  rules cancel stale proposals without mutating history.
- Added focused revision use-case and HTTP route coverage plus three mocked,
  non-mutating Playwright scenarios. All eight owner E2E flows, focused Go
  tests, frontend typecheck/unit tests, and the production Next.js build pass.
- Added reversible migration `012_owner_conversations` and PostgreSQL
  persistence for conversation scope/history, resumable operator thread IDs,
  message references, structured action proposals, decisions, and audit.
- Added the read-only `operator` runner role and strict output schema. Its
  bounded context contains persisted dashboard, project, plan, run, task, and
  approval state; unknown references or proposals outside current
  `allowed_actions` fail closed, while plan approval proposals bind the exact
  displayed fingerprint.
- Added `/api/v1/conversations` create/list/detail/message APIs and proposal
  decisions with thin HTTP handlers and application-layer validation.
- Added the responsive `/control` UI: scoped conversation creation, persistent
  timeline, resource links, pending proposal queue, and explicit execution via
  the existing confirmation-gated resource actions.
- Verified focused Go tests, nine runner protocol tests, frontend
  typecheck/unit/build, nine non-mutating Playwright flows, and the disposable
  PostgreSQL integration suite including conversation lifecycle and migration.
- Added reversible migration `013_agent_usage_and_routing`, SDK usage protocol
  forwarding, PostgreSQL aggregation, and rolling 5-hour/7-day/30-day usage
  read models.
- Added deterministic role routing: Spark-only coder, risk-aware Terra/Sol
  reviewer, Luna/Terra/Sol planner and operator selection, Terra onboarding,
  and Luna manager fallback.
- Added an enforced five-hour Sol run cap, disabled xhigh by default, and kept
  Fast service tier disabled. A running reservation is persisted before each
  Sol call so concurrent workers cannot overshoot the configured cap.
- Replaced issue and PR agent calls with validated deterministic Russian
  templates by default; `WORK_ITEM_DRAFT_MODE=agent` retains the bounded Luna
  fallback.
- Added `/api/v1/agent-usage` and the `/usage` owner screen with model/role
  breakdowns, token counters, failures, and budget utilization.
- Applied migration 013 to the local stack and rebuilt the API, worker, and UI.
  `make verify`, disposable PostgreSQL integration tests, health/readiness/API
  probes, and all ten non-mutating Playwright scenarios passed.
- Fixed the empty-conversation API contract: PostgreSQL now returns
  `messages: []` and `proposals: []` instead of JSON `null`, so a newly created
  `/control` conversation passes the strict UI schema. Disposable PostgreSQL
  integration coverage asserts both collections are non-nil and empty.
- Prepared replacement discussion plan
  `0436da42-b1cf-45de-a538-95f546f4ba9a` from the persisted HTTP reviewer
  findings and topology revision `a16d1cd2-33fa-4027-90eb-945d1a62a895`.
  Its development DAG has ms-go-sandbox at depth zero and exactly three
  prerequisite edges to the HTTP, Node, and browser validators at depth one;
  there are no reverse runtime-topology or artificial parallelism edges.
- Fixed three planning defects exposed by the live discussion rehearsal:
  explicit owner prerequisites now override reverse runtime relations without
  creating a cycle, parallelism bounds operate on actually runnable waves, and
  keyword detection no longer treats `immutable` as a database `table` signal.
  Focused regression tests cover all three cases and preserve explicit database
  migration detection.
- Generated four complete Russian fake issue proposals through the deterministic
  issue-manager role. Every proposal is `proposed`, has a label, milestone,
  assignee, high complexity, and deep model profile; all external numbers and
  URLs remain empty. The resulting exact plan fingerprint is
  `df6af246f25cb44c4b5cac8b85d3b42812d56e6be69957fb8affb5d899a1b7e6`.
- Confirmed the replacement plan remained in `discussion` with zero approvals
  and zero runs before submission, and passed `make verify`.
- After exact owner authorization, submitted plan
  `0436da42-b1cf-45de-a538-95f546f4ba9a` with unchanged fingerprint
  `df6af246f25cb44c4b5cac8b85d3b42812d56e6be69957fb8affb5d899a1b7e6`.
  It is now `awaiting_approval` with pending approval
  `bde0a209-24fe-4d61-b3f7-eb1455bd90fd`; all four work items remain
  `proposed`, and no issue/PR publication, Temporal run, task execution, push,
  merge, or deployment occurred.
- Redesigned the owner plan detail screen after a live readability review. A
  bounded page title now separates the immutable plan summary from heading
  typography; an explicit three-step guide explains task generation,
  prerequisite direction, parallel waves, versioning, and the absence of
  direct task editing.
- Replaced the default React Flow nodes and raw `prerequisite` labels with
  dark-mode-safe linked task cards, left-to-right arrow edges, centered wave
  layout, localized visible controls, and a plain-language legend. The task
  list now mirrors graph numbering and names each prerequisite by task number;
  technical fingerprint and work-item fields include owner-facing context.
- Verified the plan UI change with frontend typecheck, unit tests, production
  build, all ten Playwright owner flows, a rebuilt healthy loopback UI
  container, and live browser measurements at
  `/plans/0436da42-b1cf-45de-a538-95f546f4ba9a`. All four graph titles render
  without clipping, the three persisted edges are visible, and the page has no
  horizontal overflow. No plan, approval, work item, run, or external resource
  was mutated.
- Audited all ten plan rows in the live local database. Eight have no external
  publication, while the paused and failed pilot plans contain only simulated
  `github.example.test` issues; there are zero real external issue
  publications in the current plan history. The second awaiting-approval row
  is a legacy pre-issue-backed plan without issue proposals.
- Extended the plan summary read model with a persisted-effect classification:
  no issues, local issue drafts, fake-gateway simulation, or external
  publication. The classification is derived from stored work-item state and
  URLs, including previously published and later closed issues, rather than
  inferred from the current runtime configuration.
- Redesigned `/plans` into explicit owner-decision, preparation, approved,
  execution-history, and collapsed archive sections. The page now explains record provenance,
  separates automated test databases from the durable local database, labels
  legacy plans and fake publications, and states that `critical` is the maximum
  technical task risk rather than urgency or business priority.
- Verified the plan-list change with `make verify`, the disposable PostgreSQL
  integration suite, all ten Playwright owner flows, rebuilt healthy API/UI
  containers, and a live in-app browser inspection of the legacy, simulation,
  and external-publication labels. No plan, approval, work item, run, or
  external resource was mutated.
- Made the lifecycle executable and visible as four separate stages:
  preparation, exact-fingerprint owner decision, issue publication, and local
  execution. The page reports the configured publication mode (`fake`, GitHub
  dry-run, or real external writes) before any action, and action confirmations
  state the precise effect of approval, publication, and run creation.
- Replaced wide plan tables with responsive lifecycle cards. Each card keeps
  the plan identity, persisted publication contour, next step, technical risk,
  progress, status, and allowed actions together. At a 1280 px browser viewport
  all ten cards have zero horizontal overflow and the page has no document-wide
  horizontal scroll.
- Tightened backend plan actions: an approved plan exposes publication only
  when every task has exactly one current issue proposal. Legacy plan
  `383dede3-2393-47af-b3db-e6c52bbfa4e8` now has no impossible publication
  action, while partially published plan `0436da42-b1cf-45de-a538-95f546f4ba9a`
  correctly offers only the three remaining fake issues.
- Made the fake gateway restart-idempotent by deriving stable positive external
  numbers from project, work-item kind, and idempotency key. Publication now
  preflights the complete proposal set, preserves per-item progress, reports
  partial progress in errors, and refreshes the UI even after a partial failure.
- Added explicit owner-selected plan supersession for future plans through
  migration `014_plan_supersession`. The create-plan form defaults to an
  independent plan and can instead name one eligible predecessor; only an
  explicit selection archives that predecessor. Existing similar plans were
  intentionally not linked or rewritten by text inference.
- Added persisted run errors to plan summaries and translated the two known
  local-run failures into owner-facing explanations. Action components now
  refresh after success or failure, and list/card identity is stable so an
  error cannot drift to another plan after regrouping.
- Applied migration `014_plan_supersession` to the local database, rebuilt the
  healthy API, UI, and worker containers, and verified the result with
  `make verify`, the disposable PostgreSQL integration suite, all ten
  Playwright owner flows, and live in-app browser inspection. Existing plan,
  approval, work-item, and run rows were not normalized or otherwise changed;
  no real external resource was created.

- Added reversible migration `015_project_lifecycle` with archive timestamp and
  pre-archive status, a consistency constraint, and an active-project index.
- Added transactional, audited, idempotent project archive and exact-status
  restore operations. Archiving is rejected during scanning; scan and
  onboarding fail closed for archived projects.
- Default project listing now returns active projects only, which removes
  archived sources from planning, conversations, and topology rebuilds. The
  explicit `include_archived=true` catalog view keeps them visible to the owner.
- Added CLI, Make, HTTP, and owner-UI archive/restore operations without remote
  repository mutation or deletion.
- Extracted reusable policy from reviewed `ms-course-promts` revision
  `2a16785` into canonical template bundle `v1`, excluding journal state,
  static repository inventory, unsafe copy-paste recipes, and unsupported
  framework prescriptions.
- Deterministic onboarding now separates `.ai/rules/common.md` and template
  metadata from repository-specific service, architecture, contract, command,
  and linked-document evidence. It includes coder/reviewer roles and explicit
  bugfix, feature, refactor, review, and issue-delivery workflows.
- Added `agent-template-check`, bundle checksum/validation tests, lifecycle use
  case and HTTP tests, and owner UI schema/type coverage.
- Added `docs/knowledge-source-retirement.md` with reviewed source revisions,
  ownership rules, migration gates, and an explicit statement that no remote
  source repository has been archived or deleted.
- Verified the change with `make verify`, canonical template validation, and
  the complete migration plus PostgreSQL integration suite in a disposable
  `_test` database. Temporary database and role resources were removed.
- Added the repository's own self-contained agent bundle with the same
  byte-identical common rules and bundle checksum used by generated code-repo
  onboarding, plus project-specific architecture, command, project-lifecycle,
  onboarding, runtime, role, and task-workflow contracts.
- Completed generator parity: every eligible target receives an exact
  `.ai/manifest.yaml` plus bugfix, feature, refactor, review, and issue-delivery
  workflows even when runtime classification is unknown. Policy,
  documentation, and archive repositories are now explicitly rejected by
  onboarding so central knowledge sources do not acquire agent architecture.
- Added a documentation index, repository policy validator, and GitHub CI for
  the complete non-destructive `make verify` path. Canonical project docs no
  longer depend on an absolute path to the original architecture reference.
- Re-triaged the only partial journal result and recorded a retirement index
  for all ten historical task directories. Its external PostgreSQL and
  HTTP/NATS integration checks are now explicitly owned by `ms-go-student` and
  `ms-ts-html-validator`; no active task depends on the journal copy.
- Updated the historical onboarding runbook so it no longer instructs an
  operator to reconnect or rescan `prompts`, `journal`, or `wiki`, and removed
  machine-specific development paths from live examples.
- Declared read-only project catalog inspection and approval-gated reversible
  project archive/restore in `.ai/commands.yaml`, closing the policy gap between
  the implemented CLI/Make lifecycle and the repository command allowlist.
- Published annotated pre-retirement tags for `ms-course-promts` revision
  `2a16785` and `ms-course-journal` revision `dd74102`, both named
  `archive/pre-retirement-2026-08-13`, and verified their peeled remote targets.
- Stopped the catalog archive/restore rehearsal before any mutation: Docker API
  calls were unresponsive and the existing PostgreSQL port returned an I/O
  error for `global/pg_filenode.map` during the initial project list. Docker
  Desktop was not restarted because that can affect unrelated local containers.
- After explicit owner authorization, force-restarted only the hung Docker
  Desktop processes. The engine recovered, PostgreSQL completed crash recovery
  on the existing durable volume, and no volume or container data was deleted.
- Applied the sole pending durable-database migration,
  `015_project_lifecycle`, transactionally; migrations `001` through `014`
  remained unchanged.
- A fresh remote-main check found historical onboarding PRs had added agent
  files to the two retirement sources. Removed only those generated
  `AGENTS.md`/`.ai` files in `ms-course-promts` commit `b12d5a6` and
  `ms-course-journal` commit `0c858b8`, then published final annotated snapshot
  tag `archive/final-pre-retirement-2026-08-13` for both repositories.
- Completed catalog archive/restore rehearsals for both sources. Each now has
  two archive audit events and one restore audit event and finishes archived
  with its prior `analyzed` status retained for any future restore.
- After separate owner authorization, archived the private GitHub repositories
  `bemulima/ms-course-promts` and `bemulima/ms-course-journal`. GitHub reports
  `isArchived: true` for both; their final `main` revisions and annotated
  snapshot tags remain readable, and neither repository had an open issue or
  pull request. No local checkout, managed clone, database history, or remote
  repository was deleted.
- Audited every `course-wiki` subject page against repository-owned canonical
  documentation. All 31 subject pages are accounted for; thirteen destinations
  from the initial plan were renamed or consolidated, and remote commits after
  the reviewed source revision add only generated onboarding files.
- Created and verified complete Git bundles for `ms-course-journal`,
  `ms-course-promts`, and `course-wiki` outside their source directories. After
  the owner narrowed the cleanup to local files only, moved the three primary
  local directories to the macOS Trash. GitHub repositories, orchestrator
  managed clones, database history, and snapshots remain intact. The attempted
  GitHub scope escalation was cancelled; `delete_repo` was not granted and no
  remote repository was deleted.

## D4 — persistent data safety, backup, and restore proof

- Added explicit `make backup`, `make backup-status`, and `make restore-check`
  commands for the orchestrator PostgreSQL instance. The backup uses online
  logical dumps of every non-template database plus roles without role
  passwords; it does not stop the six-container control-plane stack or copy
  live `PGDATA`.
- Added an immutable set manifest, SHA-256 payload verification, source
  volume/image metadata, catalog snapshots, no-secret restore information, and
  strict Docker-context, mounted-volume, free-space, source-label, set-format,
  checksum, and isolated-target guards.
- Created `/Volumes/ZX10/platform-backups/course-dev-orchestrator/postgres/2026-09-24T150415Z` and restored it only into
  `d4-restore-postgres-20260924t1516` with a distinct temporary volume. The
  proof recorded database/catalog counts of `course_dev_orchestrator=37`,
  `postgres=0`, `temporal=37`, and `temporal_visibility=3`; it then removed only
  that exact D4-labeled container and volume.
- Added the cross-platform inventory and documented deferred methods for
  inactive PostgreSQL, Tarantool, JetStream, MinIO, ClickHouse, sandbox, and
  workspace state. `orchestrator_data` is intentionally not blanket-archived:
  its actual repositories/worktrees are empty and its Codex area includes auth
  and caches.
- Verified the repository with `make verify`. The separate infrastructure
  `make plan` passed; `make config` remains blocked by the unrelated missing
  `/Volumes/ZX10/Developments/nextjs/.env.example` contract.

## Remaining work

- Do not approve or run legacy plan
  `383dede3-2393-47af-b3db-e6c52bbfa4e8`; cancel it separately if the owner
  wants the historical record closed.
- Do not retry plan `d83dc80c-4df7-4274-9b70-3b00a9683a1c`: its policy task has
  reached the configured three-attempt limit and reviewer findings may change
  repository scope, which requires a new discussion fingerprint.
- Discuss a corrected plan that treats downstream rollout as non-blocking
  unless it is a true prerequisite, resolves the policy contract feedback,
  and determines the exact affected repository set from topology evidence.
- Do not use the archived `ms-course-promts` source or any historical isolated
  worktree as an execution base. Use the verified offline bundle or retained
  GitHub repository only for historical diagnostics.
- Do not switch `WORK_ITEM_GATEWAY` to `github`, push, merge, or deploy without
  separate explicit authorization.
- Exercise two simultaneously approved fake-backed plans to verify the global
  agent limit in a live Temporal run.
- Do not blindly retry the HTTP-runtime task in plan
  `a6f748b2-d619-4536-a308-219cfd364eb6`. Discuss a new plan that places the
  sandbox isolation capability before HTTP/Node/browser runtime confinement
  and binds the exact cross-repository contract before execution.
- The Node and sandbox tasks from the paused plan remain unexecuted. Preserve
  the reviewed browser commit and HTTP diagnostic worktree until the replacement
  plan decides whether to reuse, supersede, or discard them.
- Plans `b0e7f8ff-c596-465b-b338-23e16aee6f9a`,
  `edfb110c-9d44-4a0a-b57e-3bb298232b05`, and
  `0436da42-b1cf-45de-a538-95f546f4ba9a` predate explicit supersession and
  remain independent historical records. Do not guess which is current from
  summary text; normalize them only after the owner names the canonical plan.
- Plan `0436da42-b1cf-45de-a538-95f546f4ba9a` has one of four fake issues
  persisted. The repaired publication path can safely resume the remaining
  three, but do not invoke it without a separate explicit owner action.

## Exact next task

Let the owner inspect the rebuilt `/plans` lifecycle and cards. Do not normalize
the three pre-migration replacement records or publish the remaining three fake
issues for plan `0436da42-b1cf-45de-a538-95f546f4ba9a` unless the owner names
that exact action. Do not publish real issues/PR, start a plan, push, merge, or
deploy without their own subsequent explicit authorizations.

## Testing Policy task completion gate — 2026-09-30

- The execution worker now requires a pinned Testing Policy bundle, runs
  `verify:pr`, and evaluates task-bound `agent-dod.v1` evidence before it can
  complete an attempt. Missing, failed, blocked, or identity-mismatched
  evidence blocks completion.
- The consumer bundle is pinned to Verification source
  `26dce0ab38970457931a9f2ab9273918329192f9`, semantics
  `5a0422683ae97659d8f152df5a2b60d45d1893f9`, bundle SHA-256
  `bdd510d44c9467715ca85a1b95744db2d57390095c70bdc972a08748783f5d58`.
- The worker image includes the policy runtime's Node 20.19.5 executable as
  `/usr/local/bin/node20`; native CI builds the image without publishing it.
- Full `make verify` passed after the code and focused tests were updated.
- The task model does not yet provide a trusted Business Acceptance scope
  signal; the worker currently supplies `not-required`. In-scope business
  acceptance therefore remains an integration gap, and this slice does not
  establish the full program completion criterion.

## Testing Policy final wave — CDO provisioner and graph identity — 2026-10-02

- Bound `test:integration:deps` to the owner’s existing PostgreSQL lifecycle.
  The integration script creates a random, isolated Compose project, waits for
  Postgres health, checks `current_database()`, creates and migrates the `_test`
  database, runs the integration suite, and removes only that project and its
  volumes from an EXIT cleanup trap. The Postgres image is digest-pinned.
- Pinned the GitHub `ubuntu-24.04` provisioner toolchain to the image’s
  Docker Engine 28.0.4 and Docker Compose 2.38.2. CI compares the observed
  versions with the descriptor and uploads a machine-readable runtime identity
  record. The local desktop engine differs (29.2.1 / Compose 5.0.2), so a
  local dependency-backed policy run correctly cannot claim the pinned runtime.
- Added CDO-owned stable `reference_id` and `edge_id` fields to the existing
  Architecture CURRENT catalog. Their SHA-256 identities use canonical source
  identity, service manifest identity, and the owner-declared relation tuple;
  catalog validation rejects missing, duplicate, tampered, or inconsistent
  identities. Tests prove identities are invariant to database project and
  snapshot IDs and input ordering.
- `make verify`, the real-artifact Testing Policy DoD E2E, focused graph
  catalog tests, and manifest/provisioner validation pass. The Docker-backed
  integration command and exact hosted Actions result still require the
  published CI run.
- The platform `architecture-graph.v1.json` report remains blocked. The CDO
  checkout and CI do not contain a versioned CURRENT topology export or exact
  blob pins for each referenced service architecture file. Publishing a
  partial graph would omit authoritative nodes, edges, owners, or source pins.
  The required source is a versioned CDO topology/catalog export plus exact
  per-service commit, path, Git blob OID, and SHA-256 evidence for the
  owner-authored architecture files.

## Testing System — portable CURRENT graph producer — 2026-10-03

- Added CDO-owned `architecture-graph.v1` DTO and JSON Schema plus the
  separate read-only `cmd/architecture-export` producer. It uses the same
  captured persisted topology/discovery source selection as CURRENT, checks
  the catalog against the owner declarations, preserves stable IDs,
  parallel relation tuples and unresolved literal targets, and removes
  transient database identities from portable output.
- Added bounded immutable Git object resolution with canonical source/root
  checks, full commit/object validation, regular-file-only reads, raw-byte
  SHA-256 and Git blob digest verification, replacement-ref/environment
  isolation, and secret-file path rejection. Missing/untracked declarations
  stay diagnostic and never acquire fabricated pins.
- Added optional independently verified CDO-owned
  `architecture-graph-inventory.v1` scope metadata. Exact immutable inventory
  bytes must belong to the clean producer commit, contain sorted unique
  canonical source IDs and match the exported source set. Missing or
  mismatching inventory emits BLOCKED scope diagnoses; a populated local
  topology cannot assert fleet completeness. Producer build Git provenance
  remains independent from the eventual report storage commit.
- Added deterministic compact recursive key ordering and a semantic SHA-256,
  a synthetic shared-consumer fixture, schema/fixture tests, transient-ID
  invariance and parallel/unresolved-edge checks, missing/drifted-pin and
  matching/mismatching-inventory cases, forged catalog rejection, immutable
  read/path/symlink/environment checks, and producer provenance tests.
- `make architecture-export-test`, `make architecture-export-build`, and
  `make verify` pass. No live graph export, actual full-fleet inventory,
  source commit publication or downstream authoritative lock was produced.
  Prior local 37-root verification and canary evidence remain insufficient
  to populate the fleet graph lock. The checked-in graph is explicitly
  synthetic test data; the source tree is currently dirty and cannot be
  labeled as a clean independently pinned producer revision.

- Independent graph-producer review repairs: every sanitized Git invocation
  now explicitly disables lazy promisor fetching with `GIT_NO_LAZY_FETCH=1`.
  The inventory parser rejects repeated decoded keys, escaped duplicates,
  unknown fields and case variants rather than accepting last-value-wins
  JSON semantics. Matching raw SHA/blob regression fixtures prove strict
  duplicate rejection, and the Git subprocess environment regression proves
  inherited lazy-fetch overrides are removed. Focused exporter tests, binary
  build and full `make verify` pass after these repairs. No authoritative
  full-fleet artifact was produced.

## Exact pinned owner fleet producer — 2026-10-03

- Added architecture-fleet-inputs.v1 with exactly 42 sorted canonical Git owners,
  full source commits, declaration blob/digest locks, profiles and service IDs.
  `architecture-export --fleet-inputs --fleet-roots` reads immutable Git objects
  without a database and reuses architecture/v1 parsing, stable graph IDs and
  canonical serialization. The graph binds the normalized fleet lock digest.
- Pinned owner mode excludes invented provider-only consumer/subscriber edges,
  records fully unknown operation scaffold interaction count as semantic debt,
  and resolves only reviewed evidence-backed direct external resource literals.
  Partly known and unresolved declared dependencies remain BLOCKED. The existing
  persisted CURRENT export remains available.
- Focused exporter tests and binary build pass. Two full make verify runs passed
  during implementation; the latest rerun stopped at fmt-check on concurrent
  unrelated `internal/usecase/shardexecution/red_setup_test.go`. That file was
  preserved. After owner schema repairs, all 42 service/operation declaration bundles
  pass canonical validation; the candidate relation audit retains 24 unresolved
  outbound assertions after reviewed external resolution and opaque interface
  descriptor separation. These temporary
  reports are UNPUBLISHED working declaration diagnostics, not immutable fleet
  graph evidence. No authoritative graph, commit or publication was performed.


### Pinned external architecture owners

- Added classified `external_owners` to the fleet lock and `external_owner`
  graph references with exact Git declaration pins and exact service ID matching.
- Kept inventory, completeness and semantic debt denominators scoped to the
  42 repositories; an external testing profile is optional.
- Focused exporter checks passed for deterministic output, immutable pin drift,
  missing roots, classification, duplicate identities and services, unchanged
  fleet counts, and retained explicit unknown dependencies.

## Routing and contract publication prerequisites — 2026-10-06

Publication candidate includes canonical catalog/assets, routing and ContractPlan validation, original planner serialization/fingerprint APIs, and required baseline model declarations. Shard/fanout/sandbox/lifecycle runtime changes remain outside scope. Verification results are recorded by the publication audit before commit.

The Context retrieval entries below are historical dirty-tree implementation checks; exact publication verification follows them.

## Context retrieval R1 — 2026-10-06

Implemented companion `RoutingCoverageReport` without adding fields to approval-bound `RoutingResult` or `ContractPlan`. Inventory, acquisition, target/symbol projection and repository-facts limits have explicit ledgers; incomplete searches preserve NOT_VERIFIED/OMITTED rather than claiming absence. Route/config/source identities are bound to diagnostics. Focused `planner-route-test` passed offline using a temporary Makefile that omits only the `.env` include. Pre-change serialized PlannerOutput and approval fingerprint golden regression passed. R2–R4 and Gold remain in progress.

## Context retrieval R2 — 2026-10-06

Implemented versioned generic models and BuildPlan in `internal/contextretrieval`, independent of platform entities/DB/Temporal/model APIs. Explicit admitted identity/root/read paths and optional route identity mapping retain original routing; all sources remain forbidden for writes. Read-only adapters reuse discovery inventory via nil-default hooks, existing HTTP/NATS AST extraction, strict manifests, portable graph identities/pins, contractref and test files. Exact/docs, Go definitions/declarations/imports, metadata, captured graph, contract and test queries have explicit coverage; semantic callers/references/implementation/error_mapping/schema/history remain UNSUPPORTED. Focused discovery/adapters tests, legacy default-scanner checksum regression and final `context-test` passed offline.

## Context retrieval R3 — 2026-10-06

Implemented scoped hash-registered policy, inert source data, claim-specific authority, freshness, conflict preservation, deterministic ranking/overlap-safe dedup, whole-unit source/context/per-facet/per-source budgets and canonical JSON/Markdown ContextPack v1. Generated inputs are validated against already admitted snapshot documents; captured Git labels remain explicit unknowns. Semantic digest excludes operational IDs/host roots/time, and derived accounting is independently verified. Security regressions prove zero opens of named secret/token/key files and stores (including original/canonical root aliases), reject symlink descendants/hardlinks/nonregular files and withhold credential-like JSON content. Core quality/pack tests and final `context-test` passed.

## Context retrieval R4 — 2026-10-06

Implemented offline `context-prepare`, `context-expand`, `context-evaluate` before config loading. Prepare revalidates snapshot after resolving; Expand requires fresh caller scope/policy/route, live snapshot and deterministic trusted replay of initial Prepare/history. Recomputed malicious cache digests cannot promote provenance/reset budget/depth, including early blocked-return paths. Cumulative bounds retain prior evidence, and JSON/Markdown both expose blocked delta diagnostics. CLI inputs use bounded strict JSON and safe descriptor reads. Final focused tests/build and real binary smoke with empty environment passed: repeated Prepare JSON/digest identical; Expand round-trip count=1. Historical smoke fixture intentionally returns PARTIAL/ROUTING_COVERAGE_UNKNOWN; fresh bound R1 companion COMPLETE is tested with the real collector.

## Context retrieval Gold / integration — 2026-10-06

71 retrieval-gold/v1 fixtures cover 71 scenarios, including 12 adversarial cases. Actual offline CLI run: mandatory recall 34/34, overall recall 34/34, precision 36/36, conflicts 4/4; forbidden scope, silent missing required contract, stale runtime leaks, silent conflict suppression, false COMPLETE and duplicate ratio all zero. Raw missing contract 1/4 = .25 is the intentional negative case with explicit diagnostic. Token estimates sum 62185 across cases; one measured suite latency 155.274 ms is operational, not a performance guarantee. Case outcomes: COMPLETE 21, PARTIAL 37, BLOCKED 10, INVALID 2, STALE 1. Evaluator fault tests prove gate failures rather than labelling all results relevant. All R1 cap modes assert actual counters, including acquired mandatory evidence omitted at global 500.

Independent review found and verified fixes for seven P1 boundaries: token filenames, missing R1 coverage wiring, blocked Markdown outcome, generated graph inputs, policy scope, early-return forged/stale base and secret-store root aliases. Final reviewer reports no remaining P0/P1. Full secret-free `make verify` equivalent passed with repo caches/GOPROXY=off and Compose --env-file /dev/null: policy/control-plane checks, gofmt/diff checks, Go vet/all unit/workflow tests, 40 Python runner tests, runner build, 17 UI tests/build and Compose validation. Dedicated DB/MVP/real-model/OCI certification gates were not invoked because retrieval does not integrate production execution.

All program writes remain inside CDO (plus disposable temporary verification output). HEAD remains 82edd84cd63e1d40ded335463e59da22de01651b; no commit/push/branch/worktree lifecycle action. Original unrelated work was not reverted. Nine unrelated lifecycle/runner files changed concurrently since the pre-program hash baseline; these are preserved and not attributed to retrieval. No other repository, Student pilot, distribution, production gate or business WorkPackage was modified by this program. Next: separately approved read-only service pilot, not executed here. Implementation/CLI/schema/limitations are documented in [local context retrieval](agent-context-retrieval.md).



## Context retrieval exact publication verification — 2026-10-06

The standalone routing/catalog/contracts prerequisite layer and the complete R1–R4+Gold candidate both passed full secret-free verification on current origin/main. Original planner JSON/approval fingerprint compatibility, routing polarity and contract ownership passed. Final offline CLI/Prepare/Expand/determinism and Gold 71/71 passed; recall/precision 1.00 and all critical counters zero. Independent prerequisite/final reviews found P0=0 and P1=0. Original dirty source remains preserved; concurrent release documentation appends are retained and excluded from publication. Verification records and final Git publication proof are under ignored .cache/agent-context/publication-v2.

## Go context retrieval Wave 1 remediation — 2026-10-08

The owner explicitly authorized CDO ProjectSource.ConnectGit and
TaskWorktree.Prepare. Both completed: clean managed main at
`d15af3e9f80c8db3c60260c1fcb72c2495a8ebc8`, unchanged retrieval code relative to
the trusted `3b74db9f5680a128ac9120ce79d3150521dbbeb6` baseline, with a managed task
workspace/branch. The dirty original CDO and six service repositories remain
untouched. Frozen change contracts permit only CDO remediation; one shared
writer implements all changes and a separate writer owns fixture data.

Five audited P1 have RED reproductions and fixes: aggregate scalar selectors,
R1 envelope, saturated routing acquisition, exact-test sibling selection and
missing generalized outbound HTTP root. Physical replay exposed a further
manifestation of the same envelope P1: hundreds of COMPLETE named-file policy
exclusion diagnostics displaced mandatory Course evidence. Its additional RED
regression led to count/hash-bound compaction, preserving individual incomplete,
secret, stale, facet-specific and authority diagnostics.

Focused context tests and the unchanged 71 Gold passed. The native physical
replay executed all 39 original tasks after remediation: strict original gates
27/39, minimum Prepare/expanded precision .875; raw mandatory recall 165/166 and
201/205 respectively. The remaining misses are explicit unsupported SQL bytes,
missing official metric and the secret-excluded Auth assertion. Nine original
owner/layer expectations also remain unresolved or mismatched; no task wording
or label was replaced from observed output. Hence whole-wave full PASS and READY
are not claimed. Detailed safe corpus provenance and unapplied label proposals
are committed beside the integration fixtures.

Independent review initially reported P0=0/P1=2 (outbound syntax verification and
whole-wave diagnostic assertions); fixes and negative regressions are present.
Final `make verify` passed after allowing localhost fixture listeners: policy,
format, Go vet/unit/workflow tests, runner build/tests, 17 UI tests/build and
secret-free Compose configuration. The native CLI retained Gold 71/71 PASS; ten
additional focused and integration Gold variants pass. Independent re-review
reports P0=0/P1=0; whole-wave publication certification remains withheld because
the unchanged original acceptance gates are 27/39. Two separate lexical SQL
Expand probes pass on both fixtures and physical Statistic sources while
retaining semantic UNSUPPORTED diagnostics and all original 39 scores.
No commit or push: the publication plan requires full acceptance PASS before
publication. Candidate files remain in the approved managed task workspace.

## Go Wave 1 final closure audit and implementation — 2026-10-08

CDO upstream remains d15af3e; all six target revisions and 2034 admitted safe
source hashes match the remediation freeze. Original39 and original71 Gold file
hashes are frozen. Four independent read-only audits and a barrier matrix
separate nine owner/layer mismatches, lexical SQL, Auth security exclusion,
missing metric and Auth/User ownership. Root is the only shared code writer.

Six generic routing regressions and one security diagnostic regression reproduced
RED before fixes. The schema regression additionally exposed a synthetic Go
import boundary to migration DDL; DDL remains inspected evidence. Focused routing
and security tests now pass; full all 39 capability-aware acceptance and broader
verification are still running. The v2 acceptance overlay retains original166/205
and independently checks all required plan facets. No service code or excluded
Auth assertion bytes were modified/read; no business contract was invented.
Publication remains gated on exact-tree tests and final independent review.

## Go Wave 1 closure verification — 2026-10-08

Fixture and native read-only physical replay both accept all 39: 33 PASS,
3 VALID_UNSUPPORTED and 3 VALID_PARTIAL. Supported original mandatory evidence
is 165/165 Prepare and 201/201 Expand; every resulting supported required facet
is selected (334/334 and 370/370). Original raw denominators remain 166/205;
raw file matches are 166/166 and 202/205, including lexical migration bytes that
never certify the separately unsupported semantic SQL facet. Minimum precision
is .875. Native overlap duplicates are 9/520 in expanded evidence; distinct
authority claims remain selected, and critical safety counters are zero. All71 unchanged
serialized Gold pass, including 12 adversarial fixtures and 4/4 surfaced expected
conflicts; ten remediation and two separate SQL probes remain passing.

Reviewer found new closure scope and owner-gap proof issues; source-anchor
adjacent/same-clause negatives and the missing owner proof reproduced RED.
Fixes plus typed-owner-boundary and foreign-import/comment negatives pass.
Actual Course/Student typed boundaries are retrieved from existing source;
future freeze/type compatibility remains unverified. Twenty-five versioned
closure Gold variants are additive. The owner gap is explicitly a separate
hash-pinned independent ownership audit with current evidence predicates;
it is not reported as an engine-emitted diagnostic.

Full secret-free make verify passed (Go, workflow, runner 9 tests/build,
UI 17 tests/build, Compose config with /dev/null). Native CLI 71 Gold also passed;
all 39 native CLI/API equality and repeat digest checks passed.
Code re-review reports P0=0/P1=0; exact publication-tree review and final full
verification bind the already authorized normal origin/main publication.
No service runtime/metadata changes, new per-service RAG, or vector storage.

## CDO cleanup: cancellation proposal constraint — 2026-10-08

The existing plan supersession operation cancels unpublished work-item proposals.
Migration 019 permits that legitimate cancelled state while retaining paired,
positive external references for issued proposals. Its downgrade refuses to
rewrite cancelled unpublished history; operators must resolve incompatibility
before attempting the older constraint.

Independent disposable PostgreSQL regressions exercise the actual repository
operation against migrations 001–017, reproduce the old constraint rejection,
prove transactional rollback of approvals/proposals/tasks, and verify successful
supersession after 019. Compatible down/up and invalid reference-pair cases are
covered. The complete integration suite passed with owned-resource cleanup.
Migration number 018 remains reserved by the separate unpublished shard work;
019 has no dependency on it and the migration runner supports independently
applied filename versions.

Accumulated shard/composition, lifecycle, sandbox/release and agent distribution
source is being preserved separately. Its source backup is not production
certification. Distribution threat review found unresolved sensitive-read,
pre-read admission, path race and inventory-bound gaps; runtime admission remains
DENIED pending exact certification. These workstreams must not be inferred from
this isolated constraint repair. Published Wave 1 retrieval, Course fixtures,
CI evidence paths and historical owner evidence remain intact.

## Go context retrieval Wave 2 — 2026-10-08

Status: PARTIAL, feature source only; Profile v2 is not production-ready.
The verified baseline is 8e7eeb50a94eec29ccf1337c5ba232db83d61b66 after cleanup
publication. Three independent service audits froze 27 unchanged tasks and
source/test obligations before observation. Services remain read-only.

Generic CDO fixes cover whole ASCII keyword matching, declared alternate
application/usecase shape, metadata/AST-gated outgoing adapters, and explicit
analysis routing with dependent/current-state/negative/conditional guards.
ContextPack/RetrievalPlan/RoutingResult and boundary ownership are unchanged;
the additive Go profile asset is version 1.3.2. No business runtime, migrations,
local RAG, vector storage or model/provider calls were introduced.

Actual final R1/Prepare/Expand replay finds62/62 Prepare anchors,111/111 combined
Expand anchors and56/56 test anchors with precision1.00 at a uniform bounded
128K context/64K per-facet profile. Lower32K/64K diagnostic runs are retained.
All-pack classifications are9 VALID_PARTIAL and18 VALID_BLOCKED. Independent
routing obligations remain12/82 selected (70 unmet); plan-wide required facets
remain121/123 Prepare and196/198 Expand. Two T08 generic proposed contract
obligations remain explicitly diagnosed contract-adapter misses. Its actual
business DTO/application contracts exist and are retrieved; no files were
invented, obligations waived or expected labels rewritten.

Wave1 retains39 scenarios33PASS/3VALID_PARTIAL/3VALID_UNSUPPORTED and108 Gold.
Twenty-seven additive source-hashed Gold scenarios pass, including explicit
T08 gap preservation and dirty snapshot provenance. Full make verify without
.env passes; physical CLI71 core Gold and39 Wave1/27 Wave2 Prepare/Expand/repeat
API-digest comparisons pass. Independent source review P0=0/P1=0 applies to
safe feature preservation and retrieval regressions; main readiness is denied.
All3 target snapshots repeat exactly. Source preservation excludes secrets and
includes the independently pinned497-document fixture corpus.

Next: independently specify a source-bound read-only R1 analysis/typed-boundary
compatibility contract without granting business/write ownership; retain all
27 tasks,82 layer obligations and198 plan-wide requirements for acceptance.
Wave3 has not started. See go-context-retrieval-wave2.md for the bounded scope.
