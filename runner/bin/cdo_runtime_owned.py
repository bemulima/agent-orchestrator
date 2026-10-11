#!/usr/bin/env python3
"""Private host Docker authority for the broker and SDK's fixed OCI workflows.

This is an internal process adapter, never a transport operation. The authenticated
host controller supplies the private configuration. Container names and public
labels are not authority: every operation verifies a durable intent, full ID,
private owner marker and the exact container policy before using the full ID.
"""
from __future__ import annotations

import ast
from contextlib import contextmanager
from dataclasses import dataclass
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import stat
import subprocess
import sys
import time
from types import MappingProxyType
import uuid

from cdo_runtime_controller import ControllerDenied, DockerCLI, RuntimeUnknown, canonical


CONFIG_ENV = "CDO_RUNTIME_OWNED_CONFIG"
CONFIG_FIELDS = frozenset({"version", "execution_id", "controller_id", "protocol_sha256",
    "owner_marker", "state_root", "docker_executable", "docker_endpoint", "daemon_id",
    "command_image_id", "sdk_image_id", "expires_at", "allowed_mounts"})
MAX_CONFIG = 16384
MAX_LEDGER = 262144
MAX_ARGV = 131072
MAX_COMMAND_RESOURCES = 4
MAX_RESOURCES = MAX_COMMAND_RESOURCES + 1
SDK_ROOT = "/execution/sdk-private"
COMMAND_ENV = {
    "HOME": "/tmp/agent-home", "GOCACHE": "/tmp/go-build", "GOPATH": "/tmp/go",
    "GOMODCACHE": "/tmp/go-mod", "GOPROXY": "off", "GOMAXPROCS": "2",
    "GOFLAGS": "-buildvcs=false -p=2", "GIT_CONFIG_GLOBAL": "/etc/cdo/gitconfig",
    "GIT_CONFIG_SYSTEM": "/etc/cdo/gitconfig", "GIT_TERMINAL_PROMPT": "0"}
EXEC_ENV = {"PATH": "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
    "HOME": "/tmp/agent-home", "TMPDIR": "/tmp", "GOCACHE": "/tmp/go-build",
    "GOPATH": "/tmp/go", "GOMODCACHE": "/tmp/go-mod", "GOPROXY": "off",
    "GO111MODULE": "off", "GOMAXPROCS": "2", "GOFLAGS": "-buildvcs=false -p=2"}
DEPENDENCY_ENV = {k: v for k, v in EXEC_ENV.items() if k not in {"GO111MODULE", "GOMODCACHE", "GOFLAGS"}}
DEPENDENCY_ENV.update({"GOMODCACHE": "/dependency/modules", "GOSUMDB": "off",
    "GONOSUMDB": "", "GONOPROXY": "", "GOPRIVATE": "", "GOENV": "off",
    "GOWORK": "off", "GOTOOLCHAIN": "local", "GOFLAGS": "-mod=readonly -buildvcs=false -p=2", "GOVCS": "*:off"})
COMMAND_TMPFS = {"/tmp": "rw,nosuid,nodev,exec,size=268435456,mode=1777",
                 "/dev/shm": "ro,noexec,nosuid,nodev,size=4096"}
SDK_TMPFS = {SDK_ROOT: "rw,noexec,nosuid,nodev,size=8388608,nr_inodes=256,uid=100,gid=101,mode=0700",
             "/tmp": "ro,noexec,nosuid,nodev,size=4096", "/dev/shm": "ro,noexec,nosuid,nodev,size=4096"}
BROKER_KEEPALIVE = "import sys,select,time; deadline=time.monotonic()+1200\nwhile time.monotonic()<deadline:\n r,_,_=select.select([sys.stdin],[],[],1)\n if r and not sys.stdin.buffer.read1(1):break"
_HEX = re.compile(r"[a-f0-9]{64}\Z")
_NAMES = re.compile(r"cdo-(?:sandbox-broker-[a-f0-9]{16}|sdk-state-[a-f0-9]{32})\Z")


class OwnedDenied(ControllerDenied):
    pass


def _deny(code):
    raise OwnedDenied(code)


def _decode(data):
    def pairs(items):
        value = {}
        for key, item in items:
            if key in value:
                _deny("OWNED_JSON_DENIED")
            value[key] = item
        return value
    try:
        return json.loads(data, object_pairs_hook=pairs,
            parse_constant=lambda _: _deny("OWNED_JSON_DENIED"))
    except (ValueError, UnicodeError, RecursionError):
        _deny("OWNED_JSON_DENIED")


def _path(value, exists=True):
    if not isinstance(value, (str, Path)):
        _deny("OWNED_PATH_DENIED")
    path = Path(value)
    if not path.is_absolute() or re.search(r"[,\\\x00-\x1f\x7f]", str(path)):
        _deny("OWNED_PATH_DENIED")
    try:
        if path.resolve(strict=exists) != path:
            _deny("OWNED_PATH_DENIED")
    except (OSError, RuntimeError):
        _deny("OWNED_PATH_DENIED")
    return path


def _private_directory(value):
    path = _path(value)
    info = path.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) != 0o700:
        _deny("OWNED_PRIVATE_ROOT_DENIED")
    return path


def _read_private(value, maximum):
    path = _path(value)
    try:
        before = path.lstat()
        if (not stat.S_ISREG(before.st_mode) or before.st_nlink != 1 or before.st_uid != os.geteuid()
                or stat.S_IMODE(before.st_mode) not in {0o400, 0o600} or before.st_size > maximum):
            _deny("OWNED_PRIVATE_FILE_DENIED")
        fd = os.open(str(path), os.O_RDONLY | os.O_NOFOLLOW)
        with os.fdopen(fd, "rb") as source:
            current = os.fstat(source.fileno())
            if (before.st_dev, before.st_ino) != (current.st_dev, current.st_ino):
                _deny("OWNED_PRIVATE_FILE_DENIED")
            data = source.read(maximum + 1)
            if len(data) > maximum:
                _deny("OWNED_PRIVATE_FILE_DENIED")
            return data
    except OSError:
        _deny("OWNED_PRIVATE_FILE_DENIED")


@dataclass(frozen=True)
class OwnedConfig:
    path: Path
    sha256: str
    execution_id: str
    controller_id: str
    protocol_sha256: str
    owner_marker: str
    state_root: Path
    docker_executable: str
    docker_endpoint: str
    daemon_id: str
    command_image_id: str
    sdk_image_id: str
    expires_at: int
    allowed_mounts: object


def load_config(path=None):
    """Read only a private controller-created file; never accept request JSON."""
    filename = path if path is not None else os.environ.get(CONFIG_ENV)
    file = _path(filename)
    _private_directory(file.parent)
    data = _read_private(file, MAX_CONFIG)
    value = _decode(data)
    if not isinstance(value, dict) or set(value) != CONFIG_FIELDS or type(value["version"]) is not int or value["version"] != 1:
        _deny("OWNED_CONFIG_DENIED")
    try:
        if str(uuid.UUID(value["execution_id"])) != value["execution_id"] or uuid.UUID(value["execution_id"]).int == 0:
            _deny("OWNED_CONFIG_DENIED")
    except (ValueError, TypeError, AttributeError):
        _deny("OWNED_CONFIG_DENIED")
    for key in ("controller_id", "protocol_sha256", "owner_marker"):
        if not isinstance(value[key], str) or not _HEX.fullmatch(value[key]):
            _deny("OWNED_CONFIG_DENIED")
    for key in ("command_image_id", "sdk_image_id"):
        if not isinstance(value[key], str) or not re.fullmatch(r"sha256:[a-f0-9]{64}", value[key]):
            _deny("OWNED_CONFIG_DENIED")
    root = _private_directory(value["state_root"])
    executable = _path(value["docker_executable"])
    info = executable.lstat()
    if (not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_uid not in {0, os.geteuid()}
            or info.st_mode & 0o022 or not info.st_mode & 0o111):
        _deny("OWNED_DOCKER_EXECUTABLE_DENIED")
    if not isinstance(value["docker_endpoint"], str) or not value["docker_endpoint"].startswith("unix:///"):
        _deny("OWNED_DOCKER_ENDPOINT_DENIED")
    _path(value["docker_endpoint"][7:])
    if not isinstance(value["daemon_id"], str) or not re.fullmatch(r"[A-Za-z0-9:_-]{8,128}", value["daemon_id"]):
        _deny("OWNED_CONFIG_DENIED")
    if type(value["expires_at"]) is not int or value["expires_at"] <= 0:
        _deny("OWNED_CONFIG_DENIED")
    mounts = value["allowed_mounts"]
    if not isinstance(mounts, dict) or set(mounts) - {"/workspace", "/dependency", "/reference/supervisor.py", "/reference/cdo_sdk_relay.py", "/reference/schema.json"}:
        _deny("OWNED_MOUNT_CONFIGURATION_DENIED")
    frozen = {}
    for target, spec in mounts.items():
        if isinstance(spec, str):
            source = _path(spec, exists=False)
            if not source.exists() and not source.is_relative_to(root):
                _deny("OWNED_MOUNT_CONFIGURATION_DENIED")
            if source == root or file == source or file.is_relative_to(source):
                _deny("OWNED_MOUNT_CONFIGURATION_DENIED")
            frozen[target] = str(source)
        elif isinstance(spec, dict) and set(spec) == {"under", "kind"}:
            under = _private_directory(spec["under"])
            if spec["kind"] != {"/workspace": "snapshot", "/reference/schema.json": "schema"}.get(target):
                _deny("OWNED_MOUNT_CONFIGURATION_DENIED")
            frozen[target] = MappingProxyType({"under": str(under), "kind": spec["kind"]})
        else:
            _deny("OWNED_MOUNT_CONFIGURATION_DENIED")
    return OwnedConfig(file, hashlib.sha256(data).hexdigest(), value["execution_id"],
        value["controller_id"], value["protocol_sha256"], value["owner_marker"], root,
        str(executable), value["docker_endpoint"], value["daemon_id"], value["command_image_id"],
        value["sdk_image_id"], value["expires_at"], MappingProxyType(frozen))


class _Docker(DockerCLI):
    def inspect(self, target):
        if not isinstance(target, str) or not (_HEX.fullmatch(target) or _NAMES.fullmatch(target)):
            _deny("OWNED_TARGET_DENIED")
        self._check_daemon()
        code, out, err = self._run(["inspect", "--type", "container", target])
        if code:
            expected = [prefix + target.encode() for prefix in (b"Error: No such container: ", b"Error: No such object: ",
                b"Error response from daemon: No such container: ", b"Error response from daemon: No such object: ")]
            if code == 1 and out.strip() in {b"", b"[]"} and err.strip() in expected:
                return None
            raise RuntimeUnknown("OWNED_DOCKER_UNKNOWN")
        try:
            value = _decode(out)
            if not isinstance(value, list) or len(value) != 1 or not isinstance(value[0], dict):
                raise RuntimeUnknown("OWNED_INSPECTION_UNKNOWN")
            return value[0]
        except OwnedDenied:
            raise RuntimeUnknown("OWNED_INSPECTION_UNKNOWN") from None


def _root_helpers():
    """Only literals from trusted broker source, never caller-provided scripts."""
    source = Path(__file__).with_name("cdo_broker.py")
    tree = ast.parse(source.read_text())
    result = set()
    for node in tree.body:
        if isinstance(node, ast.Assign) and isinstance(node.value, ast.Constant) and isinstance(node.value.value, str):
            if any(isinstance(target, ast.Name) and target.id == "VIEW_HELPER" for target in node.targets):
                result.add(node.value.value)
        if isinstance(node, ast.ClassDef) and node.name == "Broker":
            for method in node.body:
                if not isinstance(method, ast.FunctionDef) or method.name not in {"observe_resources", "call"}:
                    continue
                allowed = "code" if method.name == "observe_resources" else "resource_code"
                for assignment in ast.walk(method):
                    if (isinstance(assignment, ast.Assign) and isinstance(assignment.value, ast.Constant)
                            and isinstance(assignment.value.value, str)
                            and any(isinstance(target, ast.Name) and target.id == allowed for target in assignment.targets)):
                        result.add(assignment.value.value)
    return frozenset(result)


class OwnedDocker:
    """Host-only compatibility adapter for fixed broker/SDK internal calls."""
    def __init__(self, config_or_path=None, docker=None, clock=time.time):
        self.config = config_or_path if isinstance(config_or_path, OwnedConfig) else load_config(config_or_path)
        self.docker = docker or _Docker(self.config.docker_executable, self.config.docker_endpoint, self.config.daemon_id)
        self.clock = clock
        self.ledger = self.config.state_root / "owned-runtime.json"
        self.helpers = _root_helpers()

    def _fresh_config(self):
        try:
            current = load_config(self.config.path)
        except OwnedDenied:
            raise RuntimeUnknown("OWNED_CONFIGURATION_CHANGED") from None
        if current.sha256 != self.config.sha256:
            raise RuntimeUnknown("OWNED_CONFIGURATION_CHANGED")

    def _initial(self):
        return {"version": 1, "config_sha256": self.config.sha256, "invalidated": False, "status": "ACTIVE", "resources": {}}

    def _read(self):
        if not self.ledger.exists():
            return self._initial()
        try:
            value = _decode(_read_private(self.ledger, MAX_LEDGER))
        except OwnedDenied:
            raise RuntimeUnknown("OWNED_LEDGER_UNKNOWN") from None
        if (not isinstance(value, dict) or set(value) != {"version", "config_sha256", "invalidated", "status", "resources"}
                or value["version"] != 1 or value["config_sha256"] != self.config.sha256
                or type(value["invalidated"]) is not bool or value["status"] not in {"ACTIVE", "INVALIDATED", "UNKNOWN", "CLEANED"}
                or not isinstance(value["resources"], dict) or len(value["resources"]) > MAX_RESOURCES):
            raise RuntimeUnknown("OWNED_LEDGER_UNKNOWN")
        for name, record in value["resources"].items():
            if (not _NAMES.fullmatch(name) or not isinstance(record, dict)
                    or set(record) != {"id", "resource", "status", "expected", "create_argv"}
                    or (record["id"] is not None and not _HEX.fullmatch(record["id"]))
                    or record["resource"] not in {"command", "sdk"} or record["status"] not in {"INTENT", "CREATED", "STARTED", "REMOVED"}
                    or not isinstance(record["expected"], dict) or not isinstance(record["create_argv"], list)):
                raise RuntimeUnknown("OWNED_LEDGER_UNKNOWN")
            expected = record["expected"]
            labels = expected.get("Config", {}).get("Labels", {})
            required = self._labels(record["resource"], expected.get("policy_sha256"))
            if (expected.get("Name") != "/" + name or not isinstance(labels, dict)
                    or any(labels.get(key) != item for key, item in required.items())
                    or any(key.startswith("cdo.") and key not in required for key in labels)):
                raise RuntimeUnknown("OWNED_LEDGER_BINDING_UNKNOWN")
        return value

    def _save(self, value):
        data = canonical(value)
        if len(data) > MAX_LEDGER:
            raise RuntimeUnknown("OWNED_LEDGER_LIMIT")
        temporary = self.ledger.with_name(".owned-runtime-" + uuid.uuid4().hex)
        fd = os.open(str(temporary), os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        try:
            with os.fdopen(fd, "wb") as output:
                output.write(data); output.flush(); os.fsync(output.fileno())
            os.replace(temporary, self.ledger)
            directory = os.open(str(self.config.state_root), os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
        finally:
            if temporary.exists():
                temporary.unlink()

    @contextmanager
    def _locked(self):
        _private_directory(self.config.state_root)
        lock = self.config.state_root / "owned-runtime.lock"
        fd = os.open(str(lock), os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
        try:
            info = os.fstat(fd)
            if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) != 0o600:
                _deny("OWNED_LOCK_DENIED")
            fcntl.flock(fd, fcntl.LOCK_EX)
            state = self._read()
            try:
                self._fresh_config()
                yield state
            except (RuntimeUnknown, OSError, subprocess.TimeoutExpired):
                state.update(invalidated=True, status="UNKNOWN")
                self._save(state)
                raise RuntimeUnknown("OWNED_RUNTIME_UNKNOWN") from None
        finally:
            os.close(fd)

    def _admit(self, state):
        if state["invalidated"] or state["status"] != "ACTIVE":
            _deny("OWNED_ATTEMPT_INVALIDATED")
        if self.clock() >= self.config.expires_at:
            state.update(invalidated=True, status="INVALIDATED"); self._save(state)
            _deny("OWNED_LEASE_EXPIRED")

    def _labels(self, resource, policy_sha256):
        return {"cdo.execution_id": self.config.execution_id, "cdo.resource": resource,
            "cdo.runtime.controller": self.config.controller_id, "cdo.runtime.protocol": self.config.protocol_sha256,
            "cdo.runtime.owner": self.config.owner_marker, "cdo.runtime.expiry": str(self.config.expires_at),
            "cdo.runtime.config": self.config.sha256, "cdo.runtime.policy": policy_sha256}

    def _mount(self, raw):
        parts = raw.split(","); entries = {}; readonly = False
        for item in parts:
            if item == "readonly" and not readonly:
                readonly = True; continue
            if "=" not in item:
                _deny("OWNED_MOUNT_DENIED")
            key, value = item.split("=", 1)
            key = {"src": "source", "dst": "target", "destination": "target"}.get(key, key)
            if key in entries:
                _deny("OWNED_MOUNT_DENIED")
            entries[key] = value
        if set(entries) != {"type", "source", "target"} or entries["type"] != "bind" or not readonly:
            _deny("OWNED_MOUNT_DENIED")
        target, source = entries["target"], _path(entries["source"])
        spec = self.config.allowed_mounts.get(target)
        if isinstance(spec, str):
            if str(source) != spec:
                _deny("OWNED_MOUNT_DENIED")
        elif spec is not None:
            under = Path(spec["under"])
            if not source.is_relative_to(under) or source == under:
                _deny("OWNED_MOUNT_DENIED")
            relative = str(source.relative_to(under))
            if spec["kind"] == "snapshot":
                # The trusted broker's publication verifier allocates exactly
                # one verification-* child under its private state directory.
                # This admits that finite layout without admitting a subtree.
                snapshot_shape = (r"(?:cdo-trusted-broker-[A-Za-z0-9_-]{6,128}/)?state/"
                                  r"(?:verification-[A-Za-z0-9_-]{6,128}/)?snapshot")
                if not re.fullmatch(snapshot_shape, relative) or not source.is_dir():
                    _deny("OWNED_MOUNT_DENIED")
            elif not re.fullmatch(r"(?:[^/]+/)?control/[^/]+\.json", relative) or not source.is_file() or source.stat().st_size > 5 * 1024 * 1024:
                _deny("OWNED_MOUNT_DENIED")
        else:
            _deny("OWNED_MOUNT_DENIED")
        if (source == self.config.state_root or self.config.path == source or self.config.path.is_relative_to(source)
                or self.ledger == source or self.ledger.is_relative_to(source)):
            _deny("OWNED_MOUNT_DENIED")
        if source.is_file():
            info = source.lstat()
            if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
                _deny("OWNED_MOUNT_DENIED")
        elif not source.is_dir():
            _deny("OWNED_MOUNT_DENIED")
        return target, str(source)

    def _argv(self, argv):
        if not isinstance(argv, (list, tuple)) or not argv or any(not isinstance(v, str) or "\0" in v for v in argv) or sum(map(len, argv)) > MAX_ARGV:
            _deny("OWNED_ARGUMENTS_DENIED")
        values = list(argv)
        if values[0] == "docker":
            values.pop(0)
        elif values[0] == self.config.docker_executable:
            if values[1:3] != ["--host", self.config.docker_endpoint]:
                _deny("OWNED_DOCKER_ENDPOINT_DENIED")
            del values[:3]
        if not values or values[0] not in {"run", "create", "start", "exec", "inspect", "rm"}:
            _deny("OWNED_OPERATION_DENIED")
        return values

    def _prepare(self, argv):
        repeat = {"--security-opt", "--ulimit", "--tmpfs", "--mount", "--env", "--label"}
        takes = repeat | {"--name", "--network", "--user", "--cap-drop", "--cpus", "--memory", "--memory-swap", "--pids-limit", "--log-driver", "--workdir", "--entrypoint", "--cgroupns", "--ipc", "--runtime", "--restart"}
        flags = {}; booleans = set(); index = 1
        while index < len(argv) and argv[index].startswith("-"):
            flag = argv[index]
            if flag in {"-i", "--rm", "--read-only"}:
                if flag in booleans:
                    _deny("OWNED_DUPLICATE_FLAG_DENIED")
                booleans.add(flag); index += 1; continue
            if flag not in takes or index + 1 >= len(argv) or (flag not in repeat and flag in flags):
                _deny("OWNED_FLAG_DENIED")
            flags.setdefault(flag, []).append(argv[index + 1]); index += 2
        if index >= len(argv):
            _deny("OWNED_IMAGE_DENIED")
        image, command = argv[index], argv[index + 1:]
        def single(key, default=None):
            return flags.get(key, [default])[0]
        name = single("--name")
        if not isinstance(name, str) or not _NAMES.fullmatch(name):
            _deny("OWNED_NAME_DENIED")
        resource = "command" if name.startswith("cdo-sandbox-broker-") else "sdk"
        expected_image = self.config.command_image_id if resource == "command" else self.config.sdk_image_id
        if image != expected_image:
            _deny("OWNED_IMAGE_DENIED")
        base = {"--network": "none", "--user": "0:0" if resource == "command" else "100:101", "--cap-drop": "ALL",
            "--cpus": "2", "--memory": "2g" if resource == "command" else "512m",
            "--memory-swap": "2g" if resource == "command" else "512m", "--pids-limit": "128", "--log-driver": "none",
            "--entrypoint": "/usr/bin/env" if resource == "command" else "python3"}
        if (not {"-i", "--read-only"} <= booleans or (resource == "sdk" and "--rm" in booleans)
                or any(single(k) != v for k, v in base.items())
                or set(flags.get("--security-opt", [])) != {"no-new-privileges=true", "seccomp=builtin"}
                or len(flags.get("--security-opt", [])) != 2):
            _deny("OWNED_RESOURCE_POLICY_DENIED")
        for key, expected in (("--cgroupns", "private"), ("--ipc", "private"), ("--runtime", "runc"), ("--restart", "no")):
            if key in flags and single(key) != expected:
                _deny("OWNED_RESOURCE_POLICY_DENIED")
        ulimits = flags.get("--ulimit", [])
        mandatory = {"nofile=256:256", "fsize=16777216:16777216"}
        if (len(set(ulimits)) != len(ulimits) or not mandatory <= set(ulimits) or set(ulimits) - mandatory - {"core=0:0"}
                or (resource == "command" and "core=0:0" not in ulimits)):
            _deny("OWNED_RESOURCE_POLICY_DENIED")
        tmpfs = {}
        for item in flags.get("--tmpfs", []):
            target, separator, options = item.partition(":")
            if not separator or target in tmpfs:
                _deny("OWNED_RESOURCE_POLICY_DENIED")
            tmpfs[target] = options
        profile = COMMAND_TMPFS if resource == "command" else SDK_TMPFS
        submitted = {"/tmp": COMMAND_TMPFS["/tmp"]} if resource == "command" else SDK_TMPFS
        if tmpfs not in (submitted, profile):
            _deny("OWNED_RESOURCE_POLICY_DENIED")
        mounts = {}
        for raw in flags.get("--mount", []):
            target, source = self._mount(raw)
            if target in mounts:
                _deny("OWNED_MOUNT_DENIED")
            mounts[target] = source
        required_mounts = {"/workspace"} if resource == "command" else {"/reference/supervisor.py", "/reference/cdo_sdk_relay.py"}
        optional_mounts = {"/dependency"} if resource == "command" else {"/reference/schema.json"}
        if not required_mounts <= set(mounts) or set(mounts) - required_mounts - optional_mounts:
            _deny("OWNED_MOUNT_DENIED")
        env = {}
        for raw in flags.get("--env", []):
            key, equal, val = raw.partition("=")
            if not equal or key in env:
                _deny("OWNED_ENVIRONMENT_DENIED")
            env[key] = val
        if env != (COMMAND_ENV if resource == "command" else {"CDO_SDK_NETWORK_RELAY": "1"}):
            _deny("OWNED_ENVIRONMENT_DENIED")
        labels = {}
        for raw in flags.get("--label", []):
            key, equal, val = raw.partition("=")
            if not equal or key in labels:
                _deny("OWNED_LABEL_DENIED")
            labels[key] = val
        if labels != {"cdo.execution_id": self.config.execution_id, "cdo.resource": resource}:
            _deny("OWNED_LABEL_DENIED")
        workdir = "/workspace" if resource == "command" else "/"
        if single("--workdir", workdir) != workdir:
            _deny("OWNED_WORKDIR_DENIED")
        if resource == "command":
            prefix = ["-i", "PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin", *[k + "=" + v for k, v in COMMAND_ENV.items()]]
            if command != prefix + ["python3", "-c", BROKER_KEEPALIVE]:
                _deny("OWNED_LAUNCH_COMMAND_DENIED")
        elif len(command) < 3 or command[:2] != ["/reference/supervisor.py", "--supervise"]:
            _deny("OWNED_LAUNCH_COMMAND_DENIED")
        elif "http://host.docker.internal:" in " ".join(command) or any("CODEX_API_KEY" in item for item in command):
            _deny("OWNED_SDK_AUTHORITY_DENIED")
        self.docker._check_daemon()
        code, out, _ = self.docker._run(["image", "inspect", image])
        try:
            image_info = _decode(out)
            if code or not isinstance(image_info, list) or len(image_info) != 1 or image_info[0]["Id"] != image:
                raise RuntimeUnknown("OWNED_IMAGE_UNKNOWN")
            inherited = image_info[0]["Config"].get("Env") or []
            image_labels = image_info[0]["Config"].get("Labels") or {}
            if not isinstance(inherited, list) or not isinstance(image_labels, dict) or image_info[0]["Config"].get("Volumes") not in (None, {}):
                raise RuntimeUnknown("OWNED_IMAGE_UNKNOWN")
            if any(k.startswith("cdo.") for k in image_labels):
                _deny("OWNED_IMAGE_LABEL_DENIED")
            actual_env = {}
            for raw in inherited:
                key, separator, val = raw.partition("=")
                if not separator or key in actual_env or re.search(r"TOKEN|SECRET|PASSWORD|CREDENTIAL|API_KEY", key, re.I):
                    _deny("OWNED_IMAGE_ENVIRONMENT_DENIED")
                actual_env[key] = val
            actual_env.update(env)
        except (KeyError, TypeError, AttributeError, OwnedDenied):
            raise RuntimeUnknown("OWNED_IMAGE_UNKNOWN") from None
        expected = {"Name": "/" + name, "Image": image,
            "Config": {"Image": image, "User": base["--user"], "Entrypoint": [base["--entrypoint"]],
                "Cmd": command, "WorkingDir": workdir, "Env": actual_env, "OpenStdin": True, "Tty": False},
            "HostConfig": {"ReadonlyRootfs": True, "Privileged": False, "NetworkMode": "none", "CgroupnsMode": "private",
                "IpcMode": "private", "PidMode": "", "UTSMode": "", "Runtime": "runc", "Isolation": "", "CgroupParent": "",
                "NanoCpus": 2000000000, "Memory": 2147483648 if resource == "command" else 536870912,
                "MemorySwap": 2147483648 if resource == "command" else 536870912, "PidsLimit": 128,
                "PublishAllPorts": False, "AutoRemove": False, "CpuPeriod": 0, "CpuQuota": 0,
                "Tmpfs": dict(profile), "RestartPolicy": {"Name": "no", "MaximumRetryCount": 0},
                "LogConfig": {"Type": "none", "Config": {}}, "CapDrop": ["ALL"], "SecurityOpt": ["no-new-privileges=true", "seccomp=builtin"],
                "Ulimits": {"core": [0, 0], "nofile": [256, 256], "fsize": [16777216, 16777216]}}, "Mounts": mounts}
        policy_sha = hashlib.sha256(canonical(expected)).hexdigest()
        expected["policy_sha256"] = policy_sha
        expected["Config"]["Labels"] = {**image_labels, **self._labels(resource, policy_sha)}
        create = ["create", "-i", "--name", name, "--network", "none", "--read-only", "--user", base["--user"],
            "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true", "--security-opt", "seccomp=builtin",
            "--cgroupns", "private", "--ipc", "private", "--runtime", "runc", "--restart", "no", "--cpus", "2",
            "--memory", base["--memory"], "--memory-swap", base["--memory-swap"], "--pids-limit", "128",
            "--ulimit", "core=0:0", "--ulimit", "nofile=256:256", "--ulimit", "fsize=16777216:16777216",
            "--log-driver", "none", "--workdir", workdir, "--entrypoint", base["--entrypoint"]]
        for key, value in sorted(self._labels(resource, policy_sha).items()):
            create += ["--label", key + "=" + value]
        for key, value in env.items():
            create += ["--env", key + "=" + value]
        for target, source in sorted(mounts.items()):
            create += ["--mount", "type=bind,source=" + source + ",target=" + target + ",readonly"]
        for target, options in sorted(profile.items()):
            create += ["--tmpfs", target + ":" + options]
        return name, resource, expected, create + [image, *command]

    def _verify(self, name, record, info, allow_bind=False):
        try:
            expected = record["expected"]
            if (info["Name"] != "/" + name or not _HEX.fullmatch(info["Id"])
                    or (record["id"] is not None and info["Id"] != record["id"]) or info["Image"] != expected["Image"]):
                raise RuntimeUnknown("OWNED_CONTAINER_IDENTITY_UNKNOWN")
            cfg, host = info["Config"], info["HostConfig"]
            for key, value in expected["Config"].items():
                if key == "Env":
                    env = {}; items = cfg[key]
                    if not isinstance(items, list):
                        raise RuntimeUnknown("OWNED_CONTAINER_POLICY_UNKNOWN")
                    for raw in items:
                        k, separator, v = raw.partition("=")
                        if not separator or k in env:
                            raise RuntimeUnknown("OWNED_CONTAINER_POLICY_UNKNOWN")
                        env[k] = v
                    if env != value:
                        raise RuntimeUnknown("OWNED_CONTAINER_POLICY_UNKNOWN")
                elif type(cfg.get(key)) is not type(value) or cfg.get(key) != value:
                    raise RuntimeUnknown("OWNED_CONTAINER_POLICY_UNKNOWN")
            if cfg.get("Volumes") not in (None, {}):
                raise RuntimeUnknown("OWNED_CONTAINER_POLICY_UNKNOWN")
            for key, value in expected["HostConfig"].items():
                actual = host.get(key)
                if key == "Ulimits":
                    actual = {}
                    for limit in host[key]:
                        if limit["Name"] in actual or type(limit["Soft"]) is not int or type(limit["Hard"]) is not int:
                            raise RuntimeUnknown("OWNED_CONTAINER_POLICY_UNKNOWN")
                        actual[limit["Name"]] = [limit["Soft"], limit["Hard"]]
                elif key == "SecurityOpt":
                    if not isinstance(actual, list) or len(actual) != len(set(actual)):
                        raise RuntimeUnknown("OWNED_CONTAINER_POLICY_UNKNOWN")
                    actual, value = sorted(actual), sorted(value)
                if type(actual) is not type(value) or actual != value:
                    raise RuntimeUnknown("OWNED_CONTAINER_POLICY_UNKNOWN")
            for key in ("CapAdd", "Devices", "DeviceRequests", "DeviceCgroupRules", "Sysctls", "Binds", "GroupAdd", "ExtraHosts", "Links", "VolumesFrom", "PortBindings", "Dns", "DnsOptions", "DnsSearch", "StorageOpt"):
                if host.get(key) not in (None, [], {}):
                    raise RuntimeUnknown("OWNED_CONTAINER_POLICY_UNKNOWN")
            mounts = {}; tmpfs = set()
            for mount in info["Mounts"]:
                target = mount["Destination"]
                if target in mounts or target in tmpfs:
                    raise RuntimeUnknown("OWNED_CONTAINER_MOUNT_UNKNOWN")
                if mount["Type"] == "tmpfs":
                    if target not in expected["HostConfig"]["Tmpfs"]:
                        raise RuntimeUnknown("OWNED_CONTAINER_MOUNT_UNKNOWN")
                    tmpfs.add(target); continue
                if mount["Type"] != "bind" or mount["RW"] is not False or mount.get("Propagation") not in {"", "rprivate"}:
                    raise RuntimeUnknown("OWNED_CONTAINER_MOUNT_UNKNOWN")
                mounts[target] = mount["Source"]
            if mounts != expected["Mounts"]:
                raise RuntimeUnknown("OWNED_CONTAINER_MOUNT_UNKNOWN")
            networks = info["NetworkSettings"]["Networks"]
            if not isinstance(networks, dict) or set(networks) - {"none"}:
                raise RuntimeUnknown("OWNED_CONTAINER_NETWORK_UNKNOWN")
            for network in networks.values():
                if any(network.get(key) for key in ("IPAddress", "GlobalIPv6Address", "Gateway", "IPv6Gateway")):
                    raise RuntimeUnknown("OWNED_CONTAINER_NETWORK_UNKNOWN")
            state = info["State"]
            if (type(state["Running"]) is not bool or type(state["ExitCode"]) is not int or type(state["OOMKilled"]) is not bool
                    or state["Status"] not in {"created", "running", "exited", "dead"}):
                raise RuntimeUnknown("OWNED_CONTAINER_STATE_UNKNOWN")
            if record["id"] is None:
                if not allow_bind or record["status"] != "INTENT":
                    raise RuntimeUnknown("OWNED_CONTAINER_ID_UNKNOWN")
                record["id"] = info["Id"]
            return info
        except (KeyError, TypeError, ValueError, AttributeError):
            raise RuntimeUnknown("OWNED_INSPECTION_UNKNOWN") from None

    def _record(self, state, target):
        if not isinstance(target, str):
            _deny("OWNED_TARGET_DENIED")
        if target in state["resources"]:
            name = target
        else:
            names = [name for name, item in state["resources"].items() if item["id"] == target]
            if len(names) != 1:
                _deny("OWNED_TARGET_DENIED")
            name = names[0]
        return name, state["resources"][name]

    def _observe(self, state, target, allow_bind=False):
        name, record = self._record(state, target)
        info = self.docker.inspect(record["id"] or name)
        if info is None and record["id"] is not None:
            # An absent ID cannot prove the bound name is absent after reuse.
            info = self.docker.inspect(name)
        if info is not None:
            self._verify(name, record, info, allow_bind=allow_bind)
        return name, record, info

    def _create(self, state, argv):
        self._admit(state)
        name, resource, expected, create = self._prepare(argv)
        existing = [item for item in state["resources"].values() if item["resource"] == resource]
        if (name in state["resources"] or any(item["status"] != "REMOVED" for item in existing)
                or (resource == "sdk" and existing) or (resource == "command" and len(existing) >= MAX_COMMAND_RESOURCES)):
            _deny("OWNED_RESOURCE_ALREADY_USED")
        if len(state["resources"]) >= MAX_RESOURCES:
            _deny("OWNED_RESOURCE_LIMIT")
        if self.docker.inspect(name) is not None:
            _deny("OWNED_EXISTING_CONTAINER_DENIED")
        record = {"id": None, "resource": resource, "status": "INTENT", "expected": expected, "create_argv": create}
        state["resources"][name] = record; self._save(state)
        code, out, _ = self.docker._run(create)
        ident = out.decode("ascii", errors="replace").strip()
        if code or not _HEX.fullmatch(ident):
            raise RuntimeUnknown("OWNED_CREATE_UNKNOWN")
        record["id"] = ident
        info = self.docker.inspect(ident)
        if info is None:
            raise RuntimeUnknown("OWNED_CREATE_UNKNOWN")
        self._verify(name, record, info)
        record["status"] = "CREATED"; self._save(state)
        return ident

    def _start(self, state, target):
        self._admit(state)
        name, record, info = self._observe(state, target)
        if info is None or record["status"] != "CREATED" or info["State"]["Status"] != "created":
            raise RuntimeUnknown("OWNED_START_UNKNOWN")
        record["status"] = "STARTED"; self._save(state)
        return ["start", "--attach", "--interactive", record["id"]]

    def _exec(self, state, argv):
        self._admit(state)
        index = 1; interactive = False; user = None; workdir = None
        if argv[index:index + 1] == ["-i"]:
            interactive = True; index += 1
        if argv[index:index + 1] != ["--user"] or index + 1 >= len(argv):
            _deny("OWNED_EXEC_DENIED")
        user = argv[index + 1]; index += 2
        if argv[index:index + 1] == ["--workdir"]:
            if index + 1 >= len(argv):
                _deny("OWNED_EXEC_DENIED")
            workdir = argv[index + 1]; index += 2
        if index >= len(argv):
            _deny("OWNED_EXEC_DENIED")
        target, command = argv[index], argv[index + 1:]
        name, record, info = self._observe(state, target)
        if record["resource"] != "command" or record["status"] != "STARTED" or info is None or info["State"]["Running"] is not True:
            raise RuntimeUnknown("OWNED_EXEC_STATE_UNKNOWN")
        if user == "0:0":
            if workdir is not None or len(command) != 3 or command[:2] != ["python3", "-c"] or command[2] not in self.helpers:
                _deny("OWNED_ROOT_EXEC_DENIED")
            if interactive != (command[2] in self._view_helpers()):
                _deny("OWNED_ROOT_EXEC_DENIED")
        elif user == "100:101" and workdir == "/tmp/view" and not interactive:
            if command[:2] != ["/usr/bin/env", "-i"]:
                _deny("OWNED_EXEC_ENVIRONMENT_DENIED")
            environment = {}; position = 2
            while position < len(command) and re.match(r"[A-Z][A-Z0-9_]*=", command[position]):
                key, val = command[position].split("=", 1)
                if key in environment:
                    _deny("OWNED_EXEC_ENVIRONMENT_DENIED")
                environment[key] = val; position += 1
            policy_env = DEPENDENCY_ENV if "/dependency" in record["expected"]["Mounts"] else EXEC_ENV
            if environment != policy_env:
                _deny("OWNED_EXEC_ENVIRONMENT_DENIED")
            suffix = command[position:]
            if not suffix or len(suffix) > 64 or sum(map(len, suffix)) > 16384:
                _deny("OWNED_EXEC_COMMAND_DENIED")
        else:
            _deny("OWNED_EXEC_DENIED")
        return ["exec", *(["-i"] if interactive else []), "--user", user,
            *(["--workdir", workdir] if workdir else []), record["id"], *command]

    def _view_helpers(self):
        tree = ast.parse(Path(__file__).with_name("cdo_broker.py").read_text())
        return {node.value.value for node in ast.walk(tree) if isinstance(node, ast.Assign)
            and isinstance(node.value, ast.Constant) and isinstance(node.value.value, str)
            and any(isinstance(target, ast.Name) and target.id == "VIEW_HELPER" for target in node.targets)}

    def _remove(self, state, target):
        name, record, info = self._observe(state, target, allow_bind=True)
        if info is not None:
            self._save(state)
            self.docker._check_daemon()
            code, _, _ = self.docker._run(["rm", "--force", record["id"]])
            if code:
                raise RuntimeUnknown("OWNED_CLEANUP_UNKNOWN")
        # Both immutable ID and exact name must be positively absent. Never
        # remove a different ID that has since acquired this name.
        if record["id"] is not None and self.docker.inspect(record["id"]) is not None:
            raise RuntimeUnknown("OWNED_CLEANUP_UNPROVEN")
        if self.docker.inspect(name) is not None:
            raise RuntimeUnknown("OWNED_CLEANUP_UNPROVEN")
        record["status"] = "REMOVED"; self._save(state)
        return record["id"]

    def _translated(self, state, values):
        operation = values[0]
        if operation in {"create", "run"}:
            ident = self._create(state, values)
            return self._start(state, ident) if operation == "run" else None, ident
        if operation == "start" and len(values) == 2:
            return self._start(state, values[1]), None
        if operation == "exec":
            return self._exec(state, values), None
        _deny("OWNED_STREAM_OPERATION_DENIED")

    def _spawn(self, translated, options):
        if options.get("shell") or "executable" in options or "preexec_fn" in options or "env" in options or "cwd" in options:
            _deny("OWNED_SUBPROCESS_OPTIONS_DENIED")
        permitted = {"stdin", "stdout", "stderr", "text", "encoding", "errors", "bufsize", "close_fds", "start_new_session", "restore_signals"}
        if set(options) - permitted:
            _deny("OWNED_SUBPROCESS_OPTIONS_DENIED")
        self.docker._check_daemon()
        return subprocess.Popen([self.config.docker_executable, "--host", self.config.docker_endpoint, *translated],
            env={"PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": "/var/empty"}, **options)

    def popen(self, argv, **options):
        values = self._argv(argv)
        with self._locked() as state:
            translated, ident = self._translated(state, values)
            if translated is None:
                _deny("OWNED_STREAM_OPERATION_DENIED")
            self._fresh_config(); self._admit(state)
            return self._spawn(translated, options)

    def run(self, argv, **options):
        values = self._argv(argv)
        operation = values[0]
        text_mode = options.pop("text", False)
        capture = options.pop("capture_output", False)
        timeout = options.pop("timeout", 15)
        check = options.pop("check", False)
        supplied_input = options.pop("input", None)
        if type(timeout) not in {int, float} or not 0 < timeout <= 60 or type(text_mode) is not bool or type(check) is not bool:
            _deny("OWNED_SUBPROCESS_OPTIONS_DENIED")
        if operation in {"inspect", "rm", "create"}:
            if supplied_input is not None or set(options) - {"stdout", "stderr"}:
                _deny("OWNED_SUBPROCESS_OPTIONS_DENIED")
            with self._locked() as state:
                if operation == "inspect":
                    format_running = len(values) == 4 and values[1:3] == ["--format", "{{.State.Running}}"]
                    if not (len(values) == 2 or format_running):
                        _deny("OWNED_INSPECTION_DENIED")
                    name, record, info = self._observe(state, values[-1], allow_bind=True)
                    if info is None:
                        out, error, code = b"[]\n", ("Error: No such object: " + values[-1] + "\n").encode(), 1
                    else:
                        self._save(state)
                        out = (("true" if info["State"]["Running"] else "false") + "\n").encode() if format_running else canonical([info])
                        error, code = b"", 0
                elif operation == "rm":
                    if len(values) != 3 or values[1] not in {"-f", "--force"}:
                        _deny("OWNED_REMOVAL_DENIED")
                    self._remove(state, values[2]); out, error, code = b"", b"", 0
                else:
                    ident = self._create(state, values); out, error, code = (ident + "\n").encode(), b"", 0
            result = subprocess.CompletedProcess(argv, code, out.decode() if text_mode else out, error.decode() if text_mode else error)
            if check and code:
                raise subprocess.CalledProcessError(code, argv, output=result.stdout, stderr=result.stderr)
            return result
        if operation not in {"exec", "run", "start"}:
            _deny("OWNED_OPERATION_DENIED")
        if capture:
            if "stdout" in options or "stderr" in options:
                _deny("OWNED_SUBPROCESS_OPTIONS_DENIED")
            options.update(stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        if supplied_input is not None:
            if "stdin" in options:
                _deny("OWNED_SUBPROCESS_OPTIONS_DENIED")
            options["stdin"] = subprocess.PIPE
        child = self.popen(argv, text=text_mode, **options)
        try:
            out, error = child.communicate(input=supplied_input, timeout=timeout)
        except BaseException:
            child.kill(); child.wait(timeout=2)
            self.invalidate()
            raise
        result = subprocess.CompletedProcess(argv, child.returncode, out, error)
        if check and child.returncode:
            raise subprocess.CalledProcessError(child.returncode, argv, output=out, stderr=error)
        return result

    def cli(self, argv):
        """Spawn the sanitized Docker child while admission is serialized."""
        values = self._argv(argv)
        if values[0] in {"inspect", "rm", "create"}:
            result = self.run(values, capture_output=True)
            if result.stdout:
                sys.stdout.buffer.write(result.stdout); sys.stdout.buffer.flush()
            if result.stderr:
                sys.stderr.buffer.write(result.stderr); sys.stderr.buffer.flush()
            return result.returncode
        with self._locked() as state:
            translated, _ = self._translated(state, values)
            self._fresh_config(); self._admit(state)
            child = self._spawn(translated, {})
        # Inherit only the existing stdio descriptors. The child was spawned
        # before invalidation can acquire the lock; later cancellation owns
        # cleanup of its recorded immutable ID.
        previous = {}
        def stop(number, _):
            if child.poll() is None:
                child.send_signal(number)
        for number in (signal.SIGTERM, signal.SIGINT):
            previous[number] = signal.signal(number, stop)
        try:
            return child.wait()
        finally:
            for number, handler in previous.items():
                signal.signal(number, handler)

    def invalidate(self):
        with self._locked() as state:
            state["invalidated"] = True
            if state["status"] != "UNKNOWN":
                state["status"] = "INVALIDATED"
            self._save(state)

    def cleanup(self):
        with self._locked() as state:
            state["invalidated"] = True
            if state["status"] != "UNKNOWN":
                state["status"] = "INVALIDATED"
            self._save(state)
            removed = []
            for name in sorted(state["resources"]):
                ident = self._remove(state, name)
                if ident is not None:
                    removed.append(ident)
            # Cleanup is reconciliation only; UNKNOWN is permanently terminal.
            if state["status"] != "UNKNOWN":
                state["status"] = "CLEANED"
            self._save(state)
            return {"status": state["status"], "invalidated": True, "resources_absent": True, "removed": removed}

    @classmethod
    def from_env(cls, docker=None, clock=time.time):
        return cls(load_config(), docker=docker, clock=clock)

    def cleanup_all(self):
        return self.cleanup()

    def status(self):
        with self._locked() as state:
            return {"status": state["status"], "invalidated": state["invalidated"],
                "resources": {name: {"id": record["id"], "status": record["status"], "resource": record["resource"]}
                    for name, record in state["resources"].items()}}

    def cleanup_resource(self, target):
        with self._locked() as state:
            ident = self._remove(state, target)
            return {"status": state["status"], "invalidated": state["invalidated"],
                "resources_absent": state["status"] != "UNKNOWN", "removed": [ident] if ident is not None else []}

    def prove_absent(self, target):
        with self._locked() as state:
            name, record, info = self._observe(state, target, allow_bind=True)
            absent = info is None
            if absent and record["id"] is not None:
                absent = self.docker.inspect(record["id"]) is None and self.docker.inspect(name) is None
            return {"resources_absent": absent and state["status"] != "UNKNOWN", "status": state["status"]}


OwnedRuntime = OwnedDocker


def cleanup_owned(config_path=None, docker=None):
    return OwnedDocker(config_path, docker=docker).cleanup()


def invalidate_owned(config_path=None):
    # Configuration and ledger validation need no daemon contact.
    return OwnedDocker(config_path, docker=object()).invalidate()


def main(argv=None):
    args = sys.argv[1:] if argv is None else argv
    if not args or args[0] != "--cli":
        _deny("OWNED_ENTRYPOINT_DENIED")
    return OwnedDocker().cli(args[1:])


if __name__ == "__main__":
    try:
        sys.exit(main())
    except ControllerDenied as error:
        print(error.code, file=sys.stderr); sys.exit(75 if isinstance(error, RuntimeUnknown) else 1)
