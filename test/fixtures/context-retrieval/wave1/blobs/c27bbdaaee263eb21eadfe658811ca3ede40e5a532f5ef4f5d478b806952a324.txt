# Repository Guidelines

## Agent bootstrap

Read .ai/rules/common.md, .ai/service.yaml, docs/README.md, docs/openapi.yaml, and every affected contract before changing files. Code, migrations, tests, and repository-owned docs are authoritative.

## Architecture invariants

- internal/domain owns user/profile/provider models; internal/usecase owns user and profile behavior; internal/port owns application interfaces; adapters own HTTP, PostgreSQL, NATS, auth/RBAC, file, image, and avatar-provider integration.
- ms-go-user owns user lifecycle and profile data. Credentials, JWTs, refresh sessions, and login identities belong to ms-go-auth; roles and permissions belong to ms-go-rbac.
- user.create-user is idempotent Core NATS request/reply provisioning. OAuth profile import fills only missing fields and treats avatar import failure as a warning.
- The current user_identity/user_provider tables and HTTP identity endpoints overlap auth identity ownership. Do not expand or remove them without an explicit compatibility/migration decision.
- Direct X-User-Id/X-User-Role trust and the default internal token are security-sensitive known risks.

## Verification and delivery

- Use .ai/commands.yaml; run policy, tracked-file gofmt, go vet ./..., and go test ./....
- API, auth/RBAC middleware, NATS, migration, status, active-user batch, avatar, and identity changes require tests plus contract updates.
- Do not run migrations, external integration/e2e, services, GitHub publication, or deployment without required authorization.
