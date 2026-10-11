#!/usr/bin/env python3
"""Trusted backend AgentRunner client; it has no local execution fallback.

The readonly credential file is installed by the trusted orchestrator. Requests
never supply a controller destination, source tree, OCI arguments or a command.
The host already holds the approved immutable request template. Only lifecycle
identity, hashes and authenticated bounded diagnostics cross this channel.
"""
from __future__ import annotations

import datetime
import hashlib
import hmac
import http.client
import json
import math
import os
import pathlib
import re
import signal
import socket
import stat
import sys
import threading
import time
import urllib.parse
import uuid

MAX_INPUT_BYTES = 1048576
MAX_RESULT_BYTES = 524288
MAX_OUTPUT_BYTES = 1048576
MAX_CREDENTIAL_BYTES = 16384
MAX_RESPONSE_BYTES = 1048576
MAX_REQUEST_BYTES = 16384
MAX_REQUEST_SECONDS = 30
MAX_EXECUTION_SECONDS = 1200
REQUEST_SECONDS = 10
ENDPOINT = "/v1/agent-executions"
OPERATIONS = ("create", "start", "inspect", "diagnostics", "cancel", "cleanup")
REQUEST_FIELDS = {
    "version", "operation", "execution_id", "attempt", "runtime_context",
    "execution_expires_at", "request_expires_at", "nonce", "signature", "cursor",
    "controller_id", "protocol_sha256", "attestation_sha256", "workpackage_id",
    "release_id", "role", "image_id", "scope_sha256", "resource_profile", "request_sha256",
}
CREDENTIAL_FIELDS = {"version", "url", "controller_id", "protocol_sha256",
                     "attestation_sha256", "template", "secret_hex"}
TEMPLATE_FIELDS = {"workpackage_id", "release_id", "role", "image_id", "scope_sha256",
                   "resource_profile", "request_sha256", "execution_expiry_max_seconds"}
RUNNER_FIELDS = {"execution_id", "execution_deadline", "attempt", "runtime_context", "role",
                 "thread_id", "working_directory", "model", "reasoning_effort", "prompt",
                 "output_schema", "sandbox_scope"}
CONTEXT_FIELDS = {"workflow_id", "workflow_run_id", "activity_id", "activity_attempt"}
PROTOCOL_DESCRIPTOR = {
    "version": 1, "endpoint": ENDPOINT, "operations": list(OPERATIONS),
    "request_fields": sorted(REQUEST_FIELDS), "request_limit": MAX_REQUEST_BYTES,
    "response_limit": MAX_RESPONSE_BYTES, "execution_limit_seconds": MAX_EXECUTION_SECONDS,
    "request_mac_domain": "cdo-agent-request-v1", "response_mac_domain": "cdo-agent-response-v1",
    "execution_key_domain": "cdo-agent-execution-v1",
    "request_template_dynamic_fields": ["attempt", "execution_deadline", "execution_id", "runtime_context"],
    "lease_renewal": "authenticated-inspect-only", "publication": "trusted-host-only",
}


class BackendDenied(RuntimeError):
    pass


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()


def protocol_sha256():
    return hashlib.sha256(canonical(PROTOCOL_DESCRIPTOR)).hexdigest()


def _object(pairs):
    result = {}
    for key, item in pairs:
        if key in result:
            raise ValueError("duplicate JSON field")
        result[key] = item
    return result


def decode(value):
    return json.loads(value, object_pairs_hook=_object,
                      parse_constant=lambda _: (_ for _ in ()).throw(ValueError("nonfinite JSON")))


def _hex(value):
    return isinstance(value, str) and re.fullmatch(r"[a-f0-9]{64}", value) is not None


def _uuid(value):
    try:
        return isinstance(value, str) and str(uuid.UUID(value)) == value
    except (ValueError, AttributeError):
        return False


def validate_context(value):
    if value == {}:
        return {}
    if not isinstance(value, dict) or set(value) != CONTEXT_FIELDS:
        raise BackendDenied("RUNTIME_CONTEXT_DENIED")
    for name in ("workflow_id", "workflow_run_id", "activity_id"):
        if not isinstance(value[name], str) or not 0 < len(value[name].encode()) <= 1024 or any(c in value[name] for c in "\0\r\n"):
            raise BackendDenied("RUNTIME_CONTEXT_DENIED")
    if type(value["activity_attempt"]) is not int or not 1 <= value["activity_attempt"] <= 100:
        raise BackendDenied("RUNTIME_CONTEXT_DENIED")
    return dict(value)


def normalize_request(request):
    """Hash all approved AgentRunRequest data; only invocation bindings vary."""
    if not isinstance(request, dict) or set(request) - RUNNER_FIELDS:
        raise BackendDenied("RUNTIME_REQUEST_FIELDS_DENIED")
    if request.get("role") not in {"coder", "reviewer"}:
        raise BackendDenied("RUNTIME_ROLE_DENIED")
    for name in ("working_directory", "prompt"):
        if not isinstance(request.get(name), str) or not request[name] or "\0" in request[name]:
            raise BackendDenied("RUNTIME_REQUEST_DENIED")
    if not isinstance(request.get("output_schema"), dict):
        raise BackendDenied("RUNTIME_REQUEST_DENIED")
    for name in ("thread_id", "model", "reasoning_effort"):
        if name in request and (not isinstance(request[name], str) or not request[name] or "\0" in request[name]):
            raise BackendDenied("RUNTIME_REQUEST_DENIED")
    scope = request.get("sandbox_scope")
    if not isinstance(scope, dict) or set(scope) != {"profile", "write_paths"}:
        raise BackendDenied("RUNTIME_SCOPE_DENIED")
    if scope["profile"] not in {"contract", "layer", "composition", "reviewer"} or (request["role"] == "reviewer") != (scope["profile"] == "reviewer"):
        raise BackendDenied("RUNTIME_SCOPE_DENIED")
    paths = scope["write_paths"]
    if not isinstance(paths, list) or len(paths) > 256 or any(not isinstance(path, str) for path in paths) or len(set(paths)) != len(paths):
        raise BackendDenied("RUNTIME_SCOPE_DENIED")
    for path in paths:
        if not isinstance(path, str) or not path or len(path.encode()) > 4096 or path.startswith("/") or "\\" in path or any(p in {"", ".", ".."} for p in path.split("/")):
            raise BackendDenied("RUNTIME_SCOPE_DENIED")
    if scope["profile"] == "reviewer" and paths:
        raise BackendDenied("RUNTIME_SCOPE_DENIED")
    if "runtime_context" in request:
        validate_context(request["runtime_context"])
    result = {name: value for name, value in request.items()
              if name not in {"execution_id", "execution_deadline", "attempt", "runtime_context"}}
    try:
        if len(canonical(request)) > MAX_INPUT_BYTES:
            raise BackendDenied("RUNTIME_INPUT_LIMIT")
    except (ValueError, TypeError, RecursionError):
        raise BackendDenied("RUNTIME_REQUEST_DENIED") from None
    return result


def request_digest(request):
    return hashlib.sha256(canonical(normalize_request(request))).hexdigest()


def request_signature(secret, value):
    return hmac.new(secret, b"cdo-agent-request-v1\0" + canonical(value), hashlib.sha256).hexdigest()


def response_signature(secret, value):
    return hmac.new(secret, b"cdo-agent-response-v1\0" + canonical(value), hashlib.sha256).hexdigest()


def execution_secret(template_secret, request):
    binding = {"execution_id": request["execution_id"], "attempt": request["attempt"],
               "runtime_context": validate_context(request.get("runtime_context", {}))}
    return hmac.new(template_secret, b"cdo-agent-execution-v1\0" + canonical(binding), hashlib.sha256).digest()


def request_secret(template_secret, request):
    return template_secret if request["operation"] == "create" else execution_secret(template_secret, request)


def response_binding(request):
    return {key: item for key, item in request.items()
            if key not in {"signature", "operation", "request_expires_at", "cursor", "version"}}


def agent_signed_request(credentials, request, operation, cursor=0, now=None, nonce=None):
    if operation not in OPERATIONS or type(cursor) is not int or cursor < 0 or operation != "diagnostics" and cursor != 0:
        raise BackendDenied("RUNTIME_OPERATION_DENIED")
    now = int(time.time()) if now is None else now
    template = credentials["template"]
    value = {key: template[key] for key in TEMPLATE_FIELDS if key != "execution_expiry_max_seconds"}
    value.update(version=1, operation=operation, execution_id=request["execution_id"],
                 attempt=request["attempt"], runtime_context=validate_context(request.get("runtime_context", {})),
                 execution_expires_at=execution_expiry(request), request_expires_at=now + MAX_REQUEST_SECONDS,
                 nonce=str(uuid.uuid4()) if nonce is None else nonce, cursor=cursor,
                 controller_id=credentials["controller_id"], protocol_sha256=credentials["protocol_sha256"],
                 attestation_sha256=credentials["attestation_sha256"])
    value["signature"] = request_signature(request_secret(bytes.fromhex(credentials["secret_hex"]), value), value)
    return value


def execution_expiry(request):
    try:
        deadline = request["execution_deadline"]
        if not isinstance(deadline, str) or not deadline.endswith("Z"):
            raise ValueError()
        stamp = datetime.datetime.fromisoformat(deadline[:-1] + "+00:00").timestamp()
        if not math.isfinite(stamp):
            raise ValueError()
        return math.floor(stamp)
    except (KeyError, TypeError, ValueError, OverflowError):
        raise BackendDenied("RUNTIME_DEADLINE_DENIED") from None


def load_credentials(path):
    if not isinstance(path, str) or not path or not pathlib.Path(path).is_absolute():
        raise BackendDenied("RUNTIME_CREDENTIALS_DENIED")
    fd = None
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_mode & 0o077 or info.st_mode & 0o200 or info.st_size > MAX_CREDENTIAL_BYTES:
            raise BackendDenied("RUNTIME_CREDENTIALS_DENIED")
        with os.fdopen(fd, "rb") as source:
            fd = None
            raw = source.read(MAX_CREDENTIAL_BYTES + 1)
        value = decode(raw)
        if not isinstance(value, dict) or set(value) != CREDENTIAL_FIELDS or type(value["version"]) is not int or value["version"] != 1:
            raise BackendDenied("RUNTIME_CREDENTIALS_DENIED")
        for name in ("controller_id", "protocol_sha256", "attestation_sha256", "secret_hex"):
            if not _hex(value[name]):
                raise BackendDenied("RUNTIME_CREDENTIALS_DENIED")
        if value["protocol_sha256"] != protocol_sha256():
            raise BackendDenied("RUNTIME_PROTOCOL_MISMATCH")
        template = value["template"]
        if not isinstance(template, dict) or set(template) != TEMPLATE_FIELDS or not _uuid(template["workpackage_id"]):
            raise BackendDenied("RUNTIME_CREDENTIALS_DENIED")
        for name in ("release_id", "scope_sha256", "request_sha256"):
            if not _hex(template[name]):
                raise BackendDenied("RUNTIME_CREDENTIALS_DENIED")
        if not isinstance(template["image_id"], str) or not re.fullmatch(r"sha256:[a-f0-9]{64}", template["image_id"]) or template["role"] not in {"contract", "layer", "composition", "reviewer"}:
            raise BackendDenied("RUNTIME_CREDENTIALS_DENIED")
        if not isinstance(template["resource_profile"], str) or not 0 < len(template["resource_profile"]) <= 128:
            raise BackendDenied("RUNTIME_CREDENTIALS_DENIED")
        seconds = template["execution_expiry_max_seconds"]
        if type(seconds) is not int or not 1 <= seconds <= MAX_EXECUTION_SECONDS:
            raise BackendDenied("RUNTIME_CREDENTIALS_DENIED")
        endpoint(value["url"])
        return value
    except (OSError, ValueError, TypeError, RecursionError, KeyError):
        raise BackendDenied("RUNTIME_CREDENTIALS_DENIED") from None
    finally:
        if fd is not None:
            os.close(fd)


def endpoint(url):
    try:
        value = urllib.parse.urlsplit(url)
        if value.scheme != "http" or value.hostname not in {"127.0.0.1", "host.docker.internal"} or value.username or value.password or value.path not in {"", "/"} or value.query or value.fragment or not value.port:
            raise ValueError()
        return value.hostname, value.port
    except (ValueError, TypeError):
        raise BackendDenied("RUNTIME_ENDPOINT_DENIED") from None


class AgentRuntimeClient:
    def __init__(self, credentials, request):
        normalize_request(request)
        if not _uuid(request.get("execution_id")) or type(request.get("attempt")) is not int or not 1 <= request["attempt"] <= 100:
            raise BackendDenied("RUNTIME_EXECUTION_IDENTITY_DENIED")
        if request_digest(request) != credentials["template"]["request_sha256"] or request["sandbox_scope"]["profile"] != credentials["template"]["role"]:
            raise BackendDenied("RUNTIME_APPROVED_REQUEST_MISMATCH")
        expiry = execution_expiry(request)
        if not int(time.time()) < expiry <= int(time.time()) + credentials["template"]["execution_expiry_max_seconds"]:
            raise BackendDenied("RUNTIME_DEADLINE_DENIED")
        self.credentials, self.request = credentials, request
        self.host, self.port = endpoint(credentials["url"])

    def call(self, operation, cursor=0):
        request = agent_signed_request(self.credentials, self.request, operation, cursor)
        encoded = canonical(request)
        if len(encoded) > MAX_REQUEST_BYTES:
            raise BackendDenied("RUNTIME_REQUEST_LIMIT")
        connection = None
        timer = None
        try:
            connection = http.client.HTTPConnection(self.host, self.port, timeout=REQUEST_SECONDS)
            connection.connect()
            connected_socket = connection.sock

            def expire():
                try:
                    connected_socket.shutdown(socket.SHUT_RDWR)
                except (OSError, AttributeError):
                    pass

            # Preserve the actual socket reference: HTTP/1.0 may detach it from
            # HTTPConnection while response.read still blocks on a slow peer.
            timer = threading.Timer(REQUEST_SECONDS, expire)
            timer.daemon = True
            timer.start()
            connection.request("POST", ENDPOINT, encoded, {"Content-Type": "application/json"})
            response = connection.getresponse()
            if response.status != 200 or response.getheader("Content-Type") != "application/json":
                raise BackendDenied("RUNTIME_TRANSPORT_UNKNOWN")
            if response.getheader("Transfer-Encoding") or len(response.headers.get_all("Content-Length") or []) != 1:
                raise BackendDenied("RUNTIME_RESPONSE_DENIED")
            body = response.read(MAX_RESPONSE_BYTES + 1)
            if len(body) > MAX_RESPONSE_BYTES:
                raise BackendDenied("RUNTIME_RESPONSE_LIMIT")
            value = decode(body)
            if not isinstance(value, dict):
                raise BackendDenied("RUNTIME_RESPONSE_DENIED")
            signature = value.pop("signature", None)
            secret = request_secret(bytes.fromhex(self.credentials["secret_hex"]), request)
            if not _hex(signature) or not hmac.compare_digest(signature, response_signature(secret, value)):
                raise BackendDenied("RUNTIME_RESPONSE_SIGNATURE_INVALID")
            binding = response_binding(request)
            if any(type(value.get(key)) is not type(item) or canonical(value.get(key)) != canonical(item) for key, item in binding.items()):
                raise BackendDenied("RUNTIME_RESPONSE_BINDING_INVALID")
            if value.get("ok") is not True:
                error = value.get("error")
                raise BackendDenied(error if isinstance(error, str) and re.fullmatch(r"[A-Z][A-Z0-9_]{0,127}", error) else "RUNTIME_TRANSPORT_UNKNOWN")
            result = value.get("result")
            if not isinstance(result, dict):
                raise BackendDenied("RUNTIME_RESPONSE_DENIED")
            return result
        except (OSError, http.client.HTTPException, ValueError, TypeError, RecursionError):
            raise BackendDenied("RUNTIME_TRANSPORT_UNKNOWN") from None
        finally:
            if timer is not None:
                timer.cancel()
            if connection is not None:
                connection.close()


def attestation_probe(credentials):
    """Observe the pinned active host without registering or starting execution."""
    # Schema verification is pure. Do not run an observer or import controller
    # or release admission logic into the backend's passive transport client.
    import cdo_runtime_attestation as attestation
    request = {"execution_id": str(uuid.uuid4()), "attempt": 1, "runtime_context": {},
               "execution_deadline": (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(
                   seconds=credentials["template"]["execution_expiry_max_seconds"])).isoformat().replace("+00:00", "Z")}
    client = AgentRuntimeClient.__new__(AgentRuntimeClient)
    client.credentials, client.request = credentials, request
    client.host, client.port = endpoint(credentials["url"])
    result = client.call("inspect")
    if (result.get("state") != "UNCREATED" or result.get("attestation_verified") is not True
            or result.get("invalid_reason") not in (None, "")
            or result.get("attestation_sha256") != credentials["attestation_sha256"]):
        raise BackendDenied("RUNTIME_ATTESTATION_MISMATCH")
    manifest = result.get("attestation")
    try:
        # The approved outer pin authenticates the exact observed manifest;
        # the existing strict schema also verifies its embedded unsigned hash.
        # A signed hash claim alone is never sufficient positive evidence.
        attestation.verify_attestation(manifest, manifest)
        if (not manifest["implementation"] or not manifest["node"]
                or manifest["protocol_sha256"] != credentials["protocol_sha256"]
                or manifest["docker"]["images"]["sdk"] != credentials["template"]["image_id"]
                or hashlib.sha256(canonical(manifest)).hexdigest() != credentials["attestation_sha256"]):
            raise BackendDenied("RUNTIME_ATTESTATION_MISMATCH")
        public_config = result.get("controller_configuration")
        if (type(public_config) is not dict or not public_config
                or hashlib.sha256(canonical(public_config)).hexdigest() != manifest["controller_configuration_sha256"]):
            raise BackendDenied("RUNTIME_ATTESTATION_CONFIGURATION_MISMATCH")
        attestation._public_json(public_config)
    except attestation.AttestationDenied:
        raise BackendDenied("RUNTIME_ATTESTATION_MANIFEST_DENIED") from None
    except (TypeError, ValueError, KeyError, RecursionError):
        raise BackendDenied("RUNTIME_ATTESTATION_MANIFEST_DENIED") from None
    return result


def validate_result(event, request, thread):
    if not isinstance(event, dict) or set(event) != {"type", "execution_id", "attempt", "thread_id", "result", "usage"} or event.get("type") != "result" or event.get("execution_id") != request["execution_id"] or event.get("attempt") != request["attempt"] or event.get("thread_id") != thread:
        raise BackendDenied("RUNTIME_STALE_RESULT_DENIED")
    usage = event["usage"]
    usage_fields = {"input_tokens", "cached_input_tokens", "output_tokens", "reasoning_output_tokens"}
    if not isinstance(usage, dict) or set(usage) - usage_fields or any(type(item) is not int or item < 0 for item in usage.values()) or usage.get("cached_input_tokens", 0) > usage.get("input_tokens", 0):
        raise BackendDenied("RUNTIME_RESULT_USAGE_DENIED")
    if not isinstance(event["result"], dict) or len(canonical(event["result"])) > MAX_RESULT_BYTES:
        raise BackendDenied("RUNTIME_RESULT_LIMIT")
    return event


def run_backend(credentials, request, emit, stop_requested=None, pause=time.sleep):
    client = AgentRuntimeClient(credentials, request)
    stop_requested = (lambda: False) if stop_requested is None else stop_requested
    cursor, output_bytes, thread, final = 0, 0, None, None
    created = False
    succeeded = False

    def live():
        if stop_requested():
            raise BackendDenied("RUNTIME_CANCELLED")
        if int(time.time()) >= execution_expiry(request):
            raise BackendDenied("RUNTIME_TIMED_OUT")

    def events():
        nonlocal cursor, output_bytes, thread, final
        result = client.call("diagnostics", cursor)
        values = result.get("events")
        next_cursor = result.get("next_cursor")
        if not isinstance(values, list) or len(values) > 256 or type(next_cursor) is not int or next_cursor != cursor + len(values):
            raise BackendDenied("RUNTIME_EVENT_CURSOR_DENIED")
        for event in values:
            if not isinstance(event, dict):
                raise BackendDenied("RUNTIME_EVENT_DENIED")
            output_bytes += len(canonical(event)) + 1
            if output_bytes > MAX_OUTPUT_BYTES:
                raise BackendDenied("RUNTIME_OUTPUT_LIMIT")
            if event.get("type") == "thread_started":
                if thread is not None or set(event) != {"type", "thread_id"} or not isinstance(event.get("thread_id"), str) or not 0 < len(event["thread_id"]) <= 255 or request.get("thread_id", event["thread_id"]) != event["thread_id"]:
                    raise BackendDenied("RUNTIME_THREAD_DENIED")
                thread = event["thread_id"]
                emit(event)
            elif event.get("type") == "result":
                if final is not None or thread is None:
                    raise BackendDenied("RUNTIME_RESULT_SEQUENCE_DENIED")
                final = validate_result(event, request, thread)
            else:
                raise BackendDenied("RUNTIME_EVENT_DENIED")
        cursor = next_cursor

    try:
        live()
        created = True
        client.call("create")
        live()
        client.call("start")
        while True:
            live()
            result = client.call("inspect")
            events()
            live()
            state = result.get("state")
            if result.get("invalid_reason") or state in {"FAILED", "CANCELLED", "UNKNOWN", "INVALIDATED", "TIMED_OUT"}:
                reason = result.get("invalid_reason")
                raise BackendDenied(reason if isinstance(reason, str) and re.fullmatch(r"[A-Z][A-Z0-9_]{0,127}", reason) else "RUNTIME_EXECUTION_FAILED")
            if state == "SUCCEEDED":
                if result.get("exit_code") != 0 or final is None:
                    raise BackendDenied("RUNTIME_SUCCESS_UNPROVEN")
                cleanup = client.call("cleanup")
                if cleanup.get("cleanup") is not True or cleanup.get("state") != "CLEANED" or cleanup.get("invalid_reason"):
                    raise BackendDenied("RUNTIME_CLEANUP_UNKNOWN")
                live()
                emit(final)
                succeeded = True
                return
            if state not in {"CREATED", "RUNNING", "VERIFYING"}:
                raise BackendDenied("RUNTIME_STATE_UNKNOWN")
            pause(0.25)
    finally:
        if created and not succeeded:
            # Never report a result after cancel, uncertain cleanup or disconnect.
            # A killed client is bounded by the host's durable renewable lease.
            try:
                client.call("cancel")
                client.call("cleanup")
            except BackendDenied:
                pass


def main():
    cancelled = False

    def stop(_signum, _frame):
        nonlocal cancelled
        cancelled = True

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    try:
        if os.environ.get("CDO_SANDBOX_EXECUTION_MODE") != "hardened-certification":
            raise BackendDenied("RUNTIME_TRANSPORT_NOT_CERTIFIED")
        raw = sys.stdin.buffer.read(MAX_INPUT_BYTES + 1)
        if len(raw) > MAX_INPUT_BYTES:
            raise BackendDenied("RUNTIME_INPUT_LIMIT")
        request = decode(raw)
        credentials = load_credentials(os.environ.get("CDO_RUNTIME_BACKEND_CREDENTIALS"))

        def emit(value):
            sys.stdout.buffer.write(canonical(value) + b"\n")
            sys.stdout.buffer.flush()

        run_backend(credentials, request, emit, lambda: cancelled)
        return 0
    except BackendDenied as error:
        sys.stderr.buffer.write(canonical({"type": "error", "message": str(error)}) + b"\n")
        return 1
    except Exception:
        # No prompts, credentials, source paths or transport traces in stderr.
        sys.stderr.buffer.write(canonical({"type": "error", "message": "RUNTIME_TRANSPORT_UNKNOWN"}) + b"\n")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
