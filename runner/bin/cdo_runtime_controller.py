#!/usr/bin/env python3
"""Trusted host-only controller for two finite, credential-free OCI recipes.

This module is not a Docker proxy or a production model execution adapter. Its
registry, helpers and durable state belong to the host administrator, outside
every execution mount. A transport must call ``handle`` with bounded strict JSON;
it must never expose this module's host-side Docker adapter to an application.
"""
from __future__ import annotations
from contextlib import contextmanager
from dataclasses import dataclass
import fcntl
import hashlib
import hmac
import json
import os
from pathlib import Path
import re
import selectors
import stat
import subprocess
import threading
import time
from types import MappingProxyType
import uuid


OPERATIONS = ("create", "start", "inspect", "diagnostics", "cancel", "cleanup")
REQUEST_FIELDS = frozenset({
    "version", "operation", "execution_id", "release_id", "workpackage_id",
    "role", "image_id", "scope_sha256", "resource_profile",
    "execution_expires_at", "request_expires_at", "nonce", "signature",
    "controller_id", "protocol_sha256",
})
MAX_REQUEST_BYTES = 4096
MAX_RESPONSE_BYTES = 4096
MAX_NONCES = 4096
TERMINATION_NONCES = 16
MAX_REQUEST_SECONDS = 60
MAX_EXECUTION_SECONDS = 1200
MAX_DOCKER_BYTES = 256 * 1024
ROLES = frozenset({"contract", "layer", "composition", "reviewer"})
PROTECTED = frozenset({".git", ".codex", ".agents", ".ssh", ".aws", ".env",
                       "docker.sock", "auth.json", "credentials", "id_rsa"})
PROFILE_VALUES = {
    "command-runtime": {"name": "command-v1", "memory": 2147483648,
                        "tmpfs": {"/tmp": "rw,nosuid,nodev,exec,size=268435456,nr_inodes=4096,uid=100,gid=101,mode=1777",
                                  "/dev/shm": "ro,noexec,nosuid,nodev,size=4096"}},
    "sdk-runtime": {"name": "sdk-v1", "memory": 536870912,
                    "tmpfs": {"/execution/sdk-private": "rw,noexec,nosuid,nodev,size=8388608,nr_inodes=256,uid=100,gid=101,mode=0700",
                              "/tmp": "ro,noexec,nosuid,nodev,size=4096",
                              "/dev/shm": "ro,noexec,nosuid,nodev,size=4096"}},
}

# These scripts are finite runtime recipes, not administrator-supplied code. The
# mounted bytes must match the appropriate literal exactly, including its hash.
COMMON_HELPER = '''import json,os,pathlib,selectors,subprocess,sys,time
status=dict(line.split(':',1) for line in pathlib.Path('/proc/self/status').read_text().splitlines() if ':' in line)
assert status['Seccomp'].strip()=='2'
assert status['NoNewPrivs'].strip()=='1'
assert int(status['CapEff'].strip(),16)==0 and int(status['CapBnd'].strip(),16)==0
for name in ('/var/run/docker.sock','/run/docker.sock'):
 try:os.stat(name)
 except (FileNotFoundError,PermissionError):pass
 else:raise AssertionError('DOCKER_SOCKET_VISIBLE')
mounts=[line.split() for line in pathlib.Path('/proc/self/mountinfo').read_text().splitlines()]
for mounted in mounts:
 assert not any(mounted[4]==root or mounted[4].startswith(root+'/') for root in ('/data','/projects','/root','/home','/Users','/private','/var/run/docker.sock','/run/docker.sock'))
assert os.getuid()==100 and os.getgid()==101
deadline=int(os.environ['CDO_RUNTIME_DEADLINE'])
assert time.time()<deadline
'''
COMMAND_HELPER = (COMMON_HELPER + '''assert any(v[4]=='/workspace' and 'ro' in v[5].split(',') for v in mounts)
env={'PATH':'/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin','HOME':'/tmp/agent-home','TMPDIR':'/tmp','GOCACHE':'/tmp/go-build','GOPATH':'/tmp/go','GOMODCACHE':'/dependency/modules' if pathlib.Path('/dependency/modules').is_dir() else '/tmp/go-mod','GOPROXY':'off','GOSUMDB':'off','GOENV':'off','GOWORK':'off','GOTOOLCHAIN':'local','GOVCS':'*:off','GOMAXPROCS':'2','GOFLAGS':'-buildvcs=false -p=2'}
result=subprocess.Popen(['/usr/local/go/bin/go','test','./...'],cwd='/workspace',env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT)
selector=selectors.DefaultSelector();selector.register(result.stdout,selectors.EVENT_READ);output=bytearray();end=min(time.time()+60,deadline)
try:
 while selector.get_map():
  assert time.time()<end
  for key,_ in selector.select(.1):
   value=os.read(key.fileobj.fileno(),256)
   if not value:selector.unregister(key.fileobj)
   else:
    output.extend(value)
    assert len(output)<=1024
 code=result.wait(timeout=max(.01,end-time.time()))
finally:
 if result.poll() is None:result.kill();result.wait(timeout=1)
print(json.dumps({'recipe':'command-runtime','controls':'PASS','go_exit_code':code,'go_output':output.decode('utf-8',errors='replace')}),flush=True)
sys.exit(code)
''').encode("utf-8")
SDK_HELPER = (COMMON_HELPER + '''root=pathlib.Path('/execution/sdk-private')
for name in ('home','tmp','cache','data','config','runtime'):(root/name).mkdir(mode=0o700)
env={'PATH':'/usr/local/bin:/usr/bin:/bin','HOME':str(root/'home'),'CODEX_HOME':str(root/'home'),'TMPDIR':str(root/'tmp'),'TMP':str(root/'tmp'),'TEMP':str(root/'tmp'),'XDG_CACHE_HOME':str(root/'cache'),'XDG_DATA_HOME':str(root/'data'),'XDG_CONFIG_HOME':str(root/'config'),'XDG_RUNTIME_DIR':str(root/'runtime')}
result=subprocess.run(['/app/runner/node_modules/.bin/codex','--version'],env=env,capture_output=True,timeout=min(30,max(.1,deadline-time.time())))
assert result.returncode==0 and result.stdout.strip()==b'codex-cli 0.144.6'
print(json.dumps({'recipe':'sdk-runtime','controls':'PASS','cli_version':'codex-cli 0.144.6'}),flush=True)
''').encode("utf-8")

PROTOCOL_DESCRIPTOR = {
    "name": "cdo.host-runtime.v1", "version": 1,
    "operations": list(OPERATIONS), "request_fields": sorted(REQUEST_FIELDS),
    "authentication": "per-execution HMAC-SHA256 canonical JSON; durable exclusive nonce",
    "maximum_request_bytes": MAX_REQUEST_BYTES,
    "maximum_request_seconds": MAX_REQUEST_SECONDS,
    "maximum_execution_seconds": MAX_EXECUTION_SECONDS,
    "maximum_diagnostics_bytes": 2048,
    "recipes": PROFILE_VALUES,
    "helper_sha256": {"command-runtime": hashlib.sha256(COMMAND_HELPER).hexdigest(),
                      "sdk-runtime": hashlib.sha256(SDK_HELPER).hexdigest()},
    "network": "none", "capabilities": [], "readonly_source": True,
    "entrypoint": ["/usr/bin/env", "-i", "PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
                   "CDO_RUNTIME_DEADLINE=<bound_execution_expires_at>",
                   "python3", "/reference/runtime.py"],
    "generic_exec": False, "daemon_admin": False, "production_admission": "DENIED",
    "ownership": "registry-key HMAC bound to private host state root; positively verified immutable container ID",
}


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"),
                      ensure_ascii=True, allow_nan=False).encode("utf-8")


def protocol_sha256():
    return hashlib.sha256(canonical(PROTOCOL_DESCRIPTOR)).hexdigest()


def controller_identity():
    return hashlib.sha256(Path(__file__).read_bytes()).hexdigest()


def helper_digest(recipe):
    if recipe not in PROFILE_VALUES:
        raise ControllerDenied("RECIPE_DENIED")
    return hashlib.sha256(COMMAND_HELPER if recipe == "command-runtime" else SDK_HELPER).hexdigest()


class ControllerDenied(Exception):
    """Only bounded, non-secret codes may cross the request boundary."""
    def __init__(self, code):
        self.code = code
        super().__init__(code)


class RuntimeUnknown(ControllerDenied):
    def __init__(self, code="DOCKER_RUNTIME_UNKNOWN"):
        super().__init__(code)


def _hex(value, image=False):
    return isinstance(value, str) and bool(re.fullmatch(("sha256:" if image else "") + "[a-f0-9]{64}", value))


def _uuid(value):
    if not isinstance(value, str):
        return False
    try:
        return str(uuid.UUID(value)) == value and uuid.UUID(value).int != 0
    except ValueError:
        return False


def _path(path):
    path = Path(path)
    if not path.is_absolute() or re.search(r"[,\\\x00-\x1f\x7f]", str(path)):
        raise ControllerDenied("TRUSTED_PATH_DENIED")
    try:
        if path.resolve(strict=True) != path:
            raise ControllerDenied("TRUSTED_PATH_DENIED")
    except (OSError, RuntimeError):
        raise ControllerDenied("TRUSTED_PATH_DENIED") from None
    if any(p in PROTECTED or p.startswith(".env.") for p in path.parts):
        raise ControllerDenied("TRUSTED_PATH_DENIED")
    return path


def _regular(path, readonly=False):
    path = _path(path)
    value = path.lstat()
    if not stat.S_ISREG(value.st_mode) or value.st_nlink != 1:
        raise ControllerDenied("TRUSTED_FILE_DENIED")
    if readonly and value.st_mode & 0o222:
        raise ControllerDenied("TRUSTED_FILE_MUTABLE")
    return path


def source_digest(root):
    """Pin a bounded, immutable regular-file fixture tree before registration."""
    root = _path(root)
    if not root.is_dir() or root.stat().st_mode & 0o222:
        raise ControllerDenied("SOURCE_MUTABLE")
    files = {}
    total = 0
    for folder, dirs, names in os.walk(root, followlinks=False):
        for name in dirs + names:
            target = _path(Path(folder, name))
            info = target.lstat()
            if stat.S_ISDIR(info.st_mode):
                if info.st_mode & 0o222:
                    raise ControllerDenied("SOURCE_MUTABLE")
            elif stat.S_ISREG(info.st_mode):
                target = _regular(target, readonly=True)
                total += info.st_size
                if total > 512 * 1024 * 1024 or len(files) >= 20000:
                    raise ControllerDenied("SOURCE_LIMIT")
                files[str(target.relative_to(root))] = hashlib.sha256(target.read_bytes()).hexdigest()
            else:
                raise ControllerDenied("SOURCE_NONREGULAR")
    return hashlib.sha256(canonical(files)).hexdigest()


@dataclass(frozen=True)
class ControllerConfig:
    controller_id: str
    state_root: Path
    command_image_id: str
    sdk_image_id: str


@dataclass(frozen=True)
class ExecutionRegistration:
    execution_id: str
    release_id: str
    workpackage_id: str
    role: str
    recipe: str
    scope_sha256: str
    execution_expires_at: int
    secret: bytes
    helper_path: Path
    helper_sha256: str
    image_id: str
    source_root: Path | None = None
    source_sha256: str | None = None
    dependency_root: Path | None = None
    dependency_sha256: str | None = None


def signed_request(registration, operation, request_expires_at, nonce=None):
    """Trusted client helper; no secret is included in the returned request."""
    request = {"version": 1, "operation": operation,
               "execution_id": registration.execution_id, "release_id": registration.release_id,
               "workpackage_id": registration.workpackage_id, "role": registration.role,
               "image_id": getattr(registration, "image_id", None),
               "scope_sha256": registration.scope_sha256,
               "resource_profile": PROFILE_VALUES[registration.recipe]["name"],
               "execution_expires_at": registration.execution_expires_at,
               "request_expires_at": request_expires_at, "nonce": nonce or str(uuid.uuid4()),
               "controller_id": getattr(registration, "controller_id", controller_identity()),
               "protocol_sha256": protocol_sha256()}
    # image_id is bound by the controller configuration. The explicit client
    # helper below supplies it without adding runtime-selectable image policy.
    if request["image_id"] is None:
        raise ControllerDenied("CLIENT_IMAGE_BINDING_REQUIRED")
    request["signature"] = hmac.new(registration.secret, canonical(request), hashlib.sha256).hexdigest()
    return request


def decode_request(data):
    if not isinstance(data, bytes) or len(data) > MAX_REQUEST_BYTES:
        raise ControllerDenied("REQUEST_SIZE_DENIED")
    def pairs(values):
        result = {}
        for key, value in values:
            if key in result:
                raise ControllerDenied("DUPLICATE_FIELD_DENIED")
            result[key] = value
        return result
    try:
        return json.loads(data, object_pairs_hook=pairs,
                          parse_constant=lambda _: (_ for _ in ()).throw(ControllerDenied("JSON_DENIED")))
    except (ValueError, UnicodeError, RecursionError):
        raise ControllerDenied("JSON_DENIED") from None


class DockerCLI:
    """Privileged host adapter. Its six methods accept only controller values."""
    def __init__(self, executable, endpoint, expected_daemon_id, timeout=15):
        executable = Path(executable)
        if (not executable.is_absolute() or timeout <= 0 or timeout > 15
                or not isinstance(endpoint, str) or not endpoint.startswith("unix://")
                or not isinstance(expected_daemon_id, str)
                or not re.fullmatch(r"[a-zA-Z0-9:_-]{8,128}", expected_daemon_id)):
            raise ControllerDenied("DOCKER_CLIENT_DENIED")
        try:
            executable = _regular(executable)
            info = executable.stat()
            if info.st_uid not in {0, os.geteuid()} or info.st_mode & 0o022 or not info.st_mode & 0o111:
                raise ControllerDenied("DOCKER_EXECUTABLE_UNTRUSTED")
        except OSError:
            raise ControllerDenied("DOCKER_EXECUTABLE_UNTRUSTED") from None
        socket = Path(endpoint[7:])
        try:
            if (not socket.is_absolute() or socket.resolve(strict=True) != socket
                    or re.search(r"[\x00-\x1f\x7f]", str(socket))
                    or not stat.S_ISSOCK(socket.stat().st_mode)
                    or socket.stat().st_uid not in {0, os.geteuid()}):
                raise ControllerDenied("DAEMON_ENDPOINT_DENIED")
        except OSError:
            raise ControllerDenied("DAEMON_ENDPOINT_DENIED") from None
        self.executable = str(executable)
        self.endpoint = endpoint
        self.expected_daemon_id = expected_daemon_id
        self.timeout = timeout

    def _run(self, arguments):
        # No shell, inherited Docker context, ambient credential environment,
        # unrestricted stdout allocation or unbounded daemon wait is permitted.
        environment = {"PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": "/var/empty"}
        process = None
        selector = None
        try:
            process = subprocess.Popen([self.executable, "--host", self.endpoint, *arguments], stdin=subprocess.DEVNULL,
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=environment)
            chunks = {"stdout": bytearray(), "stderr": bytearray()}
            selector = selectors.DefaultSelector()
            for kind in chunks:
                selector.register(getattr(process, kind), selectors.EVENT_READ, kind)
            deadline = time.monotonic() + self.timeout
            while selector.get_map():
                if time.monotonic() >= deadline:
                    raise RuntimeUnknown()
                for key, _ in selector.select(min(.1, max(0, deadline - time.monotonic()))):
                    chunk = os.read(key.fileobj.fileno(), 4096)
                    if not chunk:
                        selector.unregister(key.fileobj)
                    else:
                        chunks[key.data].extend(chunk)
                        if sum(map(len, chunks.values())) > MAX_DOCKER_BYTES:
                            raise RuntimeUnknown("DOCKER_RESPONSE_LIMIT")
            code = process.wait(timeout=max(.01, deadline - time.monotonic()))
            return code, bytes(chunks["stdout"]), bytes(chunks["stderr"])
        except (OSError, subprocess.TimeoutExpired):
            raise RuntimeUnknown() from None
        finally:
            if selector:
                selector.close()
            if process:
                if process.poll() is None:
                    process.kill()
                    process.wait(timeout=2)
                for stream in (process.stdout, process.stderr):
                    if stream:
                        stream.close()

    def _check_daemon(self):
        code, out, _ = self._run(["info", "--format", "{{.ID}}"])
        if code or out.decode("ascii", errors="replace").strip() != self.expected_daemon_id:
            raise RuntimeUnknown("DAEMON_IDENTITY_UNKNOWN")

    def create(self, arguments):
        if not arguments or arguments[0] != "create":
            raise ControllerDenied("DOCKER_OPERATION_DENIED")
        self._check_daemon()
        code, out, _ = self._run(arguments)
        value = out.decode("ascii", errors="replace").strip()
        if code or not _hex(value):
            raise RuntimeUnknown()
        return value

    def inspect(self, target):
        if not (_hex(target) or re.fullmatch(r"cdo-runtime-[a-f0-9]{32}", target)):
            raise ControllerDenied("DOCKER_TARGET_DENIED")
        self._check_daemon()
        code, out, error = self._run(["inspect", "--type", "container", target])
        if code:
            # A transport error containing a quoted error message is not absence.
            expected = (b"Error: No such container: " + target.encode(),
                        b"Error: No such object: " + target.encode(),
                        b"Error response from daemon: No such container: " + target.encode(),
                        b"Error response from daemon: No such object: " + target.encode())
            if code == 1 and out.strip() in (b"", b"[]") and error.strip() in expected:
                return None
            raise RuntimeUnknown()
        try:
            value = json.loads(out)
            if not isinstance(value, list) or len(value) != 1 or not isinstance(value[0], dict):
                raise RuntimeUnknown()
            return value[0]
        except (ValueError, UnicodeError, RecursionError):
            raise RuntimeUnknown() from None

    def _owned_operation(self, operation, target):
        if not _hex(target):
            raise ControllerDenied("DOCKER_TARGET_DENIED")
        self._check_daemon()
        code, _, _ = self._run([operation, target] if operation == "start" else
                               ["stop", "--time", "1", target] if operation == "stop" else
                               ["rm", "--force", target])
        if code:
            raise RuntimeUnknown()

    def start(self, target):
        self._owned_operation("start", target)

    def stop(self, target):
        self._owned_operation("stop", target)

    def remove(self, target):
        self._owned_operation("rm", target)

    def logs(self, target):
        if not _hex(target):
            raise ControllerDenied("DOCKER_TARGET_DENIED")
        self._check_daemon()
        code, out, error = self._run(["logs", "--tail", "32", target])
        if code:
            raise RuntimeUnknown("DIAGNOSTICS_UNKNOWN")
        output = out + error
        if len(output) > 2048:
            raise RuntimeUnknown("DIAGNOSTICS_LIMIT")
        return output.decode("utf-8", errors="replace")


class RuntimeController:
    def __init__(self, config, registrations, docker, clock=time.time):
        if config.controller_id != controller_identity() or not all(
                _hex(i, image=True) for i in (config.command_image_id, config.sdk_image_id)):
            raise ControllerDenied("CONTROLLER_CONFIG_DENIED")
        self.config = config
        self.docker = docker
        self.clock = clock
        self.state_root = _path(config.state_root)
        info = self.state_root.stat()
        if not self.state_root.is_dir() or info.st_uid != os.geteuid() or info.st_mode & 0o077:
            raise ControllerDenied("STATE_ROOT_UNTRUSTED")
        approved = {}
        for registration in registrations:
            self._validate_registration(registration)
            if registration.execution_id in approved:
                raise ControllerDenied("REGISTRY_DUPLICATE")
            approved[registration.execution_id] = registration
        if not approved or len(approved) > 128:
            raise ControllerDenied("REGISTRY_LIMIT")
        self.registrations = MappingProxyType(approved)
        self._watchdog_stop = threading.Event()
        self._watchdog = None
        self._watchdog_error = None

    def image_id(self, registration):
        return self.config.command_image_id if registration.recipe == "command-runtime" else self.config.sdk_image_id

    def request(self, execution_id, operation, request_expires_at, nonce=None):
        registration = self.registrations[execution_id]
        return signed_request(registration, operation, request_expires_at, nonce)

    def _validate_registration(self, registration):
        if (not _uuid(registration.execution_id) or not _uuid(registration.workpackage_id)
                or not _hex(registration.release_id) or not _hex(registration.scope_sha256)
                or not isinstance(registration.role, str) or registration.role not in ROLES
                or not isinstance(registration.recipe, str) or registration.recipe not in PROFILE_VALUES
                or type(registration.execution_expires_at) is not int
                or registration.execution_expires_at <= 0
                or registration.execution_expires_at > self.clock() + MAX_EXECUTION_SECONDS
                or not isinstance(registration.secret, bytes) or len(registration.secret) < 32
                or len(registration.secret) > 128
                or registration.image_id != self.image_id(registration)
                or registration.helper_sha256 != helper_digest(registration.recipe)):
            raise ControllerDenied("REGISTRATION_DENIED")
        helper = _regular(registration.helper_path, readonly=True)
        if helper.stat().st_uid != os.geteuid() or hashlib.sha256(helper.read_bytes()).hexdigest() != registration.helper_sha256:
            raise ControllerDenied("HELPER_PIN_DENIED")
        roots = [helper]
        if registration.recipe == "sdk-runtime" and (registration.source_root is not None or registration.dependency_root is not None):
            raise ControllerDenied("SDK_SOURCE_DENIED")
        if registration.recipe == "command-runtime" and registration.source_root is None:
            raise ControllerDenied("SOURCE_REQUIRED")
        for root, pin in ((registration.source_root, registration.source_sha256),
                          (registration.dependency_root, registration.dependency_sha256)):
            if (root is None) != (pin is None):
                raise ControllerDenied("SOURCE_PIN_REQUIRED")
            if root is not None:
                if not _hex(pin) or source_digest(root) != pin:
                    raise ControllerDenied("SOURCE_PIN_DENIED")
                roots.append(_path(root))
        for root in roots:
            if self.state_root == root or self.state_root in root.parents or root in self.state_root.parents:
                raise ControllerDenied("PRIVATE_STATE_MOUNT_DENIED")

    def _public_binding(self, registration):
        return {"execution_id": registration.execution_id, "release_id": registration.release_id,
                "workpackage_id": registration.workpackage_id, "role": registration.role,
                "image_id": self.image_id(registration), "scope_sha256": registration.scope_sha256,
                "resource_profile": PROFILE_VALUES[registration.recipe]["name"],
                "execution_expires_at": registration.execution_expires_at,
                "controller_id": self.config.controller_id, "protocol_sha256": protocol_sha256()}

    def _binding_digest(self, registration):
        return hashlib.sha256(canonical({**self._public_binding(registration), "recipe": registration.recipe,
            "controller_id": self.config.controller_id, "protocol_sha256": protocol_sha256(),
            "secret_sha256": hashlib.sha256(registration.secret).hexdigest(),
            "helper_path": str(registration.helper_path), "helper_sha256": registration.helper_sha256,
            "source_root": str(registration.source_root), "source_sha256": registration.source_sha256,
            "dependency_root": str(registration.dependency_root), "dependency_sha256": registration.dependency_sha256})).hexdigest()

    def _validate_request(self, request):
        if not isinstance(request, dict) or set(request) != REQUEST_FIELDS:
            raise ControllerDenied("REQUEST_FIELDS_DENIED")
        if (type(request["version"]) is not int or request["version"] != 1
                or request["operation"] not in OPERATIONS
                or not _uuid(request["execution_id"]) or not _uuid(request["nonce"])
                or not _hex(request["signature"])):
            raise ControllerDenied("REQUEST_INVALID")
        try:
            if len(canonical(request)) > MAX_REQUEST_BYTES:
                raise ControllerDenied("REQUEST_SIZE_DENIED")
        except (TypeError, ValueError, RecursionError):
            raise ControllerDenied("REQUEST_INVALID") from None
        registration = self.registrations.get(request["execution_id"])
        if registration is None:
            raise ControllerDenied("EXECUTION_UNAPPROVED")
        if any(type(request[key]) is not type(value) or request[key] != value
               for key, value in self._public_binding(registration).items()):
            raise ControllerDenied("IDENTITY_MISMATCH")
        now = self.clock()
        if (type(request["request_expires_at"]) is not int
                or not now < request["request_expires_at"] <= now + MAX_REQUEST_SECONDS):
            raise ControllerDenied("REQUEST_EXPIRED")
        value = {key: request[key] for key in request if key != "signature"}
        expected = hmac.new(registration.secret, canonical(value), hashlib.sha256).hexdigest()
        if not hmac.compare_digest(expected, request["signature"]):
            raise ControllerDenied("SIGNATURE_DENIED")
        if now >= registration.execution_expires_at and request["operation"] not in {"cancel", "cleanup", "inspect", "diagnostics"}:
            raise ControllerDenied("EXECUTION_EXPIRED")
        return registration

    @contextmanager
    def _locked(self, registration):
        root = self.state_root / registration.execution_id
        try:
            root.mkdir(mode=0o700)
            directory = os.open(self.state_root, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
        except FileExistsError:
            pass
        info = root.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o077:
            raise ControllerDenied("EXECUTION_STATE_UNTRUSTED")
        fd = os.open(root / "lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
        try:
            info = os.fstat(fd)
            if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_uid != os.geteuid() or info.st_mode & 0o077:
                raise ControllerDenied("EXECUTION_LOCK_UNTRUSTED")
            fcntl.flock(fd, fcntl.LOCK_EX)
            yield root
        finally:
            os.close(fd)

    def _save(self, root, state):
        temporary = root / ("state-" + uuid.uuid4().hex + ".tmp")
        fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "wb") as stream:
            stream.write(canonical(state))
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, root / "state.json")
        directory = os.open(root, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)

    def _load(self, root, registration):
        file = root / "state.json"
        if not os.path.lexists(file):
            if any(item.name != "lock" for item in root.iterdir()):
                raise ControllerDenied("OWNERSHIP_LEDGER_MISSING")
            state = {"version": 1, "binding_sha256": self._binding_digest(registration),
                     "status": "REGISTERED", "container_id": None, "invalidated": False,
                     "nonces": 0, "creation_attempted": False}
            self._save(root, state)
            return state
        try:
            _regular(file)
            info = file.stat()
            if info.st_uid != os.geteuid() or info.st_mode & 0o077 or info.st_size > MAX_REQUEST_BYTES:
                raise ControllerDenied("OWNERSHIP_LEDGER_UNTRUSTED")
            state = json.loads(file.read_bytes())
            if (set(state) != {"version", "binding_sha256", "status", "container_id", "invalidated", "nonces", "creation_attempted"}
                    or state["version"] != 1 or state["binding_sha256"] != self._binding_digest(registration)
                    or state["status"] not in {"REGISTERED", "CREATING", "CREATED", "STARTING", "RUNNING", "EXITED", "CANCELLING", "CANCELLED", "CLEANING", "CLEANED", "UNKNOWN"}
                    or (state["container_id"] is not None and not _hex(state["container_id"]))
                    or type(state["invalidated"]) is not bool or type(state["nonces"]) is not int
                    or type(state["creation_attempted"]) is not bool
                    or not 0 <= state["nonces"] <= MAX_NONCES + TERMINATION_NONCES):
                raise ControllerDenied("OWNERSHIP_LEDGER_INVALID")
            return state
        except (OSError, ValueError, TypeError):
            raise ControllerDenied("OWNERSHIP_LEDGER_INVALID") from None

    def _consume(self, root, registration, request, state):
        limit = MAX_NONCES + (TERMINATION_NONCES if request["operation"] in {"cancel", "cleanup"} else 0)
        if state["nonces"] >= limit:
            raise ControllerDenied("REQUEST_COUNT_EXCEEDED")
        # A crash after reservation deliberately consumes the request forever.
        nonce_file = root / ("nonce-" + request["nonce"])
        try:
            fd = os.open(nonce_file, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        except FileExistsError:
            raise ControllerDenied("REQUEST_REPLAYED") from None
        with os.fdopen(fd, "wb") as stream:
            stream.write(hashlib.sha256(canonical(request)).hexdigest().encode())
            stream.flush()
            os.fsync(stream.fileno())
        state["nonces"] += 1
        self._save(root, state)

    def name(self, registration):
        return "cdo-runtime-" + uuid.UUID(registration.execution_id).hex

    def labels(self, registration):
        ownership = hmac.new(registration.secret, b"cdo-runtime-container-owner-v1\0" +
                             canonical(self._public_binding(registration)) + b"\0" +
                             str(self.state_root).encode(), hashlib.sha256).hexdigest()
        return {"cdo.execution_id": registration.execution_id,
                "cdo.resource": "command" if registration.recipe == "command-runtime" else "sdk",
                "cdo.runtime.controller": self.config.controller_id,
                "cdo.runtime.protocol": protocol_sha256(),
                "cdo.runtime.release": registration.release_id,
                "cdo.runtime.workpackage": registration.workpackage_id,
                "cdo.runtime.role": registration.role,
                "cdo.runtime.scope": registration.scope_sha256,
                "cdo.runtime.profile": PROFILE_VALUES[registration.recipe]["name"],
                "cdo.runtime.owner": ownership,
                "cdo.runtime.expiry": str(registration.execution_expires_at)}

    def mounts(self, registration):
        result = {"/reference/runtime.py": str(registration.helper_path)}
        if registration.source_root is not None:
            result["/workspace"] = str(registration.source_root)
        if registration.dependency_root is not None:
            result["/dependency"] = str(registration.dependency_root)
        return result

    def create_arguments(self, registration):
        self._validate_registration(registration)
        profile = PROFILE_VALUES[registration.recipe]
        args = ["create", "--name", self.name(registration), "--network", "none",
                "--read-only", "--user", "100:101", "--cap-drop", "ALL",
                "--security-opt", "no-new-privileges=true", "--security-opt", "seccomp=builtin",
                "--cgroupns", "private", "--ipc", "private", "--cpus", "2",
                "--runtime", "runc",
                "--memory", str(profile["memory"]), "--memory-swap", str(profile["memory"]),
                "--pids-limit", "128", "--ulimit", "core=0:0", "--ulimit", "nofile=256:256",
                "--ulimit", "fsize=16777216:16777216", "--restart", "no", "--log-driver", "local",
                "--log-opt", "max-size=16k", "--log-opt", "max-file=1", "--log-opt", "compress=false",
                "--workdir", "/workspace" if registration.recipe == "command-runtime" else "/",
                "--entrypoint", "/usr/bin/env"]
        for key, value in sorted(self.labels(registration).items()):
            args += ["--label", key + "=" + value]
        for target, source in sorted(self.mounts(registration).items()):
            args += ["--mount", f"type=bind,source={source},target={target},readonly"]
        for target, options in sorted(profile["tmpfs"].items()):
            args += ["--tmpfs", target + ":" + options]
        return args + [self.image_id(registration), "-i", "PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
                       "CDO_RUNTIME_DEADLINE=" + str(registration.execution_expires_at),
                       "python3", "/reference/runtime.py"]

    def _verify_owned(self, registration, state, info):
        try:
            labels = info["Config"]["Labels"]
            expected = self.labels(registration)
            if (not isinstance(labels, dict) or any(labels.get(k) != v for k, v in expected.items())
                    or any(k.startswith("cdo.runtime.") and k not in expected for k in labels)
                    or info["Name"] != "/" + self.name(registration)
                    or not _hex(info["Id"]) or info["Image"] != self.image_id(registration)
                    or (state["container_id"] is not None and info["Id"] != state["container_id"])):
                raise ControllerDenied("CONTAINER_OWNERSHIP_DENIED")
            config = info["Config"]
            host = info["HostConfig"]
            profile = PROFILE_VALUES[registration.recipe]
            if (config.get("Image") != self.image_id(registration) or config.get("User") != "100:101"
                    or config.get("Entrypoint") != ["/usr/bin/env"]
                    or config.get("Cmd") != ["-i", "PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin", "CDO_RUNTIME_DEADLINE=" + str(registration.execution_expires_at), "python3", "/reference/runtime.py"]
                    or config.get("WorkingDir") != ("/workspace" if registration.recipe == "command-runtime" else "/")
                    or config.get("Volumes") not in (None, {})):
                raise ControllerDenied("CONTAINER_POLICY_DENIED")
            exact = {"ReadonlyRootfs": True, "Privileged": False, "NetworkMode": "none",
                     "CgroupnsMode": "private", "IpcMode": "private", "PidMode": "", "UTSMode": "",
                     "Runtime": "runc", "Isolation": "", "CgroupParent": "",
                     "NanoCpus": 2000000000, "Memory": profile["memory"], "MemorySwap": profile["memory"],
                     "PidsLimit": 128, "PublishAllPorts": False, "AutoRemove": False,
                     "CpuPeriod": 0, "CpuQuota": 0}
            if any(type(host.get(k)) is not type(v) or host.get(k) != v for k, v in exact.items()):
                raise ControllerDenied("CONTAINER_POLICY_DENIED")
            if (host.get("CapDrop") != ["ALL"] or host.get("CapAdd") not in (None, [])
                    or set(host.get("SecurityOpt") or []) != {"no-new-privileges=true", "seccomp=builtin"}
                    or host.get("Tmpfs") != profile["tmpfs"]
                    or host.get("RestartPolicy") != {"Name": "no", "MaximumRetryCount": 0}
                    or host.get("LogConfig") != {"Type": "local", "Config": {"max-size": "16k", "max-file": "1", "compress": "false"}}):
                raise ControllerDenied("CONTAINER_POLICY_DENIED")
            for key in ("Devices", "DeviceRequests", "DeviceCgroupRules", "Sysctls", "Binds", "GroupAdd", "ExtraHosts", "Links", "VolumesFrom", "PortBindings", "Dns", "DnsOptions", "DnsSearch"):
                if host.get(key) not in (None, [], {}):
                    raise ControllerDenied("CONTAINER_POLICY_DENIED")
            if {i["Name"]: (i["Soft"], i["Hard"]) for i in host["Ulimits"]} != {
                    "core": (0, 0), "nofile": (256, 256), "fsize": (16777216, 16777216)}:
                raise ControllerDenied("CONTAINER_POLICY_DENIED")
            mounts = {}
            for mount in info["Mounts"]:
                if mount["Type"] == "tmpfs":
                    if mount["Destination"] not in profile["tmpfs"]:
                        raise ControllerDenied("CONTAINER_MOUNT_DENIED")
                    continue
                if (mount["Type"] != "bind" or mount["RW"] is not False
                        or mount.get("Propagation") not in ("rprivate", "")
                        or mount["Destination"] in mounts):
                    raise ControllerDenied("CONTAINER_MOUNT_DENIED")
                mounts[mount["Destination"]] = mount["Source"]
            if mounts != self.mounts(registration):
                raise ControllerDenied("CONTAINER_MOUNT_DENIED")
            networks = info["NetworkSettings"]["Networks"]
            if not isinstance(networks, dict) or set(networks) - {"none"}:
                raise ControllerDenied("CONTAINER_NETWORK_DENIED")
            for value in networks.values():
                if any(value.get(k) for k in ("IPAddress", "GlobalIPv6Address", "Gateway", "IPv6Gateway")):
                    raise ControllerDenied("CONTAINER_NETWORK_DENIED")
            if (type(info["State"]["Running"]) is not bool or type(info["State"]["ExitCode"]) is not int
                    or type(info["State"]["OOMKilled"]) is not bool
                    or info["State"]["Status"] not in {"created", "running", "exited", "dead"}):
                raise RuntimeUnknown()
        except (KeyError, TypeError, ValueError, AttributeError):
            raise RuntimeUnknown("DOCKER_INSPECTION_UNKNOWN") from None

    def _observe(self, registration, state):
        target = state["container_id"] or self.name(registration)
        info = self.docker.inspect(target)
        if info is not None:
            self._verify_owned(registration, state, info)
        return info

    def _result(self, registration, state, info=None):
        result = {"version": 1, "execution_id": registration.execution_id,
                  "status": state["status"], "invalidated": state["invalidated"],
                  "container_id": state["container_id"], "production_admission": "DENIED"}
        if info is not None:
            value = info["State"]
            result["diagnostics"] = {"running": value["Running"], "exit_code": value["ExitCode"],
                                     "oom_killed": value["OOMKilled"], "container_status": value["Status"]}
        if len(canonical(result)) > MAX_RESPONSE_BYTES:
            raise RuntimeUnknown("RESPONSE_LIMIT")
        return result

    def _cleanup(self, root, registration, state, cancelled=False):
        if not state["creation_attempted"]:
            info = self._observe(registration, state)
            if info is not None:
                raise ControllerDenied("CONTAINER_NAME_COLLISION")
            state.update(status="CANCELLED" if cancelled else "CLEANED", invalidated=True)
            self._save(root, state)
            return self._result(registration, state)
        info = self._observe(registration, state)
        if info is not None:
            # Remove only the verified immutable ID, never a supplied name/label
            # filter. A second ownership observation precedes the destructive op.
            state["container_id"] = info["Id"]
            state.update(status="CLEANING", invalidated=True)
            self._save(root, state)
            fresh = self._observe(registration, state)
            if fresh is not None:
                self.docker.remove(fresh["Id"])
            if self._observe(registration, state) is not None:
                raise RuntimeUnknown("CLEANUP_UNPROVEN")
        state.update(status="CANCELLED" if cancelled else "CLEANED", invalidated=True)
        self._save(root, state)
        return self._result(registration, state)

    def handle(self, request):
        registration = self._validate_request(request)
        with self._locked(registration) as root:
            # Recheck expiry after locking, so a queued start cannot outlive its
            # authorization. Every consumed nonce survives controller restart.
            self._validate_request(request)
            state = self._load(root, registration)
            self._consume(root, registration, request, state)
            operation = request["operation"]
            try:
                if operation in {"cancel", "cleanup"}:
                    return self._cleanup(root, registration, state, operation == "cancel")
                if operation == "create":
                    if self._watchdog_error:
                        raise RuntimeUnknown("WATCHDOG_UNKNOWN")
                    if state["status"] != "REGISTERED" or state["invalidated"]:
                        raise ControllerDenied("EXECUTION_ALREADY_USED")
                    if self.docker.inspect(self.name(registration)) is not None:
                        raise ControllerDenied("CONTAINER_NAME_COLLISION")
                    arguments = self.create_arguments(registration)
                    self._validate_request(request)
                    state.update(status="CREATING", creation_attempted=True)
                    self._save(root, state)
                    state["container_id"] = self.docker.create(arguments)
                    if not _hex(state["container_id"]):
                        raise RuntimeUnknown()
                    self._save(root, state)
                    info = self._observe(registration, state)
                    if info is None:
                        raise RuntimeUnknown("CREATE_UNPROVEN")
                    state["status"] = "CREATED"
                elif operation == "start":
                    if state["status"] != "CREATED" or state["invalidated"]:
                        raise ControllerDenied("EXECUTION_START_DENIED")
                    if self._watchdog is None or not self._watchdog.is_alive() or self._watchdog_error:
                        raise ControllerDenied("LIFETIME_WATCHDOG_REQUIRED")
                    self._validate_registration(registration)
                    info = self._observe(registration, state)
                    if info is None or info["State"]["Status"] != "created":
                        raise RuntimeUnknown("START_STATE_UNKNOWN")
                    state["status"] = "STARTING"
                    self._save(root, state)
                    self._validate_request(request)
                    self.docker.start(info["Id"])
                    info = self._observe(registration, state)
                    if info is None:
                        raise RuntimeUnknown("START_UNPROVEN")
                    state["status"] = "RUNNING" if info["State"]["Running"] else "EXITED"
                else:
                    if state["status"] == "REGISTERED":
                        if self._observe(registration, state) is not None:
                            raise ControllerDenied("CONTAINER_NAME_COLLISION")
                        return self._result(registration, state)
                    info = self._observe(registration, state)
                    if state["invalidated"] and state["status"] == "UNKNOWN":
                        raise RuntimeUnknown()
                    if info is None:
                        if state["status"] not in {"CLEANED", "CANCELLED"}:
                            raise RuntimeUnknown("CONTAINER_LOST")
                    elif not info["State"]["Running"] and state["status"] == "RUNNING":
                        state["status"] = "EXITED"
                self._save(root, state)
                result = self._result(registration, state, info)
                if operation == "diagnostics" and info is not None:
                    result["diagnostics"]["output"] = self.docker.logs(info["Id"])
                    if not isinstance(result["diagnostics"]["output"], str) or len(result["diagnostics"]["output"].encode()) > 2048:
                        raise RuntimeUnknown("DIAGNOSTICS_LIMIT")
                if len(canonical(result)) > MAX_RESPONSE_BYTES:
                    raise RuntimeUnknown("RESPONSE_LIMIT")
                return result
            except (RuntimeUnknown, ControllerDenied) as error:
                if isinstance(error, RuntimeUnknown) or error.code.startswith("CONTAINER_"):
                    state.update(status="UNKNOWN", invalidated=True)
                    self._save(root, state)
                raise
            except Exception:
                state.update(status="UNKNOWN", invalidated=True)
                self._save(root, state)
                raise RuntimeUnknown() from None

    def reconcile_expired(self):
        """Host administrator watchdog: expiry cleanup, no execution revival."""
        results = []
        for registration in self.registrations.values():
            if self.clock() < registration.execution_expires_at:
                continue
            try:
                with self._locked(registration) as root:
                    state = self._load(root, registration)
                    if state["status"] in {"CLEANED", "CANCELLED"}:
                        continue
                    try:
                        results.append(self._cleanup(root, registration, state, cancelled=True))
                    except ControllerDenied:
                        state.update(status="UNKNOWN", invalidated=True)
                        self._save(root, state)
                        raise
            except ControllerDenied as error:
                results.append({"execution_id": registration.execution_id, "status": "UNKNOWN", "code": error.code})
        return results

    def start_watchdog(self):
        if self._watchdog is not None:
            raise ControllerDenied("WATCHDOG_ALREADY_STARTED")
        def watch():
            while not self._watchdog_stop.wait(.25):
                try:
                    results = self.reconcile_expired()
                    if any(result["status"] == "UNKNOWN" for result in results):
                        self._watchdog_error = "WATCHDOG_UNKNOWN"
                except Exception:
                    self._watchdog_error = "WATCHDOG_UNKNOWN"
                    return
        self._watchdog = threading.Thread(target=watch, daemon=True, name="cdo-runtime-expiry")
        self._watchdog.start()

    def close(self):
        self._watchdog_stop.set()
        if self._watchdog is not None:
            self._watchdog.join(timeout=16)
