"""Offline observer checks; included by the declared ``make sandbox-test``.

Focused command: python3 -m unittest discover -s runner/bin
                -p test_cdo_runtime_attestation.py
Docker calls and native-loader enumeration are fakes here. Full transport
certification must separately obtain positive actual host/Docker evidence.
"""
from dataclasses import replace
import copy
import hashlib
import json
import os
from pathlib import Path
import socket
import sys
import tempfile
import unittest
from unittest.mock import patch

import cdo_runtime_attestation as attest


class FakeDocker:
    def __init__(self, executable, endpoint, images):
        self.value = {"executable": str(executable), "endpoint": "unix://" + str(endpoint),
                      "daemon_id": "fixture-daemon-identity", "server_version": "29.8.1", "images": dict(images)}
        self.calls = 0

    def active_attestation(self):
        self.calls += 1
        return copy.deepcopy(self.value)


class AttestationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="cdo-attestation-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.controller = self.file("controller.py", b"# trusted controller\n")
        self.transport = self.file("transport.py", b"# trusted transport\n")
        self.dependency = self.file("dependency.py", b"# trusted dependency\n")
        self.executable = self.file("docker", b"#!/bin/sh\nexit 1\n", 0o700)
        self.endpoint = self.root / "daemon.sock"
        self.bound_socket = socket.socket(socket.AF_UNIX)
        self.bound_socket.bind(str(self.endpoint))
        self.addCleanup(self.bound_socket.close)
        self.images = {name: "sha256:" + (index * 64) for name, index in (("runner", "1"), ("sdk", "2"), ("command", "3"))}
        self.config = attest.TrustedAttestationConfig(
            controller_path=self.controller, transport_path=self.transport, protocol_sha256="4" * 64,
            controller_configuration={"controller_id": "5" * 64, "lifecycle": "bounded"},
            signing_policy={"algorithm": "HMAC-SHA256", "nonce_strategy": "durable-exclusive", "key_id": "fixture-public-id"},
            images=self.images, security_resource_profiles={"sdk": {"read_only": True, "capabilities": [], "memory": 536870912}},
            implementation_paths=(self.dependency,))
        self.adapter = FakeDocker(self.executable, self.endpoint, self.images)
        self.native = {"files": [attest._file_identity(self.dependency)], "system_shared_cache_paths": [],
                       "system_shared_cache": None, "kernel": "fixture-kernel"}
        self.mock_native = patch.object(attest, "_native_runtime", return_value=self.native)
        self.mock_native.start()
        self.addCleanup(self.mock_native.stop)

    def file(self, name, contents=b"test", mode=0o600):
        path = self.root / name
        path.write_bytes(contents)
        path.chmod(mode)
        return path

    def capture(self, config=None):
        return attest.capture_attestation(config or self.config, self.adapter)

    def resigned(self, value):
        value = copy.deepcopy(value)
        del value["attestation_sha256"]
        value["attestation_sha256"] = hashlib.sha256(attest.canonical(value)).hexdigest()
        return value

    def test_actual_interpreter_file_modules_and_digest_captured(self):
        actual = self.capture()
        self.assertEqual(actual["python"]["executable"]["path"], str(Path(sys.executable).resolve()))
        self.assertEqual(actual["python"]["version"], " ".join(sys.version.split()))
        self.assertIn("_ssl", [item["name"] for item in actual["python"]["modules"]])
        self.assertEqual(actual["controller"]["sha256"], hashlib.sha256(self.controller.read_bytes()).hexdigest())
        self.assertEqual(actual["docker"]["images"], self.images)
        self.assertTrue(attest.verify_attestation(actual, self.capture()))
        self.assertEqual(self.adapter.calls, 2)

    def test_all_loaded_stdlib_optional_inventory_is_positive_and_bound(self):
        actual = self.capture(replace(self.config, bind_all_loaded_stdlib=True))
        names = {item["name"] for item in actual["python"]["modules"]}
        self.assertIn("sysconfig", names)
        self.assertIn("ctypes", names)
        self.assertNotIn("unittest", names)
        self.assertGreater(len(names), len(attest.REQUIRED_MODULES))
        self.assertTrue(attest.verify_attestation(actual, self.capture(replace(self.config, bind_all_loaded_stdlib=True))))

    def test_changed_controller_and_transport_deny(self):
        expected = self.capture()
        for path in (self.controller, self.transport, self.dependency):
            old = path.read_bytes()
            path.write_bytes(b"changed")
            with self.assertRaisesRegex(attest.AttestationDenied, "RUNTIME_ATTESTATION_CHANGED"):
                attest.verify_attestation(expected, self.capture())
            path.write_bytes(old)

    def test_changed_public_config_signing_policy_profile_deny(self):
        expected = self.capture()
        for config in (replace(self.config, controller_configuration={"controller_id": "6" * 64}),
                       replace(self.config, signing_policy={"algorithm": "HMAC-SHA256", "nonce_strategy": "changed"}),
                       replace(self.config, security_resource_profiles={"sdk": {"memory": 100}}),
                       replace(self.config, protocol_sha256="a" * 64)):
            with self.assertRaisesRegex(attest.AttestationDenied, "RUNTIME_ATTESTATION_CHANGED"):
                attest.verify_attestation(expected, self.capture(config))

    def test_changed_docker_daemon_version_executable_deny(self):
        expected = self.capture()
        for name, replacement in (("daemon_id", "other-daemon-identity"), ("server_version", "29.8.2")):
            old = self.adapter.value[name]
            self.adapter.value[name] = replacement
            with self.assertRaisesRegex(attest.AttestationDenied, "RUNTIME_ATTESTATION_CHANGED"):
                attest.verify_attestation(expected, self.capture())
            self.adapter.value[name] = old
        self.executable.write_bytes(b"changed executable")
        with self.assertRaisesRegex(attest.AttestationDenied, "RUNTIME_ATTESTATION_CHANGED"):
            attest.verify_attestation(expected, self.capture())

    def test_wrong_image_is_unknown_not_fallback(self):
        self.adapter.value["images"]["sdk"] = "sha256:" + "9" * 64
        with self.assertRaisesRegex(attest.AttestationUnknown, "ATTESTATION_DOCKER_IDENTITY_UNKNOWN"):
            self.capture()
        self.assertEqual(self.adapter.calls, 1)

    def test_docker_unavailable_and_incomplete_deny(self):
        for value in (None, {}, {"executable": str(self.executable)}, self.adapter.value | {"extra": True}):
            with patch.object(self.adapter, "active_attestation", return_value=value):
                with self.assertRaises(attest.AttestationUnknown):
                    self.capture()
        with patch.object(self.adapter, "active_attestation", side_effect=OSError("secret must not escape")):
            with self.assertRaisesRegex(attest.AttestationUnknown, "^ATTESTATION_DOCKER_UNKNOWN$"):
                self.capture()

    def test_endpoint_must_be_canonical_actual_owned_socket(self):
        self.adapter.value["endpoint"] = "unix://" + str(self.controller)
        with self.assertRaisesRegex(attest.AttestationDenied, "ATTESTATION_DOCKER_ENDPOINT_DENIED"):
            self.capture()
        alias = self.root / "alias.sock"
        alias.symlink_to(self.endpoint)
        self.adapter.value["endpoint"] = "unix://" + str(alias)
        with self.assertRaisesRegex(attest.AttestationDenied, "ATTESTATION_PATH_DENIED"):
            self.capture()

    def test_symlink_hardlink_and_nonregular_implementation_deny(self):
        alias = self.root / "alias.py"
        alias.symlink_to(self.controller)
        with self.assertRaises(attest.AttestationDenied):
            self.capture(replace(self.config, controller_path=alias))
        linked = self.root / "linked.py"
        os.link(self.controller, linked)
        with self.assertRaises(attest.AttestationDenied):
            self.capture()
        linked.unlink()
        with self.assertRaises(attest.AttestationDenied):
            self.capture(replace(self.config, controller_path=self.root))

    def test_world_or_group_mutable_file_deny(self):
        self.controller.chmod(0o666)
        with self.assertRaisesRegex(attest.AttestationDenied, "ATTESTATION_FILE_DENIED"):
            self.capture()

    def test_protected_file_not_opened(self):
        protected = self.file("auth.json", b"private bytes")
        with patch.object(os, "open", wraps=os.open) as opened:
            with self.assertRaisesRegex(attest.AttestationDenied, "ATTESTATION_PATH_DENIED"):
                self.capture(replace(self.config, controller_path=protected))
        self.assertFalse(any(call.args[0] == str(protected) for call in opened.call_args_list))

    def test_system_alias_cannot_hide_protected_target(self):
        protected = self.file("auth.json", b"private bytes")
        alias = self.root / "ordinary.so"
        alias.symlink_to(protected)
        with patch.object(os, "open", wraps=os.open) as opened:
            with self.assertRaisesRegex(attest.AttestationDenied, "ATTESTATION_PATH_DENIED"):
                attest._file_identity(alias, resolve_system=True)
        self.assertEqual(opened.call_count, 0)

    def test_private_configuration_rejected_before_observation(self):
        for key in ("secret", "signing_key", "secret_hex", "api-token", "credentials"):
            with self.assertRaisesRegex(attest.AttestationDenied, "ATTESTATION_PRIVATE_CONFIG_DENIED"):
                self.capture(replace(self.config, controller_configuration={key: "must not fingerprint"}))
        self.assertEqual(self.adapter.calls, 0)

    def test_unknown_module_missing_mandatory_module_and_duplicates_deny(self):
        for names in (attest.REQUIRED_MODULES + ("nonexistent_attestation_fixture_module",),
                      ("json",), attest.REQUIRED_MODULES + ("json",), ([],)):
            with self.assertRaises(attest.AttestationDenied):
                self.capture(replace(self.config, python_module_names=names))

    def test_dependency_duplicate_and_size_limit_deny(self):
        with self.assertRaisesRegex(attest.AttestationDenied, "ATTESTATION_DUPLICATE_DEPENDENCY"):
            self.capture(replace(self.config, implementation_paths=(self.dependency, self.dependency)))
        with self.assertRaisesRegex(attest.AttestationDenied, "ATTESTATION_FILE_DENIED"):
            attest._file_identity(self.dependency, limit=1)

    def test_changed_read_fd_is_unknown(self):
        actual_fstat = os.fstat
        calls = 0
        def changing(descriptor):
            nonlocal calls
            calls += 1
            result = actual_fstat(descriptor)
            if calls == 2:
                self.controller.write_bytes(b"changed after hashing")
                result = actual_fstat(descriptor)
            return result
        with patch.object(os, "fstat", side_effect=changing):
            with self.assertRaisesRegex(attest.AttestationUnknown, "ATTESTATION_FILE_CHANGED"):
                attest._file_identity(self.controller)

    def test_manifest_missing_unknown_extra_digest_and_nested_schema_deny(self):
        actual = self.capture()
        modified = []
        extra = copy.deepcopy(actual); extra["extra"] = True; modified.append(self.resigned(extra))
        missing = copy.deepcopy(actual); del missing["transport"]; modified.append(self.resigned(missing))
        invalid = copy.deepcopy(actual); invalid["controller"] = {"sha256": "UNKNOWN"}; modified.append(self.resigned(invalid))
        unknown = copy.deepcopy(actual); unknown["python"]["version"] = "UNKNOWN"; modified.append(self.resigned(unknown))
        bad_native = copy.deepcopy(actual); bad_native["native_runtime"] = {}; modified.append(self.resigned(bad_native))
        extra_docker = copy.deepcopy(actual); extra_docker["docker"]["generic_api"] = True; modified.append(self.resigned(extra_docker))
        stale = copy.deepcopy(actual); stale["attestation_sha256"] = "0" * 64; modified.append(stale)
        for item in modified:
            with self.assertRaises(attest.AttestationDenied):
                attest.verify_attestation(item, item)

    def test_malformed_manifest_inputs_are_typed(self):
        for item in (None, [], {}, {"schema": attest.SCHEMA}, "UNKNOWN"):
            with self.assertRaises(attest.AttestationDenied):
                attest.verify_attestation(item, item)

    def test_node_requires_pinned_js_distribution(self):
        with self.assertRaisesRegex(attest.AttestationDenied, "ATTESTATION_NODE_DEPENDENCIES_REQUIRED"):
            self.capture(replace(self.config, node_executable=self.executable))
        with self.assertRaisesRegex(attest.AttestationDenied, "ATTESTATION_NODE_CONFIG_DENIED"):
            self.capture(replace(self.config, node_dependency_paths=(self.dependency,)))

    def test_node_actual_fixed_observer_bound_executable_and_dependencies(self):
        observed = {"version": "v22.18.0", "versions": {"node": "22.18.0", "openssl": "3.0.0"}, "shared_objects": []}
        with patch.object(attest, "_bounded_command", return_value=json.dumps(observed).encode()) as command:
            config = replace(self.config, node_executable=self.executable, node_dependency_paths=(self.dependency,))
            expected = self.capture(config)
            self.assertTrue(attest.verify_attestation(expected, self.capture(config)))
            self.assertEqual(command.call_args.args[0], str(self.executable))
            self.assertEqual(command.call_args.args[1][:2], ["--no-addons", "-e"])
            self.assertEqual(expected["node"]["dependencies"][0]["path"], str(self.dependency))
        for value in (b"UNKNOWN", b"{}", b'{"version":"UNKNOWN","versions":{},"shared_objects":[]}'):
            with patch.object(attest, "_bounded_command", return_value=value):
                with self.assertRaises(attest.AttestationUnknown):
                    self.capture(config)

    def test_bounded_executable_output_fails_typed(self):
        with self.assertRaisesRegex(attest.AttestationUnknown, "ATTESTATION_EXECUTABLE_LIMIT"):
            attest._bounded_command(str(Path(sys.executable).resolve()), ["-c", "print('x' * 70000)"])

    def test_native_unknown_has_no_os_or_empty_fallback(self):
        with patch.object(attest, "_native_runtime", side_effect=attest.AttestationUnknown("ATTESTATION_NATIVE_RUNTIME_UNKNOWN")):
            with self.assertRaisesRegex(attest.AttestationUnknown, "ATTESTATION_NATIVE_RUNTIME_UNKNOWN"):
                self.capture()

    def test_changed_native_library_or_os_identity_deny(self):
        expected = self.capture()
        self.native["kernel"] = "other-kernel"
        with self.assertRaisesRegex(attest.AttestationDenied, "RUNTIME_ATTESTATION_CHANGED"):
            attest.verify_attestation(expected, self.capture())

    def test_native_unreadable_non_system_library_denied(self):
        with self.assertRaises(attest.AttestationUnknown):
            attest._native_files([str(self.root / "missing-native.so")])


if __name__ == "__main__":
    unittest.main()
