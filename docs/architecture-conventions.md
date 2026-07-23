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
- Discovery removes runtime-only capability, contract, infrastructure,
  ownership, and relation evidence from content, policy, documentation, and
  archive reports. Language/tooling, purpose, commands, configuration, and
  instruction evidence remain available for repository-level analysis.
- Markdown files in a repository explicitly connected as `policy` are stored
  as checksum-only instruction evidence even when they live outside the usual
  `prompts/**`, `.ai/**`, or `AGENTS.md` paths. Archive Markdown is not promoted
  to policy evidence.
- Git source identity is normalized independently of checkout path. Local
  worktrees use their common Git directory when no supported remote exists.
- Discovery reports contain evidence provenance and immutable content
  fingerprints; unchanged retries reuse an existing snapshot only when the
  discovery schema version also matches. Detector-semantic changes increment
  that version so a corrected report can supersede an older snapshot without
  pretending repository content changed.
- HTTP discovery uses framework registrations plus bounded runtime-specific
  parsing for Go `net/http` `HandleFunc` handlers and Python
  `BaseHTTPRequestHandler` methods. Unrestricted conventional health handlers
  are treated as `GET`; other unrestricted Go handlers remain `ANY` unless an
  explicit method guard is present.
- Onboarding proposals are deterministic artifacts stored before approval.
  User-authored YAML/Markdown wins on conflict, while every difference remains
  visible in the proposal and unified diff.
- Approved onboarding writes are confined to `AGENTS.md` and `.ai/**` in a
  dedicated Git worktree. The connected source checkout is treated as an
  immutable clean base and verified again after apply.
- Minimal GitLab onboarding publication is a separate adapter invoked only
  after persisted approval and local verification. It matches the configured
  GitLab host, never embeds a token in Git URLs, and reuses an open MR.
- Topology is a deterministic materialized projection of latest immutable
  discovery snapshots. Fingerprint reuse and transactional replacement make
  rebuilds idempotent and prevent stale relations; repositories are not read
  again during a rebuild.
- Planning is a deterministic projection of a natural-language command and one
  immutable topology revision. A task owns exactly one project; acceptance
  criteria, write scope, verification commands, dependency depth, and risk
  flags are persisted before approval.
- A plan may originate from a raw question/idea or an existing issue. It stays
  in discussion until a dedicated read-only issue manager has produced one
  complete Russian issue proposal per task and the owner submits the version.
- `planner_fingerprint` makes repeated DAG generation idempotent. The public
  plan `fingerprint` additionally binds the canonical issue titles, bodies,
  labels, milestones, assignees, complexity, and model profiles; approval must
  echo this exact value.
- Issue and pull-request publication is available only through the bounded
  work-item gateway. Coder, reviewer, issue-manager, and PR-manager Codex
  threads never receive network or external write capabilities.
- DAG validation is an application boundary. Unknown projects, incomplete
  tasks, cycles, invalid scopes/model profiles, excessive depth, and parallel
  waves above the configured limit never reach Temporal.
- PostgreSQL stores queryable plan/run/task state and audit history. Temporal
  owns durable scheduling, dependency release, pause/resume/cancel signals,
  bounded parallel dispatch, retry, and restart recovery.

## Deliberate extensions required by this service

- Temporal workflows and activities are separated into `internal/workflow`
  and `internal/activities`; the Temporal worker is a second process mode of
  the same executable.
- The service exposes unauthenticated liveness/readiness endpoints at the root
  and reserves `/api/v1` for the internal API.
- AI model and reasoning settings are configuration values grouped by `fast`,
  `standard`, `deep`, and `review`. Defaults target the current ChatGPT-auth
  Codex recommendations and remain overridable without an API key.
- PostgreSQL and Temporal are separate readiness dependencies. Long-running
  execution state will remain authoritative in Temporal and durable metadata
  will be stored in PostgreSQL.
- Stage 6 production schedules extend Stage 5 dispatch with a long-running
  execution activity; the original signal boundary remains available for
  compatibility tests and external cancellation.
- Each task has one isolated worktree/branch and one resumable coder thread.
  Reviewers always use distinct read-only threads. Thread IDs are persisted
  from streaming start events before final structured results are accepted.
- Agent claims are untrusted input. Embedded JSON Schemas validate shape;
  independent Go code checks actual Git paths, write scope, allowlisted
  commands, artifacts, migration pairs, and contract paths before review and
  commit.
- A newly discovered prerequisite is untrusted planning input, not a dynamic
  task. The executor persists the blocker and Temporal pauses the run; the
  owner must create and approve a new issue-backed plan version before any new
  repository task can run.
- Independent plans can execute concurrently. Each DAG has a bounded parallel
  width, while the Temporal worker enforces a global activity limit across all
  plans.
- Dedicated issue and PR manager threads are read-only. They produce strict
  JSON proposals in Russian with repository metadata; separate publishers
  perform idempotent writes only after state/fingerprint checks. The PR manager
  also requires a completed committed attempt and a published task issue.
- The default work-item gateway is an in-process fake that persists only
  `github.example.test` identities, enabling full local execution tests with no
  external write. GitHub publication must be selected explicitly and remains
  dry-run unless a token and write mode are both configured.
- The SDK runner receives secrets only for its own Codex invocation. Codex
  tool subprocesses use an explicit `inherit = none` environment policy and
  receive no API key, database URL, or integration token.
- Stage 7 treats GitLab as a legacy external projection of approved plan/task state.
  The bounded adapter exposes project lookup, issues, notes, related-issue
  links, branch push, and merge-request create/update only; merge, deploy, and
  generic destructive REST methods do not exist.
- GitLab retries recover from stable HTML markers, source/target branches, and
  persisted `GitLabLink` rows. Dry-run uses a separate no-network adapter and
  never persists links or pushes branches.
- Signed GitLab webhooks verify HMAC-SHA256 over the raw Standard Webhooks
  message, enforce a five-minute timestamp window, and deduplicate delivery
  IDs transactionally. Legacy header tokens are constant-time compared.
  External issue and MR states are stored separately and never override the
  orchestrator's authoritative task result.
- Legacy GitLab synchronization is limited to dry-run previews. Real writes are
  fail-closed until a GitLab gateway consumes the same dedicated manager-agent
  proposals used by the GitHub work-item flow.
- Stage 8 keeps Telegram transport in adapters and command/callback routing in
  the application layer. Long polling persists the next update offset, while
  webhook and polling deliveries share the same update-ID deduplication path.
- Telegram mutation grants are opaque, resource-bound, user/chat-bound,
  expiring, and single-use. Only their SHA-256 hashes are persisted; message
  text and raw Telegram payloads are not stored.
- The final MVP rehearsal is opt-in because it restarts local Compose services.
  It refuses a database containing projects or commands, uses only temporary
  Git worktrees and fake external adapters, and asserts database cleanup before
  reporting success.
- Compose exposes one explicitly configured host repository root to both the
  API and worker at the stable `/projects` path. Persisted local paths always
  use that container namespace so discovery and later worker execution resolve
  the same checkout.

## Dependency direction

`cmd -> adapters -> usecase -> domain`

The domain never imports adapters. Use cases depend on domain repository
contracts, and adapters implement those contracts. Temporal workflows remain
deterministic and call side-effecting code only through activities.
