"""Authenticated, lease-bound host AgentRunner lifecycle.

Backend keys authorize one immutable WorkPackage template. Host ownership keys,
Docker configuration, process commands and source roots never come from requests.
The six operations expose execution lifecycle, never a Docker command channel.
"""
from __future__ import annotations
from dataclasses import dataclass, field
import fcntl
import hashlib
import hmac
import json
import math
import os
from pathlib import Path
import stat
import subprocess
import threading
import time
from types import MappingProxyType, SimpleNamespace
import uuid

import cdo_runtime_controller as finite
import cdo_runtime_backend as backend
import cdo_runtime_attestation as host_attestation

ControllerDenied = finite.ControllerDenied
RuntimeUnknown = finite.RuntimeUnknown
canonical = finite.canonical
OPERATIONS = finite.OPERATIONS
FIELDS = frozenset({"version", "operation", "execution_id", "release_id", "workpackage_id",
                   "role", "image_id", "scope_sha256", "resource_profile", "execution_expires_at",
                   "request_expires_at", "nonce", "signature", "controller_id", "protocol_sha256",
                   "attempt", "runtime_context", "request_sha256", "attestation_sha256", "cursor"})
BINDINGS = ("execution_id", "release_id", "workpackage_id", "role", "image_id", "scope_sha256",
            "resource_profile", "execution_expires_at", "controller_id", "protocol_sha256",
            "attempt", "runtime_context", "request_sha256", "attestation_sha256")
MAX_EVENTS_BYTES = 1024 * 1024
MAX_EVENTS = 256
MAX_RECORDS = 128
MAX_NONCES = 4096
MAX_OBSERVER_NONCES = 4096
TERMINATION_RESERVE = 16
AGENT_PROTOCOL = backend.PROTOCOL_DESCRIPTOR
CONTROLLER_SOURCE_FILES = {"finite_controller": "cdo_runtime_controller.py",
                          "agent_controller": "cdo_agent_runtime.py",
                          "owned_runtime": "cdo_runtime_owned.py",
                          "coordinator": "cdo_runtime_coordinator.py",
                          "attestation": "cdo_runtime_attestation.py", "broker": "cdo_broker.py"}
TRANSPORT_SOURCE_FILES = {"backend_client": "cdo_runtime_backend.py",
                         "http_transport": "cdo_runtime_transport.py",
                         "sdk_relay": "cdo_sdk_relay.py", "sdk_state": "cdo_sdk_state.py"}


def _implementation_identity(files):
    root = Path(__file__).resolve().parent
    inventory = {name: hashlib.sha256(finite._regular(root / file).read_bytes()).hexdigest()
                 for name, file in files.items()}
    return hashlib.sha256(canonical(inventory)).hexdigest()


def controller_identity():
    return _implementation_identity(CONTROLLER_SOURCE_FILES)


def transport_identity():
    return _implementation_identity(TRANSPORT_SOURCE_FILES)


def agent_protocol_sha256():
    return backend.protocol_sha256()


def normalize_request(request):
    return backend.normalize_request(request)


def request_digest(request):
    return hashlib.sha256(canonical(normalize_request(request))).hexdigest()


def execution_secret(secret, request):
    return hmac.new(secret, b"cdo-agent-execution-v1\0" + canonical({
        key: request[key] for key in ("execution_id", "attempt", "runtime_context")}), hashlib.sha256).digest()


def request_signature(secret, request):
    unsigned = {key: value for key, value in request.items() if key != "signature"}
    return hmac.new(secret, b"cdo-agent-request-v1\0" + canonical(unsigned), hashlib.sha256).hexdigest()


def response_signature(secret, response):
    return hmac.new(secret, b"cdo-agent-response-v1\0" + canonical(response), hashlib.sha256).hexdigest()


@dataclass(frozen=True)
class AgentTemplate:
    workpackage_id: str
    release_id: str
    role: str
    image_id: str
    scope_sha256: str
    resource_profile: str
    request: dict
    secret: bytes
    execution_expiry_max_seconds: int = 1200
    environment: dict = field(default_factory=dict)
    approved_commands: tuple = (("go", "test", "./..."),)
    owned_config: dict = field(default_factory=dict)

    @property
    def request_sha256(self):
        return request_digest(self.request)

    @property
    def template_id(self):
        return self.workpackage_id


class AgentController:
    def __init__(self, state_root, templates, coordinator_command, attestation,
                 controller_id=None, attestation_sha256=None, cleanup=None,
                 process_factory=None, clock=time.time, lease_seconds=10,
                 controller_configuration=None, registration=None, teardown=None,
                 host_cleanup=None):
        self.state_root = finite._path(state_root)
        info = self.state_root.stat()
        if not self.state_root.is_dir() or info.st_uid != os.geteuid() or info.st_mode & 0o077:
            raise ControllerDenied("AGENT_STATE_ROOT_UNTRUSTED")
        if type(lease_seconds) not in (int, float) or not 0 < lease_seconds <= 10:
            raise ControllerDenied("LEASE_POLICY_DENIED")
        self.clock = clock
        self.lease_seconds = lease_seconds
        self.coordinator_command = tuple(coordinator_command)
        if len(self.coordinator_command) != 2:
            raise ControllerDenied("COORDINATOR_COMMAND_DENIED")
        for path in self.coordinator_command:
            target = finite._regular(path)
            value = target.stat()
            if value.st_uid not in {0, os.geteuid()} or value.st_mode & 0o022:
                raise ControllerDenied("COORDINATOR_COMMAND_DENIED")
        self.config = SimpleNamespace(controller_id=controller_id or globals()["controller_identity"](),
                                      protocol_sha256=agent_protocol_sha256(),
                                      attestation_sha256=attestation_sha256)
        if not finite._hex(self.config.controller_id) or not finite._hex(attestation_sha256):
            raise ControllerDenied("AGENT_CONFIG_IDENTITY_DENIED")
        self.attestation = attestation
        self.controller_configuration = json.loads(canonical(controller_configuration or {}))
        if len(canonical(self.controller_configuration)) > MAX_EVENTS_BYTES:
            raise ControllerDenied("AGENT_CONFIGURATION_LIMIT")
        self.cleanup = cleanup or self._default_cleanup
        from cdo_runtime_coordinator import (await_coordinator_registration,
                                             validate_teardown, reconcile_cleanup)
        self.registration = registration or await_coordinator_registration
        self.teardown = teardown or validate_teardown
        self.host_cleanup = host_cleanup or reconcile_cleanup
        self.process_factory = process_factory or subprocess.Popen
        approved = {}
        for template in templates:
            if (not finite._uuid(template.workpackage_id) or not finite._hex(template.release_id)
                    or template.role not in finite.ROLES or not finite._hex(template.image_id, image=True)
                    or not finite._hex(template.scope_sha256) or template.resource_profile != "agent-v1"
                    or not isinstance(template.secret, bytes) or not 32 <= len(template.secret) <= 128
                    or type(template.execution_expiry_max_seconds) is not int
                    or not 1 <= template.execution_expiry_max_seconds <= 1200
                    or not isinstance(template.request, dict) or not isinstance(template.environment, dict)
                    or not template.approved_commands or len(template.approved_commands) > 32):
                raise ControllerDenied("AGENT_TEMPLATE_DENIED")
            if template.workpackage_id in approved:
                raise ControllerDenied("AGENT_TEMPLATE_DUPLICATE")
            try:
                normalized = normalize_request(template.request)
            except backend.BackendDenied:
                raise ControllerDenied("AGENT_TEMPLATE_REQUEST_DENIED") from None
            if (normalized["sandbox_scope"]["profile"] != template.role
                    or hashlib.sha256(canonical(normalized["sandbox_scope"])).hexdigest() != template.scope_sha256):
                raise ControllerDenied("AGENT_TEMPLATE_SCOPE_DENIED")
            for command in template.approved_commands:
                if not command or len(command) > 64 or any(not isinstance(v, str) or "\0" in v for v in command):
                    raise ControllerDenied("AGENT_COMMAND_POLICY_DENIED")
            for key, value in template.environment.items():
                if not isinstance(key, str) or not isinstance(value, str) or "\0" in key + value:
                    raise ControllerDenied("AGENT_ENVIRONMENT_DENIED")
                if key in {"CDO_RUNTIME_BACKEND_CREDENTIALS", "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG"}:
                    raise ControllerDenied("AGENT_ENVIRONMENT_DENIED")
            # Canonical copying prevents later caller mutation from widening policy.
            copied = json.loads(canonical({"request": template.request, "environment": template.environment,
                                          "owned_config": template.owned_config}))
            approved[template.workpackage_id] = AgentTemplate(
                template.workpackage_id, template.release_id, template.role, template.image_id,
                template.scope_sha256, template.resource_profile, copied["request"], template.secret,
                template.execution_expiry_max_seconds, copied["environment"],
                tuple(tuple(v) for v in template.approved_commands), copied["owned_config"])
        if not approved or len(approved) > 128:
            raise ControllerDenied("AGENT_TEMPLATE_LIMIT")
        self.templates = MappingProxyType(approved)
        self.boot_id = str(uuid.uuid4())
        self._processes = {}
        self._threads = {}
        self._stopping = threading.Event()
        self._watchdog = None
        self._health = None
        self._ownership_key = self._host_key()

    def _host_key(self):
        file = self.state_root / "host-ownership.key"
        try:
            fd = os.open(file, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
            with os.fdopen(fd, "wb") as stream:
                stream.write(os.urandom(32)); stream.flush(); os.fsync(stream.fileno())
            self._fsync(self.state_root)
        except FileExistsError:
            pass
        finite._regular(file)
        value = file.stat()
        if value.st_uid != os.geteuid() or value.st_mode & 0o077 or value.st_size != 32:
            raise ControllerDenied("HOST_OWNERSHIP_KEY_UNTRUSTED")
        return file.read_bytes()

    @staticmethod
    def _fsync(root):
        fd = os.open(root, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)

    def _atomic(self, file, value):
        if file.name == "agent-state.json":
            value = {**value, "state_mac": hmac.new(self._ownership_key,
                b"cdo-agent-state-v1\0" + canonical(value), hashlib.sha256).hexdigest()}
        temporary = file.with_name(file.name + "." + uuid.uuid4().hex + ".tmp")
        fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "wb") as stream:
            stream.write(canonical(value)); stream.flush(); os.fsync(stream.fileno())
        os.replace(temporary, file)
        self._fsync(file.parent)

    def _read(self, file):
        finite._regular(file)
        value = file.stat()
        if value.st_uid != os.geteuid() or value.st_mode & 0o077 or value.st_size > MAX_EVENTS_BYTES:
            raise ControllerDenied("AGENT_STATE_UNTRUSTED")
        try:
            return backend.decode(file.read_bytes())
        except (ValueError, UnicodeError, RecursionError):
            raise ControllerDenied("AGENT_STATE_INVALID") from None

    def _authenticate(self, request):
        if not isinstance(request, dict) or set(request) != FIELDS:
            raise ControllerDenied("AGENT_REQUEST_FIELDS_DENIED")
        if (type(request["version"]) is not int or request["version"] != 1
                or not isinstance(request["operation"], str) or request["operation"] not in OPERATIONS
                or not finite._uuid(request["execution_id"]) or not finite._uuid(request["workpackage_id"])
                or not finite._uuid(request["nonce"]) or not finite._hex(request["signature"])
                or type(request["attempt"]) is not int or not 1 <= request["attempt"] <= 100
                or type(request["cursor"]) is not int or not 0 <= request["cursor"] <= MAX_EVENTS
                or (request["operation"] != "diagnostics" and request["cursor"] != 0)):
            raise ControllerDenied("AGENT_REQUEST_INVALID")
        context = request["runtime_context"]
        if not isinstance(context, dict) or (context and set(context) != {"workflow_id", "workflow_run_id", "activity_id", "activity_attempt"}):
            raise ControllerDenied("RUNTIME_CONTEXT_DENIED")
        if context and (any(not isinstance(context[k], str) or not 1 <= len(context[k]) <= 255 or "\0" in context[k]
                            for k in ("workflow_id", "workflow_run_id", "activity_id"))
                        or type(context["activity_attempt"]) is not int or not 1 <= context["activity_attempt"] <= 100):
            raise ControllerDenied("RUNTIME_CONTEXT_DENIED")
        template = self.templates.get(request["workpackage_id"])
        if template is None:
            raise ControllerDenied("WORKPACKAGE_UNAPPROVED")
        expected = {"release_id": template.release_id, "role": template.role, "image_id": template.image_id,
                    "scope_sha256": template.scope_sha256, "resource_profile": template.resource_profile,
                    "request_sha256": template.request_sha256,
                    "controller_id": self.config.controller_id, "protocol_sha256": self.config.protocol_sha256,
                    "attestation_sha256": self.config.attestation_sha256}
        if any(type(request[key]) is not type(value) or request[key] != value for key, value in expected.items()):
            raise ControllerDenied("AGENT_IDENTITY_MISMATCH")
        if (type(request["request_expires_at"]) is not int
                or not self.clock() < request["request_expires_at"] <= self.clock() + 60
                or type(request["execution_expires_at"]) is not int
                or request["execution_expires_at"] > self.clock() + template.execution_expiry_max_seconds):
            raise ControllerDenied("AGENT_REQUEST_EXPIRED")
        if request["operation"] in {"create", "start"} and request["execution_expires_at"] <= self.clock():
            raise ControllerDenied("EXECUTION_EXPIRED")
        if not context and template.environment.get("CDO_SANDBOX_EXECUTION_MODE") != "hardened-certification":
            raise ControllerDenied("TEMPORAL_CONTEXT_REQUIRED")
        if len(canonical(request)) > 16384:
            raise ControllerDenied("AGENT_REQUEST_LIMIT")
        secret = template.secret if request["operation"] == "create" else execution_secret(template.secret, request)
        if not hmac.compare_digest(request_signature(secret, request), request["signature"]):
            raise ControllerDenied("AGENT_SIGNATURE_DENIED")
        return template, secret

    def _attest(self):
        try:
            value = self.attestation()
            configuration = self.controller_configuration
            if isinstance(value, dict) and set(value) == {"attestation", "controller_configuration"}:
                configuration = value["controller_configuration"]
                value = value["attestation"]
            if not isinstance(value, dict) or not isinstance(configuration, dict):
                raise RuntimeUnknown("ACTIVE_ATTESTATION_UNKNOWN")
            try:
                host_attestation.verify_attestation(value, value)
            except host_attestation.AttestationDenied:
                raise RuntimeUnknown("ACTIVE_ATTESTATION_INCOMPLETE") from None
            if value["node"] is None or value["protocol_sha256"] != self.config.protocol_sha256:
                raise RuntimeUnknown("ACTIVE_ATTESTATION_INCOMPLETE")
            if len(canonical(value)) > MAX_EVENTS_BYTES:
                raise RuntimeUnknown("ACTIVE_ATTESTATION_LIMIT")
            if "attestation_sha256" in value:
                unsigned = {k: v for k, v in value.items() if k != "attestation_sha256"}
                if hashlib.sha256(canonical(unsigned)).hexdigest() != value["attestation_sha256"]:
                    raise RuntimeUnknown("ACTIVE_ATTESTATION_CHANGED")
            if "controller_configuration_sha256" in value and hashlib.sha256(canonical(configuration)).hexdigest() != value["controller_configuration_sha256"]:
                raise RuntimeUnknown("ACTIVE_CONFIGURATION_CHANGED")
            if self.controller_configuration and configuration != self.controller_configuration:
                raise RuntimeUnknown("ACTIVE_CONFIGURATION_CHANGED")
            digest = hashlib.sha256(canonical(value)).hexdigest()
            if digest != self.config.attestation_sha256:
                raise RuntimeUnknown("ACTIVE_ATTESTATION_CHANGED")
            return value, configuration
        except ControllerDenied:
            raise
        except Exception:
            raise RuntimeUnknown("ACTIVE_ATTESTATION_UNKNOWN") from None

    def _binding(self, request):
        return {key: request[key] for key in BINDINGS}

    def _root(self, execution_id):
        return self.state_root / execution_id

    def _lock(self, root):
        try:
            root.mkdir(mode=0o700); self._fsync(self.state_root)
        except FileExistsError:
            pass
        value = root.lstat()
        if not stat.S_ISDIR(value.st_mode) or value.st_uid != os.geteuid() or value.st_mode & 0o077:
            raise ControllerDenied("AGENT_EXECUTION_ROOT_UNTRUSTED")
        fd = os.open(root / "lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
        value = os.fstat(fd)
        if not stat.S_ISREG(value.st_mode) or value.st_nlink != 1 or value.st_uid != os.geteuid() or value.st_mode & 0o077:
            os.close(fd); raise ControllerDenied("AGENT_LOCK_UNTRUSTED")
        fcntl.flock(fd, fcntl.LOCK_EX)
        return fd

    def _load(self, root, request=None):
        file = root / "agent-state.json"
        if not os.path.lexists(file):
            if request is None or any(p.name != "lock" for p in root.iterdir()):
                raise ControllerDenied("AGENT_STATE_MISSING")
            state = {"version": 1, "binding": self._binding(request), "state": "UNCREATED",
                     "boot_id": self.boot_id, "lease_expires_at": 0, "exit_code": None,
                     "invalid_reason": None, "cleanup_proven": False, "nonces": 0,
                     "events": [], "event_bytes": 0, "pending_result": None, "outcome": None,
                     "owned_config_sha256": None, "job_sha256": None, "launch_attempted": False,
                     "process_owner": None, "coordinator_pid": None, "coordinator_reaped": False,
                     "coordinator_absence_observed": False, "cleanup_evidence": None,
                     "reconciliation_evidence": None}
            self._atomic(file, state)
            return state
        state = self._read(file)
        if not isinstance(state, dict):
            raise ControllerDenied("AGENT_STATE_INVALID")
        signature = state.pop("state_mac", None)
        if not isinstance(signature, str) or not hmac.compare_digest(signature,
                hmac.new(self._ownership_key, b"cdo-agent-state-v1\0" + canonical(state), hashlib.sha256).hexdigest()):
            raise ControllerDenied("AGENT_STATE_IDENTITY_CHANGED")
        expected_fields = {"version", "binding", "state", "boot_id", "lease_expires_at", "exit_code",
                           "invalid_reason", "cleanup_proven", "nonces", "events", "event_bytes", "pending_result", "outcome",
                           "owned_config_sha256", "job_sha256", "launch_attempted", "process_owner",
                           "coordinator_pid", "coordinator_reaped", "coordinator_absence_observed",
                           "cleanup_evidence", "reconciliation_evidence"}
        if (not isinstance(state, dict) or set(state) != expected_fields
                or type(state["version"]) is not int or state["version"] != 1
                or state["state"] not in {"UNCREATED", "CREATED", "RUNNING", "SUCCEEDED", "FAILED", "INVALIDATED", "UNKNOWN", "CLEANED"}
                or not isinstance(state["binding"], dict) or set(state["binding"]) != set(BINDINGS)
                or state["binding"].get("execution_id") != root.name
                or (request is not None and state["binding"] != self._binding(request))
                or type(state["nonces"]) is not int or not 0 <= state["nonces"] <= MAX_NONCES + TERMINATION_RESERVE
                or type(state["cleanup_proven"]) is not bool or not isinstance(state["events"], list)
                or len(state["events"]) > MAX_EVENTS or type(state["event_bytes"]) is not int
                or not 0 <= state["event_bytes"] <= MAX_EVENTS_BYTES
                or type(state["lease_expires_at"]) not in (int, float) or not math.isfinite(state["lease_expires_at"])
                or not finite._uuid(state["boot_id"])
                or (state["exit_code"] is not None and type(state["exit_code"]) is not int)
                or (state["invalid_reason"] is not None and (not isinstance(state["invalid_reason"], str) or len(state["invalid_reason"]) > 128))
                or state["outcome"] not in {None, "SUCCEEDED", "FAILED", "INVALIDATED", "UNKNOWN"}):
            raise ControllerDenied("AGENT_STATE_INVALID")
        if (type(state["launch_attempted"]) is not bool
                or type(state["coordinator_reaped"]) is not bool
                or type(state["coordinator_absence_observed"]) is not bool
                or (state["coordinator_pid"] is not None and (type(state["coordinator_pid"]) is not int or state["coordinator_pid"] < 1))
                or (state["coordinator_reaped"] and (not state["launch_attempted"] or state["coordinator_pid"] is None))
                or any(state[k] is not None and not finite._hex(state[k]) for k in ("owned_config_sha256", "job_sha256"))):
            raise ControllerDenied("AGENT_STATE_INVALID")
        owner = state["process_owner"]
        if owner is not None and (not isinstance(owner, dict)
                or set(owner) != {"version", "controller_id", "boot_id", "owner_nonce"}
                or type(owner["version"]) is not int or owner["version"] != 1
                or owner["controller_id"] != state["binding"]["controller_id"]
                or owner["boot_id"] != state["boot_id"] or not finite._uuid(owner["boot_id"])
                or not isinstance(owner["owner_nonce"], str) or len(owner["owner_nonce"]) != 32
                or any(c not in "0123456789abcdef" for c in owner["owner_nonce"])):
            raise ControllerDenied("AGENT_STATE_INVALID")
        for key in ("cleanup_evidence", "reconciliation_evidence"):
            if state[key] is not None and not self._valid_cleanup_evidence(state, state[key]):
                raise ControllerDenied("AGENT_CLEANUP_EVIDENCE_INVALID")
        if state["cleanup_proven"] and (state["cleanup_evidence"] is None
                or state["cleanup_evidence"]["no_unknown"] is not True
                or state["state"] == "UNKNOWN" or state["outcome"] == "UNKNOWN"):
            raise ControllerDenied("AGENT_CLEANUP_EVIDENCE_INVALID")
        return state

    def _consume(self, root, request, state):
        transition = self._lock(self.state_root)
        try:
            return self._consume_locked(root, request, state)
        finally:
            os.close(transition)

    def _consume_locked(self, root, request, state):
        limit = MAX_NONCES + (TERMINATION_RESERVE if request["operation"] in {"cancel", "cleanup"} else 0)
        if state["nonces"] >= limit:
            raise ControllerDenied("AGENT_REQUEST_COUNT_EXCEEDED")
        observer = self.state_root / "observer-nonces.json"
        if os.path.lexists(observer):
            saved = self._read(observer)
            if (not isinstance(saved, dict) or set(saved) != {"nonces", "mac"}
                    or not isinstance(saved["nonces"], dict) or not isinstance(saved["mac"], str)
                    or not hmac.compare_digest(saved["mac"], hmac.new(self._ownership_key,
                        b"cdo-agent-observer-v1\0" + canonical(saved["nonces"]), hashlib.sha256).hexdigest())):
                raise RuntimeUnknown("OBSERVER_LEDGER_UNKNOWN")
            if request["nonce"] in saved["nonces"]:
                raise ControllerDenied("AGENT_REQUEST_REPLAYED")
        try:
            fd = os.open(root / ("nonce-" + request["nonce"]), os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600)
        except FileExistsError:
            raise ControllerDenied("AGENT_REQUEST_REPLAYED") from None
        with os.fdopen(fd, "wb") as stream:
            stream.write(hashlib.sha256(canonical(request)).hexdigest().encode()); stream.flush(); os.fsync(stream.fileno())
        state["nonces"] += 1
        self._atomic(root / "agent-state.json", state)

    def _observer(self, request, attestation):
        """Bounded durable replay protection without allocating an execution slot."""
        # Caller holds the transition lock, also used by execution consumption.
        self._authenticate(request)
        file = self.state_root / "observer-nonces.json"
        saved = self._read(file) if os.path.lexists(file) else None
        values = saved["nonces"] if isinstance(saved, dict) and set(saved) == {"nonces", "mac"} else {}
        if saved is not None and (not isinstance(saved, dict) or set(saved) != {"nonces", "mac"}
                or not isinstance(saved["mac"], str) or not hmac.compare_digest(saved["mac"],
                    hmac.new(self._ownership_key, b"cdo-agent-observer-v1\0" + canonical(values), hashlib.sha256).hexdigest())):
            raise RuntimeUnknown("OBSERVER_LEDGER_UNKNOWN")
        if (not isinstance(values, dict) or len(values) > MAX_OBSERVER_NONCES
                or any(not finite._uuid(k) or type(v) is not int for k, v in values.items())):
            raise RuntimeUnknown("OBSERVER_LEDGER_UNKNOWN")
        if request["nonce"] in values:
            raise ControllerDenied("AGENT_REQUEST_REPLAYED")
        values = {k: v for k, v in values.items() if v > self.clock()}
        if len(values) >= MAX_OBSERVER_NONCES:
            raise ControllerDenied("OBSERVER_REQUEST_COUNT_EXCEEDED")
        values[request["nonce"]] = request["request_expires_at"]
        self._atomic(file, {"nonces": values, "mac": hmac.new(self._ownership_key,
            b"cdo-agent-observer-v1\0" + canonical(values), hashlib.sha256).hexdigest()})
        manifest, configuration = attestation
        return {"state": "UNCREATED", "attestation_verified": True,
                "attestation_sha256": self.config.attestation_sha256,
                "attestation": manifest, "controller_configuration": configuration}

    def _claim_attempt(self, request, creating=None):
        transition = self._lock(self.state_root)
        try:
            if creating is not None:
                for root in self.state_root.iterdir():
                    if not finite._uuid(root.name) or root.name == request["execution_id"] or not os.path.lexists(root / "agent-state.json"):
                        continue
                    other = self._load(root)
                    if other["binding"]["workpackage_id"] == request["workpackage_id"] and other["state"] in {"CREATED", "RUNNING", "UNKNOWN"}:
                        raise ControllerDenied("AGENT_WORKPACKAGE_ACTIVE")
            self._claim_attempt_locked(request)
            if creating is not None:
                self._atomic(self._root(request["execution_id"]) / "agent-state.json", creating)
        finally:
            os.close(transition)

    def _claim_attempt_locked(self, request):
        identity = hashlib.sha256(canonical({k: request[k] for k in ("workpackage_id", "attempt", "runtime_context")})).hexdigest()
        file = self.state_root / ("attempt-" + identity)
        try:
            fd = os.open(file, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600)
        except FileExistsError:
            finite._regular(file)
            info = file.stat()
            if info.st_uid != os.geteuid() or info.st_mode & 0o077 or file.read_bytes() != request["execution_id"].encode():
                raise ControllerDenied("AGENT_ATTEMPT_ALREADY_USED") from None
            return
        with os.fdopen(fd, "wb") as stream:
            stream.write(request["execution_id"].encode()); stream.flush(); os.fsync(stream.fileno())
        self._fsync(self.state_root)

    def _lease(self, root, state, active=True):
        file = root / "lease.json"
        if active and os.path.lexists(file):
            previous = self._read(file)
            if (previous.get("status") != "ACTIVE" or previous.get("execution_id") != state["binding"]["execution_id"]
                    or previous.get("attempt") != state["binding"]["attempt"]):
                raise RuntimeUnknown("EXECUTION_LEASE_INVALIDATED")
        self._atomic(root / "lease.json", {"execution_id": state["binding"]["execution_id"],
            "attempt": state["binding"]["attempt"], "expires_at": state["lease_expires_at"] if active else 0,
            "status": "ACTIVE" if active else "INVALIDATED"})

    def _poison(self, root, state, reason):
        from cdo_broker import publication_lock, invalidate_publication_journal
        # The broker takes only publication.lock. Gateway callers already hold
        # the execution lock; no process wait or lifecycle gate holds this lock.
        with publication_lock(root):
            # Revoke first: a crash before the state write still leaves a
            # permanent lease fence that prevents any later journal admission.
            self._lease(root, state, active=False)
            state.update(state="INVALIDATED", invalid_reason=state["invalid_reason"] or reason,
                         outcome=state["outcome"] or "INVALIDATED", lease_expires_at=0, pending_result=None)
            self._atomic(root / "agent-state.json", state)
            journal, identity = self._publication_journal(root, state)
            if journal is not None:
                invalidate_publication_journal(journal, identity)

    def _publication_journal(self, root, state):
        template = self.templates[state["binding"]["workpackage_id"]]
        evidence = template.environment.get("CDO_CERTIFICATION_EVIDENCE")
        if not evidence:
            return None, None
        evidence = Path(evidence.replace("${EXECUTION_ROOT}", str(root)))
        if evidence.parent != root:
            raise RuntimeUnknown("AGENT_EVIDENCE_ROOT_DENIED")
        source = template.environment.get("CDO_CERTIFICATION_ROOT", template.request["working_directory"]).replace("${EXECUTION_ROOT}", str(root))
        identity = {"execution_id": state["binding"]["execution_id"], "attempt": state["binding"]["attempt"], "source_root": source}
        journal = evidence.with_name(evidence.name + ".attempt-" + hashlib.sha256(source.encode()).hexdigest()[:16] + ".publication.json")
        return journal, identity

    def _job(self, template, root, state):
        def expand(value):
            if isinstance(value, str):
                return value.replace("${EXECUTION_ROOT}", str(root))
            if isinstance(value, dict):
                return {k: expand(v) for k, v in value.items()}
            if isinstance(value, list):
                return [expand(v) for v in value]
            return value
        environment = expand(template.environment)
        from cdo_runtime_coordinator import ENVIRONMENT_KEYS
        if set(environment) - ENVIRONMENT_KEYS:
            raise ControllerDenied("AGENT_ENVIRONMENT_DENIED")
        if environment.get("CDO_CERTIFICATION_EVIDENCE"):
            evidence = Path(environment["CDO_CERTIFICATION_EVIDENCE"])
            if not evidence.is_absolute() or evidence.parent != root or evidence.name in {"", ".", ".."}:
                raise ControllerDenied("AGENT_EVIDENCE_ROOT_DENIED")
        owner = {"version": 1, "controller_id": self.config.controller_id,
                 "boot_id": self.boot_id, "owner_nonce": uuid.uuid4().hex}
        private_root = root / ("private-execution-" + owner["owner_nonce"])
        owned = expand(template.owned_config)
        # Dynamically generated snapshots/schema must stay in the single
        # registered disposable directory, outside publication/evidence files.
        for target, descriptor in owned.get("allowed_mounts", {}).items():
            if (target in {"/workspace", "/reference/schema.json"} and isinstance(descriptor, dict)
                    and descriptor.get("under") == str(root)):
                descriptor["under"] = str(private_root)
        owned_root = root / "owned"
        owned_root.mkdir(mode=0o700, exist_ok=True)
        owned.update(execution_id=state["binding"]["execution_id"], controller_id=self.config.controller_id,
                     protocol_sha256=self.config.protocol_sha256, expires_at=state["binding"]["execution_expires_at"],
                     state_root=str(owned_root), owner_marker=hmac.new(self._ownership_key,
                         b"cdo-agent-owned-v1\0" + canonical(state["binding"]) + str(root).encode(), hashlib.sha256).hexdigest())
        owned_file = root / "owned-config.json"; self._atomic(owned_file, owned)
        request = {**normalize_request(template.request), "execution_id": state["binding"]["execution_id"],
                   "attempt": state["binding"]["attempt"], "runtime_context": state["binding"]["runtime_context"],
                   "execution_deadline": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(state["binding"]["execution_expires_at"]))}
        if environment.get("CDO_CERTIFICATION_ROOT"):
            request["working_directory"] = environment["CDO_CERTIFICATION_ROOT"]
        environment.update(CDO_RUNTIME_LEASE_FILE=str(root / "lease.json"), CDO_RUNTIME_OWNED_CONFIG=str(owned_file),
                           CDO_RUNTIME_CONTROL_ROOT=str(root), CDO_RUNTIME_PRIVATE_ROOT=str(private_root),
                           CDO_RUNTIME_APPROVED_COMMANDS_JSON=canonical(template.approved_commands).decode(),
                           CODEX_SOL_MAX_RUNS_5H="20", CODEX_BUDGET_MODE="enforce")
        job = {"version": 1, "request": request, "environment": environment,
               "lease_file": str(root / "lease.json"), "owned_config": str(owned_file),
               "execution_root": str(root), "process_owner": owner}
        file = root / "job.json"; self._atomic(file, job)
        state["owned_config_sha256"] = hashlib.sha256(canonical(owned)).hexdigest()
        state["job_sha256"] = hashlib.sha256(canonical(job)).hexdigest()
        state["process_owner"] = owner
        self._atomic(root / "agent-state.json", state)
        return file

    def _default_cleanup(self, file):
        from cdo_runtime_owned import cleanup_owned
        return cleanup_owned(file)

    def _process_binding(self, state):
        value = {key: state[key] for key in ("job_sha256", "owned_config_sha256", "process_owner",
                                             "coordinator_pid", "coordinator_reaped")}
        if not state["launch_attempted"]:
            value["never_started"] = True
        return value

    def _cleanup_unknown(self, root, state, reason):
        state.update(state="UNKNOWN", outcome="UNKNOWN", invalid_reason=state["invalid_reason"] or reason,
                     cleanup_proven=False, cleanup_evidence=None)
        if state["reconciliation_evidence"] is not None:
            state["reconciliation_evidence"] = {**state["reconciliation_evidence"], "no_unknown": False}
        self._atomic(root / "agent-state.json", state)
        raise RuntimeUnknown(reason)

    def _wait_coordinator(self, root, state):
        if not state["launch_attempted"] or state["coordinator_reaped"]:
            return
        process = self._processes.get(state["binding"]["execution_id"])
        if process is None:
            # Restart recovery uses the exact durable ledger and original
            # complete observer. Missing handles alone establish no absence.
            return
        if type(getattr(process, "pid", None)) is not int or process.pid != state["coordinator_pid"]:
            self._cleanup_unknown(root, state, "COORDINATOR_PROCESS_IDENTITY_UNKNOWN")
        from cdo_runtime_coordinator import COORDINATOR_CLEANUP_WAIT_SECONDS
        try:
            code = process.wait(timeout=COORDINATOR_CLEANUP_WAIT_SECONDS)
            if type(code) is not int:
                raise RuntimeUnknown("COORDINATOR_EXIT_UNPROVEN")
        except (KeyboardInterrupt, SystemExit):
            self._cleanup_unknown(root, state, "CLEANUP_OBSERVER_INTERRUPTED")
        except Exception as error:
            reason = error.code if isinstance(error, ControllerDenied) else "COORDINATOR_EXIT_UNPROVEN"
            self._cleanup_unknown(root, state, reason)
        # Only this exact live child wait may establish reaping. Stored PID
        # recovery never signals or fabricates a reap result.
        state.update(coordinator_reaped=True, exit_code=code)
        self._atomic(root / "agent-state.json", state)

    @staticmethod
    def _valid_cleanup_evidence(state, value):
        fields = {"version", "execution_id", "attempt", "job_sha256", "owned_config_sha256", "process_owner",
                  "coordinator_pid", "coordinator_reaped", "coordinator_absence_observed", "owned_oci_absent",
                  "coordinator_absent", "node_groups_absent", "private_storage_absent", "no_unknown", "teardown_sha256"}
        if not isinstance(value, dict) or set(value) != fields or type(value["version"]) is not int or value["version"] != 1:
            return False
        if any(value[key] != state["binding"][key] for key in ("execution_id", "attempt")):
            return False
        if any(value[key] != state[key] for key in ("job_sha256", "owned_config_sha256", "process_owner", "coordinator_pid", "coordinator_reaped")):
            return False
        if (any(value[key] is not True for key in ("coordinator_absence_observed", "owned_oci_absent", "coordinator_absent",
                                                   "node_groups_absent", "private_storage_absent"))
                or type(value["no_unknown"]) is not bool
                or (value["teardown_sha256"] is not None and not finite._hex(value["teardown_sha256"]))):
            return False
        return not state["launch_attempted"] or value["teardown_sha256"] is not None

    def _cleanup(self, root, state):
        """Admit cleanup only after the complete, durable ownership proof."""
        try:
            file = root / "owned-config.json"
            job_file = root / "job.json"
            has_config = os.path.lexists(file)
            has_job = os.path.lexists(job_file)
            if ((not has_config and (state["owned_config_sha256"] is not None or state["launch_attempted"]))
                    or (has_config and hashlib.sha256(canonical(self._read(file))).hexdigest() != state["owned_config_sha256"])):
                raise RuntimeUnknown("OWNED_CONFIGURATION_UNKNOWN")
            if ((not has_job and (state["job_sha256"] is not None or state["launch_attempted"]))
                    or (has_job and (hashlib.sha256(canonical(self._read(job_file))).hexdigest() != state["job_sha256"]
                                    or self._read(job_file).get("process_owner") != state["process_owner"]))
                    or has_config != has_job):
                raise RuntimeUnknown("AGENT_JOB_IDENTITY_UNKNOWN")
            self._wait_coordinator(root, state)
            expected = self._process_binding(state)
            if state["launch_attempted"] and state["coordinator_reaped"]:
                self.teardown(job_file, expected)
            if has_config:
                result = self.cleanup(file)
                if (not isinstance(result, dict) or result.get("resources_absent") is not True
                        or result.get("status") != "CLEANED"):
                    raise RuntimeUnknown("AGENT_CLEANUP_UNKNOWN")
            if has_job:
                proof = self.host_cleanup(job_file, expected, oci_absent=True)
                if (not isinstance(proof, dict) or set(proof) != {"coordinator_absent", "node_groups_absent", "private_storage_absent", "no_unknown"}
                        or any(value is not True for value in proof.values())):
                    raise RuntimeUnknown("HOST_CLEANUP_UNPROVEN")
                private_root = root / ("private-execution-" + state["process_owner"]["owner_nonce"])
                if os.path.lexists(private_root):
                    raise RuntimeUnknown("COORDINATOR_PRIVATE_STORAGE_UNKNOWN")
            else:
                # This MACed record never admitted a job or attempted a launch.
                # File absence is checked only in conjunction with that state.
                if (state["launch_attempted"] or state["process_owner"] is not None or state["coordinator_pid"] is not None
                        or any(os.path.lexists(root / name) for name in ("host-process-owner.json", "host-process-admission.json", "host-teardown.json", "host-node-outcome.json"))
                        or any(p.name.startswith("private-execution-") for p in root.iterdir())):
                    raise RuntimeUnknown("HOST_NEVER_ADMITTED_PROOF_CHANGED")
            receipt = root / "host-teardown.json"
            if state["launch_attempted"] and not os.path.lexists(receipt):
                raise RuntimeUnknown("HOST_TEARDOWN_MISSING")
            receipt_sha = hashlib.sha256(canonical(self._read(receipt))).hexdigest() if os.path.lexists(receipt) else None
            state["coordinator_absence_observed"] = True
            evidence = {"version": 1, "execution_id": state["binding"]["execution_id"], "attempt": state["binding"]["attempt"],
                        **{key: state[key] for key in ("job_sha256", "owned_config_sha256", "process_owner", "coordinator_pid", "coordinator_reaped")},
                        "coordinator_absence_observed": True, "owned_oci_absent": True, "coordinator_absent": True,
                        "node_groups_absent": True, "private_storage_absent": True, "no_unknown": state["state"] != "UNKNOWN" and state["outcome"] != "UNKNOWN",
                        "teardown_sha256": receipt_sha}
            state["reconciliation_evidence"] = evidence
            if evidence["no_unknown"] is not True:
                self._cleanup_unknown(root, state, "AGENT_CLEANUP_UNKNOWN")
            state.update(cleanup_proven=True, cleanup_evidence=evidence)
            self._atomic(root / "agent-state.json", state)
        except (KeyboardInterrupt, SystemExit):
            self._cleanup_unknown(root, state, "CLEANUP_OBSERVER_INTERRUPTED")
        except Exception as error:
            reason = str(error) if error.__class__.__name__ == "CoordinatorDenied" else (error.code if isinstance(error, ControllerDenied) else "HOST_CLEANUP_UNKNOWN")
            self._cleanup_unknown(root, state, reason)

    def _append_event(self, state, event):
        data = canonical(event)
        if len(state["events"]) >= MAX_EVENTS or state["event_bytes"] + len(data) > MAX_EVENTS_BYTES:
            raise RuntimeUnknown("AGENT_OUTPUT_LIMIT")
        state["events"].append(event); state["event_bytes"] += len(data)

    def _monitor(self, execution_id, process):
        root = self._root(execution_id)
        failure = None
        try:
            while True:
                line = process.stdout.readline(MAX_EVENTS_BYTES + 1)
                if not line:
                    break
                if len(line) > MAX_EVENTS_BYTES:
                    raise RuntimeUnknown("AGENT_OUTPUT_LIMIT")
                event = json.loads(line)
                fd = self._lock(root)
                try:
                    state = self._load(root)
                    if state["invalid_reason"] or state["boot_id"] != self.boot_id:
                        continue
                    if not isinstance(event, dict) or event.get("type") not in {"thread_started", "result"}:
                        raise RuntimeUnknown("STALE_AGENT_EVENT")
                    if event["type"] == "result":
                        threads = [v["thread_id"] for v in state["events"] if v["type"] == "thread_started"]
                        if len(threads) != 1:
                            raise RuntimeUnknown("AGENT_RESULT_SEQUENCE_DENIED")
                        try:
                            backend.validate_result(event, state["binding"], threads[0])
                        except backend.BackendDenied:
                            raise RuntimeUnknown("STALE_AGENT_EVENT") from None
                        if state["pending_result"] is not None:
                            raise RuntimeUnknown("DUPLICATE_AGENT_RESULT")
                        state["pending_result"] = event
                    else:
                        template = self.templates[state["binding"]["workpackage_id"]]
                        if (set(event) != {"type", "thread_id"} or not isinstance(event["thread_id"], str)
                                or not 0 < len(event["thread_id"]) <= 255 or "\0" in event["thread_id"]
                                or any(v["type"] == "thread_started" for v in state["events"])
                                or template.request.get("thread_id", event["thread_id"]) != event["thread_id"]):
                            raise RuntimeUnknown("AGENT_THREAD_DENIED")
                        self._append_event(state, event)
                    self._atomic(root / "agent-state.json", state)
                finally:
                    os.close(fd)
            code = None
        except Exception as error:
            code = None
            failure = error.code if isinstance(error, ControllerDenied) else "AGENT_PROTOCOL_UNKNOWN"
        fd = self._lock(root)
        try:
            state = self._load(root)
            if state["invalid_reason"] or state["boot_id"] != self.boot_id:
                return
            if failure is not None:
                self._poison(root, state, failure)
            self._wait_coordinator(root, state)
            code = state["exit_code"]
            if failure or code != 0 or state["pending_result"] is None or self.clock() >= state["lease_expires_at"]:
                cause = None
                if state["coordinator_reaped"]:
                    receipt = self.teardown(root / "job.json", self._process_binding(state))
                    if isinstance(receipt, dict):
                        cause = receipt.get("terminal_cause")
                self._poison(root, state, failure or cause or "AGENT_PROCESS_FAILED")
                state["outcome"] = "FAILED"
                self._cleanup(root, state)
                state["state"] = "FAILED"
            else:
                self._cleanup(root, state)
                if state["invalid_reason"]:
                    return
                if (self.clock() >= state["lease_expires_at"]
                        or self.clock() >= state["binding"]["execution_expires_at"]):
                    self._poison(root, state, "EXECUTION_LEASE_EXPIRED")
                else:
                    from cdo_broker import publication_lock, read_publication_journal
                    with publication_lock(root):
                        if (self.clock() >= state["lease_expires_at"]
                                or self.clock() >= state["binding"]["execution_expires_at"]):
                            raise RuntimeUnknown("EXECUTION_LEASE_EXPIRED")
                        journal, identity = self._publication_journal(root, state)
                        value = read_publication_journal(journal) if journal is not None else None
                        if ((value is not None and (value["status"] != "COMPLETE" or any(value.get(k) != v for k, v in identity.items())))
                                or (value is None and state["binding"]["role"] != "reviewer")):
                            raise RuntimeUnknown("PUBLICATION_UNPROVEN")
                        self._append_event(state, state["pending_result"])
                        state.update(state="SUCCEEDED", outcome="SUCCEEDED", pending_result=None)
                        self._atomic(root / "agent-state.json", state)
                        self._lease(root, state, active=False)
            self._atomic(root / "agent-state.json", state)
        except (Exception, KeyboardInterrupt, SystemExit) as error:
            reason = error.code if isinstance(error, ControllerDenied) else "AGENT_MONITOR_UNKNOWN"
            if reason != "EXECUTION_LEASE_EXPIRED":
                self._health = "AGENT_MONITOR_UNKNOWN"
            if "state" in locals():
                self._poison(root, state, reason)
                if reason != "EXECUTION_LEASE_EXPIRED":
                    state.update(state="UNKNOWN", outcome="UNKNOWN", cleanup_proven=False, cleanup_evidence=None)
                    if state["reconciliation_evidence"] is not None:
                        state["reconciliation_evidence"] = {**state["reconciliation_evidence"], "no_unknown": False}
                self._atomic(root / "agent-state.json", state)
        finally:
            os.close(fd)

    def _result(self, state, operation, cursor):
        if operation == "diagnostics":
            events = state["events"][cursor:cursor + 16]
            return {"events": events, "next_cursor": cursor + len(events), "state": state["state"]}
        return {"state": state["state"], "exit_code": state["exit_code"],
                "invalid_reason": state["invalid_reason"], "cleanup_proven": state["cleanup_proven"],
                "cleanup": state["cleanup_proven"],
                "cleanup_evidence": state["cleanup_evidence"],
                "reconciliation_evidence": state["reconciliation_evidence"],
                "outcome": state["outcome"], "attestation_verified": True,
                "attestation_sha256": self.config.attestation_sha256}

    def _operation_failure(self, root, state, reason, admission_started=False):
        self._poison(root, state, reason)
        state.update(state="UNKNOWN", outcome="UNKNOWN", cleanup_proven=False, cleanup_evidence=None)
        if state["reconciliation_evidence"] is not None:
            state["reconciliation_evidence"] = {**state["reconciliation_evidence"], "no_unknown": False}
        self._atomic(root / "agent-state.json", state)
        if admission_started:
            # A successfully ACKed producer can own Node/OCI/storage even if
            # monitor admission fails. This handler retains teardown ownership
            # and attempts the full protocol after revoking its lease. UNKNOWN
            # remains irreversible even when physical absence is established.
            try:
                self._cleanup(root, state)
            except ControllerDenied:
                if state["reconciliation_evidence"] is None:
                    self._health = "AGENT_ADMISSION_CLEANUP_UNKNOWN"

    def handle(self, request):
        template, _ = self._authenticate(request)
        attestation = self._attest()
        execution_id = request["execution_id"]
        root = self._root(execution_id)
        transition = self._lock(self.state_root)
        try:
            if request["operation"] == "inspect" and not os.path.lexists(root):
                return self._observer(request, attestation)
            if not os.path.lexists(root):
                if sum(finite._uuid(p.name) for p in self.state_root.iterdir()) >= MAX_RECORDS:
                    raise ControllerDenied("AGENT_EXECUTION_COUNT_EXCEEDED")
                root.mkdir(mode=0o700); self._fsync(self.state_root)
                # The directory is the bounded reservation. Bind it durably
                # before releasing the same lock that made the capacity check.
                self._load(root, request)
        finally:
            os.close(transition)
        fd = self._lock(root)
        admission_started = False
        try:
            self._authenticate(request)
            state = self._load(root, request)
            self._consume(root, request, state)
            operation = request["operation"]
            if operation in {"cancel", "cleanup"}:
                self._claim_attempt(request)
                if state["outcome"] != "SUCCEEDED":
                    self._poison(root, state, "EXECUTION_CANCELLED" if operation == "cancel" else "EXECUTION_CLEANUP")
                self._cleanup(root, state)
                if operation == "cleanup":
                    state["state"] = "CLEANED"
                self._atomic(root / "agent-state.json", state)
            elif operation == "create":
                if state["state"] != "UNCREATED" or state["invalid_reason"]:
                    raise ControllerDenied("AGENT_EXECUTION_ALREADY_USED")
                if self._health or self._watchdog is None or not self._watchdog.is_alive():
                    raise RuntimeUnknown("AGENT_WATCHDOG_UNKNOWN")
                # One persisted attempt cannot be rebound to a second UUID.
                state.update(state="CREATED", lease_expires_at=min(self.clock() + self.lease_seconds, request["execution_expires_at"]))
                self._claim_attempt(request, creating=state)
                self._lease(root, state)
                self._job(template, root, state)
            elif operation == "start":
                if state["state"] != "CREATED" or state["invalid_reason"] or state["boot_id"] != self.boot_id:
                    raise ControllerDenied("AGENT_START_DENIED")
                if self._health or self._watchdog is None or not self._watchdog.is_alive() or self.clock() >= state["lease_expires_at"]:
                    raise RuntimeUnknown("AGENT_LEASE_UNKNOWN")
                if (state["job_sha256"] is None or hashlib.sha256(canonical(self._read(root / "job.json"))).hexdigest() != state["job_sha256"]
                        or state["owned_config_sha256"] is None or hashlib.sha256(canonical(self._read(root / "owned-config.json"))).hexdigest() != state["owned_config_sha256"]):
                    raise RuntimeUnknown("AGENT_JOB_IDENTITY_CHANGED")
                state.update(state="RUNNING", launch_attempted=True); self._atomic(root / "agent-state.json", state)
                admission_started = True
                process = self.process_factory([*self.coordinator_command, str(root / "job.json")],
                    stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                    env={"PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": "/var/empty"}, start_new_session=True)
                self._processes[execution_id] = process
                if type(getattr(process, "pid", None)) is not int or process.pid < 1:
                    raise RuntimeUnknown("COORDINATOR_PROCESS_IDENTITY_UNKNOWN")
                # Persist the child authority before its admission ACK. A
                # coordinator cannot launch Node until the host helper ACKs.
                state["coordinator_pid"] = process.pid
                self._atomic(root / "agent-state.json", state)
                expected = self._process_binding(state)
                from cdo_runtime_coordinator import CoordinatorDenied
                try:
                    registration = self.registration(root / "job.json", process)
                except CoordinatorDenied as error:
                    raise RuntimeUnknown(str(error)) from None
                except (KeyboardInterrupt, SystemExit):
                    raise RuntimeUnknown("CLEANUP_OBSERVER_INTERRUPTED") from None
                if registration != expected:
                    raise RuntimeUnknown("COORDINATOR_REGISTRATION_IDENTITY_UNKNOWN")
                thread = threading.Thread(target=self._monitor, args=(execution_id, process), daemon=True)
                self._threads[execution_id] = thread; thread.start()
            elif operation == "inspect" and state["state"] in {"CREATED", "RUNNING"}:
                if state["invalid_reason"] or state["boot_id"] != self.boot_id or self.clock() >= state["lease_expires_at"]:
                    self._poison(root, state, "EXECUTION_LEASE_EXPIRED")
                    self._cleanup(root, state)
                else:
                    state["lease_expires_at"] = min(self.clock() + self.lease_seconds, request["execution_expires_at"])
                    self._lease(root, state); self._atomic(root / "agent-state.json", state)
            result = self._result(state, operation, request["cursor"])
            if operation == "inspect" and state["state"] == "UNCREATED":
                result["attestation"], result["controller_configuration"] = attestation
            return result
        except RuntimeUnknown as error:
            if "state" in locals():
                self._operation_failure(root, state, error.code, admission_started)
            raise
        except (OSError, ValueError, TypeError, RecursionError):
            if "state" in locals():
                self._operation_failure(root, state, "AGENT_STATE_UNKNOWN", admission_started)
            raise RuntimeUnknown("AGENT_STATE_UNKNOWN") from None
        except (KeyboardInterrupt, SystemExit):
            if "state" in locals():
                self._operation_failure(root, state, "CLEANUP_OBSERVER_INTERRUPTED", admission_started)
            raise RuntimeUnknown("CLEANUP_OBSERVER_INTERRUPTED") from None
        except Exception:
            if admission_started and "state" in locals():
                self._operation_failure(root, state, "AGENT_ADMISSION_UNKNOWN", True)
                raise RuntimeUnknown("AGENT_ADMISSION_UNKNOWN") from None
            raise
        finally:
            os.close(fd)

    def response(self, request):
        _, secret = self._authenticate(request)
        value = {key: request[key] for key in BINDINGS}
        value["nonce"] = request["nonce"]
        try:
            value.update(ok=True, result=self.handle(request))
        except ControllerDenied as error:
            value.update(ok=False, error=error.code)
        value["signature"] = response_signature(secret, value)
        return value

    def startup_reconcile(self):
        """Old boot attempts are invalidated; recovery never resumes execution."""
        results = []
        for root in self.state_root.iterdir():
            if not finite._uuid(root.name):
                continue
            if self._recover_unused_reservation(root):
                continue
            fd = self._lock(root)
            try:
                state = self._load(root)
                if state["state"] in {"UNCREATED", "CLEANED"}:
                    continue
                if state["boot_id"] != self.boot_id:
                    self._poison(root, state, "CONTROLLER_RESTARTED")
                    self._cleanup(root, state)
                    results.append(self._result(state, "inspect", 0))
            except Exception:
                self._health = "AGENT_RECONCILIATION_UNKNOWN"
                results.append({"state": "UNKNOWN", "execution_id": root.name})
            finally:
                os.close(fd)
        return results

    def _recover_unused_reservation(self, root):
        """Only an unconsumed, unlaunched reservation can release capacity."""
        transition = self._lock(self.state_root)
        fd = None
        try:
            if not os.path.lexists(root):
                return True
            # Do not wait for an execution while holding the transition lock.
            # A live handler uses the reverse order to consume its nonce.
            info = root.lstat()
            if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o077:
                return False
            lock = root / "lock"
            fd = os.open(lock, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
            value = os.fstat(fd)
            if (not stat.S_ISREG(value.st_mode) or value.st_nlink != 1
                    or value.st_uid != os.geteuid() or value.st_mode & 0o077):
                return False
            try:
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                return False
            if {p.name for p in root.iterdir()} - {"lock", "agent-state.json"}:
                return False
            file = root / "agent-state.json"
            if os.path.lexists(file):
                state = self._load(root)
                if (state["state"] != "UNCREATED" or state["nonces"] != 0 or state["launch_attempted"]
                        or state["owned_config_sha256"] is not None or state["job_sha256"] is not None
                        or state["events"] or state["pending_result"] is not None
                        or state["invalid_reason"] is not None or state["outcome"] is not None):
                    return False
                file.unlink()
            lock.unlink(); root.rmdir(); self._fsync(self.state_root)
            return True
        except (ControllerDenied, OSError):
            return False
        finally:
            if fd is not None:
                os.close(fd)
            os.close(transition)

    def start_watchdog(self):
        if self._watchdog is not None:
            raise ControllerDenied("AGENT_WATCHDOG_ALREADY_STARTED")
        def watch():
            while not self._stopping.wait(.1):
                transition = self._lock(self.state_root)
                try:
                    roots = tuple(self.state_root.iterdir())
                finally:
                    os.close(transition)
                for root in roots:
                    if not finite._uuid(root.name):
                        continue
                    try:
                        fd = self._lock(root)
                        try:
                            state = self._load(root)
                            if state["state"] in {"CREATED", "RUNNING"} and self.clock() >= state["lease_expires_at"]:
                                self._poison(root, state, "EXECUTION_LEASE_EXPIRED")
                                self._cleanup(root, state)
                        finally:
                            os.close(fd)
                    except Exception:
                        self._health = "AGENT_WATCHDOG_UNKNOWN"
        self._watchdog = threading.Thread(target=watch, daemon=True, name="cdo-agent-lease")
        self._watchdog.start()

    def close(self, cleanup_active=True):
        self._stopping.set()
        if self._watchdog:
            self._watchdog.join(timeout=3)
        if cleanup_active:
            for root in self.state_root.iterdir():
                if not finite._uuid(root.name):
                    continue
                fd = self._lock(root)
                try:
                    state = self._load(root)
                    if state["state"] in {"CREATED", "RUNNING"}:
                        self._poison(root, state, "CONTROLLER_CLOSED")
                        self._cleanup(root, state)
                except ControllerDenied:
                    self._health = "AGENT_SHUTDOWN_UNKNOWN"
                finally:
                    os.close(fd)
