import datetime
import copy
import hashlib
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import cdo_runtime_backend as backend
import cdo_runtime_attestation as attestation
import cdo_release_identity as identity


def runner_request():
    return {"role": "coder", "working_directory": "/approved/.cdo-sandbox-certification-fixture",
            "prompt": "fixture", "output_schema": {"type": "object"},
            "sandbox_scope": {"profile": "layer", "write_paths": ["internal/value.go"]},
            "execution_id": "00000000-0000-4000-8000-000000000001", "attempt": 1,
            "execution_deadline": (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(seconds=90)).isoformat().replace("+00:00", "Z")}


def credentials(request):
    return {"version": 1, "url": "http://127.0.0.1:12345", "controller_id": "1" * 64,
            "protocol_sha256": backend.protocol_sha256(), "attestation_sha256": "2" * 64,
            "secret_hex": "3" * 64, "template": {
                "workpackage_id": "00000000-0000-4000-8000-000000000002",
                "release_id": "4" * 64, "role": "layer", "image_id": "sha256:" + "5" * 64,
                "scope_sha256": "6" * 64, "resource_profile": "agent-runtime-v1",
                "request_sha256": backend.request_digest(request), "execution_expiry_max_seconds": 1200}}


def final_event(request):
    return {"type": "result", "execution_id": request["execution_id"], "attempt": request["attempt"],
            "thread_id": "fixture-thread", "result": {"status": "completed"}, "usage": {"input_tokens": 1, "output_tokens": 1}}


def attestation_fixture(request):
    """Complete, pure authenticated records; no host runtime is observed."""
    def file(name):
        return {"path": "/approved/" + name, "sha256": hashlib.sha256(name.encode()).hexdigest(),
                "bytes": 123, "mode": 0o444, "uid": 501, "gid": 20}

    names = set(identity.CONTROLLER_SOURCE_FILES.values()) | set(identity.TRANSPORT_SOURCE_FILES.values())
    names |= {"cdo_go_dependencies.py", "cdo_reconcile.py"}
    files = {name: file("runner/bin/" + name) for name in names}
    release = {name: "8" * 64 if name.endswith("_sha256") else "1.0.0" for name in identity.STATIC_IDENTITY_KEYS}
    for key, name in identity.FILE_IDENTITIES.items():
        if pathlib.Path(name).name in files:
            release[key] = files[pathlib.Path(name).name]["sha256"]
    release["source_manifest_sha256"] = "4" * 64
    for key, sources in (("agent_runtime_controller_sha256", identity.CONTROLLER_SOURCE_FILES),
                         ("agent_runtime_transport_sha256", identity.TRANSPORT_SOURCE_FILES)):
        release[key] = hashlib.sha256(backend.canonical({name: files[source]["sha256"] for name, source in sources.items()})).hexdigest()
    release["agent_runtime_protocol_sha256"] = backend.protocol_sha256()
    images = {"runner": "sha256:" + "7" * 64, "sdk": "sha256:" + "5" * 64, "command": "sha256:" + "9" * 64}
    release.update({key: images[role] for key, role in identity.IMAGE_KEYS.items()}, docker_version="29.8.1")
    credential = credentials(request)
    credential["controller_id"] = release["agent_runtime_controller_sha256"]
    credential["template"]["scope_sha256"] = hashlib.sha256(backend.canonical(request["sandbox_scope"])).hexdigest()
    config = {"release_identity": release, "lease_seconds": 10, "production_admission": "DENIED",
              "budget": {"mode": "enforce", "max_runs_5h": 20}, "templates": [{**credential["template"],
                "sandbox_scope": request["sandbox_scope"], "environment": {"CDO_SANDBOX_EXECUTION_MODE": "hardened-certification"},
                "owned_config": {"version": 1, "docker_endpoint": "unix:///approved/docker.sock"},
                "approved_commands": [["go", "test", "./..."]],
                "capability_key_id": hashlib.sha256(b"key-id-v1" + bytes.fromhex(credential["secret_hex"])).hexdigest()}]}
    manifest = {"schema": attestation.SCHEMA, "controller": files["cdo_runtime_controller.py"],
                "transport": files["cdo_runtime_transport.py"], "implementation": sorted(files.values(), key=lambda item: item["path"]),
                "protocol_sha256": backend.protocol_sha256(), "runtime_dependencies": [],
                "python": {"executable": file("python3"), "version": "3.13.5", "implementation": "CPython",
                           "cache_tag": "cpython-313", "openssl_version": "OpenSSL 3.5.0",
                           "modules": [{"name": name, "kind": "built-in"} for name in sorted(attestation.REQUIRED_MODULES)]},
                "node": {"executable": file("node"), "version": "v22.0.0", "versions": {"node": "22.0.0"},
                         "dependencies": [file("runner/dist/index.js")], "native_dependencies": {"files": [], "system_shared_cache_paths": []}},
                "native_runtime": {"files": [file("libpython.so")], "system_shared_cache_paths": [], "system_shared_cache": None, "kernel": "Linux 6.0"},
                "controller_configuration_sha256": hashlib.sha256(backend.canonical(config)).hexdigest(),
                "signing_policy_sha256": "a" * 64, "security_resource_profiles_sha256": "b" * 64,
                "docker": {"executable": file("docker"), "endpoint": "unix:///approved/docker.sock", "daemon_id": "daemon-1234", "server_version": "29.8.1", "images": images}}
    manifest["attestation_sha256"] = hashlib.sha256(backend.canonical(manifest)).hexdigest()
    credential["attestation_sha256"] = hashlib.sha256(backend.canonical(manifest)).hexdigest()
    result = {"state": "UNCREATED", "attestation_verified": True, "attestation_sha256": credential["attestation_sha256"],
              "attestation": manifest, "controller_configuration": config}
    return credential, result


def repin(credential, result):
    """Permit testing malformed-but-self-consistent schemas, not only hash drift."""
    manifest = result["attestation"]
    if "controller_configuration" in result:
        manifest["controller_configuration_sha256"] = hashlib.sha256(backend.canonical(result["controller_configuration"])).hexdigest()
    manifest.pop("attestation_sha256", None)
    manifest["attestation_sha256"] = hashlib.sha256(backend.canonical(manifest)).hexdigest()
    credential["attestation_sha256"] = hashlib.sha256(backend.canonical(manifest)).hexdigest()
    result["attestation_sha256"] = credential["attestation_sha256"]


class Headers(dict):
    def get_all(self, name):
        return [self[name]] if name in self else []


class FakeConnection:
    def __init__(self, credential, mutate=None):
        self.credential, self.mutate = credential, mutate
        self.closed = False

    def connect(self):
        self.sock = None

    def request(self, method, endpoint, encoded, headers):
        assert method == "POST" and endpoint == backend.ENDPOINT
        self.request_value = backend.decode(encoded)

    def getresponse(self):
        value = {**backend.response_binding(self.request_value), "ok": True, "result": {"state": "RUNNING"}}
        if self.mutate:
            self.mutate(value)
        secret = backend.request_secret(bytes.fromhex(self.credential["secret_hex"]), self.request_value)
        value["signature"] = backend.response_signature(secret, value)
        self.body = backend.canonical(value)
        self.status = 200
        self.headers = Headers({"Content-Type": "application/json", "Content-Length": str(len(self.body))})
        return self

    def getheader(self, name):
        return self.headers.get(name)

    def read(self, limit):
        return self.body[:limit]

    def close(self):
        self.closed = True


class BackendClientTests(unittest.TestCase):
    def setUp(self):
        self.request = runner_request()
        self.credentials = credentials(self.request)

    def test_template_hash_excludes_only_trusted_invocation_fields(self):
        changed = dict(self.request, execution_id="00000000-0000-4000-8000-000000000003", attempt=2,
                       execution_deadline="2030-01-01T00:00:00Z", runtime_context={"workflow_id": "wf", "workflow_run_id": "run", "activity_id": "activity", "activity_attempt": 2})
        self.assertEqual(backend.request_digest(changed), backend.request_digest(self.request))
        for field, value in (("prompt", "different"), ("model", "different"), ("thread_id", "different"), ("output_schema", {"type": "string"}), ("working_directory", "/other")):
            self.assertNotEqual(backend.request_digest(dict(self.request, **{field: value})), backend.request_digest(self.request))

    def test_unknown_fields_and_invalid_scope_deny(self):
        for change in ({"docker_flags": ["--privileged"]}, {"controller_url": "http://unrelated"},
                       {"sandbox_scope": {"profile": "layer", "write_paths": ["../sibling"]}},
                       {"sandbox_scope": {"profile": "layer", "write_paths": [[]]}},
                       {"runtime_context": {"workflow_id": "claimed"}}):
            with self.subTest(change=change), self.assertRaises(backend.BackendDenied):
                backend.normalize_request(dict(self.request, **change))

    def test_client_denies_request_and_baseline_drift(self):
        for change in ({"prompt": "changed"}, {"execution_id": "invalid"}, {"attempt": True}, {"execution_deadline": "expired"}):
            with self.subTest(change=change), self.assertRaises(backend.BackendDenied):
                backend.AgentRuntimeClient(self.credentials, dict(self.request, **change))

    def test_signed_request_has_only_fixed_bindings_and_nonce(self):
        signed = backend.agent_signed_request(self.credentials, self.request, "create", now=100, nonce="00000000-0000-4000-8000-000000000004")
        self.assertEqual(set(signed), backend.REQUEST_FIELDS)
        self.assertNotIn("prompt", signed)
        self.assertNotIn("working_directory", signed)
        self.assertNotIn("secret_hex", signed)
        signature = signed.pop("signature")
        self.assertEqual(signature, backend.request_signature(bytes.fromhex(self.credentials["secret_hex"]), signed))
        self.assertNotEqual(signature, backend.response_signature(bytes.fromhex(self.credentials["secret_hex"]), signed))

    def test_endpoints_and_generic_operations_denied(self):
        for url in ("http://example.com:80", "http://127.0.0.1:80/v1/docker", "http://user:password@127.0.0.1:80", "http://127.0.0.1:80?proxy=1", "file:///var/run/docker.sock"):
            with self.subTest(url=url), self.assertRaises(backend.BackendDenied):
                backend.endpoint(url)
        for operation, cursor in (("exec", 0), ("inspect", 1), ("diagnostics", -1), ("cleanup", True)):
            with self.assertRaises(backend.BackendDenied):
                backend.agent_signed_request(self.credentials, self.request, operation, cursor)

    def test_credentials_readonly_regular_single_link(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = pathlib.Path(temporary) / "credentials.json"
            path.write_bytes(backend.canonical(self.credentials))
            path.chmod(0o400)
            self.assertEqual(backend.load_credentials(str(path)), self.credentials)
            path.chmod(0o600)
            with self.assertRaises(backend.BackendDenied):
                backend.load_credentials(str(path))
            path.chmod(0o400)
            alias = pathlib.Path(temporary) / "alias"
            alias.symlink_to(path)
            with self.assertRaises(backend.BackendDenied):
                backend.load_credentials(str(alias))
            alias.unlink()
            os.link(path, alias)
            with self.assertRaises(backend.BackendDenied):
                backend.load_credentials(str(path))

    def test_response_is_authenticated_and_request_bound(self):
        connection = FakeConnection(self.credentials)
        with patch.object(backend.http.client, "HTTPConnection", return_value=connection):
            result = backend.AgentRuntimeClient(self.credentials, self.request).call("inspect")
        self.assertEqual(result, {"state": "RUNNING"})
        self.assertTrue(connection.closed)
        for field, replacement in (("nonce", "old"), ("execution_id", "other"), ("attempt", 2), ("workpackage_id", "other"), ("request_sha256", "0" * 64), ("release_id", "0" * 64), ("role", "reviewer"), ("attestation_sha256", "0" * 64), ("controller_id", "0" * 64), ("runtime_context", {"workflow_id": "other"})):
            connection = FakeConnection(self.credentials, lambda value, f=field, v=replacement: value.update({f: v}))
            with self.subTest(field=field), patch.object(backend.http.client, "HTTPConnection", return_value=connection), self.assertRaisesRegex(backend.BackendDenied, "BINDING_INVALID"):
                backend.AgentRuntimeClient(self.credentials, self.request).call("inspect")

    def test_transport_failure_is_typed_without_fallback(self):
        with patch.object(backend.http.client, "HTTPConnection", side_effect=OSError("no route")), self.assertRaisesRegex(backend.BackendDenied, "RUNTIME_TRANSPORT_UNKNOWN"):
            backend.AgentRuntimeClient(self.credentials, self.request).call("start")
        # Construction failure is typed; no local execution implementation exists.
        source = pathlib.Path(backend.__file__).read_text()
        self.assertNotIn("import subprocess", source)
        self.assertNotIn("/var/run/docker.sock", source)

    def test_per_execution_key_binds_attempt_and_actual_activity(self):
        root = bytes.fromhex(self.credentials["secret_hex"])
        current = backend.execution_secret(root, self.request)
        for changed in (dict(self.request, execution_id="other"), dict(self.request, attempt=2),
                        dict(self.request, runtime_context={"workflow_id": "wf", "workflow_run_id": "run", "activity_id": "activity", "activity_attempt": 1})):
            self.assertNotEqual(current, backend.execution_secret(root, changed))
        signed = backend.agent_signed_request(self.credentials, self.request, "inspect")
        signature = signed.pop("signature")
        self.assertEqual(signature, backend.request_signature(current, signed))
        self.assertNotEqual(signature, backend.request_signature(root, signed))

    def test_attestation_probe_does_not_create_execution(self):
        operations = []
        credential, expected = attestation_fixture(self.request)

        def inspect(client, operation, cursor=0):
            operations.append(operation)
            self.assertNotIn("prompt", client.request)
            return expected

        with patch.object(backend.AgentRuntimeClient, "call", inspect):
            result = backend.attestation_probe(credential)
        self.assertEqual(operations, ["inspect"])
        self.assertEqual(result["state"], "UNCREATED")

    def test_attestation_probe_verifies_self_pin_and_public_configuration(self):
        credential, result = attestation_fixture(self.request)
        with patch.object(backend.AgentRuntimeClient, "call", return_value=result):
            self.assertEqual(backend.attestation_probe(credential), result)
        result["controller_configuration"] = {"release_identity": {"runner_image": "changed"}}
        with patch.object(backend.AgentRuntimeClient, "call", return_value=result), self.assertRaisesRegex(backend.BackendDenied, "CONFIGURATION_MISMATCH"):
            backend.attestation_probe(credential)

    def test_attestation_probe_missing_required_authenticated_evidence_denied(self):
        for field in ("attestation", "controller_configuration", "attestation_verified"):
            credential, result = attestation_fixture(self.request)
            result.pop(field)
            with self.subTest(field=field), patch.object(backend.AgentRuntimeClient, "call", return_value=result), self.assertRaises(backend.BackendDenied):
                backend.attestation_probe(credential)

    def test_attestation_probe_incomplete_self_consistent_evidence_denied(self):
        mutations = {
            "schema": lambda value: value["attestation"].pop("schema"),
            "implementation": lambda value: value["attestation"].update(implementation=[]),
            "python_modules": lambda value: value["attestation"]["python"].update(modules=[]),
            "node": lambda value: value["attestation"].update(node=None),
            "docker_image": lambda value: value["attestation"]["docker"]["images"].pop("runner"),
            "configuration": lambda value: value.update(controller_configuration={}),
            "current_protocol": lambda value: value["attestation"].update(protocol_sha256="0" * 64),
            "current_sdk_image": lambda value: value["attestation"]["docker"]["images"].update(sdk="sha256:" + "0" * 64),
        }
        for name, mutation in mutations.items():
            credential, result = attestation_fixture(self.request)
            mutation(result)
            repin(credential, result)
            with self.subTest(name=name), patch.object(backend.AgentRuntimeClient, "call", return_value=result), self.assertRaises(backend.BackendDenied):
                backend.attestation_probe(credential)

    def test_attestation_probe_altered_and_stale_current_hashes_denied(self):
        original, evidence = attestation_fixture(self.request)
        for name in ("manifest", "config", "self_digest", "outer_digest", "stale"):
            credential, result = copy.deepcopy((original, evidence))
            if name == "manifest":
                result["attestation"]["python"]["version"] = "3.12.0"
            elif name == "config":
                result["controller_configuration"]["lease_seconds"] = 20
            elif name == "self_digest":
                result["attestation"]["attestation_sha256"] = "0" * 64
                credential["attestation_sha256"] = hashlib.sha256(backend.canonical(result["attestation"])).hexdigest()
                result["attestation_sha256"] = credential["attestation_sha256"]
            elif name == "outer_digest":
                result["attestation_sha256"] = "0" * 64
            else:
                result["attestation"]["docker"]["daemon_id"] = "stale-daemon"
                repin(credential, result)
                credential = original
            with self.subTest(name=name), patch.object(backend.AgentRuntimeClient, "call", return_value=result), self.assertRaises(backend.BackendDenied):
                backend.attestation_probe(credential)

    def test_result_requires_fresh_identity_and_bounded_usage(self):
        valid = final_event(self.request)
        self.assertEqual(backend.validate_result(valid, self.request, "fixture-thread"), valid)
        for event in (dict(valid, execution_id="old"), dict(valid, attempt=2), dict(valid, usage={"input_tokens": 0, "cached_input_tokens": 1}), dict(valid, result={"text": "x" * backend.MAX_RESULT_BYTES})):
            with self.assertRaises(backend.BackendDenied):
                backend.validate_result(event, self.request, "fixture-thread")

    def test_result_waits_for_success_and_positive_cleanup(self):
        calls, emitted = [], []

        class Client:
            def __init__(self, *_):
                pass

            def call(_self, operation, cursor=0):
                calls.append(operation)
                if operation == "inspect":
                    return {"state": "SUCCEEDED", "exit_code": 0}
                if operation == "diagnostics":
                    return {"events": [{"type": "thread_started", "thread_id": "fixture-thread"}, final_event(self.request)], "next_cursor": 2}
                if operation == "cleanup":
                    self.assertEqual([event["type"] for event in emitted], ["thread_started"])
                    return {"state": "CLEANED", "cleanup": True}
                return {}

        with patch.object(backend, "AgentRuntimeClient", Client):
            backend.run_backend(self.credentials, self.request, emitted.append)
        self.assertEqual([event["type"] for event in emitted], ["thread_started", "result"])
        self.assertEqual(calls, ["create", "start", "inspect", "diagnostics", "cleanup"])

    def test_unknown_cleanup_and_cancelled_late_green_never_emit_result(self):
        for unknown in (True, False):
            calls, emitted, cancelled = [], [], [False]

            class Client:
                def __init__(self, *_):
                    pass

                def call(_self, operation, cursor=0):
                    calls.append(operation)
                    if operation == "inspect":
                        return {"state": "SUCCEEDED", "exit_code": 0}
                    if operation == "diagnostics":
                        return {"events": [{"type": "thread_started", "thread_id": "fixture-thread"}, final_event(self.request)], "next_cursor": 2}
                    if operation == "cleanup":
                        cancelled[0] = not unknown
                        return {"state": "UNKNOWN", "cleanup": False} if unknown else {"state": "CLEANED", "cleanup": True}
                    return {}

            with self.subTest(unknown=unknown), patch.object(backend, "AgentRuntimeClient", Client), self.assertRaises(backend.BackendDenied):
                backend.run_backend(self.credentials, self.request, emitted.append, lambda: cancelled[0])
            self.assertEqual([event["type"] for event in emitted], ["thread_started"])
            self.assertIn("cancel", calls)

    def test_uncertain_create_attempt_still_requests_owned_cleanup(self):
        calls = []

        class Client:
            def __init__(self, *_):
                pass

            def call(_self, operation, cursor=0):
                calls.append(operation)
                if operation == "create":
                    raise backend.BackendDenied("RUNTIME_TRANSPORT_UNKNOWN")
                return {}

        with patch.object(backend, "AgentRuntimeClient", Client), self.assertRaises(backend.BackendDenied):
            backend.run_backend(self.credentials, self.request, lambda _: self.fail("unexpected result"))
        self.assertEqual(calls, ["create", "cancel", "cleanup"])

    def test_production_gate_rejects_before_input_or_credentials(self):
        environment = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "CDO_SANDBOX_EXECUTION_MODE": "production", "CDO_RUNTIME_BACKEND_CREDENTIALS": "/nonexistent"}
        result = subprocess.run([sys.executable, backend.__file__], input=b"secret-fixture-prompt", env=environment, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, b"")
        self.assertIn(b"RUNTIME_TRANSPORT_NOT_CERTIFIED", result.stderr)
        self.assertNotIn(b"secret-fixture", result.stderr)


if __name__ == "__main__":
    unittest.main()
