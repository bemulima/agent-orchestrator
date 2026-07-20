# Repository guidelines

## Before changing files

- Read `docs/architecture-conventions.md`, `docs/implementation-plan.md`, and
  `docs/progress.md`.
- Update `docs/progress.md` after each completed implementation stage.
- Do not claim a stage is complete unless its relevant checks pass.

## Architecture

- `cmd/course-dev-orchestrator` is the explicit composition root and CLI.
- `internal/domain` contains entities, sentinel errors, and repository contracts.
- `internal/usecase` contains application operations; keep handlers thin.
- `internal/adapters/http` owns chi routers and transport DTOs.
- `internal/adapters/postgres` owns pgx repositories and SQL concerns.
- `internal/workflow` must remain deterministic; side effects belong in
  `internal/activities`.
- Preserve dependency direction: adapters depend on use cases/domain, never the
  reverse.

## Security invariants

- Discovery is read-only until a persisted approval exists.
- Never read `.env` files from managed repositories or log secret values.
- Validate canonical repository paths against all configured allowed roots.
- One task uses one repository, worktree, branch, and Codex thread.
- Never implement automatic merge, deploy, or destructive commands without
  resource-specific approval.

## Go and tests

- Use Go standard style, `gofmt`, context-first methods, `Err*` sentinels, and
  feature-oriented filenames.
- Keep Go caches under `.cache`:
  `XDG_CACHE_HOME=$PWD/.cache`, `GOCACHE=$PWD/.cache/go-build`,
  `GOMODCACHE=$PWD/.cache/gomod`, `GOBIN=$PWD/.cache/bin`.
- Run `make verify` before handoff. Integration tests must use fixtures, never
  real user repositories.
