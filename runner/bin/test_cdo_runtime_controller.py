"""Isolated controller adversarial checks; no Docker daemon or model is called."""
from __future__ import annotations
import concurrent.futures
from copy import deepcopy
from dataclasses import replace
import hashlib
import hmac
import json
import multiprocessing
import os
from pathlib import Path
import socket
import stat
from types import SimpleNamespace
import tempfile
import time
import unittest
from unittest.mock import patch
import uuid

import cdo_runtime_controller as runtime


class FakeDocker:
    def __init__(self):
        self.controller = None
        self.registration = None
        self.info = None
        self.calls = []
        self.unknown = False
        self.remove_unknown = False
        self.output = '{"controls":"PASS"}'

    def _call(self, operation, target):
        self.calls.append((operation, target))
        if self.unknown:
            raise runtime.RuntimeUnknown()

    def inspect(self, target):
        self._call("inspect", target)
        if self.info and target in (self.info["Id"], self.info["Name"][1:]):
            return deepcopy(self.info)
        return None

    def create(self, arguments):
        self._call("create", arguments)
        controller = self.controller
        registration = self.registration
        profile = runtime.PROFILE_VALUES[registration.recipe]
        self.info = {
            "Id": "a" * 64, "Name": "/" + controller.name(registration),
            "Image": registration.image_id,
            "Config": {"Image": registration.image_id, "User": "100:101",
                       "Entrypoint": ["/usr/bin/env"],
                       "Cmd": ["-i", "PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
                               "CDO_RUNTIME_DEADLINE=" + str(registration.execution_expires_at),
                               "python3", "/reference/runtime.py"],
                       "WorkingDir": "/workspace" if registration.recipe == "command-runtime" else "/",
                       "Volumes": None, "Labels": controller.labels(registration)},
            "HostConfig": {"ReadonlyRootfs": True, "Privileged": False, "NetworkMode": "none",
                           "CgroupnsMode": "private", "IpcMode": "private", "PidMode": "", "UTSMode": "",
                           "Runtime": "runc", "Isolation": "", "CgroupParent": "",
                           "NanoCpus": 2000000000, "Memory": profile["memory"],
                           "MemorySwap": profile["memory"], "PidsLimit": 128,
                           "PublishAllPorts": False, "AutoRemove": False, "CpuPeriod": 0, "CpuQuota": 0,
                           "CapDrop": ["ALL"], "CapAdd": [],
                           "SecurityOpt": ["no-new-privileges=true", "seccomp=builtin"],
                           "Tmpfs": profile["tmpfs"],
                           "RestartPolicy": {"Name": "no", "MaximumRetryCount": 0},
                           "LogConfig": {"Type": "local", "Config": {"max-size": "16k", "max-file": "1", "compress": "false"}},
                           "Ulimits": [{"Name": name, "Soft": value, "Hard": value}
                                       for name, value in (("core", 0), ("nofile", 256), ("fsize", 16777216))]},
            "Mounts": [{"Type": "bind", "Source": source, "Destination": target,
                        "RW": False, "Propagation": "rprivate"}
                       for target, source in controller.mounts(registration).items()],
            "NetworkSettings": {"Networks": {"none": {}}},
            "State": {"Running": False, "ExitCode": 0, "OOMKilled": False, "Status": "created"},
        }
        return self.info["Id"]

    def start(self, target):
        self._call("start", target)
        self.info["State"].update(Running=True, Status="running")

    def stop(self, target):
        self._call("stop", target)
        self.info["State"].update(Running=False, Status="exited")

    def remove(self, target):
        self._call("remove", target)
        if self.remove_unknown:
            raise runtime.RuntimeUnknown()
        if self.info and self.info["Id"] == target:
            self.info = None

    def logs(self, target):
        self._call("logs", target)
        return self.output


def process_request(config, registration, request, queue):
    try:
        controller = runtime.RuntimeController(config, [registration], FakeDocker(), clock=lambda: 2000000000)
        result = controller.handle(request)
        queue.put(result["status"])
    except runtime.ControllerDenied as error:
        queue.put(error.code)


class RuntimeControllerTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name).resolve()
        self.state = self.root / "host-state"
        self.state.mkdir(mode=0o700)
        self.source = self.root / "source"
        self.source.mkdir()
        (self.source / "go.mod").write_text("module fixture\n\ngo 1.23\n")
        (self.source / "fixture_test.go").write_text("package fixture\n")
        for file in self.source.iterdir():
            file.chmod(0o444)
        self.source.chmod(0o555)
        self.helper = self.root / "command-helper.py"
        self.helper.write_bytes(runtime.COMMAND_HELPER)
        self.helper.chmod(0o444)
        self.now = 2000000000
        self.config = runtime.ControllerConfig(runtime.controller_identity(), self.state,
                                               "sha256:" + "1" * 64, "sha256:" + "2" * 64)
        self.registration = runtime.ExecutionRegistration(
            execution_id=str(uuid.uuid4()), release_id="3" * 64, workpackage_id=str(uuid.uuid4()),
            role="layer", recipe="command-runtime", scope_sha256="4" * 64,
            execution_expires_at=self.now + 300, secret=os.urandom(32),
            helper_path=self.helper, helper_sha256=runtime.helper_digest("command-runtime"),
            image_id=self.config.command_image_id, source_root=self.source,
            source_sha256=runtime.source_digest(self.source))
        self.docker = FakeDocker()
        self.controller = self.new_controller(self.docker)
        self.docker.controller = self.controller
        self.docker.registration = self.registration
        self.controller.start_watchdog()

    def tearDown(self):
        self.controller.close()
        for folder, _, _ in os.walk(self.root):
            Path(folder).chmod(0o700)
        self.temporary.cleanup()

    def new_controller(self, docker):
        return runtime.RuntimeController(self.config, [self.registration], docker, clock=lambda: self.now)

    def request(self, requested_operation, **values):
        value = self.controller.request(self.registration.execution_id, requested_operation, int(self.now) + 30)
        value.update(values)
        return value

    def resign(self, request, secret=None):
        unsigned = {key: value for key, value in request.items() if key != "signature"}
        request["signature"] = hmac.new(secret or self.registration.secret, runtime.canonical(unsigned), hashlib.sha256).hexdigest()
        return request

    def create(self):
        return self.controller.handle(self.request("create"))

    def test_finite_lifecycle_and_bounded_diagnostics(self):
        self.assertEqual(self.create()["status"], "CREATED")
        self.assertEqual(self.controller.handle(self.request("start"))["status"], "RUNNING")
        result = self.controller.handle(self.request("diagnostics"))
        self.assertEqual(result["diagnostics"]["output"], self.docker.output)
        self.assertEqual(result["production_admission"], "DENIED")
        self.assertNotIn(str(self.source), json.dumps(result))
        self.assertNotIn("secret", json.dumps(result))
        self.assertEqual(self.controller.handle(self.request("cancel"))["status"], "CANCELLED")
        self.assertEqual(self.controller.handle(self.request("cleanup"))["status"], "CLEANED")
        self.assertIsNone(self.docker.info)
        removed = [target for operation, target in self.docker.calls if operation == "remove"]
        self.assertEqual(removed, ["a" * 64])

    def test_fixed_policy_and_no_caller_docker_arguments(self):
        arguments = self.controller.create_arguments(self.registration)
        self.assertEqual(arguments[0], "create")
        for flag in ("--read-only", "--cap-drop", "--security-opt", "--cpus", "--memory",
                     "--memory-swap", "--pids-limit", "--ulimit", "--tmpfs"):
            self.assertIn(flag, arguments)
        for denied in ("--privileged", "--cap-add", "--volume", "--publish", "--device", "--pid"):
            self.assertNotIn(denied, arguments)
        self.assertIn(self.registration.image_id, arguments)
        self.assertNotIn("/var/run/docker.sock", " ".join(arguments))
        for key, value in (("argv", ["sh", "-c", "id"]), ("image", "latest"),
                           ("labels", {}), ("mounts", []), ("network", "host"),
                           ("privileged", True), ("resource_limits", {"memory": 1})):
            with self.subTest(field=key), self.assertRaises(runtime.ControllerDenied):
                self.controller.handle(self.resign(self.request("create", **{key: value})))
        self.assertEqual(self.docker.calls, [])

    def test_all_identity_spoofing_and_signed_field_mutation(self):
        changes = {"execution_id": str(uuid.uuid4()), "release_id": "f" * 64,
                   "workpackage_id": str(uuid.uuid4()), "role": "reviewer",
                   "image_id": "sha256:" + "f" * 64, "scope_sha256": "f" * 64,
                   "resource_profile": "unrestricted", "execution_expires_at": self.now + 1,
                   "controller_id": "f" * 64, "protocol_sha256": "f" * 64}
        for field, value in changes.items():
            with self.subTest(field=field), self.assertRaises(runtime.ControllerDenied):
                self.controller.handle(self.resign(self.request("create", **{field: value})))
        original = self.request("create")
        for field, value in (("operation", "cancel"), ("nonce", str(uuid.uuid4())),
                             ("request_expires_at", self.now + 40)):
            with self.subTest(field=field), self.assertRaisesRegex(runtime.ControllerDenied, "SIGNATURE"):
                self.controller.handle({**original, field: value})
        self.assertEqual(self.docker.calls, [])

    def test_cross_execution_secret_cannot_authenticate(self):
        with self.assertRaisesRegex(runtime.ControllerDenied, "SIGNATURE"):
            self.controller.handle(self.resign(self.request("create"), os.urandom(32)))
        self.assertEqual(self.docker.calls, [])

    def test_strict_types_uuid_and_operation_policy(self):
        cases = [{"version": True}, {"request_expires_at": True}, {"execution_expires_at": float(self.now + 300)},
                 {"execution_id": self.registration.execution_id.upper()}, {"nonce": "00000000-0000-0000-0000-000000000000"},
                 {"signature": "unknown"}, {"operation": "exec"}, {"operation": "prune"},
                 {"operation": "build"}, {"operation": "remove-volume"}, {"operation": []}]
        for update in cases:
            with self.subTest(update=update), self.assertRaises(runtime.ControllerDenied):
                self.controller.handle(self.request("create", **update))
        self.assertEqual(self.docker.calls, [])

    def test_strict_json_duplicate_size_nonfinite_and_shape(self):
        for data in (b'{"version":1,"version":1}', b'{"value":NaN}', b'{' + b' ' * 4096,
                     b'\xff', b'[{"version":1}]'):
            with self.subTest(data=data[:30]), self.assertRaises(runtime.ControllerDenied):
                self.controller.handle(runtime.decode_request(data))
        self.assertEqual(self.docker.calls, [])

    def test_request_expiry_and_bounded_window(self):
        for expiry in (self.now - 1, self.now, self.now + 61):
            with self.assertRaisesRegex(runtime.ControllerDenied, "REQUEST_EXPIRED"):
                self.controller.handle(self.resign(self.request("create", request_expires_at=expiry)))
        self.assertEqual(self.docker.calls, [])

    def test_replay_and_atomic_thread_race(self):
        request = self.request("create")
        def call(_):
            try:
                return self.controller.handle(request)["status"]
            except runtime.ControllerDenied as error:
                return error.code
        with concurrent.futures.ThreadPoolExecutor(max_workers=16) as pool:
            results = list(pool.map(call, range(32)))
        self.assertEqual(results.count("CREATED"), 1)
        self.assertEqual(results.count("REQUEST_REPLAYED"), 31)
        self.assertEqual(sum(op == "create" for op, _ in self.docker.calls), 1)

    def test_atomic_nonce_race_across_processes(self):
        context = multiprocessing.get_context("fork")
        queue = context.Queue()
        request = self.request("inspect")
        processes = [context.Process(target=process_request, args=(self.config, self.registration, request, queue))
                     for _ in range(8)]
        for process in processes:
            process.start()
        for process in processes:
            process.join(timeout=5)
            self.assertEqual(process.exitcode, 0)
        results = [queue.get(timeout=1) for _ in processes]
        self.assertEqual(results.count("REGISTERED"), 1)
        self.assertEqual(results.count("REQUEST_REPLAYED"), 7)

    def test_restart_preserves_replay_and_container_binding(self):
        request = self.request("create")
        self.controller.handle(request)
        restarted = self.new_controller(self.docker)
        try:
            with self.assertRaisesRegex(runtime.ControllerDenied, "REPLAYED"):
                restarted.handle(request)
            self.assertEqual(restarted.handle(self.request("inspect"))["container_id"], "a" * 64)
            with self.assertRaisesRegex(runtime.ControllerDenied, "ALREADY_USED"):
                restarted.handle(self.request("create"))
        finally:
            restarted.close()

    def test_unique_nonces_cannot_recreate_or_resurrect(self):
        self.create()
        with self.assertRaisesRegex(runtime.ControllerDenied, "ALREADY_USED"):
            self.create()
        self.controller.handle(self.request("cancel"))
        for operation in ("create", "start"):
            with self.assertRaises(runtime.ControllerDenied):
                self.controller.handle(self.request(operation))
        self.assertIsNone(self.docker.info)

    def test_start_cancel_concurrency_cannot_resurrect(self):
        self.create()
        requests = [self.request("start"), self.request("cancel")]
        def call(request):
            try:
                return self.controller.handle(request)["status"]
            except runtime.ControllerDenied as error:
                return error.code
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            results = list(pool.map(call, requests))
        self.assertIn("CANCELLED", results)
        self.assertIsNone(self.docker.info)

    def test_owner_labels_image_name_and_id_verified_before_remove(self):
        self.create()
        original = deepcopy(self.docker.info)
        cases = [("Image", "sha256:" + "f" * 64), ("Name", "/product-service")]
        for field, value in cases:
            self.docker.info = {**deepcopy(original), field: value}
            with self.subTest(field=field), self.assertRaises(runtime.ControllerDenied):
                self.controller.handle(self.request("cleanup"))
        self.docker.info = deepcopy(original)
        self.docker.info["Id"] = "b" * 64
        inspect = self.docker.inspect
        self.docker.inspect = lambda target: deepcopy(self.docker.info)
        try:
            with self.assertRaisesRegex(runtime.ControllerDenied, "OWNERSHIP"):
                self.controller.handle(self.request("cleanup"))
        finally:
            self.docker.inspect = inspect
        self.docker.info = deepcopy(original)
        self.docker.info["Config"]["Labels"]["cdo.runtime.workpackage"] = str(uuid.uuid4())
        with self.assertRaisesRegex(runtime.ControllerDenied, "OWNERSHIP"):
            self.controller.handle(self.request("cancel"))
        self.assertFalse(any(op in {"remove", "stop"} for op, _ in self.docker.calls))

    def test_negative_runtime_profile_matrix(self):
        self.create()
        original = deepcopy(self.docker.info)
        updates = [("Privileged", True), ("CapAdd", ["SYS_ADMIN"]), ("CapDrop", []),
                   ("SecurityOpt", ["seccomp=unconfined"]), ("NetworkMode", "host"),
                   ("Memory", 0), ("MemorySwap", -1), ("NanoCpus", 0), ("PidsLimit", -1),
                   ("ReadonlyRootfs", False), ("PidMode", "host"), ("IpcMode", "host"),
                   ("Devices", [{"PathOnHost": "/dev/sda"}]), ("ExtraHosts", ["host:host-gateway"]),
                   ("Tmpfs", {}), ("LogConfig", {"Type": "json-file", "Config": {}})]
        for field, value in updates:
            self.docker.info = deepcopy(original)
            self.docker.info["HostConfig"][field] = value
            with self.subTest(field=field), self.assertRaises(runtime.ControllerDenied):
                self.controller.handle(self.request("cleanup"))
        self.assertFalse(any(op in {"remove", "stop"} for op, _ in self.docker.calls))

    def test_arbitrary_mounts_entrypoint_and_network_attachment_denied(self):
        self.create()
        original = deepcopy(self.docker.info)
        mutations = [lambda v: v["Config"].update(Cmd=["sh"]),
                     lambda v: v["Config"].update(Entrypoint=["sh"]),
                     lambda v: v["Mounts"].append({"Type": "bind", "Source": "/var/run/docker.sock", "Destination": "/sock", "RW": False}),
                     lambda v: v["Mounts"][0].update(RW=True),
                     lambda v: v["NetworkSettings"]["Networks"].update(bridge={})]
        for mutation in mutations:
            self.docker.info = deepcopy(original)
            mutation(self.docker.info)
            with self.assertRaises(runtime.ControllerDenied):
                self.controller.handle(self.request("cleanup"))
        self.assertFalse(any(op == "remove" for op, _ in self.docker.calls))

    def test_unknown_latched_and_owned_cleanup_requires_positive_observation(self):
        self.create()
        self.docker.unknown = True
        with self.assertRaises(runtime.RuntimeUnknown):
            self.controller.handle(self.request("inspect"))
        with self.assertRaises(runtime.ControllerDenied):
            self.controller.handle(self.request("start"))
        request = self.request("cleanup")
        with self.assertRaises(runtime.RuntimeUnknown):
            self.controller.handle(request)
        self.docker.unknown = False
        with self.assertRaisesRegex(runtime.ControllerDenied, "REPLAYED"):
            self.controller.handle(request)
        result = self.controller.handle(self.request("cleanup"))
        self.assertTrue(result["invalidated"])
        self.assertEqual(result["status"], "CLEANED")

    def test_unknown_registered_cleanup_never_asserts_absence(self):
        self.docker.unknown = True
        with self.assertRaises(runtime.RuntimeUnknown):
            self.controller.handle(self.request("cleanup"))

    def test_partial_create_crash_recovery_checks_exact_name_ownership(self):
        original = self.docker.create
        def partial(arguments):
            original(arguments)
            raise runtime.RuntimeUnknown()
        self.docker.create = partial
        with self.assertRaises(runtime.RuntimeUnknown):
            self.create()
        self.assertIsNotNone(self.docker.info)
        result = self.controller.handle(self.request("cleanup"))
        self.assertEqual(result["status"], "CLEANED")
        self.assertIsNone(self.docker.info)

    def test_failed_create_cannot_remove_other_controller_container(self):
        self.create()
        other_root = self.root / "other-host-state"
        other_root.mkdir(mode=0o700)
        other = runtime.RuntimeController(replace(self.config, state_root=other_root),
                                          [self.registration], self.docker, clock=lambda: self.now)
        original_inspect = self.docker.inspect
        original_create = self.docker.create
        preflight = True
        def race_inspect(target):
            nonlocal preflight
            if preflight:
                preflight = False
                return None
            return original_inspect(target)
        def failed_create(arguments):
            raise runtime.RuntimeUnknown()
        try:
            self.assertNotEqual(other.labels(self.registration)["cdo.runtime.owner"],
                                self.controller.labels(self.registration)["cdo.runtime.owner"])
            self.docker.inspect = race_inspect
            self.docker.create = failed_create
            with self.assertRaises(runtime.RuntimeUnknown):
                other.handle(other.request(self.registration.execution_id, "create", self.now + 30))
            self.docker.inspect = original_inspect
            with self.assertRaisesRegex(runtime.ControllerDenied, "OWNERSHIP"):
                other.handle(other.request(self.registration.execution_id, "cleanup", self.now + 30))
            self.assertIsNotNone(self.docker.info)
            self.assertFalse(any(op == "remove" for op, _ in self.docker.calls))
        finally:
            self.docker.inspect = original_inspect
            self.docker.create = original_create
            other.close()

    def test_malformed_inspection_and_cleanup_failure_remain_unknown(self):
        self.create()
        self.docker.info.pop("State")
        with self.assertRaises(runtime.RuntimeUnknown):
            self.controller.handle(self.request("cleanup"))
        self.assertFalse(any(op == "remove" for op, _ in self.docker.calls))

    def test_expired_execution_denies_launch_allows_signed_cleanup(self):
        self.create()
        self.now += 301
        with self.assertRaisesRegex(runtime.ControllerDenied, "EXECUTION_EXPIRED"):
            self.controller.handle(self.request("start"))
        result = self.controller.handle(self.request("cleanup"))
        self.assertEqual(result["status"], "CLEANED")

    def test_watchdog_reaps_expiry_without_client_request(self):
        self.create()
        self.controller.handle(self.request("start"))
        self.now += 301
        deadline = time.monotonic() + 2
        while self.docker.info is not None and time.monotonic() < deadline:
            time.sleep(.02)
        self.assertIsNone(self.docker.info)
        self.assertTrue(any(op == "remove" for op, _ in self.docker.calls))

    def test_watchdog_required_and_failure_blocks_start(self):
        self.create()
        self.controller.close()
        with self.assertRaisesRegex(runtime.ControllerDenied, "WATCHDOG"):
            self.controller.handle(self.request("start"))

    def test_corrupt_missing_and_symlink_ledgers_fail_closed(self):
        self.create()
        file = self.state / self.registration.execution_id / "state.json"
        original = file.read_bytes()
        for invalid in (b'{', b'{}', b'{"nonces":true}'):
            file.write_bytes(invalid)
            with self.assertRaises(runtime.ControllerDenied):
                self.controller.handle(self.request("create"))
        file.write_bytes(original)
        file.unlink()
        with self.assertRaisesRegex(runtime.ControllerDenied, "LEDGER_MISSING"):
            self.create()
        file.symlink_to(self.root / "nonexistent")
        with self.assertRaises(runtime.ControllerDenied):
            self.create()
        self.assertEqual(sum(op == "create" for op, _ in self.docker.calls), 1)

    def test_changed_registration_rejected_after_restart(self):
        self.create()
        for changed in (replace(self.registration, secret=os.urandom(32)),
                        replace(self.registration, scope_sha256="f" * 64),
                        replace(self.registration, workpackage_id=str(uuid.uuid4()))):
            other = runtime.RuntimeController(self.config, [changed], self.docker, clock=lambda: self.now)
            try:
                with self.assertRaisesRegex(runtime.ControllerDenied, "LEDGER_INVALID"):
                    other.handle(other.request(changed.execution_id, "inspect", self.now + 30))
            finally:
                other.close()

    def test_nonce_capacity_reserves_termination_even_after_unknown(self):
        self.create()
        file = self.state / self.registration.execution_id / "state.json"
        state = json.loads(file.read_bytes())
        state["nonces"] = runtime.MAX_NONCES
        file.write_bytes(runtime.canonical(state))
        with self.assertRaisesRegex(runtime.ControllerDenied, "COUNT_EXCEEDED"):
            self.controller.handle(self.request("inspect"))
        self.docker.remove_unknown = True
        with self.assertRaises(runtime.RuntimeUnknown):
            self.controller.handle(self.request("cancel"))
        self.docker.remove_unknown = False
        self.assertEqual(self.controller.handle(self.request("cleanup"))["status"], "CLEANED")

    def test_diagnostics_oversize_invalidates_execution(self):
        self.create()
        self.docker.output = "x" * 2049
        with self.assertRaisesRegex(runtime.RuntimeUnknown, "DIAGNOSTICS_LIMIT"):
            self.controller.handle(self.request("diagnostics"))
        with self.assertRaises(runtime.ControllerDenied):
            self.controller.handle(self.request("start"))

    def test_helper_source_links_mutability_and_content_pin(self):
        self.helper.chmod(0o644)
        with self.assertRaisesRegex(runtime.ControllerDenied, "MUTABLE"):
            self.new_controller(self.docker)
        self.helper.write_bytes(b"print('unapproved')")
        self.helper.chmod(0o444)
        with self.assertRaisesRegex(runtime.ControllerDenied, "PIN"):
            self.new_controller(self.docker)
        self.helper.chmod(0o644)
        self.helper.write_bytes(runtime.COMMAND_HELPER)
        self.helper.chmod(0o444)
        self.source.chmod(0o755)
        with self.assertRaisesRegex(runtime.ControllerDenied, "MUTABLE"):
            self.new_controller(self.docker)
        self.source.chmod(0o555)

    def test_sdk_has_no_source_or_auth_mount_and_exact_helper(self):
        helper = self.root / "sdk-helper.py"
        helper.write_bytes(runtime.SDK_HELPER)
        helper.chmod(0o444)
        registration = replace(self.registration, execution_id=str(uuid.uuid4()), recipe="sdk-runtime",
                               image_id=self.config.sdk_image_id, source_root=None, source_sha256=None,
                               helper_path=helper, helper_sha256=runtime.helper_digest("sdk-runtime"))
        docker = FakeDocker()
        controller = runtime.RuntimeController(self.config, [registration], docker, clock=lambda: self.now)
        try:
            arguments = controller.create_arguments(registration)
            self.assertEqual(controller.mounts(registration), {"/reference/runtime.py": str(helper)})
            self.assertIn("none", arguments)
            self.assertNotIn(str(self.source), " ".join(arguments))
            with self.assertRaisesRegex(runtime.ControllerDenied, "SDK_SOURCE"):
                runtime.RuntimeController(self.config, [replace(registration, source_root=self.source, source_sha256=runtime.source_digest(self.source))], docker, clock=lambda: self.now)
        finally:
            controller.close()

    def test_protocol_digest_and_controller_file_identity(self):
        self.assertEqual(runtime.protocol_sha256(), hashlib.sha256(runtime.canonical(runtime.PROTOCOL_DESCRIPTOR)).hexdigest())
        self.assertEqual(runtime.controller_identity(), hashlib.sha256(Path(runtime.__file__).read_bytes()).hexdigest())
        self.assertFalse(runtime.PROTOCOL_DESCRIPTOR["generic_exec"])
        self.assertFalse(runtime.PROTOCOL_DESCRIPTOR["daemon_admin"])
        self.assertEqual(runtime.PROTOCOL_DESCRIPTOR["production_admission"], "DENIED")

    def test_helper_checks_mounts_and_socket_access_not_image_directories(self):
        helper = compile(runtime.COMMON_HELPER, "finite-common-helper", "exec")
        status = "Seccomp: 2\nNoNewPrivs: 1\nCapEff: 0000\nCapBnd: 0000\n"
        mountinfo = "1 0 0:1 / / ro - overlay overlay ro\n2 1 0:2 / /workspace ro - ext4 fixture ro\n"
        def read(path):
            return status if str(path) == "/proc/self/status" else mountinfo
        with patch.object(Path, "read_text", read), patch.object(runtime.os, "getuid", return_value=100), \
                patch.object(runtime.os, "getgid", return_value=101), \
                patch.object(runtime.os, "stat", side_effect=FileNotFoundError), \
                patch.dict(runtime.os.environ, {"CDO_RUNTIME_DEADLINE": str(int(time.time()) + 60)}):
            exec(helper, {})
            with patch.object(runtime.os, "stat", side_effect=PermissionError):
                exec(helper, {})
            with patch.object(runtime.os, "stat", return_value=object()), self.assertRaisesRegex(AssertionError, "DOCKER_SOCKET_VISIBLE"):
                exec(helper, {})
            mountinfo += "3 1 0:3 / /projects ro - ext4 host ro\n"
            with self.assertRaises(AssertionError):
                exec(helper, {})

    def test_readonly_tree_links_secrets_and_private_state_are_denied(self):
        self.source.chmod(0o755)
        link = self.source / "linked.go"
        link.symlink_to(self.source / "fixture_test.go")
        self.source.chmod(0o555)
        with self.assertRaises(runtime.ControllerDenied):
            runtime.source_digest(self.source)
        self.source.chmod(0o755)
        link.unlink()
        os.link(self.source / "fixture_test.go", link)
        self.source.chmod(0o555)
        with self.assertRaises(runtime.ControllerDenied):
            runtime.source_digest(self.source)
        self.source.chmod(0o755)
        link.unlink()
        secret = self.source / ".env.runtime"
        secret.write_bytes(b"inert placeholder")
        secret.chmod(0o444)
        self.source.chmod(0o555)
        with self.assertRaises(runtime.ControllerDenied):
            runtime.source_digest(self.source)

    def test_unknown_watchdog_health_blocks_new_execution(self):
        self.create()
        self.docker.unknown = True
        self.now += 301
        deadline = time.monotonic() + 2
        while self.controller._watchdog_error is None and time.monotonic() < deadline:
            time.sleep(.02)
        self.assertEqual(self.controller._watchdog_error, "WATCHDOG_UNKNOWN")
        self.docker.unknown = False
        self.assertEqual(self.controller.handle(self.request("cleanup"))["status"], "CLEANED")

    def test_registered_read_does_not_hide_name_collision(self):
        self.docker.create(self.controller.create_arguments(self.registration))
        with self.assertRaisesRegex(runtime.ControllerDenied, "COLLISION"):
            self.controller.handle(self.request("inspect"))
        with self.assertRaises(runtime.ControllerDenied):
            self.controller.handle(self.request("cleanup"))
        self.assertFalse(any(op == "remove" for op, _ in self.docker.calls))


class DockerAdapterTests(unittest.TestCase):
    def setUp(self):
        self.adapter = object.__new__(runtime.DockerCLI)
        self.adapter.expected_daemon_id = "trusted-daemon-id"
        self.calls = []

    def responses(self, values):
        pending = iter(values)
        def run(arguments):
            self.calls.append(arguments)
            return next(pending)
        self.adapter._run = run

    def test_only_exact_absence_error_is_absent(self):
        target = "a" * 64
        self.responses([(0, b"trusted-daemon-id\n", b""), (1, b"", b"Error: No such container: " + target.encode())])
        self.assertIsNone(self.adapter.inspect(target))
        self.responses([(0, b"trusted-daemon-id\n", b""), (1, b"[]\n", b"Error: No such container: " + target.encode())])
        self.assertIsNone(self.adapter.inspect(target))
        self.responses([(0, b"trusted-daemon-id\n", b""), (1, b"[]\n", b"Error response from daemon: No such container: " + target.encode() + b"\n")])
        self.assertIsNone(self.adapter.inspect(target))
        self.responses([(0, b"trusted-daemon-id\n", b""), (1, b"[{}]\n", b"Error: No such container: " + target.encode())])
        with self.assertRaises(runtime.RuntimeUnknown):
            self.adapter.inspect(target)
        for error in (b"Cannot connect", b"daemon error: No such container: " + target.encode(),
                      b"Error: No such container: unrelated", b"UNKNOWN"):
            self.responses([(0, b"trusted-daemon-id", b""), (1, b"", error)])
            with self.assertRaises(runtime.RuntimeUnknown):
                self.adapter.inspect(target)

    def test_wrong_daemon_id_and_unapproved_targets_fail_closed(self):
        self.responses([(0, b"different-daemon", b"")])
        with self.assertRaisesRegex(runtime.RuntimeUnknown, "DAEMON_IDENTITY"):
            self.adapter.inspect("a" * 64)
        for target in ("product-container", "--help", "/var/run/docker.sock", "a" * 63):
            with self.assertRaises(runtime.ControllerDenied):
                self.adapter.remove(target)

    def test_malformed_inspection_is_unknown(self):
        for output in (b"{}", b"[]", b"[{},{}]", b"not-json", b"\xff"):
            self.responses([(0, b"trusted-daemon-id", b""), (0, output, b"")])
            with self.assertRaises(runtime.RuntimeUnknown):
                self.adapter.inspect("a" * 64)

    def test_diagnostics_log_bytes_capped(self):
        self.responses([(0, b"trusted-daemon-id", b""), (0, b"x" * 2049, b"")])
        with self.assertRaisesRegex(runtime.RuntimeUnknown, "DIAGNOSTICS_LIMIT"):
            self.adapter.logs("a" * 64)
        self.assertEqual(self.calls[-1], ["logs", "--tail", "32", "a" * 64])

    def test_successful_logs_include_stderr_and_share_byte_limit(self):
        self.responses([(0, b"trusted-daemon-id", b""), (0, b"stdout\n", b"stderr\n")])
        self.assertEqual(self.adapter.logs("a" * 64), "stdout\nstderr\n")
        self.responses([(0, b"trusted-daemon-id", b""), (0, b"x" * 1024, b"y" * 1025)])
        with self.assertRaisesRegex(runtime.RuntimeUnknown, "DIAGNOSTICS_LIMIT"):
            self.adapter.logs("a" * 64)
        self.responses([(0, b"trusted-daemon-id", b""), (1, b"bounded", b"daemon error")])
        with self.assertRaisesRegex(runtime.RuntimeUnknown, "DIAGNOSTICS_UNKNOWN"):
            self.adapter.logs("a" * 64)

    def test_executable_and_endpoint_trust_checks_without_execution(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            executable = root / "docker-client"
            executable.write_text("inert executable fixture")
            executable.chmod(0o555)
            socket_path = root / "host-daemon-endpoint"
            socket_path.write_bytes(b"inert socket fixture")
            real_stat = Path.stat
            def trusted_socket(path, *args, **kwargs):
                if path == socket_path:
                    return SimpleNamespace(st_mode=stat.S_IFSOCK | 0o600, st_uid=os.geteuid())
                return real_stat(path, *args, **kwargs)
            with patch.object(Path, "stat", trusted_socket):
                runtime.DockerCLI(executable, "unix://" + str(socket_path), "trusted-daemon-id")
                executable.chmod(0o777)
                with self.assertRaisesRegex(runtime.ControllerDenied, "EXECUTABLE"):
                    runtime.DockerCLI(executable, "unix://" + str(socket_path), "trusted-daemon-id")
                executable.chmod(0o555)
                alias = root / "alias"
                alias.symlink_to(executable)
                with self.assertRaises(runtime.ControllerDenied):
                    runtime.DockerCLI(alias, "unix://" + str(socket_path), "trusted-daemon-id")
                with self.assertRaises(runtime.ControllerDenied):
                    runtime.DockerCLI(executable, "tcp://localhost:2375", "trusted-daemon-id")


if __name__ == "__main__":
    unittest.main()
