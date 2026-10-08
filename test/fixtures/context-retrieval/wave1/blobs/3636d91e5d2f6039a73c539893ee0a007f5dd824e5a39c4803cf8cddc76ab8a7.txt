# Disposable verification identity fixture

`ms-go-auth-fixture provision-verification-identity` is a closed, one-shot Auth
owner command built only into the disposable V1 verification image. It is not
an HTTP or Gateway route and does not run during Auth startup or migration.

The command accepts `--run-id` and `--user-id`; it reads the password from
stdin. It derives the email as `v1+<run-id>@verification.invalid` and only
accepts the canonical `student` role. The current Auth email validator accepts
this `.invalid` domain, which cannot receive mail. The fixture does not use
signup, email verification, OAuth, User provisioning, or Tarantool. It creates
one password credential through Auth's PostgreSQL owner adapter and verifies
the role through Auth's RBAC client to the private `rbac.assign-role` and
`rbac.checkRole` request/reply contracts.

Execution requires all of the following: the explicit fixture-enabled mode,
`AUTH_APP_ENV=v1-r0`, the expected Auth DB host/user/name, migrations disabled,
the V1 NATS address, no JWT signing secret/key in the fixture process, and a
read-only mounted ownership marker whose run ID, Compose project, and network
match the command input. The server entrypoint does not expose this command.

The first successful invocation prints only JSON with status, run ID, Auth user
ID, and role. An exact repeat returns `already_present` only if the same
password already verifies and RBAC confirms the same role. It never resets an
existing password or changes an incompatible user. If Auth insertion succeeds
but RBAC assignment cannot be confirmed, it prints a sanitized `incomplete`
failure and exits nonzero. The disposable orchestrator then tears down the full
project; it does not attempt a cross-service rollback.

This fixture is not signup and does not assert email ownership. It creates no
JWT or refresh session. Tokens are issued only by a later real Gateway signin.

The local `ms-go-auth-fixture --hash-password` mode reads a single plaintext password from stdin (8–72 bytes), performs bcrypt, and emits only the hash to stdout. It connects to no service and grants no role or token. The owning coordinator captures stdout into a private file (0600) and must never log that output. Invalid input and hashing failures emit only static phase metadata; password arguments/environment are not accepted. This replaces provider HTTP hash extraction in controlled release fixtures.
