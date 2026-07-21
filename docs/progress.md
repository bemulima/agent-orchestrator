# Implementation progress

Last updated: 2026-07-21

## Current status

Stages 1–3 are complete and verified. The Docker Compose stack is currently
running with PostgreSQL, Temporal, Temporal UI, the HTTP API, and the worker.
No user repository has been connected. Stage 3 verification used only
disposable fixture repositories. Stages 4–8 have not been implemented and are
not represented by placeholders.

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

## Files changed

- Root: `.dockerignore`, `.env.dist`, `.gitignore`, `AGENTS.md`, `Makefile`,
  `README.md`, `docker-compose.yml`, `go.mod`, `go.sum`.
- Entrypoint: `cmd/course-dev-orchestrator/main.go`.
- Domain/config: `internal/config/*`, `internal/domain/*`.
- Application/transport: `internal/usecase/health/*`,
  `internal/usecase/project/*`,
  `internal/adapters/http/*`.
- Infrastructure: `internal/adapters/git/*`, `internal/adapters/gitlab/*`,
  `internal/adapters/postgres/*`, `internal/discovery/*`,
  `internal/adapters/temporal/*`, `internal/activities/*`,
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

## Remaining work

- Stages 4–8 remain as specified in `docs/implementation-plan.md`: topology,
  planning/DAG execution, Codex runner/verification, GitLab, and Telegram.

## Exact next task

Implement Stage 4 topology and contract drift from the persisted Stage 2
discovery evidence: deterministic capability/ownership/relation rebuild,
producer/consumer correlation, impact paths, drift severity, and API/CLI
queries.
