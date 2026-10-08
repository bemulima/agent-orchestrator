#!/usr/bin/python3
"""Trusted, fail-closed bwrap adapter for containers that cannot mount procfs.

Codex's Linux helper expects Bubblewrap to report a denied proc mount using a
specific path. Bubblewrap 0.12 reports ``on /proc`` instead, so that helper
version misses its documented no-proc fallback. This adapter probes with a
harmless command once per container, then omits only ``--proc /proc`` when the
probe proves that exact mount is denied. User, PID, network, filesystem,
seccomp, and permission-profile arguments remain owned by Codex.
"""

from __future__ import annotations

import fcntl
import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path


REAL_BWRAP = "/usr/bin/bwrap"
CODEX_HOME = Path(os.environ.get("CODEX_HOME", "/data/codex"))
STATE_FILE = CODEX_HOME / ".cdo-bwrap-proc-state-v1"
LOCK_FILE = CODEX_HOME / ".cdo-bwrap-proc-state-v1.lock"
TRACE_SWITCH = CODEX_HOME / ".cdo-bwrap-trace-proc-fallback"
TRACE_FILE = CODEX_HOME / ".cdo-bwrap-proc-fallback.jsonl"
PROC_DENIAL = re.compile(
    rb"^bwrap: Can't mount proc on (?:/proc|/newroot/proc): "
    rb"(?:Operation not permitted|Permission denied|Invalid argument)$"
)


def is_proc_mount_denial(stderr: bytes) -> bool:
    """Recognize only the known proc-mount denial, never generic bwrap errors."""
    return any(PROC_DENIAL.fullmatch(line.rstrip(b"\r")) for line in stderr.splitlines())


def without_proc_mount(args: list[str]) -> list[str]:
    """Remove one exact proc mount while requiring user and PID isolation."""
    proc_positions = [index for index, arg in enumerate(args) if arg == "--proc"]
    if len(proc_positions) != 1:
        raise ValueError("proc fallback requires exactly one --proc option")
    index = proc_positions[0]
    if index + 1 >= len(args) or args[index + 1] != "/proc":
        raise ValueError("proc fallback requires the exact --proc /proc option")
    if any(flag not in args for flag in ("--unshare-user", "--unshare-pid", "--unshare-net")):
        raise ValueError("proc fallback requires user, PID and network namespaces")
    return args[:index] + args[index + 2 :]


def probe_command() -> list[str]:
    """A harmless, capability-free probe matching the required namespaces."""
    return [
        "--unshare-user",
        "--unshare-pid",
        "--unshare-net",
        "--ro-bind",
        "/",
        "/",
        "--proc",
        "/proc",
        "/bin/true",
    ]


def _trace(value: dict[str, object]) -> None:
    if not TRACE_SWITCH.exists():
        return
    payload = (json.dumps(value, sort_keys=True) + "\n").encode("utf-8")
    fd = os.open(TRACE_FILE, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
    try:
        os.write(fd, payload)
    finally:
        os.close(fd)


def _read_state() -> str | None:
    try:
        state = STATE_FILE.read_text(encoding="ascii").strip()
    except FileNotFoundError:
        return None
    if state not in {"proc-ok", "no-proc"}:
        raise RuntimeError("invalid bwrap proc capability state; refusing to continue")
    return state


def _write_state(state: str) -> None:
    # Replace atomically on the same mounted filesystem as CODEX_HOME. The
    # container mounts /tmp and /data separately, so staging under /tmp makes
    # os.replace fail with EXDEV and correctly—but unnecessarily—blocks launch.
    fd, name = tempfile.mkstemp(prefix=".cdo-bwrap-state-", dir=CODEX_HOME)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="ascii") as state_file:
            state_file.write(state + "\n")
        os.replace(name, STATE_FILE)
    finally:
        try:
            os.unlink(name)
        except FileNotFoundError:
            pass


def _probe() -> tuple[str | None, bytes, int]:
    command = [REAL_BWRAP, *probe_command()]
    with tempfile.TemporaryFile(dir="/tmp") as stderr_file:
        result = subprocess.run(
            command,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=stderr_file,
            close_fds=False,
            check=False,
        )
        stderr_file.seek(0)
        stderr = stderr_file.read()
    if result.returncode == 0:
        return "proc-ok", stderr, result.returncode
    if is_proc_mount_denial(stderr):
        return "no-proc", stderr, result.returncode
    return None, stderr, result.returncode


def _state() -> tuple[str, bytes]:
    lock_fd = os.open(LOCK_FILE, os.O_CREAT | os.O_RDWR, 0o600)
    try:
        fcntl.flock(lock_fd, fcntl.LOCK_EX)
        state = _read_state()
        if state is not None:
            return state, b""
        state, stderr, status = _probe()
        if state is None:
            _trace(
                {
                    "event": "probe-failed-closed",
                    "command": [REAL_BWRAP, *probe_command()],
                    "exit_code": status,
                    "stderr": stderr.decode("utf-8", errors="replace"),
                }
            )
            if stderr:
                sys.stderr.buffer.write(stderr)
            raise SystemExit(status or 1)
        _write_state(state)
        _trace(
            {
                "event": "probe-complete",
                "command": [REAL_BWRAP, *probe_command()],
                "state": state,
                "exit_code": status,
                "stderr": stderr.decode("utf-8", errors="replace"),
            }
        )
        return state, stderr
    finally:
        fcntl.flock(lock_fd, fcntl.LOCK_UN)
        os.close(lock_fd)


def main() -> int:
    args = sys.argv[1:]
    try:
        state, probe_stderr = _state()
        if state == "no-proc" and "--proc" in args:
            effective_args = without_proc_mount(args)
        else:
            effective_args = args
    except (OSError, RuntimeError, ValueError) as error:
        print(f"cdo bwrap adapter refused sandbox launch: {error}", file=sys.stderr)
        return 1

    _trace(
        {
            "event": "bwrap-launch",
            "original_command": [REAL_BWRAP, *args],
            "effective_command": [REAL_BWRAP, *effective_args],
            "proc_state": state,
            "probe_stderr": probe_stderr.decode("utf-8", errors="replace"),
        }
    )
    os.execv(REAL_BWRAP, [REAL_BWRAP, *effective_args])
    return 127


if __name__ == "__main__":
    raise SystemExit(main())
