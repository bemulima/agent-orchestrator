# Implementation progress

Last updated: 2026-07-23

## Current status

Stages 1–8 and the final cross-stage MVP rehearsal are complete and verified.
The Docker Compose stack is currently
running with PostgreSQL, Temporal, Temporal UI, the HTTP API, and the worker.
All 38 requested repositories are connected: 25 through orchestrator-managed
clones of remote default branches and the original 13 through previously
reviewed clean local checkouts. The catalog contains 31 services, two frontends, one
infrastructure repository, and one repository in each of the policy,
documentation, content, and archive roles. The user's primary checkouts were
not modified. The materialized landscape currently includes 34 runtime
services, 433 capabilities, 131 ownership records, 472 contracts, four
explicit relations, and 12 reported contract drifts.

Local Codex CLI execution now uses the existing ChatGPT login by default; no
`CODEX_API_KEY` is required. A three-project planning smoke test was completed
without starting execution. Evidence-backed semantic enrichment is implemented
as a proposal-only analyst pass: it cannot modify a connected checkout or
affect topology until the owner reviews, approves, applies, and rescans its
proposal. A live pilot for `ms-go-http-runtime-validator` produced run
`95482dd4-4a59-48b6-8a51-61e99ec4e662` with 31 verified semantic facts, seven
open questions, and two approved Taskfile commands. The owner approved it on
2026-07-23; dry-run and real apply passed in the isolated
`ai/onboard-ms-go-http-runtime-validator-95482dd44a59` worktree, commit
`5b3c99391d2379b1e9ddeb40f772a371abaf0e85` was pushed, and GitHub draft PR #6
was opened. No live coding task has yet been executed against the 38 projects.
Stage 7 used fake/dry-run GitLab adapters, a local HTTP server, and disposable
PostgreSQL rows; no real GitLab project, issue, branch, or MR was changed.
Stage 8 used a fake Bot API adapter, signed local webhook requests, and
disposable PostgreSQL rows; no real Telegram bot, user, or chat was contacted.

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

## Remaining work

- Review and merge the published pilot PR through the repository workflow before
  rescanning its approved semantic report into the trusted topology.
- Enrich the remaining 37 repositories in reviewed waves; do not approve or
  apply proposals in bulk.
- After the platform context is accepted, run the first real multi-project
  coding plan with independent reviewer agents.

## Exact next task

Rebuild the Compose services with discovery schema v9, rescan all connected
projects, verify that example/test ownership and route false positives are gone,
then regenerate and review `ms-go-validation-orchestrator` semantic enrichment.
