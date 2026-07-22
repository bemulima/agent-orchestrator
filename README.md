# course-dev-orchestrator

Internal development orchestrator for the educational platform. It will map
service capabilities/contracts, plan approved multi-repository work as a DAG,
run isolated Codex agents through Temporal, verify their output, and integrate
with self-hosted GitLab and Telegram. It is not a public runtime service and it
never merges or deploys automatically.

The current implementation includes Stage 1 platform bootstrap, Stage 2
project connection/read-only discovery, Stage 3 evidence-backed onboarding
with approval-gated isolated writes, Stage 4 materialized service topology
with contract drift and impact queries, and Stage 5 approval-gated planning
with a durable Temporal DAG scheduler, and Stage 6 isolated Codex execution
with independent verification and review, Stage 7 self-hosted GitLab issue/MR
synchronization and signed webhooks, and the Stage 8 Telegram owner interface
with polling/webhook delivery and replay-safe inline approvals.

## Architecture

- `cmd/course-dev-orchestrator`: composition root and CLI process modes.
- `internal/domain`: orchestrator entities, errors, and repository contracts.
- `internal/usecase`: application operations.
- `internal/adapters/http`: chi routes, handlers, and structured request logs.
- `internal/adapters/postgres`: pgx infrastructure.
- `internal/adapters/git`: allowlisted local Git resolution and managed clones.
- `internal/discovery`: bounded read-only inventory and evidence detectors.
- `internal/onboarding`: deterministic proposal/manifests and safe merge rules.
- `internal/topology`: deterministic catalog, relation, and drift builder.
- `internal/planning`: deterministic evidence planner and DAG validation.
- `internal/agent`: embedded coder/reviewer JSON Schemas and validation.
- `internal/execution`: prompt boundary, verification, review, and task executor.
- `internal/adapters/gitlab`: bounded self-hosted GitLab REST, fake, and dry-run adapters.
- `internal/adapters/telegram`: bounded Bot API client and durable long poller.
- `runner`: pinned TypeScript `@openai/codex-sdk` process adapter.
- `internal/workflow`: deterministic Temporal workflows.
- `internal/activities`: side-effecting Temporal activities.
- `db/migrations`: tracked PostgreSQL schema migrations.

The precise conventions inherited from `ms-go-course` are documented in
[docs/architecture-conventions.md](docs/architecture-conventions.md).

## Quick start

Requirements: Docker with Compose, Go 1.23+ (the module selects toolchain
1.24.4), and Node.js 18+ for local runner tests.

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
course-dev-orchestrator telegram
course-dev-orchestrator config-check
course-dev-orchestrator project-connect --path /absolute/repository --role service
course-dev-orchestrator project-connect --git-url https://git.example/group/repository.git --role service
course-dev-orchestrator project-list
course-dev-orchestrator project-show --service repository-name
course-dev-orchestrator project-scan --service repository-name
course-dev-orchestrator project-report --service repository-name
course-dev-orchestrator project-onboard --service repository-name [--dry-run]
course-dev-orchestrator project-diff --run-id UUID
course-dev-orchestrator project-approve --run-id UUID --actor owner [--comment text]
course-dev-orchestrator project-reject --run-id UUID --actor owner [--comment text]
course-dev-orchestrator project-apply --run-id UUID [--dry-run]
course-dev-orchestrator topology
course-dev-orchestrator contracts
course-dev-orchestrator contract-drift
course-dev-orchestrator dependencies --service repository-name
course-dev-orchestrator consumers --service repository-name
course-dev-orchestrator plan --file command.md [--project-ids uuid,uuid]
course-dev-orchestrator plan-show --plan-id UUID
course-dev-orchestrator plan-approve --plan-id UUID --actor owner [--comment text]
course-dev-orchestrator plan-reject --plan-id UUID --actor owner [--comment text]
course-dev-orchestrator plan-run --plan-id UUID
course-dev-orchestrator run-status --run-id UUID
course-dev-orchestrator run-pause --run-id UUID
course-dev-orchestrator run-resume --run-id UUID
course-dev-orchestrator run-cancel --run-id UUID
course-dev-orchestrator task-show --task-id UUID
course-dev-orchestrator task-log --task-id UUID
course-dev-orchestrator task-retry --task-id UUID
course-dev-orchestrator task-cancel --task-id UUID
course-dev-orchestrator gitlab-sync --plan-id UUID
course-dev-orchestrator gitlab-links --plan-id UUID
course-dev-orchestrator version
```

`config-check` prints only a redacted summary. It never prints connection
strings, tokens, webhook secrets, Telegram IDs, or model names.

## Configuration

Copy `.env.dist` to `.env` (or run `make bootstrap`). Important groups:

- PostgreSQL: `DATABASE_URL` for local processes and `POSTGRES_*` for Compose.
- Temporal: `TEMPORAL_HOST_PORT`, `TEMPORAL_NAMESPACE`,
  `TEMPORAL_TASK_QUEUE`.
- Repository mount: `PROJECTS_HOST_ROOT` (an absolute host path is recommended).
- Repository safety: `REPOSITORY_ALLOWED_ROOTS`, `REPOSITORY_STORAGE_PATH`,
  `WORKTREE_STORAGE_PATH` (all absolute container paths).
- Discovery bounds: `DISCOVERY_MAX_FILES`, `DISCOVERY_MAX_FILE_BYTES`,
  `DISCOVERY_MAX_TOTAL_BYTES`, `DISCOVERY_MAX_DEPTH`.
- Onboarding bounds/commit identity: `ONBOARDING_MAX_FILE_BYTES`,
  `ONBOARDING_MAX_TOTAL_BYTES`, `ONBOARDING_AUTHOR_NAME`,
  `ONBOARDING_AUTHOR_EMAIL`.
- Limits: `MAX_TASK_ATTEMPTS=3`, `MAX_REVIEW_ATTEMPTS=2`,
  `MAX_REPLANS=2`, `MAX_PARALLEL_TASKS=3`,
  `MAX_REQUIRED_TASK_DEPTH=3`.
- Codex execution: the default is the existing ChatGPT login from local
  `codex-cli`. Run `codex login` once and `make codex-auth-sync`; `make up`
  synchronizes that login automatically when it exists. No API key is
  required. `CODEX_MODEL_FAST`, `CODEX_MODEL_STANDARD`, `CODEX_MODEL_DEEP`,
  and `CODEX_MODEL_REVIEW` select models by task profile and reviewer role.
  Empty values defer model selection to Codex.
- GitLab: `GITLAB_BASE_URL`, `GITLAB_TOKEN`, `GITLAB_CONTROL_PROJECT`, and
  `GITLAB_DRY_RUN`. New GitLab 19+ webhooks should use
  `GITLAB_WEBHOOK_SIGNING_TOKEN=whsec_<base64-key>`; the legacy
  `GITLAB_WEBHOOK_SECRET` header token remains supported during migration.
- Telegram: `TELEGRAM_BOT_TOKEN`, comma-separated
  `TELEGRAM_ALLOWED_USER_IDS`/`TELEGRAM_ALLOWED_CHAT_IDS`,
  `TELEGRAM_POLL_TIMEOUT`, and `TELEGRAM_CALLBACK_TTL`. Polling is the default.
  To use a webhook, also set an HTTPS `TELEGRAM_WEBHOOK_URL` and a 16–256
  character `TELEGRAM_WEBHOOK_SECRET` accepted by the Bot API.

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

For the Docker Compose stack, `PROJECTS_HOST_ROOT` is the host directory bound
into both the API and worker at `/projects`. Persist container paths such as
`/projects/microservices/service-name`; host `/Users/...` paths do not exist in
the containers. The reviewed inventory, exclusions, roles, connection waves,
and exact container commands are in
[`docs/repository-onboarding-runbook.md`](docs/repository-onboarding-runbook.md).

## Approved onboarding

Stage 3 turns the latest clean discovery snapshot into a stored proposal and
unified diff. Proposal generation is read-only: neither `AGENTS.md` nor `.ai`
is written in the connected checkout. Existing user-authored YAML values and
Markdown instructions are preserved; differing discovered values become
explicit conflicts. Prompt/instruction files are linked by path and checksum,
never copied or rewritten automatically. Generated repository paths are
portable and do not embed an absolute local checkout path.

Only files supported by discovery evidence are proposed. Depending on the
evidence, this can include `AGENTS.md`, `.ai/service.yaml`, architecture,
commands, HTTP/event/database contracts, specialized agent instructions,
workflows, and `.ai/discovery/latest-report.json`. Missing evidence means the
corresponding file is omitted.

The normal owner flow is:

```sh
make project-onboard SERVICE=repository-name
make project-diff RUN_ID=uuid
make project-apply RUN_ID=uuid DRY_RUN=true
make project-approve RUN_ID=uuid ACTOR=owner COMMENT="reviewed proposal"
make project-apply RUN_ID=uuid
```

`project-apply DRY_RUN=true` validates the base commit, proposal/file
checksums, formats, and source cleanliness without creating a worktree. A real
apply requires a persisted approval and creates an `ai/onboard-*` branch in a
dedicated path below `WORKTREE_STORAGE_PATH`. It atomically writes and stages
only `AGENTS.md` and `.ai/**`, runs `git diff --check`, commits there, and then
verifies that the connected source checkout still has its original clean HEAD.
Repeated prepare/apply operations reuse the same run and commit. Without
GitLab configuration, apply stops at the local worktree commit. With a matching
GitLab base URL/token, `GITLAB_DRY_RUN=true` still suppresses all external
writes; setting it to `false` after approval pushes the onboarding branch,
reuses or creates an open merge request, and persists its `GitLabLink`. GitLab
credentials are never added to a remote URL or logged. Stage 3 never merges or
deploys. Stage 7 uses the same host/token boundary for broader synchronization.

The Stage 3 HTTP API is also synchronous:

- `POST /api/v1/projects/{projectId}/onboard`;
- `GET /api/v1/onboarding-runs/{runId}`;
- `GET /api/v1/onboarding-runs/{runId}/diff`;
- `POST /api/v1/onboarding-runs/{runId}/approve`;
- `POST /api/v1/onboarding-runs/{runId}/reject`;
- `POST /api/v1/onboarding-runs/{runId}/apply`.

The prepare/apply request body is `{"dry_run":true|false}`. Approval and
rejection accept `{"actor":"owner","comment":"..."}`.

## Service topology and contract drift

Stage 4 rebuilds a versioned, materialized catalog from the latest persisted
discovery snapshot of every scanned project. The rebuild does not reopen or
write connected repositories. Content, policy, documentation, and archive
repositories remain visible as projects but never become runtime topology
nodes.

The catalog stores service purpose and stack, capabilities, database/resource
ownership, HTTP/event/database/gRPC contracts, gateway/frontend/infrastructure
relations, and producer/consumer drift. HTTP paths and event subjects retain
their observed version while using a canonical contract code for correlation.
Missing producers are `critical`, incompatible producer/consumer versions are
`error`, and ambiguous multiple producers are `warning`. Rebuilds with the
same fingerprint reuse the current revision; changed rebuilds atomically
replace all materialized rows, so stale relations cannot remain.

```sh
make topology
make contracts
make contract-drift
make dependencies SERVICE=repository-name
make consumers SERVICE=repository-name
```

`dependencies` returns direct outgoing relations and transitive incoming
impact; `consumers` returns direct consumers and the same deterministic impact
closure. Project IDs and unique project names are accepted. The Stage 4 HTTP
API is synchronous:

- `POST /api/v1/topology/rebuild`;
- `GET /api/v1/topology` (optional generic `q` filter);
- `GET /api/v1/topology/services` (`q`, `role`, `kind` filters);
- `GET /api/v1/topology/contracts` (`q`, `project_id`, `type`, `direction`);
- `GET /api/v1/topology/contract-drift` (`severity`, `project_id`);
- `GET /api/v1/projects/{projectId}/dependencies`;
- `GET /api/v1/projects/{projectId}/contracts`;
- `GET /api/v1/projects/{projectId}/consumers`.

## Commands, plans, and durable scheduling

Stage 5 converts a natural-language command into a persisted, deterministic
plan over the current topology revision. The planner selects explicit or
evidence-matched projects, creates exactly one task per repository, records
acceptance criteria and write scopes, marks migration/public-contract risk,
adds project dependencies, and assigns verification commands. The validator
rejects unknown projects, missing fields, invalid scopes/profiles, cycles,
excessive dependency depth, and waves wider than `MAX_PARALLEL_TASKS`.

Every MVP plan requires a persisted approval before it can run. Repeated plan,
approval, and run requests reuse the same durable records and Temporal
workflow. The workflow dispatches dependency-ready tasks with bounded
parallelism and supports pause, resume, cancel, activity retry/heartbeat, and
worker restart recovery.

```sh
make plan FILE=change.md
make plan-show PLAN_ID=uuid
make plan-approve PLAN_ID=uuid ACTOR=owner COMMENT="reviewed"
make plan-run PLAN_ID=uuid
make run-status RUN_ID=uuid
make run-pause RUN_ID=uuid
make run-resume RUN_ID=uuid
make run-cancel RUN_ID=uuid
make task-show TASK_ID=uuid
make task-log TASK_ID=uuid
make task-retry TASK_ID=uuid
make task-cancel TASK_ID=uuid
```

Stage 6 extends the durable scheduler with one deterministic `ai/task-*`
worktree, branch, and coder thread per task. The worker persists a new thread
ID from the SDK event stream before accepting the final result, validates the
result against an embedded JSON Schema, and independently compares claims to
Git, write scope, allowlisted checks, artifacts, migrations, and contract
paths. A separate read-only reviewer thread must approve the actual worktree
before the worker commits. The connected source checkout remains a clean,
unchanged base; Stage 6 never pushes, merges, or deploys.

Every coder and reviewer receives the current connected landscape: service
purposes, capabilities, ownership, contracts, relations, and contract drift.
It also receives the complete connected-project catalog, including policy,
documentation, content, and archive repositories with their discovery
evidence and conflicts.
The agent still verifies evidence in its own worktree and requests a bounded
task for another connected repository instead of editing across checkouts.

Reviewer changes resume the same coder thread, but each review uses a fresh
thread. Blocked tasks can request at most three bounded cross-project tasks;
Temporal runs them first and resumes the parent subject to replan/depth/attempt
limits. Manual blockers and verification/review changes pause the run until
`task-retry`. `task-log` exposes durable attempts, structured results,
verification evidence, and artifact metadata.

The TypeScript runner communicates with Go over bounded JSONL, disables agent
network access and approvals, uses `workspace-write` for coders and
`read-only` for reviewers, and applies an explicit secret-free shell
environment policy. The worker stores only a private copy of the local
`codex-cli` credential file in its durable volume; the host Codex history and
configuration are not mounted. Credentials, database settings, and integration
tokens are never included in prompts or child tool environments.

The Stage 5 API is available under `/api/v1`:

- `POST /commands`, `GET /commands/{commandId}`, and
  `POST /commands/{commandId}/plan`;
- `GET /plans/{planId}`, `GET /plans/{planId}/tasks`, and plan
  `approve`, `reject`, and `run` actions;
- `GET /runs/{runId}` and run `pause`, `resume`, and `cancel` actions;
- `GET /tasks/{taskId}`, task `attempts` and `artifacts` queries, and task
  `retry` and `cancel` actions.

Plan lifecycle states are `draft`, `planned`, `awaiting_approval`, `approved`,
`running`, `paused`, `completed`, `failed`, and `cancelled`. Task states are
`draft`, `planned`, `ready`, `running`, `blocked`, `verification`,
`changes_requested`, `completed`, `failed`, and `cancelled`.

## Self-hosted GitLab synchronization

Stage 7 projects an approved plan into a configurable self-hosted GitLab
instance. `GITLAB_CONTROL_PROJECT` receives the parent plan issue; every task
gets an issue in its own connected GitLab project. The adapter maintains
orchestrator labels, Markdown checklists, related-issue links, and
marker-keyed status comments while preserving unrelated user labels. Logical
child issues use the edition-portable related-issues API rather than a
paid-tier hierarchy.

```sh
make gitlab-sync PLAN_ID=uuid
make gitlab-links PLAN_ID=uuid
```

`GITLAB_DRY_RUN=true` is the default. It returns deterministic issue/MR
previews without HTTP calls, Git pushes, or `GitLabLink` writes. Real writes
require the persisted plan approval. Repeated syncs recover by embedded
resource markers and source/target branches, so they reuse issues, comments,
links, branches, and merge requests. A completed task publishes only its
verified `ai/task-*` commit and opens or updates an MR against the recorded
default branch. No adapter method can merge or deploy.

The Stage 7 HTTP routes are:

- `POST /api/v1/plans/{planId}/gitlab/sync`;
- `GET /api/v1/plans/{planId}/gitlab`;
- `POST /api/v1/integrations/gitlab/webhook`.

For GitLab 19+, the webhook receiver verifies the Standard Webhooks
HMAC-SHA256 signature over the raw body, checks a five-minute timestamp
window, and deduplicates the stable `webhook-id`. Older self-hosted versions
can use a constant-time `X-Gitlab-Token` comparison. Issue, MR, and related
pipeline events update separate persisted external states; they never make an
internal task successful and never trigger merge or deploy. Raw webhook
payloads and authentication values are not stored.

## Telegram owner interface

Run `make telegram`. With no webhook URL it removes any previous webhook and
starts long polling from the durable next offset. With a webhook URL it calls
`setWebhook` once; the already-running API receives signed updates at:

- `POST /api/v1/integrations/telegram/webhook`.

Both the Telegram user ID and chat ID must be allowlisted. Supported commands
are `/start`, `/help`, `/projects`, `/connect`, `/analyze`, `/topology`,
`/plan`, `/status`, `/approve`, `/reject`, `/pause`, `/resume`, `/retry`,
`/cancel`, and `/issues`. Natural Russian/English requests are routed through
the same project/planning operations used by CLI and HTTP.

`/approve`, `/reject`, `/pause`, `/resume`, `/retry`, and `/cancel` never
perform the requested change from message text. They issue a short-lived
inline button bound to the exact user, chat, action, resource type, and UUID.
The database stores only a SHA-256 token hash; consumption is atomic, so stale,
cross-user, and repeated clicks fail. Plan cards expose `Подтвердить`,
`Показать задачи`, `Изменить`, and `Отклонить` callbacks.

Telegram update rows contain no command text or raw webhook payload. Replies
are bounded and sanitized; full prompts, `.env` content, logs, diffs, bot
tokens, and adapter errors are never sent. Large results are summarized, with
`/issues <plan-uuid>` returning bounded GitLab issue/MR links.

## Quality commands

```sh
make fmt
make lint
make test
make verify
make mvp-rehearsal
```

After `make up && make migrate`, run `make test-integration` to verify the
schema and PostgreSQL state machines, including durable coder/reviewer thread
separation, Telegram update/callback replay protection, verification, and
artifacts. Tests use only disposable
repositories/fixtures and never access user repositories.

`make mvp-rehearsal` is the final cross-stage check and deliberately requires
an empty project database. It creates a temporary local Git service, runs real
PostgreSQL discovery/onboarding/topology/planning state machines, approves the
plan through a fake Telegram callback, and executes the real Temporal workflow
with a deterministic fake coder/reviewer. While the coder activity is active,
the target restarts PostgreSQL, Temporal, the API, and the normal worker, then
replaces the fixture worker and verifies resume with one durable coder thread.
It synchronizes twice through the fake GitLab boundary and proves that issues,
the MR, branch, plan run, attempt, and review were not duplicated. Fixture
database rows and temporary repositories are removed before success. No Codex,
GitLab, Telegram, or user repository is contacted.
