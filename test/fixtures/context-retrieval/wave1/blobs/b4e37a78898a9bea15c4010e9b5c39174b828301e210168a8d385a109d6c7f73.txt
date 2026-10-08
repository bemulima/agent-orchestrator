# Architecture

ms-go-auth owns credentials, OAuth login identities, OAuth transactions, JWT issuance and verification, and refresh sessions. User profiles belong to ms-go-user and role assignments belong to ms-go-rbac. Echo transport exposes HTTP, Core NATS provides request/reply coordination, Auth verification policy and its direct Tarantool adapter own verification-code flows, and PostgreSQL persists auth state.

`internal/domain` owns auth models and ports. `internal/usecase` depends only on those ports for persistence, verification policy, NATS RPC, and OAuth. Inbound HTTP code is in `internal/transport/http/api/v1` and `internal/transport/http/private`; outbound implementations are in `internal/infrastructure`. `cmd/ms-go-auth` wires all dependencies and owns process lifecycle. Neither domain nor usecase imports transport, infrastructure, or GORM.

Google and GitHub OAuth Authorization Code flows are implemented with one-time state and PKCE. This supersedes the former wiki statement that OAuth was only a stub. OAuth can link by normalized verified email and supports accounts without an initial password.

Signup completion reserves a random immutable operation and principal in PostgreSQL
before asking Tarantool to consume proof. The operation is keyed by normalized
email and a SHA256 fingerprint of length-framed domain, email, and trimmed code;
raw codes are never stored or logged by Auth. This low-entropy fingerprint is an
operation key, not a credential; existing provider code-at-rest security remains
a residual risk. Auth's atomic Tarantool receipt operation can recover the same
operation after a lost response within the original signup hard expiry.

Auth freezes the receipt owner, original password hash, and receipt expiry in
`auth_signup_completion`. Principal creation commits with its ownership marker;
an existing account is reusable only when the marker, reserved ID, normalized
email, and original credential match exactly. Auth synchronously requires User
creation acknowledgement, the canonical student assignment acknowledgement, and
RBAC readback. A durable verified-to-completed CAS then owns exactly one signup
token attempt. Completed-code replay is denied, including after token response
loss; ordinary password signin remains available to the completed account.

Password-authenticated signin for an owned verified pending account retries the
same provisioning invariant and terminal CAS, even after proof receipt expiry.
A changed credential or principal fails closed. Refresh and OAuth share the
pending-principal token gate and cannot perform that repair. Persistence and
provider recovery ports are mandatory; no in-memory or destructive-consume
fallback exists in production. The frozen cross-service semantics are defined
by `.ai/contracts/auth-user-rbac-provisioning-v1.md`.

Verification storage, permissions, migrations, cleanup, and retained legacy state are defined by [verification storage](verification-storage.md).
