"""Host ownership and recovery checks. No Docker daemon or model calls."""
from copy import deepcopy
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
import unittest
from unittest.mock import patch
import uuid

import cdo_runtime_owned as owned
from cdo_oci_profile import docker_command


class FakeDocker:
    def __init__(self, ledger):
        self.ledger = ledger
        self.containers = {}
        self.calls = []
        self.unknown = False
        self.partial_create = False
        self.remove_failure = False
        self.image_env = ["PATH=/usr/local/bin:/usr/bin:/bin"]
        self.image_labels = {}
        self.next_id = 1

    def _check_daemon(self):
        self.calls.append(("daemon", None))
        if self.unknown:
            raise owned.RuntimeUnknown()

    def inspect(self, target):
        self._check_daemon()
        self.calls.append(("inspect", target))
        for info in self.containers.values():
            if target in {info["Id"], info["Name"][1:]}:
                return deepcopy(info)
        return None

    def _run(self, argv):
        self.calls.append((argv[0], list(argv)))
        if self.unknown:
            raise owned.RuntimeUnknown()
        if argv[:2] == ["image", "inspect"]:
            return 0, json.dumps([{"Id": argv[2], "Config": {"Env": self.image_env, "Labels": self.image_labels, "Volumes": None}}]).encode(), b""
        if argv[0] == "create":
            name = argv[argv.index("--name") + 1]
            record = json.loads(self.ledger.read_text())["resources"][name]
            expected = deepcopy(record["expected"])
            expected.pop("policy_sha256")
            expected["Id"] = "%064x" % self.next_id; self.next_id += 1
            expected["Config"]["Env"] = [k + "=" + v for k, v in expected["Config"]["Env"].items()]
            expected["HostConfig"]["Ulimits"] = [{"Name": name, "Soft": limits[0], "Hard": limits[1]}
                for name, limits in expected["HostConfig"]["Ulimits"].items()]
            expected["Mounts"] = [{"Type": "bind", "Source": source, "Destination": target, "RW": False, "Propagation": "rprivate"}
                for target, source in expected["Mounts"].items()]
            expected["State"] = {"Status": "created", "Running": False, "ExitCode": 0, "OOMKilled": False}
            expected["NetworkSettings"] = {"Networks": {}}
            self.containers[expected["Id"]] = expected
            if self.partial_create:
                raise owned.RuntimeUnknown("CREATION_RESPONSE_LOST")
            return 0, (expected["Id"] + "\n").encode(), b""
        if argv[0] == "rm":
            if self.remove_failure:
                return 1, b"", b"daemon unavailable"
            self.containers.pop(argv[-1], None)
            return 0, b"", b""
        raise AssertionError("Unexpected fake command: %r" % argv)


class OwnedRuntimeTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name).resolve(); self.root.chmod(0o700)
        self.state = self.root / "private"; self.state.mkdir(mode=0o700)
        self.control = self.root / "control"; self.control.mkdir(mode=0o700)
        self.snapshot = self.state / "snapshot"; self.snapshot.mkdir()
        (self.snapshot / "fixture").write_text("fixture")
        self.executable = self.root / "docker-client"; self.executable.write_text("fixture executable"); self.executable.chmod(0o700)
        # No socket creation is needed for the injected, daemon-free driver.
        self.socket_path = self.root / "daemon.sock"; self.socket_path.write_text("fake endpoint")
        self.supervisor = self.root / "supervisor.py"; self.supervisor.write_text("fixture")
        self.relay = self.root / "relay.py"; self.relay.write_text("fixture")
        self.schema = self.control / "schema.json"; self.schema.write_text("{}")
        self.now = 2000000000
        self.values = {"version": 1, "execution_id": str(uuid.uuid4()), "controller_id": "c" * 64,
            "protocol_sha256": "d" * 64, "owner_marker": "e" * 64, "state_root": str(self.state),
            "docker_executable": str(self.executable), "docker_endpoint": "unix://" + str(self.socket_path),
            "daemon_id": "fixture-daemon", "command_image_id": "sha256:" + "1" * 64,
            "sdk_image_id": "sha256:" + "2" * 64, "expires_at": self.now + 300,
            "allowed_mounts": {"/workspace": str(self.snapshot), "/reference/supervisor.py": str(self.supervisor),
                "/reference/cdo_sdk_relay.py": str(self.relay), "/reference/schema.json": str(self.schema)}}
        self.config_file = self.root / "owned.json"
        self.write_config()
        self.fake = FakeDocker(self.state / "owned-runtime.json")
        self.runtime = owned.OwnedRuntime(self.config_file, docker=self.fake, clock=lambda: self.now)
        self.name = "cdo-sandbox-broker-" + "a" * 16
        self.sdk_name = "cdo-sdk-state-" + "b" * 32

    def tearDown(self):
        self.tmp.cleanup()

    def write_config(self):
        self.config_file.write_text(json.dumps(self.values)); self.config_file.chmod(0o600)

    def command(self, operation="run", name=None):
        args = docker_command(self.values["command_image_id"], self.snapshot, "reviewer", [],
            ["python3", "-c", owned.BROKER_KEEPALIVE], name or self.name)
        args[1] = operation
        args[2:2] = ["-i", "--label", "cdo.execution_id=" + self.values["execution_id"], "--label", "cdo.resource=command"]
        args[args.index("--user") + 1] = "0:0"
        return args

    def sdk(self):
        args = ["run", "--name", self.sdk_name, "-i", "--log-driver", "none", "--read-only", "--user", "100:101",
            "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true", "--security-opt", "seccomp=builtin",
            "--ulimit", "nofile=256:256", "--ulimit", "fsize=16777216:16777216", "--pids-limit", "128",
            "--memory", "512m", "--memory-swap", "512m", "--cpus", "2", "--network", "none", "--env", "CDO_SDK_NETWORK_RELAY=1",
            "--label", "cdo.execution_id=" + self.values["execution_id"], "--label", "cdo.resource=sdk"]
        for target, options in owned.SDK_TMPFS.items():
            args += ["--tmpfs", target + ":" + options]
        for target, source in (("/reference/supervisor.py", self.supervisor), ("/reference/cdo_sdk_relay.py", self.relay), ("/reference/schema.json", self.schema)):
            args += ["--mount", "type=bind,src=" + str(source) + ",dst=" + target + ",readonly"]
        return args + ["--entrypoint", "python3", self.values["sdk_image_id"], "/reference/supervisor.py", "--supervise", "exec", "--cd", owned.SDK_ROOT + "/control"]

    def create(self):
        result = self.runtime.run(self.command("create"), capture_output=True, text=True)
        return result.stdout.strip()

    def start(self):
        ident = self.create()
        with patch.object(self.runtime, "_spawn", return_value=object()):
            self.runtime.popen(["start", self.name])
        self.fake.containers[ident]["State"].update(Status="running", Running=True)
        return ident

    def ledger(self):
        return json.loads(self.runtime.ledger.read_text())

    def test_create_records_exact_id_and_policy_before_start(self):
        captured = []
        def spawned(argv, options):
            state = self.ledger(); record = state["resources"][self.name]
            self.assertEqual(record["status"], "STARTED")
            self.assertEqual(record["id"], argv[-1])
            self.assertEqual(record["expected"]["Config"]["Labels"]["cdo.runtime.owner"], self.values["owner_marker"])
            captured.append(argv)
            return object()
        with patch.object(self.runtime, "_spawn", side_effect=spawned):
            self.runtime.popen(self.command(), stdin=subprocess.PIPE)
        self.assertEqual(captured[0][:3], ["start", "--attach", "--interactive"])
        create = next(args for verb, args in self.fake.calls if verb == "create")
        self.assertNotIn("--rm", create)
        self.assertIn("--cgroupns", create)
        self.assertIn("core=0:0", create)
        self.assertIn("/dev/shm:ro,noexec,nosuid,nodev,size=4096", create)

    def test_positive_owned_cleanup_uses_only_full_id_and_proves_absence(self):
        ident = self.create()
        result = self.runtime.cleanup_all()
        self.assertEqual(result["status"], "CLEANED")
        self.assertTrue(result["resources_absent"])
        self.assertEqual(result["removed"], [ident])
        removals = [argv for operation, argv in self.fake.calls if operation == "rm"]
        self.assertEqual(removals, [["rm", "--force", ident]])
        self.assertIn(("inspect", ident), self.fake.calls)
        self.assertIn(("inspect", self.name), self.fake.calls)
        with self.assertRaisesRegex(owned.OwnedDenied, "INVALIDATED"):
            self.runtime.run(self.command("create", "cdo-sandbox-broker-" + "b" * 16))

    def test_public_label_spoof_and_unknown_target_are_denied(self):
        self.create()
        for argv in (["inspect", "other-service"], ["rm", "-f", "cdo-sdk-state-" + "c" * 32],
                ["exec", "--user", "0:0", "other-service", "python3", "-c", "print(1)"]):
            with self.assertRaisesRegex(owned.OwnedDenied, "TARGET"):
                self.runtime.run(argv)
        self.assertFalse(any(op == "rm" for op, _ in self.fake.calls))

    def test_private_marker_cross_controller_and_policy_drift_block_cleanup(self):
        mutations = [lambda info: info["Config"]["Labels"].update({"cdo.runtime.owner": "f" * 64}),
            lambda info: info["Config"]["Labels"].update({"cdo.runtime.controller": "f" * 64}),
            lambda info: info["HostConfig"].update({"Privileged": True}),
            lambda info: info["HostConfig"].pop("Memory"),
            lambda info: info["HostConfig"].update({"PidsLimit": None}),
            lambda info: info["HostConfig"].update({"NetworkMode": "host"}),
            lambda info: info["Mounts"].append({"Type": "bind", "Source": "/var/run/docker.sock", "Destination": "/socket", "RW": False}),
            lambda info: info["Config"].update({"Cmd": ["sh"]})]
        ident = self.create(); original = deepcopy(self.fake.containers[ident])
        for mutate in mutations:
            self.fake.containers[ident] = deepcopy(original); mutate(self.fake.containers[ident])
            with self.assertRaises(owned.RuntimeUnknown):
                self.runtime.cleanup()
            self.assertEqual(self.ledger()["status"], "UNKNOWN")
            self.assertFalse(any(op == "rm" for op, _ in self.fake.calls))

    def test_unknown_is_terminal_even_after_later_confirmed_cleanup(self):
        ident = self.create(); self.fake.unknown = True
        with self.assertRaises(owned.RuntimeUnknown):
            self.runtime.run(["inspect", self.name])
        self.assertTrue(self.ledger()["invalidated"])
        self.fake.unknown = False
        result = self.runtime.cleanup()
        self.assertEqual(result["status"], "UNKNOWN")
        self.assertTrue(result["resources_absent"])
        self.assertNotIn(ident, self.fake.containers)
        with self.assertRaisesRegex(owned.OwnedDenied, "INVALIDATED"):
            self.runtime.popen(["start", self.name])

    def test_lost_create_response_recovers_private_intent_without_restarting(self):
        self.fake.partial_create = True
        with self.assertRaises(owned.RuntimeUnknown):
            self.create()
        record = self.ledger()["resources"][self.name]
        self.assertIsNone(record["id"])
        self.assertEqual(record["status"], "INTENT")
        restarted = owned.OwnedRuntime(self.config_file, docker=self.fake, clock=lambda: self.now)
        result = restarted.cleanup_all()
        self.assertEqual(result["status"], "UNKNOWN")
        self.assertTrue(result["resources_absent"])
        self.assertEqual(len(result["removed"]), 1)
        self.assertEqual(self.ledger()["resources"][self.name]["status"], "REMOVED")

    def test_name_reuse_never_removes_unrelated_container_or_reports_absence(self):
        ident = self.create(); replacement = deepcopy(self.fake.containers.pop(ident))
        replacement["Id"] = "f" * 64
        self.fake.containers[replacement["Id"]] = replacement
        with self.assertRaises(owned.RuntimeUnknown):
            self.runtime.cleanup()
        self.assertIn(replacement["Id"], self.fake.containers)
        self.assertFalse(any(op == "rm" for op, _ in self.fake.calls))
        with self.assertRaises(owned.RuntimeUnknown):
            self.runtime.run(["inspect", self.name])

    def test_cleanup_command_error_never_proves_absence(self):
        self.create(); self.fake.remove_failure = True
        with self.assertRaises(owned.RuntimeUnknown):
            self.runtime.cleanup()
        self.assertEqual(self.ledger()["status"], "UNKNOWN")

    def test_raw_daemon_and_extra_flags_are_denied(self):
        for argv in (["ps"], ["prune"], ["info"], ["image", "inspect", self.values["command_image_id"]], ["stop", self.name]):
            with self.assertRaises(owned.OwnedDenied):
                self.runtime.run(argv)
        for addition in (["--privileged"], ["--cap-add", "SYS_ADMIN"], ["--network", "host"],
                ["--mount", "type=bind,src=/var/run/docker.sock,dst=/socket,readonly"],
                ["--env", "CODEX_API_KEY=fixture"], ["--label", "cdo.runtime.owner=fixture"]):
            args = self.command("create"); args[2:2] = addition
            with self.assertRaises(owned.OwnedDenied):
                self.runtime.run(args)
        self.assertFalse(any(op == "create" for op, _ in self.fake.calls))

    def test_required_hard_limits_and_authless_sdk_relay(self):
        for flag in ("--memory", "--memory-swap", "--pids-limit", "--security-opt"):
            args = self.command("create"); index = args.index(flag); del args[index:index + 2]
            with self.assertRaises(owned.OwnedDenied):
                self.runtime.run(args)
        args = self.sdk(); args[0] = "create"
        ident = self.runtime.run(args, capture_output=True, text=True).stdout.strip()
        self.assertEqual(self.fake.containers[ident]["Config"]["User"], "100:101")
        self.assertEqual(self.fake.containers[ident]["HostConfig"]["NetworkMode"], "none")
        self.assertFalse(any("auth.json" in mount["Source"] for mount in self.fake.containers[ident]["Mounts"]))

    def test_env_reset_command_and_exact_root_helpers_only(self):
        ident = self.start()
        env = [k + "=" + v for k, v in owned.EXEC_ENV.items()]
        argv = ["exec", "--user", "100:101", "--workdir", "/tmp/view", self.name, "/usr/bin/env", "-i", *env, "go", "test", "./..."]
        with patch.object(self.runtime, "_spawn", return_value=object()) as spawn:
            self.runtime.popen(argv)
        self.assertEqual(spawn.call_args.args[0][5], ident)
        for bad in (["exec", "--user", "0:0", self.name, "python3", "-c", "print('arbitrary')"],
                ["exec", "--user", "100:101", "--workdir", "/tmp/view", self.name, "sh", "-c", "id"],
                argv[:7] + ["PATH=/usr/bin", *env[1:], "go", "test", "./..."]):
            with self.assertRaises(owned.OwnedDenied):
                self.runtime.popen(bad)
        view = next(iter(self.runtime._view_helpers()))
        with patch.object(self.runtime, "_spawn", return_value=object()):
            self.runtime.popen(["exec", "-i", "--user", "0:0", self.name, "python3", "-c", view])

    def test_lease_expiry_and_config_change_deny_admission(self):
        self.create(); self.now += 301
        with self.assertRaisesRegex(owned.OwnedDenied, "LEASE_EXPIRED"):
            self.runtime.popen(["start", self.name])
        self.assertTrue(self.ledger()["invalidated"])
        self.values["controller_id"] = "f" * 64; self.write_config()
        with self.assertRaises(owned.RuntimeUnknown):
            self.runtime.run(["inspect", self.name])

    def test_private_config_unknown_fields_links_modes_and_scope_denied(self):
        self.values["arbitrary_command"] = ["sh"]; self.write_config()
        with self.assertRaises(owned.OwnedDenied):
            owned.load_config(self.config_file)
        del self.values["arbitrary_command"]; self.write_config(); self.config_file.chmod(0o644)
        with self.assertRaises(owned.OwnedDenied):
            owned.load_config(self.config_file)
        self.config_file.chmod(0o600)
        link = self.root / "link.json"; link.symlink_to(self.config_file)
        with self.assertRaises(owned.OwnedDenied):
            owned.load_config(link)
        self.values["allowed_mounts"]["/workspace"] = str(self.root); self.write_config()
        with self.assertRaises(owned.OwnedDenied):
            owned.load_config(self.config_file)

    def test_bound_future_private_snapshot_and_schema_subtrees(self):
        self.values["allowed_mounts"]["/workspace"] = {"under": str(self.root), "kind": "snapshot"}
        self.values["allowed_mounts"]["/reference/schema.json"] = {"under": str(self.root), "kind": "schema"}
        self.write_config()
        self.runtime = owned.OwnedRuntime(self.config_file, docker=self.fake, clock=lambda: self.now)
        self.assertEqual(self.runtime._mount("type=bind,source=" + str(self.schema) + ",target=/reference/schema.json,readonly"), ("/reference/schema.json", str(self.schema)))
        wrong = self.root / "credentials.json"; wrong.write_text("{}")
        with self.assertRaises(owned.OwnedDenied):
            self.runtime._mount("type=bind,source=" + str(wrong) + ",target=/reference/schema.json,readonly")
        job = self.root / "cdo-trusted-broker-abc123"; job.mkdir(mode=0o700); state = job / "state"; state.mkdir(); snapshot = state / "snapshot"; snapshot.mkdir()
        self.assertEqual(self.runtime._mount("type=bind,source=" + str(snapshot) + ",target=/workspace,readonly"), ("/workspace", str(snapshot)))

    def test_finite_staged_verification_snapshot_layout(self):
        self.values["allowed_mounts"]["/workspace"] = {"under": str(self.root), "kind": "snapshot"}
        self.write_config()
        self.runtime = owned.OwnedRuntime(self.config_file, docker=self.fake, clock=lambda: self.now)
        state = self.root / "cdo-trusted-broker-ABc123" / "state"
        state.mkdir(parents=True)
        verification = state / "verification-ab_cd123"; verification.mkdir(mode=0o700)
        snapshot = verification / "snapshot"; snapshot.mkdir()
        self.assertEqual(self.runtime._mount("type=bind,source=" + str(snapshot) + ",target=/workspace,readonly"), ("/workspace", str(snapshot)))
        for child in ("other-ab_cd123", "verification-x", "verification-ab_cd123/verification-abcdefgh"):
            forbidden = state / child / "snapshot"; forbidden.mkdir(parents=True)
            with self.assertRaises(owned.OwnedDenied):
                self.runtime._mount("type=bind,source=" + str(forbidden) + ",target=/workspace,readonly")
        link = state / "verification-abcdefgh"; link.symlink_to(verification)
        with self.assertRaises(owned.OwnedDenied):
            self.runtime._mount("type=bind,source=" + str(link / "snapshot") + ",target=/workspace,readonly")

    def test_daemon_absence_requires_exact_bounded_error(self):
        driver = object.__new__(owned._Docker)
        driver._check_daemon = lambda: None
        for code, out, error in ((1, b"", b"daemon timeout No such object: " + self.name.encode()),
                (1, b"", b"Error: No such object: other"), (2, b"", b"Error: No such object: " + self.name.encode()),
                (1, b"unexpected", b"Error: No such object: " + self.name.encode())):
            driver._run = lambda _, result=(code, out, error): result
            with self.assertRaises(owned.RuntimeUnknown):
                driver.inspect(self.name)
        driver._run = lambda _: (1, b"[]\n", b"Error: No such object: " + self.name.encode() + b"\n")
        self.assertIsNone(driver.inspect(self.name))

    def test_image_identity_and_environment_are_actively_checked(self):
        self.fake.image_env = ["API_KEY=fixture"]
        with self.assertRaises(owned.RuntimeUnknown):
            self.create()
        self.assertFalse(any(op == "create" for op, _ in self.fake.calls))
        self.assertEqual(self.ledger()["status"], "UNKNOWN")

    def test_sequential_verification_resources_preserve_active_lease(self):
        for index in range(owned.MAX_COMMAND_RESOURCES):
            name = "cdo-sandbox-broker-" + "%016x" % index
            ident = self.runtime.run(self.command("create", name), text=True).stdout.strip()
            result = self.runtime.cleanup_resource(name)
            self.assertEqual(result["status"], "ACTIVE")
            self.assertFalse(result["invalidated"])
            self.assertEqual(result["removed"], [ident])
            self.assertTrue(self.runtime.prove_absent(name)["resources_absent"])
        self.assertEqual(self.runtime.status()["status"], "ACTIVE")
        with self.assertRaisesRegex(owned.OwnedDenied, "RESOURCE_ALREADY_USED"):
            self.runtime.run(self.command("create", "cdo-sandbox-broker-" + "f" * 16))
        sdk = self.sdk(); sdk[0] = "create"
        self.runtime.run(sdk)
        self.assertEqual(len(self.runtime.status()["resources"]), owned.MAX_RESOURCES)

    def test_active_command_resource_cannot_be_replaced(self):
        self.create()
        with self.assertRaisesRegex(owned.OwnedDenied, "RESOURCE_ALREADY_USED"):
            self.runtime.run(self.command("create", "cdo-sandbox-broker-" + "b" * 16))
        self.assertEqual(sum(op == "create" for op, _ in self.fake.calls), 1)

    def test_unknown_reconciliation_cannot_claim_sdk_cleanup_success(self):
        self.create(); self.fake.unknown = True
        with self.assertRaises(owned.RuntimeUnknown):
            self.runtime.run(["inspect", self.name])
        self.fake.unknown = False
        result = self.runtime.cleanup_resource(self.name)
        self.assertEqual(result["status"], "UNKNOWN")
        self.assertFalse(result["resources_absent"])
        self.assertFalse(self.runtime.prove_absent(self.name)["resources_absent"])

    def test_malformed_or_unreadable_config_is_terminal_after_restoration(self):
        self.create(); original = self.config_file.read_bytes()
        self.config_file.write_text('{"invalid":')
        with self.assertRaises(owned.RuntimeUnknown):
            self.runtime.run(["inspect", self.name])
        self.config_file.write_bytes(original)
        self.assertEqual(self.runtime.status()["status"], "UNKNOWN")
        with self.assertRaisesRegex(owned.OwnedDenied, "INVALIDATED"):
            self.runtime.popen(["start", self.name])

    def test_canonical_docker_prefix_is_fixed_to_private_configuration(self):
        ident = self.create()
        args = [self.values["docker_executable"], "--host", self.values["docker_endpoint"], "inspect", self.name]
        result = self.runtime.run(args, text=True)
        self.assertEqual(json.loads(result.stdout)[0]["Id"], ident)
        args[2] = "unix:///other/socket"
        with self.assertRaisesRegex(owned.OwnedDenied, "ENDPOINT_DENIED"):
            self.runtime.run(args)

    def test_cli_spawns_before_concurrent_invalidation_can_acquire_lock(self):
        self.create(); done = threading.Event(); attempted = threading.Event(); workers = []
        class Child:
            def poll(self):
                return None
            def wait(self):
                self_test.assertTrue(done.wait(2))
                return 0
        self_test = self
        def spawned(argv, options):
            def invalidator():
                attempted.set(); self.runtime.invalidate(); done.set()
            thread = threading.Thread(target=invalidator); thread.start(); workers.append(thread)
            self.assertTrue(attempted.wait(2))
            self.assertFalse(done.is_set())
            self.assertEqual(self.ledger()["status"], "ACTIVE")
            return Child()
        with patch.object(self.runtime, "_spawn", side_effect=spawned):
            self.assertEqual(self.runtime.cli(["start", self.name]), 0)
        for thread in workers:
            thread.join(timeout=2)
        self.assertEqual(self.runtime.status()["status"], "INVALIDATED")

    def test_incomplete_exec_option_is_a_bounded_denial(self):
        self.start()
        with self.assertRaises(owned.OwnedDenied):
            self.runtime.popen(["exec", "--user", "100:101", "--workdir"])


if __name__ == "__main__":
    unittest.main()
