#!/usr/bin/env python3
"""Create immutable, logical backups of the running orchestrator PostgreSQL instance.

The script intentionally uses Docker labels rather than ``docker compose`` so it
does not read a local .env file.  PostgreSQL runs in the container's local socket
as its operating-system ``postgres`` user, so no database credential is read or
written by this tool.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import socket
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


FORMAT_VERSION = 1
EXPECTED_CONTEXT = os.environ.get("EXPECTED_DOCKER_CONTEXT", "desktop-linux")
BACKUP_ROOT = Path(os.environ.get("BACKUP_ROOT", "/Volumes/ZX10/platform-backups"))
ZX10_ROOT = Path("/Volumes/ZX10")
COMPONENT = "course-dev-orchestrator-postgres"
MIN_FREE_BYTES = 1024 * 1024 * 1024
COMPOSE_PROJECT = "course-dev-orchestrator"


class BackupError(RuntimeError):
    """A guard or source inspection failed before a backup was considered valid."""


def run(argv: list[str], *, stdout: Any | None = None) -> str:
    completed = subprocess.run(
        argv,
        check=False,
        stdin=subprocess.DEVNULL,
        stdout=stdout if stdout is not None else subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=stdout is None,
    )
    if completed.returncode:
        detail = completed.stderr.strip()
        raise BackupError(f"command failed ({argv[0]}): {detail or 'no diagnostic'}")
    return "" if stdout is not None else completed.stdout


def docker(*args: str, stdout: Any | None = None) -> str:
    return run(["docker", *args], stdout=stdout)


def require_context() -> None:
    actual = docker("context", "show").strip()
    if actual != EXPECTED_CONTEXT:
        raise BackupError(
            f"refusing Docker context {actual!r}; expected {EXPECTED_CONTEXT!r}. "
            "Set EXPECTED_DOCKER_CONTEXT only after an explicit environment review."
        )


def require_backup_root(*, create: bool) -> Path:
    if not ZX10_ROOT.is_dir() or not os.path.ismount(ZX10_ROOT):
        raise BackupError(f"{ZX10_ROOT} is not a mounted host volume")
    expected = (ZX10_ROOT / "platform-backups").resolve()
    actual = BACKUP_ROOT.resolve()
    if actual != expected:
        raise BackupError(f"BACKUP_ROOT must be exactly {expected}, got {actual}")
    if actual.exists() and not actual.is_dir():
        raise BackupError(f"backup root exists but is not a directory: {actual}")
    if create:
        actual.mkdir(mode=0o700, parents=True, exist_ok=True)
    free_bytes = shutil.disk_usage(ZX10_ROOT).free
    if free_bytes < MIN_FREE_BYTES:
        raise BackupError(
            f"{ZX10_ROOT} has {free_bytes} free bytes; at least {MIN_FREE_BYTES} are required"
        )
    return actual


def current_postgres_container() -> str:
    ids = [
        item
        for item in docker(
            "ps",
            "--filter",
            f"label=com.docker.compose.project={COMPOSE_PROJECT}",
            "--filter",
            "label=com.docker.compose.service=postgres",
            "--format",
            "{{.ID}}",
        ).splitlines()
        if item
    ]
    if len(ids) != 1:
        raise BackupError(
            "expected exactly one running course-dev-orchestrator PostgreSQL container; "
            f"found {len(ids)}"
        )
    return ids[0]


def source_metadata(container: str) -> dict[str, Any]:
    labels = json.loads(docker("inspect", "--format={{json .Config.Labels}}", container))
    if labels.get("com.docker.compose.project") != COMPOSE_PROJECT:
        raise BackupError("source container does not have the expected Compose project label")
    if labels.get("com.docker.compose.service") != "postgres":
        raise BackupError("source container does not have the expected postgres service label")
    state = docker("inspect", "--format={{.State.Running}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}", container).strip()
    if state != "true|healthy":
        raise BackupError(f"source PostgreSQL must be running and healthy; found {state!r}")
    image = docker("inspect", "--format={{.Config.Image}}", container).strip()
    if not image.startswith("postgres:"):
        raise BackupError(f"source image is not an expected postgres image: {image!r}")
    mounts = json.loads(docker("inspect", "--format={{json .Mounts}}", container))
    data_mounts = [
        mount
        for mount in mounts
        if mount.get("Type") == "volume" and mount.get("Destination") == "/var/lib/postgresql/data"
    ]
    if len(data_mounts) != 1 or not data_mounts[0].get("Name"):
        raise BackupError("source PostgreSQL does not have exactly one named PGDATA volume")
    volume = data_mounts[0]["Name"]
    if not volume.startswith("course-dev-orchestrator_"):
        raise BackupError(f"source PGDATA volume has an unexpected name: {volume!r}")
    return {
        "container": container,
        "container_name": docker("inspect", "--format={{.Name}}", container).strip().lstrip("/"),
        "service": "postgres",
        "compose_project": COMPOSE_PROJECT,
        "volume": volume,
        "image": image,
    }


def psql(container: str, database: str, sql: str) -> str:
    return docker(
        "exec",
        "-u",
        "postgres",
        container,
        "psql",
        "-X",
        "-U",
        "postgres",
        "-d",
        database,
        "-At",
        "-F",
        "\t",
        "-v",
        "ON_ERROR_STOP=1",
        "-c",
        sql,
    )


def write_text(path: Path, contents: str) -> None:
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    path.write_text(contents, encoding="utf-8")


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def safe_database_filename(database: str) -> str:
    if not database or any(character not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_" for character in database):
        raise BackupError(f"database name cannot be represented safely in this backup format: {database!r}")
    return database


def payload_entries(backup_set: Path) -> list[dict[str, Any]]:
    entries: list[dict[str, Any]] = []
    for path in sorted(item for item in backup_set.rglob("*") if item.is_file() and item.name not in {"manifest.json", "checksums.sha256"}):
        relative = path.relative_to(backup_set).as_posix()
        entries.append({"path": relative, "size_bytes": path.stat().st_size, "sha256": sha256(path)})
    return entries


def create_backup(component: str) -> Path:
    if component not in {"orchestrator-postgres", "all-active"}:
        raise BackupError(
            "supported components are orchestrator-postgres and all-active; "
            "other declared platform data has no active, validated backup mechanism"
        )
    require_context()
    root = require_backup_root(create=True)
    container = current_postgres_container()
    source = source_metadata(container)
    component_root = root / "course-dev-orchestrator" / "postgres"
    if component_root.exists() and not component_root.is_dir():
        raise BackupError(f"component backup path is not a directory: {component_root}")
    component_root.mkdir(mode=0o700, parents=True, exist_ok=True)
    created_at = datetime.now(timezone.utc)
    set_name = created_at.strftime("%Y-%m-%dT%H%M%SZ")
    backup_set = component_root / set_name
    try:
        backup_set.mkdir(mode=0o700)
    except FileExistsError as error:
        raise BackupError(f"backup set already exists and will not be overwritten: {backup_set}") from error

    try:
        database_rows = psql(
            container,
            "postgres",
            "SELECT datname, pg_database_size(datname) "
            "FROM pg_database WHERE datallowconn AND NOT datistemplate ORDER BY datname",
        ).splitlines()
        databases: list[tuple[str, int]] = []
        for row in database_rows:
            name, size = row.split("\t", maxsplit=1)
            safe_database_filename(name)
            databases.append((name, int(size)))
        if not databases:
            raise BackupError("refusing to create an empty PostgreSQL backup")
        write_text(
            backup_set / "source-databases.tsv",
            "".join(f"{name}\t{size}\n" for name, size in databases),
        )
        roles = psql(
            container,
            "postgres",
            "SELECT rolname, rolsuper, rolcanlogin, rolcreatedb, rolcreaterole "
            "FROM pg_roles WHERE rolname NOT LIKE 'pg_%' ORDER BY rolname",
        )
        write_text(backup_set / "roles.tsv", roles)
        version = psql(container, "postgres", "SHOW server_version").strip()

        globals_path = backup_set / "globals.sql"
        with globals_path.open("wb") as handle:
            docker(
                "exec",
                "-u",
                "postgres",
                container,
                "pg_dumpall",
                "--globals-only",
                "--no-role-passwords",
                "-U",
                "postgres",
                stdout=handle,
            )

        for database, _size in databases:
            catalog = psql(
                container,
                database,
                "SELECT schemaname, tablename FROM pg_tables "
                "WHERE schemaname NOT IN ('pg_catalog', 'information_schema') "
                "ORDER BY schemaname, tablename",
            )
            write_text(backup_set / "catalog" / f"{safe_database_filename(database)}.tsv", catalog)
            dump_path = backup_set / "databases" / f"{safe_database_filename(database)}.dump"
            dump_path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            with dump_path.open("wb") as handle:
                docker(
                    "exec",
                    "-u",
                    "postgres",
                    container,
                    "pg_dump",
                    "--format=custom",
                    "--create",
                    "--verbose",
                    "-U",
                    "postgres",
                    f"--dbname={database}",
                    stdout=handle,
                )
            if dump_path.stat().st_size == 0:
                raise BackupError(f"empty database dump produced for {database}")

        restore_info = {
            "restore_status": "PENDING_ISOLATED_RESTORE_CHECK",
            "restore_scope": "A restore check creates only an explicit d4-restore-postgres-* container and matching labeled volume.",
            "source_databases": [name for name, _size in databases],
            "temporal_databases": [name for name, _size in databases if name in {"temporal", "temporal_visibility"}],
        }
        write_text(backup_set / "restore-info.json", json.dumps(restore_info, indent=2, sort_keys=True) + "\n")
        files = payload_entries(backup_set)
        checksums = "".join(f"{item['sha256']}  {item['path']}\n" for item in files)
        write_text(backup_set / "checksums.sha256", checksums)
        manifest = {
            "format_version": FORMAT_VERSION,
            "created_at": created_at.isoformat().replace("+00:00", "Z"),
            "hostname": socket.gethostname(),
            "source_component": COMPONENT,
            "source_container": source["container_name"],
            "source_service": source["service"],
            "source_compose_project": source["compose_project"],
            "source_volume": source["volume"],
            "software_image": source["image"],
            "postgres_version": version,
            "backup_method": "online logical: pg_dumpall --globals-only --no-role-passwords plus pg_dump --format=custom --create per non-template database",
            "files": files,
            "restore_status": "PENDING_ISOLATED_RESTORE_CHECK",
            "secrets": "No environment values or role passwords are stored in metadata; roles.sql omits role passwords.",
        }
        write_text(backup_set / "manifest.json", json.dumps(manifest, indent=2, sort_keys=True) + "\n")
    except Exception:
        # A partially written set is intentionally retained for forensic inspection and is
        # rejected by the restore verifier because it lacks a complete manifest/checksum set.
        raise
    print(f"created immutable PostgreSQL backup set: {backup_set}")
    return backup_set


def backup_status() -> None:
    root = require_backup_root(create=False)
    component_root = root / "course-dev-orchestrator" / "postgres"
    if not component_root.is_dir():
        print("no course-dev-orchestrator PostgreSQL backup sets found")
        return
    found = False
    for manifest_path in sorted(component_root.glob("*/manifest.json")):
        found = True
        try:
            manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
            if manifest.get("format_version") != FORMAT_VERSION:
                status = "UNSUPPORTED_FORMAT"
            else:
                status = str(manifest.get("restore_status", "MISSING_RESTORE_STATUS"))
                proof_dir = component_root / "restore-verifications"
                for proof_path in sorted(proof_dir.glob(f"{manifest_path.parent.name}--*.json")):
                    proof = json.loads(proof_path.read_text(encoding="utf-8"))
                    if (
                        proof.get("format_version") == FORMAT_VERSION
                        and proof.get("backup_set") == manifest_path.parent.name
                        and proof.get("restore_status") == "VERIFIED_ISOLATED_RESTORE"
                    ):
                        status = "VERIFIED_ISOLATED_RESTORE"
                        break
            size = sum(item.stat().st_size for item in manifest_path.parent.rglob("*") if item.is_file())
            print(f"{manifest_path.parent.name}\t{status}\t{size} bytes")
        except (OSError, ValueError):
            print(f"{manifest_path.parent.name}\tINVALID_MANIFEST")
    if not found:
        print("no course-dev-orchestrator PostgreSQL backup sets found")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    subcommands = parser.add_subparsers(dest="command", required=True)
    backup = subcommands.add_parser("backup", help="write one immutable online PostgreSQL backup set")
    backup.add_argument("--component", default="orchestrator-postgres")
    subcommands.add_parser("status", help="list backup-set metadata without contacting Docker")
    return parser.parse_args()


def main() -> int:
    try:
        args = parse_args()
        if args.command == "backup":
            create_backup(args.component)
        else:
            backup_status()
        return 0
    except BackupError as error:
        print(f"backup safety check failed: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
