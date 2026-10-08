# Architecture

ms-go-user owns user lifecycle/status and profile data. Authentication credentials and tokens were split to ms-go-auth by migration 0006; roles belong to ms-go-rbac; file bytes and image derivatives belong to their respective services.

HTTP transport exposes authenticated user routes, admin/moderator routes, and a token-protected internal active-user batch. Infrastructure implements PostgreSQL, NATS, auth/RBAC, file, image, and avatar-provider integration; `cmd/user-service` is the composition root. Core NATS user.create-user provisions an idempotent user/profile pair and may import missing OAuth profile fields. Provider avatars are limited to HTTPS allowlisted hosts, 5 MB, and JPEG/PNG/WebP before storage.

Two ownership conflicts remain. user_identity/user_provider and user-facing identity endpoints coexist with ms-go-auth auth_identity. Also AuthMiddleware accepts X-User-Id and X-User-Role as trusted without checking the caller/proxy. Direct service exposure therefore permits header spoofing. These are current risks requiring separate compatibility and security work.

`MS_RBAC` and native `USER_NATIVE_RBAC_URL` contain only the RBAC origin,
`scheme://host[:port]`. The RBAC adapter composes the provider-declared `/api/v1`
mount and the principal operation suffix. Existing configurations containing
`/api/v1` must be changed to the origin when adopting this adapter. The mount is
verified from ms-go-rbac router source, rather than inferred from other services.

The adapter rejects non-HTTP(S) URLs, missing hosts, invalid ports, credentials,
paths (including a trailing slash), queries, and fragments before any HTTP call.
The constructor keeps the Client interface and returns configuration errors from
all client operations.
