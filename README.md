# course-dev-orchestrator

Internal development orchestrator for the educational platform. It will map
service capabilities/contracts, plan approved multi-repository work as a DAG,
run isolated Codex agents through Temporal, verify their output, and integrate
with self-hosted GitLab and Telegram. It is not a public runtime service and it
never merges or deploys automatically.

The current implementation includes Stage 1 platform bootstrap and Stage 2
project connection/read-only discovery. Onboarding writes, topology, planning,
Codex execution, GitLab, and Telegram remain explicitly tracked in
[docs/progress.md](docs/progress.md).

## Architecture

- `cmd/course-dev-orchestrator`: composition root and CLI process modes.
- `internal/domain`: orchestrator entities, errors, and repository contracts.
- `internal/usecase`: application operations.
- `internal/adapters/http`: chi routes, handlers, and structured request logs.
- `internal/adapters/postgres`: pgx infrastructure.
- `internal/adapters/git`: allowlisted local Git resolution and managed clones.
- `internal/discovery`: bounded read-only inventory and evidence detectors.
- `internal/workflow`: deterministic Temporal workflows.
- `internal/activities`: side-effecting Temporal activities.
- `db/migrations`: tracked PostgreSQL schema migrations.

The precise conventions inherited from `ms-go-course` are documented in
[docs/architecture-conventions.md](docs/architecture-conventions.md).

## Quick start

Requirements: Docker with Compose and Go 1.23+ (the module selects toolchain
1.24.4).

```sh
make bootstrap
make up
make migrate
curl http://localhost:8080/health
curl http://localhost:8080/ready
make workflow-probe
```

Temporal UI is available at `http://localhost:8233` by default. Run
`make temporal-ui` to print the configured address.

`make down` keeps PostgreSQL and orchestrator volumes. To delete them, the user
must explicitly run `docker compose down -v`; no Make target hides that
destructive action.

## Process modes

```text
course-dev-orchestrator serve
course-dev-orchestrator worker
course-dev-orchestrator workflow-probe
course-dev-orchestrator config-check
course-dev-orchestrator project-connect --path /absolute/repository --role service
course-dev-orchestrator project-connect --git-url https://git.example/group/repository.git --role service
course-dev-orchestrator project-list
course-dev-orchestrator project-show --service repository-name
course-dev-orchestrator project-scan --service repository-name
course-dev-orchestrator project-report --service repository-name
course-dev-orchestrator version
```

`config-check` prints only a redacted summary. It never prints connection
strings, tokens, webhook secrets, Telegram IDs, or model names.

## Configuration

Copy `.env.dist` to `.env` (or run `make bootstrap`). Important groups:

- PostgreSQL: `DATABASE_URL` for local processes and `POSTGRES_*` for Compose.
- Temporal: `TEMPORAL_HOST_PORT`, `TEMPORAL_NAMESPACE`,
  `TEMPORAL_TASK_QUEUE`.
- Repository safety: `REPOSITORY_ALLOWED_ROOTS`,
  `REPOSITORY_STORAGE_PATH`, `WORKTREE_STORAGE_PATH` (all absolute).
- Discovery bounds: `DISCOVERY_MAX_FILES`, `DISCOVERY_MAX_FILE_BYTES`,
  `DISCOVERY_MAX_TOTAL_BYTES`, `DISCOVERY_MAX_DEPTH`.
- Limits: `MAX_TASK_ATTEMPTS=3`, `MAX_REVIEW_ATTEMPTS=2`,
  `MAX_REPLANS=2`, `MAX_PARALLEL_TASKS=3`,
  `MAX_REQUIRED_TASK_DEPTH=3`.
- Model profiles: `CODEX_MODEL_FAST`, `CODEX_MODEL_STANDARD`,
  `CODEX_MODEL_DEEP`, `CODEX_MODEL_REVIEW`. Empty values defer model selection
  to the future runner; code contains no model name.
- Integrations: GitLab and Telegram variables are reserved for Stages 7 and 8.

Comma-separate multiple repository roots and Telegram IDs. Never commit `.env`.

## Project connection and discovery

Local repositories must resolve below `REPOSITORY_ALLOWED_ROOTS`. Symlink
escapes, relative paths, non-Git directories, unreadable HEADs, credentialed
HTTP Git URLs, and unsupported URL schemes are rejected. Git URLs are cloned
into a deterministic, collision-safe directory below
`REPOSITORY_STORAGE_PATH`; an existing checkout is never overwritten.

Project identity is based on the normalized remote when available and on the
Git common directory otherwise. Consequently linked issue worktrees do not
become duplicate projects. Every scan records the current/default branch,
commit, dirty state, repository role, content checksum, and evidence-rich
report. Repeated scans of unchanged content reuse the existing snapshot.

Repository roles are `service`, `frontend`, `infrastructure`, `content`,
`policy`, `documentation`, `archive`, and `unknown`. Non-runtime roles are
intentionally kept out of runtime service classification.

Discovery never writes to a connected checkout. It excludes `.git`, caches,
dependencies, build output, symlinks, private `.env` files, and key/certificate
files. Environment example files contribute key names only. Each inferred
fact contains a confidence score, source path, and explanation. Existing
`prompts` and `.ai` files are checksummed and analyzed for conflicts but are
not changed.

Make wrappers use the same application operations:

```sh
make project-connect PATH=/absolute/repository ROLE=service
make project-connect GIT_URL=https://git.example/group/repository.git ROLE=service
make project-list
make project-show SERVICE=repository-name
make project-scan SERVICE=repository-name
make project-report SERVICE=repository-name
```

`PROJECT_PATH` is also accepted instead of `PATH`. The latter is preserved for
the product-specified command interface without breaking the shell executable
search path.

The Stage 2 HTTP API is synchronous and available under `/api/v1`:

- `POST /api/v1/projects/connect`;
- `GET /api/v1/projects`;
- `GET /api/v1/projects/{projectId}`;
- `POST /api/v1/projects/{projectId}/scan`;
- `GET /api/v1/projects/{projectId}/reports/latest`.

Connecting the same source and retrying an unchanged scan are idempotent.

## Quality commands

```sh
make fmt
make lint
make test
make verify
```

After `make up && make migrate`, run `make test-integration` to verify the
schema and PostgreSQL project/snapshot idempotency. Tests use only disposable
fixtures and never access user repositories.
