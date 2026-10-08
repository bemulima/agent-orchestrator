"""Trusted candidate OCI command profile. Never called with model-provided flags.

This is a probeable command boundary candidate, not a certified SDK backend.
Only the orchestrator may supply a canonical assigned workspace and exact scope.
"""
from pathlib import Path
import re

LIMITS = {"cpus": "2", "memory": "2g", "pids": "128", "seconds": 1200,
          "output_bytes": 1048576, "tmp_bytes": 268435456, "file_bytes": 16777216}
ROLES = {"contract", "layer", "composition", "reviewer"}
PROTECTED = {".git", ".codex", ".agents", ".ssh", ".aws"}

def writable_files(workspace: Path, role: str, paths: list[str]) -> list[Path]:
    if role not in ROLES or (role == "reviewer" and paths) or (role not in {"reviewer", "layer"} and not paths):
        raise ValueError("invalid role scope")
    if len(paths) > 128 or len(set(paths)) != len(paths):
        raise ValueError("invalid scope cardinality")
    if re.search(r'[\x00-\x1f\x7f,]', str(workspace)):
        raise ValueError("unsafe mount source")
    if not workspace.is_absolute() or workspace.resolve(strict=True) != workspace or not workspace.is_dir():
        raise ValueError("workspace is not canonical")
    result = []
    for name in sorted(paths):
        parts = name.split("/")
        if any(x in {"", ".", ".."} | PROTECTED or x == ".env" or x.startswith(".env.") for x in parts) or re.search(r'[\x00-\x1f\x7f\\*?\[\],]', name):
            raise ValueError("unsafe path")
        if role == "composition" and not name.startswith("cmd/"):
            raise ValueError("composition path is outside cmd")
        target = workspace
        for part in parts:
            target = target / part
            if target.is_symlink():
                raise ValueError("symlink scope")
        if not target.is_file() or target.stat().st_nlink != 1:
            raise ValueError("candidate requires existing, singly linked files")
        result.append(target)
    return result

def docker_command(image: str, workspace: Path, role: str, paths: list[str], command: list[str], name: str) -> list[str]:
    files = writable_files(workspace, role, paths)
    if not re.fullmatch(r'[a-z0-9][a-z0-9./:@_-]+', image) or not re.fullmatch(r'cdo-sandbox-[a-z0-9-]+', name):
        raise ValueError("invalid trusted image/name")
    if not command or any(not isinstance(x, str) or '\0' in x for x in command):
        raise ValueError("invalid command")
    args = ["docker", "run", "--rm", "--name", name, "--network", "none", "--read-only", "--user", "100:101",
            "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true", "--security-opt", "seccomp=builtin",
            "--cpus", LIMITS["cpus"], "--memory", LIMITS["memory"], "--memory-swap", LIMITS["memory"],
            "--pids-limit", LIMITS["pids"], "--ulimit", "core=0:0", "--ulimit", "nofile=256:256",
            "--ulimit", f'fsize={LIMITS["file_bytes"]}:{LIMITS["file_bytes"]}',
            "--tmpfs", f'/tmp:rw,nosuid,nodev,exec,size={LIMITS["tmp_bytes"]},mode=1777',
            "--log-driver", "none", "--mount", f'type=bind,source={workspace},target=/workspace,readonly',
            "--workdir", "/workspace", "--env", "HOME=/tmp/agent-home", "--env", "GOCACHE=/tmp/go-build",
            "--env", "GOPATH=/tmp/go", "--env", "GOMODCACHE=/tmp/go-mod", "--env", "GOPROXY=off", "--env", "GOMAXPROCS=2", "--env", "GOFLAGS=-buildvcs=false -p=2",
            "--env", "GIT_CONFIG_GLOBAL=/etc/cdo/gitconfig", "--env", "GIT_CONFIG_SYSTEM=/etc/cdo/gitconfig",
            "--env", "GIT_TERMINAL_PROMPT=0", "--entrypoint", "/usr/bin/env"]
    for target in files:
        args += ["--mount", f'type=bind,source={target},target=/workspace/{target.relative_to(workspace)}']
    # env -i additionally drops image-level inherited values, including future secrets.
    return args + [image, "-i", "PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin", "HOME=/tmp/agent-home",
                   "GOCACHE=/tmp/go-build", "GOPATH=/tmp/go", "GOMODCACHE=/tmp/go-mod", "GOPROXY=off", "GOMAXPROCS=2", "GOFLAGS=-buildvcs=false -p=2",
                   "GIT_CONFIG_GLOBAL=/etc/cdo/gitconfig", "GIT_CONFIG_SYSTEM=/etc/cdo/gitconfig",
                   "GIT_TERMINAL_PROMPT=0", *command]
