#!/usr/bin/env python3
"""Verify a PostgreSQL backup by restoring it only into an explicit D4 target."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


FORMAT_VERSION = 1
EXPECTED_CONTEXT = os.environ.get("EXPECTED_DOCKER_CONTEXT", "desktop-linux")
BACKUP_ROOT = Path(os.environ.get("BACKUP_ROOT", "/Volumes/ZX10/platform-backups"))
ZX10_ROOT = Path("/Volumes/ZX10")
COMPONENT_ROOT = ZX10_ROOT / "platform-backups" / "course-dev-orchestrator" / "postgres"
MIN_FREE_BYTES = 2 * 1024 * 1024 * 1024
RESTORE_PREFIX = "d4-restore-postgres-"
RESTORE_ADMIN = "d4_restore_admin"


class RestoreError(RuntimeError):
    """A restore validation guard failed."""


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
        raise RestoreError(f"command failed ({argv[0]}): {detail or 'no diagnostic'}")
    return "" if stdout is not None else completed.stdout


def docker(*args: str, stdout: Any | None = None) -> str:
    return run(["docker", *args], stdout=stdout)


def require_context_and_space() -> None:
    actual = docker("context", "show").strip()
    if actual != EXPECTED_CONTEXT:
        raise RestoreError(f"refusing Docker context {actual!r}; expected {EXPECTED_CONTEXT!r}")
    if not ZX10_ROOT.is_dir() or not os.path.ismount(ZX10_ROOT):
        raise RestoreError(f"{ZX10_ROOT} is not a mounted host volume")
    if shutil.disk_usage(ZX10_ROOT).free < MIN_FREE_BYTES:
        raise RestoreError(f"at least {MIN_FREE_BYTES} bytes of free ZX10 space are required")


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def load_and_verify_set(raw_path: str) -> tuple[Path, dict[str, Any]]:
    expected_root = COMPONENT_ROOT.resolve()
    if BACKUP_ROOT.resolve() != (ZX10_ROOT / "platform-backups").resolve():
        raise RestoreError(f"BACKUP_ROOT must be exactly {ZX10_ROOT / 'platform-backups'}")
    backup_set = Path(raw_path).resolve()
    try:
        backup_set.relative_to(expected_root)
    except ValueError as error:
        raise RestoreError(f"backup set must be below {expected_root}") from error
    if backup_set.parent != expected_root or not backup_set.is_dir():
        raise RestoreError("backup set must be one direct immutable set directory")
    manifest_path = backup_set / "manifest.json"
    checksums_path = backup_set / "checksums.sha256"
    if not manifest_path.is_file() or not checksums_path.is_file():
        raise RestoreError("backup set is missing manifest.json or checksums.sha256")
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except ValueError as error:
        raise RestoreError("backup manifest is not valid JSON") from error
    if manifest.get("format_version") != FORMAT_VERSION:
        raise RestoreError(f"unsupported backup format version: {manifest.get('format_version')!r}")
    if manifest.get("source_component") != "course-dev-orchestrator-postgres":
        raise RestoreError("backup set is not an orchestrator PostgreSQL backup")
    image = str(manifest.get("software_image", ""))
    if not image.startswith("postgres:"):
        raise RestoreError("backup set does not name a supported PostgreSQL image")
    files = manifest.get("files")
    if not isinstance(files, list) or not files:
        raise RestoreError("backup manifest contains no payload files")
    expected_checksums: dict[str, str] = {}
    for item in files:
        if not isinstance(item, dict) or not isinstance(item.get("path"), str) or not isinstance(item.get("sha256"), str):
            raise RestoreError("backup manifest has malformed file metadata")
        relative = Path(item["path"])
        if relative.is_absolute() or ".." in relative.parts:
            raise RestoreError("backup manifest has an unsafe payload path")
        payload = backup_set / relative
        if not payload.is_file():
            raise RestoreError(f"backup payload is missing: {relative}")
        # A database without user tables has a valid empty catalog snapshot. The
        # actual logical dumps and globals are the recovery payloads that must
        # never be empty.
        if (relative.parts[0] == "databases" or relative.as_posix() == "globals.sql") and payload.stat().st_size == 0:
            raise RestoreError(f"required backup payload is empty: {relative}")
        actual = sha256(payload)
        if actual != item["sha256"]:
            raise RestoreError(f"checksum mismatch for {relative}")
        expected_checksums[relative.as_posix()] = actual
    listed_checksums: dict[str, str] = {}
    for line in checksums_path.read_text(encoding="utf-8").splitlines():
        try:
            digest, relative = line.split("  ", maxsplit=1)
        except ValueError as error:
            raise RestoreError("checksums.sha256 has malformed content") from error
        listed_checksums[relative] = digest
    if listed_checksums != expected_checksums:
        raise RestoreError("checksums.sha256 does not match the manifest payload list")
    source_databases = backup_set / "source-databases.tsv"
    if not source_databases.is_file() or not (backup_set / "globals.sql").is_file():
        raise RestoreError("backup set is missing PostgreSQL restore prerequisites")
    return backup_set, manifest


def require_target(target: str, manifest: dict[str, Any]) -> tuple[str, str]:
    if not target.startswith(RESTORE_PREFIX) or len(target) <= len(RESTORE_PREFIX):
        raise RestoreError(f"restore target must begin with {RESTORE_PREFIX!r}")
    if any(character not in "abcdefghijklmnopqrstuvwxyz0123456789-" for character in target):
        raise RestoreError("restore target may use only lowercase letters, digits, and hyphens")
    volume = f"{target}-data"
    source_container = str(manifest.get("source_container", ""))
    source_volume = str(manifest.get("source_volume", ""))
    if target == source_container or volume == source_volume:
        raise RestoreError("restore target overlaps the live source container or volume")
    if docker("ps", "-a", "--filter", f"name=^/{target}$", "--format", "{{.ID}}").strip():
        raise RestoreError(f"restore container already exists: {target}")
    if docker("volume", "ls", "--filter", f"name=^${volume}$", "--format", "{{.Name}}").strip():
        raise RestoreError(f"restore volume already exists: {volume}")
    return target, volume


def psql(container: str, database: str, sql: str) -> str:
    return docker(
        "exec", "-u", "postgres", container,
        "psql", "-X", "-U", RESTORE_ADMIN, "-d", database,
        "-At", "-F", "\t", "-v", "ON_ERROR_STOP=1", "-c", sql,
    )


def wait_for_postgres(container: str) -> None:
    for _ in range(30):
        completed = subprocess.run(
            [
                "docker", "exec", "-u", "postgres", container,
                "psql", "-X", "-U", RESTORE_ADMIN, "-d", RESTORE_ADMIN,
                "-v", "ON_ERROR_STOP=1", "-c", "SELECT 1",
            ],
            check=False,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        if completed.returncode == 0:
            return
        time.sleep(1)
    raise RestoreError("isolated PostgreSQL target did not become ready within 30 seconds")


def copy_and_restore(backup_set: Path, container: str, databases: list[str]) -> None:
    globals_path = backup_set / "globals.sql"
    docker("cp", str(globals_path), f"{container}:/tmp/d4-globals.sql")
    docker(
        "exec", "-u", "postgres", container,
        "psql", "-X", "-U", RESTORE_ADMIN, "-d", RESTORE_ADMIN,
        "-v", "ON_ERROR_STOP=1", "-f", "/tmp/d4-globals.sql",
    )
    for database in databases:
        dump = backup_set / "databases" / f"{database}.dump"
        if not dump.is_file():
            raise RestoreError(f"database dump is missing: {database}")
        target_dump = f"/tmp/d4-{database}.dump"
        docker("cp", str(dump), f"{container}:{target_dump}")
        restore_args = [
            "exec", "-u", "postgres", container,
            "pg_restore", "-U", RESTORE_ADMIN,
            "--exit-on-error",
        ]
        # initdb always creates the administrative `postgres` database. It is
        # empty in this target, so restore its contents in place; creating it
        # again would be an attempted overwrite and pg_restore correctly fails.
        if database == "postgres":
            restore_args.extend(["-d", "postgres"])
        else:
            restore_args.extend(["-d", RESTORE_ADMIN, "--create"])
        restore_args.append(target_dump)
        docker(*restore_args)


def read_database_names(backup_set: Path) -> list[str]:
    names: list[str] = []
    for row in (backup_set / "source-databases.tsv").read_text(encoding="utf-8").splitlines():
        name, _size = row.split("\t", maxsplit=1)
        if not name or any(character not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_" for character in name):
            raise RestoreError("source-databases.tsv contains an unsupported database name")
        names.append(name)
    if not names:
        raise RestoreError("source-databases.tsv is empty")
    return names


def verify_catalogs(backup_set: Path, container: str, databases: list[str]) -> dict[str, int]:
    restored_databases = set(
        psql(container, RESTORE_ADMIN, "SELECT datname FROM pg_database WHERE datallowconn").splitlines()
    )
    missing = sorted(set(databases) - restored_databases)
    if missing:
        raise RestoreError(f"restored target is missing databases: {', '.join(missing)}")
    table_counts: dict[str, int] = {}
    for database in databases:
        expected_catalog = (backup_set / "catalog" / f"{database}.tsv")
        if not expected_catalog.is_file():
            raise RestoreError(f"catalog file is missing: {database}")
        expected = set(item for item in expected_catalog.read_text(encoding="utf-8").splitlines() if item)
        actual = set(
            item for item in psql(
                container,
                database,
                "SELECT schemaname, tablename FROM pg_tables "
                "WHERE schemaname NOT IN ('pg_catalog', 'information_schema') "
                "ORDER BY schemaname, tablename",
            ).splitlines() if item
        )
        if actual != expected:
            raise RestoreError(f"catalog mismatch after restore for database {database}")
        table_counts[database] = len(actual)
    if not {"temporal", "temporal_visibility"}.issubset(set(databases)):
        raise RestoreError("backup does not include both Temporal persistence databases")
    if table_counts["temporal"] == 0 or table_counts["temporal_visibility"] == 0:
        raise RestoreError("Temporal persistence database catalog is unexpectedly empty after restore")
    return table_counts


def require_owned_label(kind: str, name: str) -> None:
    if kind == "container":
        actual = docker("inspect", "--format={{index .Config.Labels \"codex.d4.restore\"}}", name).strip()
    else:
        actual = docker("volume", "inspect", "--format={{index .Labels \"codex.d4.restore\"}}", name).strip()
    if actual != "true":
        raise RestoreError(f"refusing to remove {kind} without the D4 ownership label: {name}")


def write_restore_proof(backup_set: Path, target: str, table_counts: dict[str, int]) -> Path:
    proof_dir = backup_set.parent / "restore-verifications"
    proof_dir.mkdir(mode=0o700, exist_ok=True)
    proof = proof_dir / f"{backup_set.name}--{target}.json"
    if proof.exists():
        raise RestoreError(f"restore proof already exists and will not be overwritten: {proof}")
    payload = {
        "format_version": FORMAT_VERSION,
        "backup_set": backup_set.name,
        "checked_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "restore_status": "VERIFIED_ISOLATED_RESTORE",
        "target": target,
        "target_volume": f"{target}-data",
        "catalog_table_counts": table_counts,
        "cleanup": "container and labeled temporary volume removed after successful proof",
    }
    proof.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    return proof


def restore_check(backup_set_argument: str, target_argument: str) -> None:
    require_context_and_space()
    backup_set, manifest = load_and_verify_set(backup_set_argument)
    target, volume = require_target(target_argument, manifest)
    databases = read_database_names(backup_set)
    image = str(manifest["software_image"])
    created = False
    try:
        docker("volume", "create", "--label", "codex.d4.restore=true", volume)
        docker(
            "run", "-d", "--name", target, "--network", "none", "--label", "codex.d4.restore=true",
            "-e", f"POSTGRES_USER={RESTORE_ADMIN}", "-e", f"POSTGRES_DB={RESTORE_ADMIN}",
            # The disposable target has no published port and no network.
            # Trust avoids creating or persisting a throwaway password solely
            # to run the restore proof.
            "-e", "POSTGRES_HOST_AUTH_METHOD=trust",
            "-v", f"{volume}:/var/lib/postgresql/data", image,
        )
        created = True
        wait_for_postgres(target)
        copy_and_restore(backup_set, target, databases)
        table_counts = verify_catalogs(backup_set, target, databases)
        proof = write_restore_proof(backup_set, target, table_counts)
        require_owned_label("container", target)
        docker("rm", "-f", target)
        require_owned_label("volume", volume)
        docker("volume", "rm", volume)
        print(f"isolated restore check passed; proof: {proof}")
    except Exception:
        if created:
            print(
                f"isolated restore target retained for inspection: container={target} volume={volume}",
                file=sys.stderr,
            )
        raise


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--backup-set", required=True, help="explicit immutable backup-set directory")
    parser.add_argument("--target", required=True, help="explicit isolated d4-restore-postgres-* target name")
    return parser.parse_args()


def main() -> int:
    try:
        args = parse_args()
        restore_check(args.backup_set, args.target)
        return 0
    except RestoreError as error:
        print(f"restore safety check failed: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
