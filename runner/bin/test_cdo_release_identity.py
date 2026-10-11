"""Offline installed-byte and authenticated-host admission regressions."""
import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import cdo_release_identity as release


class RuntimeUnknownTests(unittest.TestCase):
 def test_missing_installed_build_cannot_produce_identity(self):
  with patch.object(release.pathlib.Path,'read_text',side_effect=OSError('unavailable')):
   with self.assertRaises(OSError):release.observe(Path('/missing/cdo-release-fixture'))
 def test_hash_is_exact_bytes(self):
  with patch.object(release.pathlib.Path,'read_bytes',return_value=b'actual'):
   self.assertEqual(release.sha('unused'),'e5c6fde86910ded72db5cc7afc32f850440d4ef7caa5dbb69f5bdc0d3e39cb3b')


class InstalledHostIdentityTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="cdo-release-observer-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        files = {}
        for name in sorted(release.REQUIRED_FILES):
            file = self.root / name
            file.parent.mkdir(parents=True, exist_ok=True)
            file.write_bytes(("fixture bytes: " + name).encode())
            files[name] = release.sha(file)
        identity = {key: "a" * 64 for key in release.STATIC_IDENTITY_KEYS}
        identity.update(sdk_version="0.144.6", cli_version="codex 0.144.6", go_version="go version go1.25.1 linux/amd64")
        for key, file in release.FILE_IDENTITIES.items():
            identity[key] = files[file]
        for key, sources in (("agent_runtime_controller_sha256", release.CONTROLLER_SOURCE_FILES),
                             ("agent_runtime_transport_sha256", release.TRANSPORT_SOURCE_FILES)):
            identity[key] = release.digest({name: files["runner/bin/" + file] for name, file in sources.items()})
        identity["agent_runtime_protocol_sha256"] = release.backend.protocol_sha256()
        identity["runner_manifest_sha256"] = release.digest({"installed_files": files, "installed_symlinks": {}})
        self.build = {"identity": identity, "installed_files": files, "installed_symlinks": {},
                      "source_manifest_sha256": identity["source_manifest_sha256"]}
        self.write_build()
        self.public_config = {"release_identity": dict(identity), "configuration": "fixture-complete-runtime"}
        actual_files = [self.file_identity("/trusted/bin/" + Path(name).name, value)
                        for name, value in files.items() if name.startswith("runner/bin/")]
        by_name = {Path(item["path"]).name: item for item in actual_files}
        native = self.file_identity("/trusted/libpython", "c" * 64)
        self.manifest = {
            "schema": release.attestation.SCHEMA,
            "controller": by_name["cdo_agent_runtime.py"],
            "transport": by_name["cdo_runtime_transport.py"],
            "implementation": sorted(actual_files, key=lambda item: item["path"]),
            "protocol_sha256": identity["agent_runtime_protocol_sha256"],
            "python": {"executable": self.file_identity("/trusted/python", "b" * 64),
                       "version": "3.9.0", "implementation": "cpython", "cache_tag": "cpython-39",
                       "openssl_version": "OpenSSL fixture",
                       "modules": [{"name": name, "kind": "built-in"} for name in sorted(release.attestation.REQUIRED_MODULES)]},
            "node": {"executable": self.file_identity("/trusted/node", "d" * 64), "version": "v24.0.0",
                     "versions": {"node": "24.0.0"}, "dependencies": [self.file_identity("/trusted/dist/host.js", "e" * 64)],
                     "native_dependencies": {"files": [native], "system_shared_cache_paths": []}},
            "runtime_dependencies": [native],
            "native_runtime": {"files": [native], "system_shared_cache_paths": [], "system_shared_cache": None, "kernel": "fixture-kernel"},
            "controller_configuration_sha256": release.digest(self.public_config),
            "signing_policy_sha256": "f" * 64,
            "security_resource_profiles_sha256": "0" * 64,
            "docker": {"executable": self.file_identity("/trusted/docker", "1" * 64),
                       "endpoint": "unix:///trusted/docker.sock", "daemon_id": "fixture-daemon-identity", "server_version": "29.8.1",
                       "images": {name: "sha256:" + value * 64 for name, value in (("runner", "2"), ("sdk", "3"), ("command", "4"))}},
        }
        self.seal(self.manifest)
        self.credentials = {"controller_id": identity["agent_runtime_controller_sha256"],
                            "protocol_sha256": identity["agent_runtime_protocol_sha256"],
                            "attestation_sha256": release.digest(self.manifest),
                            "template": {"release_id": identity["source_manifest_sha256"], "execution_expiry_max_seconds": 60,
                                         "image_id": self.manifest["docker"]["images"]["sdk"]},
                            "url": "http://127.0.0.1:12345", "secret_hex": "5" * 64}
        self.result = {"state": "UNCREATED", "invalid_reason": None, "attestation_verified": True,
                       "attestation_sha256": self.credentials["attestation_sha256"],
                       "attestation": self.manifest, "controller_configuration": self.public_config}

    def file_identity(self, name, value):
        return {"path": name, "sha256": value, "bytes": 1, "mode": 0o555, "uid": 0, "gid": 0}

    def write_build(self):
        file = self.root / "release/build.json"
        if file.exists():
            file.chmod(0o600)
        file.write_text(json.dumps(self.build, sort_keys=True))
        file.chmod(0o400)

    def seal(self, manifest):
        manifest.pop("attestation_sha256", None)
        manifest["attestation_sha256"] = release.digest(manifest)

    def observe(self, result=None, credentials=None):
        with patch.object(release.backend, "load_credentials", return_value=credentials or self.credentials), \
             patch.object(release.backend, "attestation_probe", return_value=self.result if result is None else result):
            return release.observe(self.root)

    def test_static_twenty_and_live_twenty_five_are_separate(self):
        self.assertEqual(len(release.installed_identity(self.root)), 20)
        actual = self.observe()
        self.assertEqual(set(actual), release.RELEASE_IDENTITY_KEYS)
        self.assertEqual(len(actual), 25)
        self.assertEqual(actual["active_host_attestation_sha256"], release.digest(self.manifest))
        self.assertNotEqual(actual["active_host_attestation_sha256"], self.manifest["attestation_sha256"])
        self.assertEqual(actual["runner_image"], self.manifest["docker"]["images"]["runner"])
        self.assertNotIn("secret_hex", json.dumps(actual))
        self.assertNotIn("controller_configuration", actual)

    def test_static_build_never_accepts_dynamic_host_or_missing_complete_pins(self):
        original = copy.deepcopy(self.build)
        for key in ("agent_runtime_controller_sha256", "agent_runtime_protocol_sha256", "agent_runtime_transport_sha256"):
            self.build = copy.deepcopy(original)
            del self.build["identity"][key]
            self.write_build()
            with self.assertRaisesRegex(release.IdentityDenied, "BUILD_INVENTORY_DENIED"):
                self.observe()
        self.build = original
        self.build["identity"]["active_host_attestation_sha256"] = self.credentials["attestation_sha256"]
        self.write_build()
        with self.assertRaisesRegex(release.IdentityDenied, "BUILD_INVENTORY_DENIED"):
            self.observe()

    def test_inventory_omission_mutation_and_malformed_hash_deny_before_host(self):
        original = copy.deepcopy(self.build)
        for mutate in (lambda: self.build["installed_files"].pop("runner/bin/cdo_agent_runtime.py"),
                       lambda: self.build["identity"].update(agent_runtime_controller_sha256="UNKNOWN"),
                       lambda: self.build["installed_files"].update({"runner/bin/cdo_sdk_relay.py": "0" * 64})):
            self.build = copy.deepcopy(original)
            mutate()
            self.write_build()
            with patch.object(release.backend, "load_credentials", return_value=self.credentials), \
                 patch.object(release.backend, "attestation_probe") as probe:
                with self.assertRaises(release.IdentityDenied):
                    release.observe(self.root)
                probe.assert_not_called()
        self.build = original
        self.write_build()
        (self.root / "runner/bin/cdo_sdk_relay.py").write_bytes(b"changed installed implementation")
        with patch.object(release.backend, "load_credentials") as load:
            with self.assertRaisesRegex(release.IdentityDenied, "BUILD_FILE_MISMATCH"):
                release.observe(self.root)
            load.assert_not_called()

    def test_unsafe_inventory_path_and_symlink_parent_deny(self):
        self.build["installed_files"]["../outside"] = "a" * 64
        self.build["identity"]["runner_manifest_sha256"] = release.digest({"installed_files": self.build["installed_files"], "installed_symlinks": self.build["installed_symlinks"]})
        self.write_build()
        with self.assertRaisesRegex(release.IdentityDenied, "BUILD_PATH_DENIED"):
            self.observe()
        del self.build["installed_files"]["../outside"]
        self.build["identity"]["runner_manifest_sha256"] = release.digest({"installed_files": self.build["installed_files"], "installed_symlinks": self.build["installed_symlinks"]})
        self.write_build()
        original = self.root / "runner/bin"
        moved = self.root / "runner/moved-bin"
        original.rename(moved)
        original.symlink_to(moved, target_is_directory=True)
        with self.assertRaisesRegex(release.IdentityDenied, "BUILD_PATH_DENIED"):
            self.observe()

    def test_other_release_controller_or_protocol_credentials_deny(self):
        for name in ("controller_id", "protocol_sha256", "release_id"):
            credentials = copy.deepcopy(self.credentials)
            (credentials["template"] if name == "release_id" else credentials)[name] = "0" * 64
            with self.assertRaisesRegex(release.IdentityDenied, "HOST_RELEASE_IDENTITY_MISMATCH"):
                self.observe(credentials=credentials)

    def test_changed_link_and_changed_record_cannot_preserve_release_identity(self):
        alias = self.root / "runner/bin/trusted-alias"
        alias.symlink_to("cdo_runtime_backend.py")
        self.build["installed_symlinks"]["runner/bin/trusted-alias"] = "cdo_runtime_backend.py"
        self.build["identity"]["runner_manifest_sha256"] = release.digest({
            "installed_files": self.build["installed_files"], "installed_symlinks": self.build["installed_symlinks"]})
        self.write_build()
        release.installed_identity(self.root)
        alias.unlink()
        alias.symlink_to("cdo_runtime_controller.py")
        with self.assertRaisesRegex(release.IdentityDenied, "BUILD_LINK_MISMATCH"):
            release.installed_identity(self.root)
        self.build["installed_symlinks"]["runner/bin/trusted-alias"] = "cdo_runtime_controller.py"
        self.write_build()
        with self.assertRaisesRegex(release.IdentityDenied, "BUILD_INVENTORY_MISMATCH"):
            release.installed_identity(self.root)

    def test_missing_unknown_or_invalid_inspect_identity_deny(self):
        for field, value in (("state", "UNKNOWN"), ("state", "RUNNING"), ("invalid_reason", "CANCELLED"),
                             ("attestation_verified", False), ("attestation_sha256", "UNKNOWN")):
            result = dict(self.result)
            result[field] = value
            with self.assertRaisesRegex(release.IdentityDenied, "HOST_ATTESTATION_DENIED"):
                self.observe(result)
        result = dict(self.result)
        del result["attestation"]
        with self.assertRaises(release.attestation.AttestationDenied):
            self.observe(result)

    def test_manifest_configuration_and_outer_digest_are_both_verified(self):
        result = copy.deepcopy(self.result)
        result["controller_configuration"]["configuration"] = "changed"
        with self.assertRaisesRegex(release.IdentityDenied, "HOST_ATTESTATION_MISMATCH"):
            self.observe(result)
        result = copy.deepcopy(self.result)
        result["attestation"]["node"]["version"] = "v24.0.1"
        self.seal(result["attestation"])
        with self.assertRaisesRegex(release.IdentityDenied, "HOST_ATTESTATION_MISMATCH"):
            self.observe(result)
        result = copy.deepcopy(self.result)
        result["attestation"]["attestation_sha256"] = "0" * 64
        with self.assertRaises(release.attestation.AttestationDenied):
            self.observe(result)

    def test_changed_active_implementation_cannot_match_unchanged_backend(self):
        for name in ("cdo_agent_runtime.py", "cdo_runtime_backend.py", "cdo_sdk_relay.py", "cdo_sdk_state.py", "cdo_broker.py"):
            result = copy.deepcopy(self.result)
            for field in ("controller", "transport"):
                if Path(result["attestation"][field]["path"]).name == name:
                    result["attestation"][field]["sha256"] = "0" * 64
            for item in result["attestation"]["implementation"]:
                if Path(item["path"]).name == name:
                    item["sha256"] = "0" * 64
            self.seal(result["attestation"])
            credentials = copy.deepcopy(self.credentials)
            credentials["attestation_sha256"] = result["attestation_sha256"] = release.digest(result["attestation"])
            with self.assertRaisesRegex(release.IdentityDenied, "HOST_IMPLEMENTATION_MISMATCH"):
                self.observe(result, credentials)

    def test_unknown_docker_or_missing_node_dependency_denies(self):
        for section, field, value in (("docker", "server_version", "UNKNOWN"), ("docker", "images", {}),
                                      ("node", "dependencies", [])):
            result = copy.deepcopy(self.result)
            result["attestation"][section][field] = value
            self.seal(result["attestation"])
            credentials = copy.deepcopy(self.credentials)
            credentials["attestation_sha256"] = result["attestation_sha256"] = release.digest(result["attestation"])
            with self.assertRaises((release.IdentityDenied, release.attestation.AttestationDenied)):
                self.observe(result, credentials)

    def test_transport_unavailable_has_no_static_identity_fallback(self):
        with patch.object(release.backend, "load_credentials", return_value=self.credentials), \
             patch.object(release.backend, "attestation_probe", side_effect=release.backend.BackendDenied("RUNTIME_TRANSPORT_UNKNOWN")):
            with self.assertRaisesRegex(release.backend.BackendDenied, "RUNTIME_TRANSPORT_UNKNOWN"):
                release.observe(self.root)

    def test_attestation_probe_rejects_missing_approved_sdk_image(self):
        credentials = copy.deepcopy(self.credentials)
        credentials["template"].pop("image_id", None)
        with patch.object(release.backend.AgentRuntimeClient, "call", return_value=self.result) as call:
            with self.assertRaisesRegex(release.backend.BackendDenied, "RUNTIME_ATTESTATION_MANIFEST_DENIED"):
                release.backend.attestation_probe(credentials)
        call.assert_called_once_with("inspect")

    def test_attestation_probe_rejects_mismatched_approved_sdk_image(self):
        credentials = copy.deepcopy(self.credentials)
        credentials["template"]["image_id"] = "sha256:" + "9" * 64
        with patch.object(release.backend.AgentRuntimeClient, "call", return_value=self.result) as call:
            with self.assertRaisesRegex(release.backend.BackendDenied, "RUNTIME_ATTESTATION_MISMATCH"):
                release.backend.attestation_probe(credentials)
        call.assert_called_once_with("inspect")

    def test_attestation_probe_accepts_exact_approved_sdk_image(self):
        with patch.object(release.backend.AgentRuntimeClient, "call", return_value=self.result) as call:
            self.assertEqual(release.backend.attestation_probe(self.credentials), self.result)
        call.assert_called_once_with("inspect")

    def test_new_host_pin_does_not_allow_changed_sdk_image(self):
        result = copy.deepcopy(self.result)
        result["attestation"]["docker"]["images"]["sdk"] = "sha256:" + "9" * 64
        self.seal(result["attestation"])
        credentials = copy.deepcopy(self.credentials)
        credentials["attestation_sha256"] = result["attestation_sha256"] = release.digest(result["attestation"])
        with patch.object(release.backend.AgentRuntimeClient, "call", return_value=result) as call:
            with self.assertRaisesRegex(release.backend.BackendDenied, "RUNTIME_ATTESTATION_MISMATCH"):
                release.backend.attestation_probe(credentials)
        call.assert_called_once_with("inspect")

    def test_other_approved_image_role_cannot_replace_sdk_image(self):
        for role in ("runner", "command"):
            with self.subTest(role=role):
                credentials = copy.deepcopy(self.credentials)
                credentials["template"]["image_id"] = self.manifest["docker"]["images"][role]
                with patch.object(release.backend.AgentRuntimeClient, "call", return_value=self.result) as call:
                    with self.assertRaisesRegex(release.backend.BackendDenied, "RUNTIME_ATTESTATION_MISMATCH"):
                        release.backend.attestation_probe(credentials)
                call.assert_called_once_with("inspect")

    def test_observer_uses_only_fresh_uncreated_inspections(self):
        observed = []
        def inspect(client, operation, cursor=0):
            observed.append((operation, client.request["execution_id"]))
            return self.result
        with patch.object(release.backend, "load_credentials", return_value=self.credentials), \
             patch.object(release.backend.AgentRuntimeClient, "call", inspect):
            self.assertEqual(release.observe(self.root), release.observe(self.root))
        self.assertEqual([item[0] for item in observed], ["inspect", "inspect"])
        self.assertNotEqual(observed[0][1], observed[1][1])

if __name__=='__main__':unittest.main()
