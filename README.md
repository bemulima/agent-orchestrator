# course-dev-orchestrator

Internal development orchestrator for the educational platform. It will map
service capabilities/contracts, plan approved multi-repository work as a DAG,
run isolated Codex agents through Temporal, verify their output, and integrate
with self-hosted GitLab and Telegram. It is not a public runtime service and it
never merges or deploys automatically.

The current implementation is Stage 1: platform bootstrap. Project discovery,
onboarding, topology, planning, Codex execution, GitLab, and Telegram are
tracked explicitly as remaining work in [docs/progress.md](docs/progress.md).

## Architecture

- `cmd/course-dev-orchestrator`: composition root and CLI process modes.
- `internal/domain`: orchestrator entities, errors, and repository contracts.
- `internal/usecase`: application operations.
- `internal/adapters/http`: chi routes, handlers, and structured request logs.
- `internal/adapters/postgres`: pgx infrastructure.
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
- Limits: `MAX_TASK_ATTEMPTS=3`, `MAX_REVIEW_ATTEMPTS=2`,
  `MAX_REPLANS=2`, `MAX_PARALLEL_TASKS=3`,
  `MAX_REQUIRED_TASK_DEPTH=3`.
- Model profiles: `CODEX_MODEL_FAST`, `CODEX_MODEL_STANDARD`,
  `CODEX_MODEL_DEEP`, `CODEX_MODEL_REVIEW`. Empty values defer model selection
  to the future runner; code contains no model name.
- Integrations: GitLab and Telegram variables are reserved for Stages 7 and 8.

Comma-separate multiple repository roots and Telegram IDs. Never commit `.env`.

## Quality commands

```sh
make fmt
make lint
make test
make verify
```

After `make up && make migrate`, run `make test-integration` to verify that the
initial schema exists. Tests never access user repositories.
