# Architecture conventions

This project follows the technical architecture of
`/Users/marat/Developments/microservices/ms-go-course`. Business entities and
orchestrator workflows are specific to this repository; only the organization
and engineering conventions come from the reference service.

## Adopted conventions

- The executable composition root lives in `cmd/course-dev-orchestrator` and
  performs explicit constructor/struct wiring. There is no DI container.
- Domain entities, sentinel errors, and repository contracts live under
  `internal/domain`.
- Application operations are small, feature-oriented types under
  `internal/usecase`; their public entrypoint is `Handle`.
- Infrastructure is isolated under `internal/adapters`. HTTP uses `chi`,
  PostgreSQL uses `pgx`, and Temporal integration has its own adapter.
- Git checkout resolution is an adapter under `internal/adapters/git`; the
  bounded detector engine is isolated under `internal/discovery` and depends
  only on domain contracts.
- HTTP handlers decode and validate DTOs, delegate to use cases, and render one
  shared JSON error envelope. They do not contain orchestration rules.
- Repository interfaces accept `context.Context` as the first parameter.
  PostgreSQL implementations use the `RepoPG` suffix.
- Configuration is loaded from environment variables with `envconfig`.
- Structured logging uses `zap`; secret values and agent prompts are never
  logged.
- SQL migrations are ordered `*.up.sql`/`*.down.sql` pairs in
  `db/migrations` and are applied to PostgreSQL transactionally.
- Tests use Go's testing package, table-driven cases, and `testify` where it
  improves assertions. Unit tests stay beside code; infrastructure tests live
  under `test/integration`.
- Local tool caches live only under `.cache`.
- Repository role and runtime `ServiceKind` are separate concepts. Content,
  policy, documentation, and archive repositories never become runtime
  topology nodes merely because they contain executable tooling.
- Git source identity is normalized independently of checkout path. Local
  worktrees use their common Git directory when no supported remote exists.
- Discovery reports contain evidence provenance and immutable content
  fingerprints; unchanged retries reuse an existing snapshot.

## Deliberate extensions required by this service

- Temporal workflows and activities are separated into `internal/workflow`
  and `internal/activities`; the Temporal worker is a second process mode of
  the same executable.
- The service exposes unauthenticated liveness/readiness endpoints at the root
  and reserves `/api/v1` for the internal API.
- AI model names are configuration values grouped by `fast`, `standard`,
  `deep`, and `review`; no model name is compiled into the code.
- PostgreSQL and Temporal are separate readiness dependencies. Long-running
  execution state will remain authoritative in Temporal and durable metadata
  will be stored in PostgreSQL.

## Dependency direction

`cmd -> adapters -> usecase -> domain`

The domain never imports adapters. Use cases depend on domain repository
contracts, and adapters implement those contracts. Temporal workflows remain
deterministic and call side-effecting code only through activities.
