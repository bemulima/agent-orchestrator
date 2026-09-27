# PostgreSQL backup and isolated restore

This repository owns the active D4 backup path for its PostgreSQL and Temporal
state. The live `postgres_data` Docker volume remains in place. The backup is an
online logical export, so it neither stops PostgreSQL nor copies live `PGDATA`.

## Commands

```sh
make backup BACKUP_COMPONENT=orchestrator-postgres
make backup-status
make restore-check BACKUP_SET=/Volumes/ZX10/platform-backups/course-dev-orchestrator/postgres/<timestamp> \
  RESTORE_TARGET=d4-restore-postgres-<unique-id>
```

`BACKUP_COMPONENT=all-active` currently selects the same validated PostgreSQL
component. The command fails for unsupported components rather than creating an
empty backup. `restore-check` has no default source or destination: both the
backup set and a new target name are required.

## Backup set

Each execution creates one new UTC-named directory below
`/Volumes/ZX10/platform-backups/course-dev-orchestrator/postgres/`. It contains:

- `manifest.json` with format version, source container/service/volume, image,
  PostgreSQL version, method, payload sizes and SHA-256 values;
- `globals.sql`, produced with `pg_dumpall --globals-only --no-role-passwords`;
- a `--format=custom --create` dump for every non-template database;
- source database size metadata and non-private schema/table catalogs;
- `checksums.sha256` and `restore-info.json`.

The manifest contains no environment values or passwords. The payload is
immutable: a successful later validation produces a separate record under
`restore-verifications/`.

The source instance currently contains `course_dev_orchestrator`, `postgres`,
`temporal`, and `temporal_visibility`. Backing up every non-template database
is required because Temporal uses the same PostgreSQL server; the latter two
databases carry its main and visibility persistence.

## Safety controls

The scripts require Docker context `desktop-linux`, the mounted `/Volumes/ZX10`
volume, free-space minimums, one healthy Compose-labeled source PostgreSQL
container, a complete format-version-1 set, and matching SHA-256 values.

The restore target must start with `d4-restore-postgres-` and must not exist.
The script creates a new labeled volume and a new PostgreSQL container using a
distinct restore-admin role. It restores roles and every database, checks that
the expected databases and schema/table catalogs match without selecting row
contents, requires nonempty Temporal and Temporal visibility catalogs, writes a
proof record, then removes only that exact D4-labeled container and volume.

On validation failure, it preserves the exact temporary resources for
inspection. It never writes to the live source volume or container.
