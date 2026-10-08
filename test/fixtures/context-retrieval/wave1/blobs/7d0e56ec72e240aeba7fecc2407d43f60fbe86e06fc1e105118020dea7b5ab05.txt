# Native macOS development

User Service runs as a macOS process while Docker supplies shared PostgreSQL
and NATS. Its existing environment-based configuration stays topology-neutral;
the native adapter supplies only development endpoints and reuses the approved
local infrastructure secrets.

## Start shared infrastructure

From `learning-platform-infrastructure`:

```sh
make native-infra-up
make native-infra-check
```

This provides PostgreSQL at `127.0.0.1:5432` and NATS at
`nats://127.0.0.1:4222`. It does not start User Service, RBAC, Filestorage, or
the identity application service.

## Run User Service natively

From this repository:

```sh
task migrate:native-status
task migrate:native
task run:native
```

The adapter reads `../../learning-platform-infrastructure/.env` by default.
Set `LW_INFRA_ENV_FILE` only when that approved local secret file is elsewhere.
It derives the `lw_user` database credentials without printing them and applies
these values:

| Setting | Native value |
| --- | --- |
| PostgreSQL | `127.0.0.1:5432`, database and role `lw_user` |
| NATS | `nats://127.0.0.1:4222` |
| RBAC HTTP | `http://127.0.0.1:18080` |
| User Service HTTP port | `18082` |

`USER_NATIVE_HTTP_PORT`, `USER_NATIVE_HTTP_HOST`,
`USER_NATIVE_POSTGRES_HOST`, `USER_NATIVE_POSTGRES_PORT`,
`USER_NATIVE_NATS_URL`, `USER_NATIVE_RBAC_URL`, FileStorage/Image Processor
URLs and their scoped tokens, and `USER_NATIVE_DB_DSN` override the corresponding
native adapter settings. The native process binds to `127.0.0.1` by default.
Keep passwords, JWT material, and service tokens in the approved infrastructure
dotenv file; the helper does not print their values.

FileStorage and Image Processor clients are configured at
`127.0.0.1:18085` and `127.0.0.1:18086` by default. Their HTTP calls are only
needed for avatar operations; this native startup check does not require those
application services to be running. Identity verification belongs to Auth; the
User Service has no verification-service endpoint configuration.

`GET /internal/health` is the existing listener availability endpoint. It does
not probe PostgreSQL or NATS. User Service connects to NATS during startup;
its `user.create-user` Core NATS request/reply handler remains unchanged.

Stop the native process with `Ctrl-C`. To stop shared infrastructure without
deleting volumes or networks, run `make native-infra-down` in the infrastructure
repository.

## Docker Compose path

Standalone Docker mode remains available:

```sh
task up
task migrate-up
```

Compose starts its own PostgreSQL and NATS services. It exposes Nginx at
`127.0.0.1:8000` and standalone PostgreSQL at `127.0.0.1:5433` by default.
Override `NGINX_HOST_PORT` or `POSTGRES_HOST_PORT` when those host ports are in
use. Docker and native migration tasks use the same `user-service-migrate`
command and the same `migrations` directory; only their database endpoints
differ. Stop standalone resources with `task down`.
