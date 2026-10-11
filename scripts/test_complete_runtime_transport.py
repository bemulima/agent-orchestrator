"""Deterministic harness regressions; these are not runtime certification.

The module is imported without starting Provider or invoking Docker. Source
route checks ensure the tested helpers are reachable from the dispatcher;
runtime operations and the lifecycle analyzer are mocked explicitly.
"""
import argparse
import ast
import hashlib
import hmac
import importlib.util
import io
import json
import pathlib
import subprocess
import tempfile
import types
import unittest
from unittest import mock


SCRIPT = pathlib.Path(__file__).with_name("probe-complete-runtime-transport.py")
SPEC = importlib.util.spec_from_file_location("complete_runtime_transport", SCRIPT)
PROBE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PROBE)

REQUIRED_CASES = (
    "four-roles", "adversarial", "activity-cancel", "workflow-cancel",
    "active-cancel", "before-publication", "verification", "partial-publication",
    "timeout", "controller-kill", "controller-restart", "backend-kill",
    "broker-interruption", "lease-expiry", "runtime-unknown",
    "transport-disconnect", "stale-result", "stale-publication", "security-failure",
)


class CompleteRouteRegressionTest(unittest.TestCase):
    def setUp(self):
        self.source = SCRIPT.read_text()
        self.tree = ast.parse(self.source)

    def function(self, name):
        return next(node for node in self.tree.body if isinstance(node, ast.FunctionDef) and node.name == name)

    def call_strings(self, name):
        return {
            value.value
            for call in ast.walk(self.function(name)) if isinstance(call, ast.Call)
            for value in ast.walk(call) if isinstance(value, ast.Constant) and isinstance(value.value, str)
        }

    def call_names(self, name):
        definitions = {node.name: node for node in self.tree.body if isinstance(node, ast.FunctionDef)}
        pending, visited, result = [name], set(), set()
        while pending:
            current = pending.pop()
            if current in visited:
                continue
            visited.add(current)
            calls = {call.func.id for call in ast.walk(definitions[current])
                     if isinstance(call, ast.Call) and isinstance(call.func, ast.Name)}
            result.update(calls)
            pending.extend(calls & definitions.keys() - visited)
        return result

    def main_arguments(self, case):
        return [str(SCRIPT), "--case", case, "--output", "/synthetic/output",
                "--runner-image", "sha256:" + "1" * 64,
                "--sdk-image", "sha256:" + "2" * 64,
                "--command-image", "sha256:" + "3" * 64,
                "--source-manifest", "4" * 64,
                "--go-bundle", "/synthetic/dependency", "--go-bundle-manifest-sha256", "5" * 64, "--driver", "/synthetic/driver"]

    def test_all_has_explicit_full_case_dispatch(self):
        expanded = getattr(PROBE, "expanded_cases", None)
        self.assertTrue(callable(expanded), "--all must dispatch full runtime cases, not only four roles")
        self.assertEqual(tuple(expanded("all")), REQUIRED_CASES)
        for case in REQUIRED_CASES:
            self.assertEqual(tuple(expanded(case)), (case,))

    def test_adversarial_case_launches_backend_probe(self):
        self.assertIn("launch_backend_adversarial", self.call_names("main_probe"),
                      "adversarial must invoke the authenticated backend probe inside the backend OCI boundary")

    def test_backend_kill_runs_exact_history_observer(self):
        calls = self.call_strings("main_probe")
        self.assertFalse("BACKEND_INTERRUPTION_REQUIRES_EXACT_RUN_HISTORY_OBSERVER" in calls,
                         "backend-kill must complete through a read-only exact-run history observer")
        self.assertIn("launch_terminal_observer", self.call_names("main_probe"))

    def test_lease_expiry_stops_only_bound_backend_renewal(self):
        self.assertIn("pause_backend_for_lease_expiry", self.call_names("main_probe"),
                      "lease-expiry must stop renewals from the exact owned backend")

    def test_main_route_has_implemented_terminal_artifact_finalizer(self):
        self.assertTrue(callable(getattr(PROBE, "finalize_case", None)),
                        "main route must implement terminal/action evidence before invoking the analyzer")
        self.assertIn("finalize_case", self.call_names("main_probe"))

    def test_cli_accepts_every_required_full_route_case(self):
        for case in REQUIRED_CASES:
            with self.subTest(case=case), mock.patch.object(PROBE, "main_probe", return_value=0) as run, \
                    mock.patch.object(PROBE.sys, "argv", self.main_arguments(case)), \
                    mock.patch.object(PROBE.sys, "stderr", io.StringIO()):
                try:
                    result = PROBE.main()
                except SystemExit as error:
                    self.fail("required complete-route case rejected: " + case + ": " + str(error))
                self.assertEqual(result, 0)
                self.assertEqual(run.call_args.args[0].case, case)


class FakeDocker:
    """Records narrow Docker calls; it never invokes a process or a runtime."""
    def __init__(self, full_id="a" * 64, image="sha256:" + "1" * 64, on_copy=None, start_code=0):
        self.full_id, self.image, self.actions, self.paused = full_id, image, [], False
        self.on_copy, self.start_code = on_copy, start_code
        self.running = True

    def run(self, argv, **kwargs):
        self.actions.append((list(argv), kwargs))
        if argv[0] == "create":
            return subprocess.CompletedProcess(argv, 0, self.full_id + "\n", "")
        if argv[0] == "inspect":
            value = [{"Id": self.full_id, "Image": self.image,
                      "State": {"Running": self.running, "Paused": self.paused, "ExitCode": 0}, "Mounts": []}]
            return subprocess.CompletedProcess(argv, 0, json.dumps(value), "")
        if argv[0] == "pause":
            self.paused = True
        elif argv[0] == "unpause":
            self.paused = False
        elif argv[0] == "kill":
            self.running = False
        elif argv[0] == "cp" and self.on_copy is not None:
            self.on_copy(argv)
        elif argv[0] == "start":
            return subprocess.CompletedProcess(argv, self.start_code, "", "")
        return subprocess.CompletedProcess(argv, 0, "", "")

    def cleanup(self, full_id, image):
        self.actions.append((["cleanup", full_id, image], {}))


class PureTemporaryTest(unittest.TestCase):
    def setUp(self):
        cache = SCRIPT.parents[1] / ".cache"
        cache.mkdir(exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(prefix="complete-transport-harness-unit-", dir=cache)
        self.addCleanup(self.temporary.cleanup)
        self.root = pathlib.Path(self.temporary.name)

    def helper(self, name):
        value = getattr(PROBE, name, None)
        self.assertTrue(callable(value), "missing complete-route helper: " + name)
        return value

    @staticmethod
    def option_values(argv, option):
        return [argv[index + 1] for index, value in enumerate(argv[:-1]) if value == option]


class AllCaseDispatchTest(PureTemporaryTest):
    def test_all_runs_each_case_once_in_declared_order_with_separate_outputs(self):
        output = self.root / "all"
        seen = []

        def runner(args):
            seen.append((args.case, args.output))
            self.assertEqual(args.source_manifest, "4" * 64)
            child = pathlib.Path(args.output)
            child.mkdir(mode=0o700)
            PROBE.save(child / "complete-transport-evidence.json", {"pass": True})
            return 0

        args = argparse.Namespace(case="all", output=str(output), source_manifest="4" * 64)
        self.assertEqual(self.helper("run_all")(args, runner), 0)
        self.assertEqual([case for case, _ in seen], list(REQUIRED_CASES))
        self.assertEqual([path for _, path in seen], [str(output / case) for case in REQUIRED_CASES])
        self.assertEqual(args.case, "all")
        result = PROBE.read(output / "complete-transport-evidence.json")
        self.assertIs(result["pass"], True)
        self.assertEqual(list(result["cases"]), sorted(REQUIRED_CASES))
        self.assertEqual(result["required_cases"], list(REQUIRED_CASES))
        self.assertEqual(result["budget"], "20/enforce")
        self.assertEqual(result["production_admission"], "DENIED")
        self.assertEqual(result["real_model_calls"], 0)

    def test_runtime_denial_stops_all_without_retry_or_other_case(self):
        seen = []

        def runner(args):
            seen.append(args.case)
            raise RuntimeError("synthetic concrete runtime denial")

        output = self.root / "denied"
        args = argparse.Namespace(case="all", output=str(output))
        self.assertEqual(self.helper("run_all")(args, runner), 1)
        self.assertEqual(seen, ["four-roles"])
        result = PROBE.read(output / "complete-transport-evidence.json")
        self.assertIs(result["pass"], False)
        self.assertIs(result["full_certification_claimed"], False)
        self.assertEqual(result["production_admission"], "DENIED")
        self.assertEqual(result["budget"], "20/enforce")
        self.assertEqual(result["cases"]["four-roles"]["code"], "synthetic concrete runtime denial")

    def test_false_case_evidence_stops_all_even_when_exit_zero(self):
        seen = []

        def runner(args):
            seen.append(args.case)
            child = pathlib.Path(args.output)
            child.mkdir(mode=0o700)
            PROBE.save(child / "complete-transport-evidence.json", {"pass": False})
            return 0

        self.assertEqual(self.helper("run_all")(argparse.Namespace(output=str(self.root / "failed")), runner), 1)
        self.assertEqual(seen, ["four-roles"])


class BackendLaunchTest(PureTemporaryTest):
    def setUp(self):
        super().setUp()
        self.image = "sha256:" + "1" * 64
        self.evidence = self.root / "evidence"
        self.evidence.mkdir(mode=0o700)
        self.launcher = self.root / "launcher.py"
        self.launcher.write_text("# immutable synthetic harness fixture\n")
        self.launcher.chmod(0o400)
        self.driver = self.root / "driver"
        self.driver.write_text("synthetic immutable driver fixture\n")
        self.driver.chmod(0o400)
        self.start = {"workflow_id": "synthetic-workflow", "workflow_run_id": "exact-original-run"}
        PROBE.save(self.evidence / "workflow-start.json", self.start)

    def recipe(self):
        return PROBE.private_backend_recipe(self.image,
            ((self.root / "backend-key.json", "/verification/backend-key.json"),
             (self.root / "request.json", "/verification/request.json")),
            {"CDO_RUNTIME_BACKEND_CREDENTIALS": "/verification/backend-key.json",
             "CDO_SANDBOX_EXECUTION_MODE": "hardened-certification",
             "CODEX_SOL_MAX_RUNS_5H": "20", "CODEX_BUDGET_MODE": "enforce"}, "synthetic original bootstrap")

    def assert_bounded(self, argv):
        self.assertIn("--read-only", argv)
        self.assertEqual(self.option_values(argv, "--cap-drop"), ["ALL"])
        self.assertEqual(self.option_values(argv, "--security-opt"), ["no-new-privileges=true", "seccomp=builtin"])
        self.assertEqual(self.option_values(argv, "--pids-limit"), ["128"])
        self.assertEqual(self.option_values(argv, "--memory"), ["512m"])
        self.assertEqual(self.option_values(argv, "--memory-swap"), ["512m"])
        self.assertEqual(self.option_values(argv, "--cpus"), ["2"])
        for mount in self.option_values(argv, "--mount"):
            self.assertTrue(mount.endswith(",readonly"))
            self.assertNotIn("docker.sock", mount)
        self.assertNotIn("--privileged", argv)
        evidence_tmpfs = self.option_values(argv, "--tmpfs")[0]
        for required in ("size=33554432", "nr_inodes=256", "noexec", "mode=0700", "uid=", "gid="):
            self.assertIn(required, evidence_tmpfs)

    def test_private_evidence_uses_current_host_credential_owner(self):
        with mock.patch.object(PROBE.os, "geteuid", return_value=501), \
                mock.patch.object(PROBE.os, "getegid", return_value=20):
            argv = self.recipe()
        self.assertEqual(self.option_values(argv, "--user"), ["501:20"])
        evidence_tmpfs = self.option_values(argv, "--tmpfs")[0]
        self.assertIn("uid=501", evidence_tmpfs)
        self.assertIn("gid=20", evidence_tmpfs)

    def test_private_recipe_rejects_mount_separator_injection(self):
        with self.assertRaisesRegex(RuntimeError, "PROBE_MOUNT_PATH_DENIED"):
            PROBE.private_backend_recipe(self.image, (("/synthetic/path,readonly=false", "/verification/key"),), {}, "fixture")

    def test_adversarial_launch_preserves_bounds_and_uses_actual_backend_command(self):
        report = {"pass": True, "historical_finite_128_counted": False, "cases": [{"denied": True}]}

        def copy(argv):
            self.assertEqual(argv[1], "a" * 64 + ":/execution/evidence/agent-adversarial.json")
            PROBE.save(pathlib.Path(argv[2]), report)

        docker, backend_ids = FakeDocker(image=self.image, on_copy=copy), ["b" * 64]
        base = self.recipe()
        original = list(base)
        result = self.helper("launch_backend_adversarial")(docker, base, self.image, self.launcher,
                                                           self.evidence, backend_ids)
        self.assertEqual(result, report)
        self.assertEqual(backend_ids, ["b" * 64])
        created = next(argv for argv, _ in docker.actions if argv[0] == "create")
        self.assert_bounded(created)
        self.assertIn("--backend-adversarial", created[-1])
        self.assertIn("/verification/backend-key.json", created[-1])
        self.assertIn("/verification/request.json", created[-1])
        self.assertIn("/execution/evidence/agent-adversarial.json", created[-1])
        self.assertIn("type=bind,src=" + str(self.launcher) + ",dst=/verification/harness.py,readonly",
                      self.option_values(created, "--mount"))
        self.assertEqual(base, original, "launch must not mutate the approved base argv")
        self.assertEqual(docker.actions[-1][0], ["cleanup", docker.full_id, self.image])

    def test_adversarial_failed_report_is_typed_and_cleans_only_owned_backend(self):
        def copy(argv):
            PROBE.save(pathlib.Path(argv[2]), {"pass": False, "historical_finite_128_counted": False})

        docker, backend_ids = FakeDocker(image=self.image, on_copy=copy), []
        with self.assertRaisesRegex(RuntimeError, "COMPLETE_ROUTE_ADVERSARIAL_FAILED"):
            self.helper("launch_backend_adversarial")(docker, self.recipe(), self.image, self.launcher,
                                                      self.evidence, backend_ids)
        self.assertEqual(backend_ids, [])
        self.assertEqual(docker.actions[-1][0], ["cleanup", docker.full_id, self.image])

    def test_terminal_observer_is_key_free_read_only_and_bound_to_original_run(self):
        def copy(argv):
            self.assertEqual(argv[1], "a" * 64 + ":/execution/evidence/.")
            self.assertEqual(pathlib.Path(argv[2]), self.evidence)
            PROBE.save(self.evidence / "observer-complete.json", {**self.start, "started_execution": False})

        docker, backend_ids = FakeDocker(image=self.image, on_copy=copy), []
        result = self.helper("launch_terminal_observer")(docker, self.image, self.driver, self.evidence, backend_ids)
        self.assertEqual(result, self.evidence)
        self.assertEqual(backend_ids, [])
        created = next(argv for argv, _ in docker.actions if argv[0] == "create")
        self.assert_bounded(created)
        self.assertIn("--observe-terminal", created[-1])
        self.assertIn("/verification/original", created[-1])
        mounts = self.option_values(created, "--mount")
        self.assertEqual(mounts, ["type=bind,src=" + str(self.driver) + ",dst=/verification/driver,readonly",
                                  "type=bind,src=" + str(self.evidence) + ",dst=/verification/original,readonly"])
        environment = self.option_values(created, "--env")
        self.assertIn("CDO_WORKFLOW_ID=" + self.start["workflow_id"], environment)
        self.assertFalse(any("CREDENTIAL" in value or "CDO_RUNNER_COMMAND=" in value for value in environment))
        self.assertNotIn("backend-key", " ".join(created))
        self.assertEqual(docker.actions[-1][0], ["cleanup", docker.full_id, self.image])

    def test_observer_rejects_other_run_and_still_cleans(self):
        def copy(argv):
            PROBE.save(self.evidence / "observer-complete.json", {**self.start, "workflow_run_id": "other-run", "started_execution": False})

        docker, backend_ids = FakeDocker(image=self.image, on_copy=copy), []
        with self.assertRaisesRegex(RuntimeError, "EXACT_RUN_OBSERVER_FAILED"):
            self.helper("launch_terminal_observer")(docker, self.image, self.driver, self.evidence, backend_ids)
        self.assertEqual(backend_ids, [])
        self.assertEqual(docker.actions[-1][0], ["cleanup", docker.full_id, self.image])

    def test_observer_rejects_started_execution(self):
        def copy(argv):
            PROBE.save(self.evidence / "observer-complete.json", {**self.start, "started_execution": True})

        docker = FakeDocker(image=self.image, on_copy=copy)
        with self.assertRaisesRegex(RuntimeError, "EXACT_RUN_OBSERVER_FAILED"):
            self.helper("launch_terminal_observer")(docker, self.image, self.driver, self.evidence, [])

    def test_observer_missing_original_run_cannot_create_container(self):
        PROBE.save(self.evidence / "workflow-start.json", {"workflow_id": "synthetic-workflow"})
        docker = FakeDocker(image=self.image)
        with self.assertRaisesRegex(RuntimeError, "EXACT_ORIGINAL_WORKFLOW_IDENTITY_REQUIRED"):
            self.helper("launch_terminal_observer")(docker, self.image, self.driver, self.evidence, [])
        self.assertEqual(docker.actions, [])

    def test_observer_other_image_cannot_start(self):
        docker, backend_ids = FakeDocker(image="sha256:" + "9" * 64), []
        with self.assertRaisesRegex(RuntimeError, "PROBE_BACKEND_OWNERSHIP_MISMATCH"):
            self.helper("launch_terminal_observer")(docker, self.image, self.driver, self.evidence, backend_ids)
        self.assertFalse(any(argv[0] == "start" for argv, _ in docker.actions))
        self.assertEqual(backend_ids, [])


class LeaseExpiryControlTest(unittest.TestCase):
    def helper(self):
        value = getattr(PROBE, "pause_backend_for_lease_expiry", None)
        self.assertTrue(callable(value), "lease expiry needs a bound backend pause and durable watchdog proof")
        return value

    def test_waits_for_typed_persisted_expiry_and_cleanup_before_unpause(self):
        docker = FakeDocker()
        pending = {"invalid_reason": "", "cleanup_proven": False, "lease_expires_at": 1000.0}
        expired_without_cleanup = {"invalid_reason": "EXECUTION_LEASE_EXPIRED", "cleanup_proven": False}
        expired = {"invalid_reason": "EXECUTION_LEASE_EXPIRED", "cleanup_proven": True}
        reader = mock.Mock(side_effect=[pending, pending, expired_without_cleanup, expired])
        proof = {}

        def wait(predicate, seconds, code):
            self.assertTrue(docker.paused)
            self.assertGreater(seconds, 0)
            self.assertFalse(predicate(), "active state is not lease-expiry proof")
            self.assertFalse(predicate(), "expiry without cleanup is not terminal proof")
            result = predicate()
            self.assertTrue(result)
            self.assertTrue(docker.paused)
            return result

        result = self.helper()(docker, docker.full_id, docker.image, reader, wait=wait, proof=proof)
        self.assertEqual(result["invalid_reason"], "EXECUTION_LEASE_EXPIRED")
        self.assertIs(result["cleanup_proven"], True)
        self.assertEqual([args for args, _ in docker.actions if args[0] in {"pause", "unpause"}],
                         [["pause", docker.full_id], ["unpause", docker.full_id]])
        self.assertFalse(docker.paused)
        self.assertEqual(proof, {"authenticated_renewal_stopped": True, "backend_paused_observed": True,
                                 "last_authenticated_lease_expires_at": 1000.0})

    def test_wait_failure_still_unpauses_exact_backend(self):
        docker = FakeDocker()
        wait = mock.Mock(side_effect=RuntimeError("synthetic watchdog timeout"))
        with self.assertRaisesRegex(RuntimeError, "synthetic watchdog timeout"):
            self.helper()(docker, docker.full_id, docker.image, mock.Mock(), wait=wait)
        self.assertEqual([args for args, _ in docker.actions if args[0] in {"pause", "unpause"}],
                         [["pause", docker.full_id], ["unpause", docker.full_id]])

    def test_mismatched_backend_image_cannot_be_paused(self):
        docker = FakeDocker(image="sha256:" + "9" * 64)
        with self.assertRaises(RuntimeError):
            self.helper()(docker, docker.full_id, "sha256:" + "1" * 64, mock.Mock(), wait=mock.Mock())
        self.assertFalse(any(args[0] in {"pause", "unpause"} for args, _ in docker.actions))


class CaseControlTest(PureTemporaryTest):
    def setUp(self):
        super().setUp()
        self.docker = FakeDocker()
        self.context_identity = {"workflow_id": "synthetic-workflow", "workflow_run_id": "original-unit-run",
                                 "activity_id": "3", "activity_attempt": 1}
        self.state = {"binding": {"execution_id": "11111111-1111-4111-8111-111111111111",
                                  "runtime_context": self.context_identity},
                      "invalid_reason": "EXECUTION_LEASE_EXPIRED", "cleanup_proven": True,
                      "lease_expires_at": 1000.0}
        self.context = {"docker": self.docker, "full_id": self.docker.full_id, "image": self.docker.image,
                        "evidence": self.root, "driver": self.root / "driver", "backend_ids": [],
                        "copy_evidence": mock.Mock(return_value=self.root)}

    def perform(self, case):
        with mock.patch.object(PROBE, "exact_state", return_value=self.state):
            return self.helper("perform_case")(case, self.context)

    def test_cancellation_checkpoint_records_bound_identity_and_exact_phase(self):
        cases = {"activity-cancel": "active", "workflow-cancel": "active", "active-cancel": "active",
                 "before-publication": "before-publication", "verification": "verification",
                 "partial-publication": "publication-applied-1"}
        for case, phase in cases.items():
            with self.subTest(case=case):
                self.docker.actions.clear()
                self.assertEqual(self.perform(case), {})
                checkpoint = PROBE.read(self.root / "transport-checkpoint.json")
                self.assertEqual(checkpoint, {"execution_id": self.state["binding"]["execution_id"],
                                              "runtime_context": self.context_identity, "phase": phase})
                commands = [argv for argv, _ in self.docker.actions]
                self.assertEqual(len(commands), 1)
                self.assertEqual(commands[0][:4], ["exec", self.docker.full_id, "python3", "-c"])
                self.assertEqual(commands[0][-1], "open('/execution/evidence/cancel','w').close()")

    def test_backend_kill_waits_for_persisted_lease_then_original_run_observer(self):
        with mock.patch.object(PROBE, "exact_state", return_value=self.state), \
                mock.patch.object(PROBE, "wait_for", side_effect=lambda predicate, seconds, code: predicate()) as wait, \
                mock.patch.object(PROBE, "launch_terminal_observer", return_value=self.root) as observer:
            facts = self.helper("perform_case")("backend-kill", self.context)
        self.assertIs(facts["backend_process_absent"], True)
        self.assertIs(facts["lease_expired"], True)
        wait.assert_called_once()
        observer.assert_called_once_with(self.docker, self.docker.image, self.context["driver"],
                                          self.root, self.context["backend_ids"])
        self.assertEqual([argv for argv, _ in self.docker.actions if argv[0] == "kill"],
                         [["kill", "--signal", "KILL", self.docker.full_id]])
        self.context["copy_evidence"].assert_called_once()

    def test_lease_control_dispatches_only_current_backend_and_signed_state_reader(self):
        def paused(*args, proof):
            proof.update(authenticated_renewal_stopped=True, backend_paused_observed=True,
                         last_authenticated_lease_expires_at=1000.0)
            return self.state

        with mock.patch.object(PROBE, "exact_state", return_value=self.state), \
                mock.patch.object(PROBE, "pause_backend_for_lease_expiry", side_effect=paused) as pause, \
                mock.patch.object(PROBE.time, "time", return_value=1001.0):
            facts = self.helper("perform_case")("lease-expiry", self.context)
            args = pause.call_args.args
            self.assertEqual(args[:3], (self.docker, self.docker.full_id, self.docker.image))
            self.assertEqual(args[3](), self.state)
        self.assertEqual(facts, {"lease_expired": True, "authenticated_renewal_stopped": True,
                                "backend_paused_observed": True,
                                "expiry_observed_after_last_renewal": True,
                                "last_authenticated_lease_expires_at": 1000.0})

    def test_stale_result_requires_unchanged_actual_workflow_and_task(self):
        PROBE.save(self.root / "stale-observation.json", {"workflow_status": "Running", "task_status": "running"})
        with mock.patch.object(PROBE, "wait_for", side_effect=lambda predicate, seconds, code: predicate()):
            self.assertEqual(self.perform("stale-result"), {})
        commands = [argv for argv, _ in self.docker.actions]
        self.assertEqual([argv[-1] for argv in commands], ["open('/execution/evidence/stale','w').close()",
                                                         "open('/execution/evidence/cancel','w').close()"])

    def test_stale_result_that_changes_task_is_rejected(self):
        PROBE.save(self.root / "stale-observation.json", {"workflow_status": "Running", "task_status": "completed"})
        with mock.patch.object(PROBE, "wait_for", side_effect=lambda predicate, seconds, code: predicate()), \
                self.assertRaisesRegex(RuntimeError, "STALE_RESULT_CHANGED_ACTUAL_WORKFLOW"):
            self.perform("stale-result")
        self.assertEqual(len(self.docker.actions), 1)

    def test_unknown_case_cannot_run_arbitrary_control(self):
        with self.assertRaisesRegex(ValueError, "PROBE_CASE_CONTROL_DENIED"):
            self.perform("arbitrary-control")
        self.assertEqual(self.docker.actions, [])


class AuthenticatedStateTest(unittest.TestCase):
    def setUp(self):
        cache = SCRIPT.parents[1] / ".cache"
        cache.mkdir(exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(prefix="complete-transport-state-unit-", dir=cache)
        self.addCleanup(self.temporary.cleanup)
        self.root = pathlib.Path(self.temporary.name)
        self.key = b"\x01" * 32
        key_file = self.root / "host-ownership.key"
        key_file.write_bytes(self.key)
        key_file.chmod(0o600)
        self.workpackage = "22222222-2222-4222-8222-222222222222"
        self.execution = "11111111-1111-4111-8111-111111111111"
        execution_root = self.root / self.execution
        execution_root.mkdir(mode=0o700)
        self.file = execution_root / "agent-state.json"
        self.state = {"binding": {"execution_id": self.execution, "workpackage_id": self.workpackage,
                                  "runtime_context": {"workflow_id": "unit", "workflow_run_id": "exact-unit-run",
                                                      "activity_id": "3", "activity_attempt": 1}},
                      "state": "CLEANED", "invalid_reason": "EXECUTION_LEASE_EXPIRED", "cleanup_proven": True}
        self.write_signed(self.state)

    def write_signed(self, state):
        value = {**state, "state_mac": hmac.new(self.key, b"cdo-agent-state-v1\0" + PROBE.canonical(state), hashlib.sha256).hexdigest()}
        self.file.write_bytes(PROBE.canonical(value))
        self.file.chmod(0o600)
        return value

    def helper(self):
        value = getattr(PROBE, "authenticated_state_records", None)
        self.assertTrue(callable(value), "terminal bookkeeping requires authenticated controller state")
        return value

    def test_selects_authenticated_workpackage_state(self):
        records = self.helper()(self.root, self.workpackage)
        self.assertEqual(len(records), 1)
        self.assertEqual(records[0]["binding"], self.state["binding"])
        self.assertIs(records[0]["cleanup_proven"], True)

    def test_altered_signed_state_cannot_be_observed_as_terminal(self):
        value = self.write_signed(self.state)
        value["cleanup_proven"] = False
        self.file.write_bytes(PROBE.canonical(value))
        with self.assertRaisesRegex(RuntimeError, "PROBE_STATE_MAC_INVALID"):
            self.helper()(self.root, self.workpackage)

    def test_binding_filter_cannot_hide_bad_mac(self):
        value = self.write_signed(self.state)
        value["binding"]["workpackage_id"] = "33333333-3333-4333-8333-333333333333"
        self.file.write_bytes(PROBE.canonical(value))
        with self.assertRaises(RuntimeError):
            self.helper()(self.root, self.workpackage)

    def test_other_authenticated_workpackage_is_not_selected(self):
        self.state["binding"]["workpackage_id"] = "33333333-3333-4333-8333-333333333333"
        self.write_signed(self.state)
        self.assertEqual(self.helper()(self.root, self.workpackage), [])

    def test_valid_mac_cannot_rebind_execution_directory(self):
        self.state["binding"]["execution_id"] = "44444444-4444-4444-8444-444444444444"
        self.write_signed(self.state)
        with self.assertRaisesRegex(RuntimeError, "PROBE_STATE_EXECUTION_MISMATCH"):
            self.helper()(self.root, self.workpackage)

    def test_duplicate_json_keys_are_not_authenticated_evidence(self):
        value = self.write_signed(self.state)
        text = PROBE.canonical(value).decode().replace('"cleanup_proven":true',
                                                     '"cleanup_proven":false,"cleanup_proven":true')
        self.file.write_text(text)
        with self.assertRaisesRegex((RuntimeError, ValueError), "PROBE_DUPLICATE_JSON_FIELD"):
            self.helper()(self.root, self.workpackage)

    def test_readable_host_key_is_not_accepted(self):
        (self.root / "host-ownership.key").chmod(0o644)
        with self.assertRaises(RuntimeError):
            self.helper()(self.root, self.workpackage)


class TerminalArtifactTest(PureTemporaryTest):
    def setUp(self):
        super().setUp()
        self.execution = "11111111-1111-4111-8111-111111111111"
        self.runtime_context = {"workflow_id": "synthetic-workflow", "workflow_run_id": "original-unit-run",
                                "activity_id": "3", "activity_attempt": 1}
        self.state_root = self.root / "state"
        self.execution_root = self.state_root / self.execution
        (self.execution_root / "owned").mkdir(parents=True, mode=0o700)
        self.evidence = self.root / "evidence"
        self.evidence.mkdir(mode=0o700)
        self.fixture = self.root / "fixture"
        (self.fixture / "internal").mkdir(parents=True)
        (self.fixture / "internal/value.go").write_text("package sample\nfunc Value() int{return 0}\n")
        self.job, self.owned = {"version": 1, "environment": {}}, {"version": 1, "fixture": "synthetic"}
        PROBE.save(self.execution_root / "job.json", self.job)
        PROBE.save(self.execution_root / "owned-config.json", self.owned)
        self.state = {
            "binding": {"execution_id": self.execution, "attempt": 1, "runtime_context": self.runtime_context},
            "state": "CLEANED", "outcome": "SUCCEEDED", "invalid_reason": None, "cleanup_proven": True,
            "job_sha256": PROBE.sha(PROBE.canonical(self.job)),
            "owned_config_sha256": PROBE.sha(PROBE.canonical(self.owned)),
            "process_owner": {"owner_nonce": "a" * 32}, "coordinator_pid": 2233,
            "coordinator_reaped": True,
        }
        binding = {name: self.state[name] for name in ("job_sha256", "owned_config_sha256", "process_owner")}
        binding.update(self.state["binding"])
        self.teardown = {"binding": binding, "node_group_absent": True, "no_unknown": True}
        PROBE.save(self.execution_root / "host-teardown.json", self.teardown)
        self.receipt = {
            "version": 1, "execution_id": self.execution, "attempt": 1,
            "job_sha256": self.state["job_sha256"], "owned_config_sha256": self.state["owned_config_sha256"],
            "process_owner": self.state["process_owner"], "coordinator_pid": self.state["coordinator_pid"],
            "coordinator_reaped": True,
            "coordinator_absence_observed": True, "owned_oci_absent": True, "coordinator_absent": True,
            "node_groups_absent": True, "private_storage_absent": True, "no_unknown": True,
            "teardown_sha256": PROBE.sha(PROBE.canonical(self.teardown)),
        }
        self.state["cleanup_evidence"] = self.receipt
        PROBE.save(self.execution_root / "owned/owned-runtime.json", {"resources": {
            "command": {"resource": "command", "id": "c" * 64, "status": "REMOVED"},
            "sdk": {"resource": "sdk", "id": "d" * 64, "status": "REMOVED"},
        }})
        self.publication = {"execution_id": self.execution, "attempt": 1, "status": "COMPLETE"}
        PROBE.save(self.execution_root / "unit.publication.json", self.publication)
        self.docker = FakeDocker()
        self.docker.prove_absent = mock.Mock(return_value=True)
        self.context = {"state_root": self.state_root, "evidence": self.evidence, "bundle": self.root / "bundle",
                        "docker": self.docker, "data": {"fixture": self.fixture, "public_template": {"role": "layer"}}}
        self.inspection = {"Mounts": [], "Config": {"Env": ["CODEX_SOL_MAX_RUNS_5H=20", "CODEX_BUDGET_MODE=enforce"]}}

    def finalize(self, case="success", facts=None):
        analyze = mock.Mock(return_value={"status": "PASS", "case": "success" if case == "success" else case,
                                          "assertions": {"synthetic_analyzer_fixture": True}})
        analyzer = types.SimpleNamespace(analyze=analyze)
        spec = types.SimpleNamespace(loader=types.SimpleNamespace(exec_module=mock.Mock()))
        with mock.patch.object(PROBE.importlib.util, "spec_from_file_location", return_value=spec), \
                mock.patch.object(PROBE.importlib.util, "module_from_spec", return_value=analyzer):
            result = self.helper("finalize_case")(self.context, case, {} if facts is None else facts,
                                                  [self.state], self.inspection, True)
        return result, PROBE.read(self.evidence / "transport-terminal.json"), analyze

    def test_success_requires_bound_compound_receipt_and_exact_owned_absence(self):
        result, terminal, analyze = self.finalize()
        self.assertEqual(result["status"], "PASS")
        self.assertEqual(terminal["status"], "VERIFIED")
        self.assertEqual(terminal["execution_id"], self.execution)
        self.assertEqual(terminal["runtime_context"], self.runtime_context)
        self.assertIs(terminal["cleanup"]["compound_receipt_authenticated"], True)
        self.assertEqual(terminal["cleanup"]["state"], "CLEANED")
        self.assertEqual(self.docker.prove_absent.call_args_list, [mock.call("c" * 64), mock.call("d" * 64)])
        analyze.assert_called_once_with(self.evidence, "success", "layer")

    def test_changed_job_stops_before_analyzer(self):
        PROBE.save(self.execution_root / "job.json", {**self.job, "tampered": True})
        with self.assertRaisesRegex(RuntimeError, "TERMINAL_JOB_IDENTITY_CHANGED"):
            self.finalize()
        self.assertFalse((self.evidence / "transport-terminal.json").exists())

    def test_changed_teardown_receipt_cannot_mark_verified(self):
        self.receipt["teardown_sha256"] = "0" * 64
        result, terminal, _ = self.finalize()
        self.assertNotEqual(result["status"], "PASS", "an analyzer verdict cannot bypass an unbound physical cleanup receipt")
        self.assertEqual(terminal["status"], "NOT_VERIFIED")
        self.assertIs(terminal["cleanup"]["compound_receipt_authenticated"], False)
        self.assertEqual(terminal["cleanup"]["state"], "UNPROVEN")

    def test_truthy_receipt_proof_cannot_mark_verified(self):
        self.receipt["node_groups_absent"] = "true"
        result, terminal, _ = self.finalize()
        self.assertNotEqual(result["status"], "PASS", "literal cleanup booleans are required before runtime PASS")
        self.assertEqual(terminal["status"], "NOT_VERIFIED")
        self.assertIs(terminal["cleanup"]["compound_receipt_authenticated"], False)

    def test_positive_owned_absence_is_required_even_with_complete_receipt(self):
        self.docker.prove_absent.return_value = False
        result, terminal, _ = self.finalize()
        self.assertNotEqual(result["status"], "PASS")
        self.assertEqual(terminal["cleanup"]["state"], "UNPROVEN")
        self.assertEqual(terminal["status"], "NOT_VERIFIED")

    def test_private_storage_still_present_cannot_pass(self):
        (self.execution_root / ("private-execution-" + "a" * 32)).mkdir()
        result, terminal, _ = self.finalize()
        self.assertNotEqual(result["status"], "PASS")
        self.assertIs(terminal["cleanup"]["private_state_absent"], False)

    def test_backend_socket_authority_cannot_pass(self):
        self.inspection["Mounts"] = [{"Source": "/var/run/docker.sock", "Destination": "/var/run/docker.sock"}]
        result, terminal, _ = self.finalize()
        self.assertNotEqual(result["status"], "PASS")
        self.assertIs(terminal["backend_authority"]["no_docker_socket_mount"], False)

    def test_backend_remote_authority_cannot_pass(self):
        self.inspection["Config"]["Env"].append("DOCKER_HOST=tcp://unapproved.example:2376")
        result, terminal, _ = self.finalize()
        self.assertNotEqual(result["status"], "PASS")
        self.assertIs(terminal["backend_authority"]["no_remote_docker_authority"], False)

    def test_unknown_uses_physical_reconciliation_receipt_and_preserves_unknown(self):
        self.state.update(state="UNKNOWN", outcome="UNKNOWN", cleanup_proven=False, invalid_reason="AGENT_CLEANUP_UNKNOWN")
        self.state["cleanup_evidence"] = {"bogus": "old receipt must not be used"}
        reconciliation = {**self.receipt, "no_unknown": False}
        self.state["reconciliation_evidence"] = reconciliation
        self.publication["status"] = "FAILED"
        PROBE.save(self.execution_root / "unit.publication.json", self.publication)
        _, terminal, _ = self.finalize("runtime-unknown", {"runtime_unobservable": True,
                                                          "runtime_observable_after_reconciliation": True})
        self.assertEqual(terminal["status"], "UNKNOWN")
        self.assertIs(terminal["unknown_latched"], True)
        self.assertIs(terminal["cleanup"]["reconciled"], True)
        self.assertEqual(terminal["cleanup"]["state"], "CLEANED")
        self.assertEqual(terminal["cleanup_evidence"], reconciliation)
        self.assertEqual(terminal["publication"]["status"], "FAILED")

    def test_action_artifact_is_bound_to_sealed_execution_and_context(self):
        self.state.update(outcome="FAILED", invalid_reason="EXECUTION_LEASE_EXPIRED")
        self.publication["status"] = "FAILED"
        PROBE.save(self.execution_root / "unit.publication.json", self.publication)
        self.finalize("backend-kill", {"backend_process_absent": True, "lease_expired": True})
        control = PROBE.read(self.evidence / "control-completed.json")
        self.assertEqual(control["execution_id"], self.execution)
        self.assertEqual(control["runtime_context"], self.runtime_context)
        self.assertEqual(control["invalid_reason"], "EXECUTION_LEASE_EXPIRED")
        self.assertEqual(control["action"], "backend-kill")
        self.assertIs(control["performed"], True)

    def test_action_facts_cannot_override_bound_identity(self):
        self.finalize("backend-kill", {"backend_process_absent": True, "execution_id": "unrelated",
                                       "runtime_context": {"workflow_run_id": "unrelated"}, "action": "success"})
        control = PROBE.read(self.evidence / "control-completed.json")
        self.assertEqual(control["execution_id"], self.execution)
        self.assertEqual(control["runtime_context"], self.runtime_context)
        self.assertEqual(control["action"], "backend-kill")


class PureBoundaryTest(unittest.TestCase):
    def test_exact_owned_absence_rejects_other_id_and_ambiguous_errors(self):
        owned, other = "a" * 64, "b" * 64
        complete = subprocess.CompletedProcess([], 1, "[]\n", "Error: No such container: " + owned + "\n")
        self.assertTrue(PROBE.HostDocker._absence(complete, owned))
        self.assertFalse(PROBE.HostDocker._absence(complete, other))
        ambiguous = subprocess.CompletedProcess([], 1, "", "daemon unavailable: No such container: " + owned)
        self.assertFalse(PROBE.HostDocker._absence(ambiguous, owned))

    def test_provider_call_plans_can_be_checked_without_opening_socket(self):
        provider = PROBE.Provider.__new__(PROBE.Provider)
        provider.role, provider.security_stress = "reviewer", False
        calls, denied = provider._calls()
        self.assertTrue(any(item["category"] == "reviewer_write" for item in denied))
        self.assertEqual(calls[-1], ("command", {"argv": ["go", "test", "./..."]}))


class ControllerRestartCleanupTest(unittest.TestCase):
    """Run the actual main finally block with disposable fake child handles."""
    def cleanup(self, controllers, docker, providers):
        tree = ast.parse(SCRIPT.read_text())
        main = next(node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == "main_probe")
        outer = next(node for node in main.body if isinstance(node, ast.Try) and node.finalbody)
        namespace = dict(vars(PROBE))
        namespace.update(controller=controllers[0], controller_processes=controllers,
                         backend_ids=["a" * 64], docker=docker, providers=providers,
                         args=types.SimpleNamespace(runner_image="sha256:" + "1" * 64))
        exec(compile(ast.Module(body=outer.finalbody, type_ignores=[]), str(SCRIPT), "exec"), namespace)

    def test_restarted_controller_is_stopped_in_main_finally(self):
        original = mock.Mock()
        original.poll.return_value = 0
        restarted = mock.Mock()
        restarted.poll.return_value = None
        docker, provider = mock.Mock(), mock.Mock()
        self.cleanup([original, restarted], docker, [provider])
        restarted.terminate.assert_called_once_with()
        restarted.wait.assert_called_once_with(timeout=30)
        original.terminate.assert_not_called()
        provider.close.assert_called_once_with()

    def test_controller_cleanup_unknown_still_cleans_other_owned_resources(self):
        controller = mock.Mock()
        controller.poll.return_value = None
        controller.wait.side_effect = subprocess.TimeoutExpired("owned-controller", 30)
        docker, provider = mock.Mock(), mock.Mock()
        with self.assertRaisesRegex(RuntimeError, "PROBE_RESOURCE_CLEANUP_UNKNOWN"):
            self.cleanup([controller], docker, [provider])
        docker.cleanup.assert_called_once_with("a" * 64, "sha256:" + "1" * 64)
        provider.close.assert_called_once_with()


class ApprovedDependencyPinTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        root = pathlib.Path(self.temp.name).resolve()
        self.source, self.bundle = root / "source", root / "bundle"
        self.source.mkdir()
        (self.source / "go.mod").write_text("module fixture\n")
        (self.source / "go.sum").write_text("")
        (self.bundle / "modules").mkdir(parents=True)
        manifest = {"schema_version": 1, "repository": str(self.source), "toolchain_image": "sha256:" + "3" * 64,
                    "files": {}, "cache_identity": PROBE.sha(b"{}"),
                    "inputs": {name: PROBE.sha((self.source / name).read_bytes()) for name in ("go.mod", "go.sum")}}
        PROBE.save(self.bundle / "manifest.json", manifest)
        self.args = types.SimpleNamespace(go_bundle=str(self.bundle), command_image=manifest["toolchain_image"],
            go_bundle_manifest_sha256=PROBE.sha((self.bundle / "manifest.json").read_bytes()))

    def tearDown(self):
        self.temp.cleanup()

    def test_replacement_manifest_cannot_replace_the_approved_pin(self):
        PROBE.load_dependency_bundle(self.args)
        manifest = PROBE.read(self.bundle / "manifest.json")
        manifest["prepared_at"] = "changed-after-preflight"
        PROBE.save(self.bundle / "manifest.json", manifest)
        with self.assertRaisesRegex(Exception, "GO_DEPENDENCY_MANIFEST_MISMATCH"):
            PROBE.load_dependency_bundle(self.args)

    def test_inventory_change_denies_with_the_same_manifest_pin(self):
        (self.bundle / "modules/unapproved.go").write_text("package escape\n")
        with self.assertRaisesRegex(Exception, "GO_DEPENDENCY_CONTENT_MISMATCH"):
            PROBE.load_dependency_bundle(self.args)

    def test_external_prepared_source_change_requires_repreparation(self):
        (self.source / "go.mod").write_text("module changed\n")
        with self.assertRaisesRegex(Exception, "GO_DEPENDENCY_REPREPARATION_REQUIRED"):
            PROBE.load_dependency_bundle(self.args)


class HostDockerDaemonContinuityTest(unittest.TestCase):
    def test_daemon_change_latches_unknown_before_next_effect(self):
        with tempfile.TemporaryDirectory() as temporary:
            docker = PROBE.HostDocker("/synthetic/docker", "unix:///approved.sock", pathlib.Path(temporary).resolve())
            docker.daemon = "approved-daemon"
            observations = iter(("approved-daemon", "replacement-daemon"))
            effects = []
            def invoke(argv, **kwargs):
                if argv[-3:] == ["info", "--format", "{{.ID}}"]:
                    return subprocess.CompletedProcess(argv, 0, next(observations) + "\n", "")
                effects.append(argv)
                return subprocess.CompletedProcess(argv, 0, "", "")
            with mock.patch.object(PROBE.subprocess, "run", side_effect=invoke) as run:
                docker.run(["ps", "-aq", "--no-trunc"])
                with self.assertRaisesRegex(RuntimeError, "PROBE_HOST_DOCKER_UNKNOWN"):
                    docker.run(["create", "immutable-image"])
                calls = run.call_count
                with self.assertRaisesRegex(RuntimeError, "PROBE_HOST_DOCKER_UNKNOWN"):
                    docker.run(["inspect", "owned-id"])
                self.assertEqual(run.call_count, calls, "UNKNOWN must not retry the denied runtime observer")
            self.assertEqual(len(effects), 1)


class HostEndpointContinuityTest(unittest.TestCase):
    def setup_route(self, args, source=None):
        main = next(node for node in ast.parse(source or SCRIPT.read_text()).body
                    if isinstance(node, ast.FunctionDef) and node.name == "main_probe")
        body = []
        for node in main.body:
            if isinstance(node, ast.Assign) and any(isinstance(target, ast.Name) and target.id == "before" for target in node.targets):
                break
            body.append(node)
        namespace = dict(vars(PROBE), args=args)
        setup = ast.FunctionDef(name="setup", args=main.args, body=body, decorator_list=[])
        module = ast.fix_missing_locations(ast.Module(body=[setup], type_ignores=[]))
        exec(compile(module, str(SCRIPT), "exec"), namespace)
        namespace["setup"](args)

    def test_preflight_endpoint_change_denies_before_runtime_effects(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary).resolve()
            endpoint = root / "docker.sock"
            endpoint.write_bytes(b"socket stand-in; never connected")
            executable = pathlib.Path(PROBE.sys.executable).resolve()
            args = types.SimpleNamespace(case="four-roles", output=str(root / "evidence"),
                runner_image="sha256:" + "1" * 64, sdk_image="sha256:" + "2" * 64,
                command_image="sha256:" + "3" * 64, source_manifest="4" * 64,
                docker_executable=str(executable), docker_endpoint="unix:///different/approved.sock",
                daemon_id="approved-daemon")
            docker = mock.Mock()
            docker.run.return_value = subprocess.CompletedProcess([], 0, "approved-daemon\n", "")
            with mock.patch.object(PROBE.shutil, "which", return_value=str(executable)), \
                 mock.patch.object(PROBE.subprocess, "check_output", side_effect=["desktop-linux\n", "unix://" + str(endpoint) + "\n"]), \
                 mock.patch.object(PROBE, "HostDocker", return_value=docker):
                with self.assertRaisesRegex(RuntimeError, "PROBE_HOST_CONFIGURATION_CHANGED"):
                    self.setup_route(args)
            docker.inventory.assert_not_called()


if __name__ == "__main__":
    unittest.main()
