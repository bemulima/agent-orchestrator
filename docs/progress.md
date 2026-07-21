# Implementation progress

Last updated: 2026-07-20

## Current status

Stages 1 and 2 are complete and verified. The Docker Compose stack is currently
running with PostgreSQL, Temporal, Temporal UI, the HTTP API, and the worker.
No user repository has been connected. Stages 3–8 have not been implemented
and are not represented by placeholders.

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

## Files changed

- Root: `.dockerignore`, `.env.dist`, `.gitignore`, `AGENTS.md`, `Makefile`,
  `README.md`, `docker-compose.yml`, `go.mod`, `go.sum`.
- Entrypoint: `cmd/course-dev-orchestrator/main.go`.
- Domain/config: `internal/config/*`, `internal/domain/*`.
- Application/transport: `internal/usecase/health/*`,
  `internal/usecase/project/*`,
  `internal/adapters/http/*`.
- Infrastructure: `internal/adapters/git/*`, `internal/adapters/postgres/*`,
  `internal/discovery/*`,
  `internal/adapters/temporal/*`, `internal/activities/*`,
  `internal/workflow/*`.
- Database/runtime: `db/migrations/*`, `scripts/migrate*.sh`,
  `docker/Dockerfile`.
- Tests: package-local `*_test.go` files and
  `test/integration/postgres_schema_test.go`, plus Stage 2 discovery fixtures.
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

## Remaining work

- Stages 3–8 remain as specified in `docs/implementation-plan.md`; in
  particular `.ai` onboarding, topology, planning, Codex runner, GitLab, and
  Telegram are not yet implemented.

## Exact next task

Implement Stage 3 proposal persistence and the read-only onboarding generator:
build evidence-backed `.ai/*` and `AGENTS.md` merge proposals, preserve existing
instructions, store unified diffs without touching source checkouts, and add
approval/rejection plus dry-run tests before any worktree write path.
