#!/usr/bin/env python3
"""Disposable complete transport certification with actual SDK/Temporal runtimes.

The host owns Docker. The backend receives an immutable request and one readonly
template key, never a source tree or a Docker socket. SDK and command containers
are created only by the authenticated controller's fixed coordinator policy.
No real model, qualification grant, production admission or product is used.
"""
from __future__ import annotations

import argparse
import dataclasses
import datetime
import hashlib
import hmac
import http.client
import http.server
import inspect
import importlib.util
import json
import os
import re
from pathlib import Path
import secrets
import shutil
import signal
import stat
import subprocess
import sys
import threading
import time
import uuid

ROLES = ("contract", "layer", "composition", "reviewer")
MAX_RECORD = 8 * 1024 * 1024
ALL_CASES = ("four-roles", "adversarial", "activity-cancel", "workflow-cancel", "active-cancel",
                 "before-publication", "verification", "partial-publication", "timeout",
                 "controller-kill", "controller-restart", "backend-kill", "broker-interruption",
                 "lease-expiry", "runtime-unknown", "transport-disconnect", "stale-result",
                 "stale-publication", "security-failure")
CASE_PHASES = {"before-publication": "before-publication", "verification": "verification",
               "partial-publication": "publication-applied-1", "stale-publication": "publication-applied-1"}
REQUIRED_RUNTIME_CAPABILITIES = (
    "desktop-linux fixed canonical Docker endpoint with exact owned OCI lifecycle operations",
    "host loopback bind for deterministic SDK provider and authenticated controller",
    "fixed /bin/ps complete trusted process-group observations",
    "signals accepted for exact fresh owned host children only",
    "disposable Temporal server and matching immutable runner/SDK/command images",
)


def expanded_cases(case):
    if case == "all":
        return ALL_CASES
    if case not in (*ALL_CASES, "success"):
        raise ValueError("PROBE_CASE_DENIED")
    return (case,)


def run_all(args, case_runner):
    output = Path(args.output).resolve()
    output.mkdir(mode=0o700, parents=False, exist_ok=False)
    records = {}
    for case in expanded_cases("all"):
        child = argparse.Namespace(**vars(args))
        child.case, child.output = case, str(output / case)
        try:
            code = case_runner(child)
            evidence = read(Path(child.output) / "complete-transport-evidence.json")
            passed = code == 0 and evidence.get("pass") is True
            records[case] = {"pass": passed, "evidence_sha256": sha(regular(Path(child.output) / "complete-transport-evidence.json"))}
        except Exception as error:
            records[case] = {"pass": False, "error": type(error).__name__, "code": str(error)[:200]}
        save(output / "complete-transport-evidence.json", {"case": "all", "cases": records,
             "required_cases": list(ALL_CASES), "source_scenario_selection_complete": True,
             "full_certification_claimed": False, "pass": False, "real_model_calls": 0,
             "production_admission": "DENIED", "budget": "20/enforce"})
        if not records[case]["pass"]:
            # A concrete denial is terminal. Do not retry or continue other
            # runtime paths after it; preserve the exact partial suite evidence.
            return 1
    result = {"case": "all", "cases": records, "required_cases": list(ALL_CASES),
              "source_scenario_selection_complete": True, "full_certification_claimed": True,
              "pass": len(records) == len(ALL_CASES) and all(item["pass"] for item in records.values()),
              "real_model_calls": 0, "production_admission": "DENIED", "budget": "20/enforce"}
    save(output / "complete-transport-evidence.json", result)
    return 0 if result["pass"] else 1


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()


def sha(value):
    return hashlib.sha256(value).hexdigest()


def save(path, value, mode=0o600):
    data = canonical(value)
    if len(data) > MAX_RECORD:
        raise RuntimeError("PROBE_EVIDENCE_LIMIT")
    temporary = path.with_name(path.name + ".tmp")
    descriptor = os.open(temporary, os.O_CREAT | os.O_TRUNC | os.O_WRONLY | os.O_NOFOLLOW, mode)
    with os.fdopen(descriptor, "wb") as output:
        output.write(data)
        output.flush()
        os.fsync(output.fileno())
    temporary.chmod(mode)
    os.replace(temporary, path)


def regular(path, limit=MAX_RECORD):
    path = Path(path)
    info = path.lstat()
    if path.resolve(strict=True) != path or not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_size > limit:
        raise RuntimeError("PROBE_REGULAR_FILE_REQUIRED")
    return path.read_bytes()


def read(path):
    def fields(pairs):
        value = {}
        for key, item in pairs:
            if key in value:
                raise ValueError("PROBE_DUPLICATE_JSON_FIELD")
            value[key] = item
        return value
    return json.loads(regular(path), object_pairs_hook=fields,
                      parse_constant=lambda _: (_ for _ in ()).throw(ValueError("PROBE_NONFINITE_JSON")))


def wait_for(predicate, seconds, code):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        result = predicate()
        if result:
            return result
        time.sleep(.1)
    raise RuntimeError(code)


class Provider:
    """Actual Responses/SSE protocol, deterministic broker calls only."""
    def __init__(self, role, fixture, output, hold=False, security_stress=False):
        self.role, self.fixture, self.output = role, fixture, output
        self.security_stress = security_stress
        self.calls, self.denials = self._calls()
        self.index, self.seen, self.responses, self.tool_names = 0, [], [], set()
        self.release = threading.Event()
        if not hold:
            self.release.set()
        provider = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_POST(self):
                if self.path != "/v1/responses" or self.headers.get("Transfer-Encoding"):
                    self.send_error(403)
                    return
                try:
                    size = int(self.headers.get("Content-Length", "0"))
                    if not 0 < size <= 4 * 1024 * 1024:
                        raise ValueError()
                    payload = json.loads(self.rfile.read(size))
                    if not isinstance(payload, dict):
                        raise ValueError()
                except (ValueError, TypeError):
                    self.send_error(400)
                    return
                tools = payload.get("tools", [])
                for tool in tools + [tool for item in payload.get("input", []) if item.get("type") == "tool_search_output" for tool in item.get("tools", [])]:
                    if tool.get("name"):
                        provider.tool_names.add(tool["name"])
                    if tool.get("type") == "namespace":
                        provider.tool_names.update(tool["name"] + "__" + item["name"] for item in tool.get("tools", []))
                provider.seen.append({"tools": tools, "input_types": [item.get("type") for item in payload.get("input", [])]})
                provider.responses.extend(item for item in payload.get("input", []) if item.get("type") == "function_call_output")
                save(provider.output / "provider-observation.json", provider.evidence())
                if not provider.release.wait(180):
                    self.send_error(503)
                    return
                index = provider.index
                if index < len(provider.calls):
                    name, args = provider.calls[index]
                    if name == "tool_search":
                        item = {"type": "tool_search_call", "execution": "client", "arguments": args, "call_id": "call_" + str(index), "id": "ts_" + str(index), "status": "completed"}
                    else:
                        item = {"type": "function_call", **({"namespace": "mcp__cdo"} if name != "update_plan" else {}), "name": name,
                                "arguments": json.dumps(args), "call_id": "call_" + str(index), "id": "fc_" + str(index), "status": "completed"}
                else:
                    item = {"type": "message", "role": "assistant", "id": "msg_final", "status": "completed",
                            "content": [{"type": "output_text", "text": '{"status":"completed"}'}]}
                provider.index += 1
                response = {"id": "resp_" + str(provider.index), "object": "response", "status": "completed", "output": [item], "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}
                events = [{"type": "response.created", "response": {**response, "status": "in_progress", "output": []}},
                          {"type": "response.output_item.added", "output_index": 0, "item": item},
                          {"type": "response.output_item.done", "output_index": 0, "item": item},
                          {"type": "response.completed", "response": response}]
                try:
                    self.send_response(200)
                    self.send_header("Content-Type", "text/event-stream")
                    self.end_headers()
                    for event in events:
                        self.wfile.write(("data: " + json.dumps(event) + "\n\n").encode())
                        self.wfile.flush()
                except (BrokenPipeError, ConnectionResetError):
                    pass

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.server.daemon_threads = True
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def _calls(self):
        if self.security_stress:
            return ([("tool_search", {"query": "cdo read_file propose_file command", "limit": 10})]
                    + [("update_plan", {"explanation": "x" * 600000,
                        "plan": [{"step": "bounded actual SDK state stress " + str(index), "status": "in_progress"}]})
                       for index in range(20)]), []
        calls, denied = [("tool_search", {"query": "cdo read_file propose_file command", "limit": 10})], []

        def deny(name, arguments, category):
            denied.append({"call_id": "call_" + str(len(calls)), "category": category})
            calls.append((name, arguments))

        for path in ("/execution/sdk-private/home/state_5.sqlite", "/reference/auth.json", "/root/.codex/auth.json", "/root/.ssh/id_rsa", "/var/run/docker.sock", "../sibling-state"):
            deny("read_file", {"path": path}, "private_read")
        calls.append(("read_file", {"path": "internal/value.go"}))
        for path in ("internal/sibling.go", "contracts/frozen.go", "../escape"):
            deny("propose_file", {"path": path, "content": "forbidden"}, "scope_write")
        for argv in (["git", "status"], ["curl", "https://example.invalid"], ["python3", "-c", "print('forbidden')"], ["go", "test", "./...", "-args", "allocate-unbounded"]):
            deny("command", {"argv": argv}, "command_policy")
        if self.role == "layer":
            calls.append(("command", {"argv": ["go", "test", "./..."]}))
            calls.append(("propose_file", {"path": "internal/value.go", "content": 'package sample\nimport "github.com/google/uuid"\nfunc Value() int {return int(uuid.MustParse("11111111-1111-4111-8111-111111111111").Version())*10+2}\n'}))
        elif self.role == "contract":
            calls.append(("propose_file", {"path": "contracts/current.go", "content": "package contracts\nvar Boundary=42\n"}))
        elif self.role == "composition":
            calls.append(("propose_file", {"path": "cmd/main.go", "content": "package main\nvar Wiring=42\n"}))
        else:
            deny("propose_file", {"path": "internal/value.go", "content": "forbidden"}, "reviewer_write")
        calls.append(("command", {"argv": ["go", "test", "./..."]}))
        return calls, denied

    def evidence(self):
        return {"transport": "DETERMINISTIC_RESPONSES_ACTUAL_SDK", "role": self.role,
                "requests": self.seen, "tool_names": sorted(self.tool_names),
                "tool_results": self.responses, "calls": [(name, {"bytes": len(canonical(args)), "sha256": sha(canonical(args))}
                                                            if name == "update_plan" else args) for name, args in self.calls], "expected_denials": self.denials,
                "real_model_calls": 0}

    def assertions(self):
        results = {item["call_id"]: str(item.get("output", "")) for item in self.responses}
        commands = [("call_" + str(index), args) for index, (name, args) in enumerate(self.calls) if name == "command" and args["argv"] == ["go", "test", "./..."]]
        native = {"apply_patch", "exec_command", "shell_command", "shell", "view_image", "write_stdin", "create_goal", "update_goal", "skills"}
        def command_result(value):
            if isinstance(value, str):
                try:
                    value = json.loads(value)
                except ValueError:
                    return None
            if isinstance(value, dict):
                if type(value.get("exit_code")) is int and isinstance(value.get("output"), str):
                    return value
                for nested in value.values():
                    found = command_result(nested)
                    if found is not None:
                        return found
            if isinstance(value, list):
                for nested in value:
                    found = command_result(nested)
                    if found is not None:
                        return found
            return None

        first = command_result(results.get(commands[0][0])) if commands else None
        last = command_result(results.get(commands[-1][0])) if commands else None
        return {"all_requested_denials_observed": all(item["call_id"] in results and "DENIED" in results[item["call_id"]] for item in self.denials),
                "native_tools_absent": not (native & self.tool_names),
                "approved_go_command_observed": bool(commands) and all(ident in results for ident, _ in commands),
                "layer_behavior_red": self.role != "layer" or bool(first and first["exit_code"] != 0 and "expected42" in first["output"]),
                "go_behavior_green": bool(last and last["exit_code"] == 0),
                "sdk_provider_completed": self.index > len(self.calls)}

    def close(self):
        self.release.set()
        self.server.shutdown()
        self.server.server_close()
        save(self.output / "provider-observation.json", self.evidence())


def copy_host_bundle(repo, output):
    bundle = output / "host-bundle"
    bundle.mkdir(mode=0o700)
    files = []
    for subtree, pattern in (("runner/bin", "*.py"), ("runner/dist", "*.js"), ("runner/node_modules/@openai/codex-sdk", "*.js"), ("runner/node_modules/@openai/codex-sdk", "package.json")):
        for source in sorted((repo / subtree).rglob(pattern)):
            data = regular(source, 16 * 1024 * 1024)
            relative = source.relative_to(repo)
            target = bundle / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(data)
            # Public implementation bytes are readonly; the SDK uid100 must
            # read its two explicitly mounted supervisors. Keys stay mode400.
            target.chmod(0o555 if relative.name == "cdo_sdk_state.py" else 0o444)
            files.append({"path": str(relative), "sha256": sha(data)})
    launcher = bundle / "controller-launcher.py"
    launcher.write_bytes(regular(Path(__file__).resolve(), 2 * 1024 * 1024))
    launcher.chmod(0o400)
    files.append({"path": "controller-launcher.py", "sha256": sha(launcher.read_bytes())})
    analyzer = bundle / "lifecycle-analyzer.py"
    analyzer.write_bytes(regular(repo / "scripts/probe-complete-transport-lifecycle.py"))
    analyzer.chmod(0o444)
    files.append({"path": "lifecycle-analyzer.py", "sha256": sha(analyzer.read_bytes())})
    package = bundle / "runner/package.json"
    package.write_bytes(regular(repo / "runner/package.json"))
    package.chmod(0o400)
    files.append({"path": "runner/package.json", "sha256": sha(package.read_bytes())})
    save(output / "host-bundle-inventory.json", files, 0o400)
    return bundle


class HostDocker:
    def __init__(self, executable, endpoint, output):
        self.executable, self.endpoint, self.output = str(executable), endpoint, output
        self.actions = []
        self.daemon = None
        self.unknown = False

    def run(self, argv, *, check=True, timeout=40):
        if self.unknown:
            raise RuntimeError("PROBE_HOST_DOCKER_UNKNOWN_NO_RETRY")
        if self.daemon is not None:
            try:
                observed = subprocess.run([self.executable, "--host", self.endpoint, "info", "--format", "{{.ID}}"],
                                          capture_output=True, text=True, timeout=15,
                                          env={"PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": "/var/empty"})
                self.actions.append({"argv": ["info", "--format", "{{.ID}}"], "code": observed.returncode,
                                     "stdout": observed.stdout[:1048576], "stderr": observed.stderr[:65536]})
                save(self.output / "host-docker-actions.json", self.actions)
                if observed.returncode or observed.stdout.strip() != self.daemon:
                    raise RuntimeError("PROBE_HOST_DOCKER_UNKNOWN_DAEMON_CHANGED")
            except Exception:
                self.unknown = True
                raise RuntimeError("PROBE_HOST_DOCKER_UNKNOWN_DAEMON_OBSERVATION") from None
        command = [self.executable, "--host", self.endpoint, *argv]
        try:
            result = subprocess.run(command, capture_output=True, text=True, timeout=timeout,
                                    env={"PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": "/var/empty"})
        except Exception:
            self.unknown = True
            raise RuntimeError("PROBE_HOST_DOCKER_UNKNOWN_OPERATION") from None
        self.actions.append({"argv": argv, "code": result.returncode, "stdout": result.stdout[:1024 * 1024], "stderr": result.stderr[:65536]})
        save(self.output / "host-docker-actions.json", self.actions)
        if check and result.returncode:
            self.unknown = True
            raise RuntimeError("PROBE_HOST_DOCKER_FAILURE")
        return result

    def inventory(self):
        ids = self.run(["ps", "-aq", "--no-trunc"]).stdout.split()
        if not ids:
            return {}
        values = json.loads(self.run(["inspect", *ids]).stdout)
        return {item["Id"]: {"created": item["Created"], "image": item["Image"]} for item in values}

    def cleanup(self, full_id, expected_image):
        observed = self.run(["inspect", full_id], check=False)
        if observed.returncode:
            if self._absence(observed, full_id):
                return
            raise RuntimeError("PROBE_BACKEND_CLEANUP_UNKNOWN")
        values = json.loads(observed.stdout)
        if len(values) != 1 or values[0]["Id"] != full_id or values[0]["Image"] != expected_image:
            raise RuntimeError("PROBE_BACKEND_OWNERSHIP_MISMATCH")
        self.run(["rm", "-f", full_id])
        observed = self.run(["inspect", full_id], check=False)
        if not self._absence(observed, full_id):
            raise RuntimeError("PROBE_BACKEND_CLEANUP_UNPROVEN")

    @staticmethod
    def _absence(observed, full_id):
        errors = {prefix + full_id for prefix in ("Error: No such container: ", "Error: No such object: ",
                  "Error response from daemon: No such container: ", "Error response from daemon: No such object: ")}
        return (bool(re.fullmatch(r"[a-f0-9]{64}", full_id)) and observed.returncode == 1
                and observed.stdout.strip() in {"", "[]"} and observed.stderr.strip() in errors)

    def prove_absent(self, full_id):
        return self._absence(self.run(["inspect", full_id], check=False), full_id)


def assert_owned_backend(docker, full_id, image):
    if not isinstance(full_id, str) or not re.fullmatch(r"[a-f0-9]{64}", full_id):
        raise RuntimeError("PROBE_FULL_BACKEND_ID_REQUIRED")
    values = json.loads(docker.run(["inspect", full_id]).stdout)
    if len(values) != 1 or values[0].get("Id") != full_id or values[0].get("Image") != image:
        raise RuntimeError("PROBE_BACKEND_OWNERSHIP_MISMATCH")
    return values[0]


def private_backend_recipe(image, mounts, environment, bootstrap):
    uid, gid = os.geteuid(), os.getegid()
    argv = ["create", "--name", "cdo-complete-observer-" + uuid.uuid4().hex,
            "--read-only", "--user", str(uid) + ":" + str(gid), "--cap-drop", "ALL",
            "--security-opt", "no-new-privileges=true", "--security-opt", "seccomp=builtin",
            "--pids-limit", "128", "--memory", "512m", "--memory-swap", "512m", "--cpus", "2",
            "--ulimit", "nofile=256:256", "--ulimit", "fsize=8388608:8388608",
            "--tmpfs", "/execution/evidence:rw,nosuid,nodev,noexec,size=33554432,nr_inodes=256,mode=0700,uid=" + str(uid) + ",gid=" + str(gid),
            "--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=1048576,nr_inodes=64,mode=1777"]
    for source, target in mounts:
        if any(value in str(source) for value in (",", "\0", "\n")):
            raise RuntimeError("PROBE_MOUNT_PATH_DENIED")
        argv.extend(["--mount", "type=bind,src=" + str(source) + ",dst=" + target + ",readonly"])
    for name, value in environment.items():
        argv.extend(["--env", name + "=" + str(value)])
    return argv + ["--entrypoint", "python3", image, "-c", bootstrap]


def launch_backend_adversarial(docker, base_argv, image, launcher, evidence, backend_ids):
    """Run the authenticated negative client inside the actual bounded backend."""
    index = base_argv.index(image)
    argv = list(base_argv[:index])
    argv[argv.index("--name") + 1] = "cdo-complete-adversarial-" + uuid.uuid4().hex
    bootstrap = "import os;os.execv('/usr/local/bin/python3',['python3','/verification/harness.py','--backend-adversarial','/verification/backend-key.json','/verification/request.json','/execution/evidence/agent-adversarial.json'])"
    argv += ["--mount", "type=bind,src=" + str(launcher) + ",dst=/verification/harness.py,readonly", image, "-c", bootstrap]
    full_id = docker.run(argv).stdout.strip()
    backend_ids.append(full_id)
    try:
        assert_owned_backend(docker, full_id, image)
        result = docker.run(["start", "--attach", full_id], check=False, timeout=300)
        docker.run(["cp", full_id + ":/execution/evidence/agent-adversarial.json", str(evidence / "agent-adversarial.json")])
        report = read(evidence / "agent-adversarial.json")
        if result.returncode or report.get("pass") is not True or report.get("historical_finite_128_counted") is not False:
            raise RuntimeError("COMPLETE_ROUTE_ADVERSARIAL_FAILED")
        return report
    finally:
        docker.cleanup(full_id, image)
        backend_ids.remove(full_id)


def launch_terminal_observer(docker, image, driver, evidence, backend_ids, *, temporal_address="host.docker.internal:17233"):
    """Observe exactly the lost run; never rerun ExecutePlanTask or mount keys."""
    start = read(evidence / "workflow-start.json")
    if not start.get("workflow_id") or not start.get("workflow_run_id"):
        raise RuntimeError("EXACT_ORIGINAL_WORKFLOW_IDENTITY_REQUIRED")
    bootstrap = "import shutil,os;shutil.copytree('/verification/original','/execution/evidence',dirs_exist_ok=True);os.execv('/verification/driver',['/verification/driver','--observe-terminal'])"
    argv = private_backend_recipe(image, ((driver, "/verification/driver"), (evidence, "/verification/original")),
        {"CDO_LIFECYCLE_OUTPUT": "/execution/evidence", "CDO_WORKFLOW_ID": start["workflow_id"],
         "CDO_TEMPORAL_ADDRESS": temporal_address, "CDO_OBSERVER_TIMEOUT_SECONDS": "180"}, bootstrap)
    full_id = docker.run(argv).stdout.strip()
    backend_ids.append(full_id)
    try:
        inspection = assert_owned_backend(docker, full_id, image)
        save(evidence.parent / "terminal-observer-inspection.json", inspection)
        result = docker.run(["start", "--attach", full_id], check=False, timeout=240)
        docker.run(["cp", full_id + ":/execution/evidence/.", str(evidence)])
        observed = read(evidence / "observer-complete.json")
        if (result.returncode or observed.get("started_execution") is not False
                or observed.get("workflow_id") != start["workflow_id"]
                or observed.get("workflow_run_id") != start["workflow_run_id"]):
            raise RuntimeError("EXACT_RUN_OBSERVER_FAILED")
        return evidence
    finally:
        docker.cleanup(full_id, image)
        backend_ids.remove(full_id)


def pause_backend_for_lease_expiry(docker, full_id, image, state_reader, *, wait=wait_for, proof=None):
    assert_owned_backend(docker, full_id, image)
    docker.run(["pause", full_id])
    try:
        paused = assert_owned_backend(docker, full_id, image)
        if paused.get("State", {}).get("Paused") is not True:
            raise RuntimeError("BACKEND_RENEWAL_NOT_SUSPENDED")
        last = state_reader()
        if proof is not None:
            proof.update(authenticated_renewal_stopped=True, backend_paused_observed=True,
                         last_authenticated_lease_expires_at=last["lease_expires_at"])
        def expired():
            state = state_reader()
            if state.get("invalid_reason") == "EXECUTION_LEASE_EXPIRED" and state.get("cleanup_proven") is True:
                return state
            return None
        # Only the backend is suspended. The host controller watchdog remains
        # alive and must durably expire the authenticated execution lease.
        return wait(expired, 30, "ACTUAL_LEASE_EXPIRY_NOT_OBSERVED")
    finally:
        docker.run(["unpause", full_id])


def authenticated_state_records(state_root, workpackage_id):
    key_path = state_root / "host-ownership.key"
    info = key_path.lstat()
    key = regular(key_path, 32)
    if len(key) != 32 or info.st_uid != os.geteuid() or info.st_mode & 0o077:
        raise RuntimeError("PROBE_HOST_OWNERSHIP_KEY_UNTRUSTED")
    result = []
    for path in state_root.glob("*/agent-state.json"):
        value = read(path)
        signature = value.pop("state_mac", None)
        expected = hmac.new(key, b"cdo-agent-state-v1\0" + canonical(value), hashlib.sha256).hexdigest()
        if not isinstance(signature, str) or not hmac.compare_digest(signature, expected):
            raise RuntimeError("PROBE_STATE_MAC_INVALID")
        if value.get("binding", {}).get("execution_id") != path.parent.name:
            raise RuntimeError("PROBE_STATE_EXECUTION_MISMATCH")
        if value["binding"].get("workpackage_id") == workpackage_id:
            result.append(value)
    return result


def exact_state(context):
    values = authenticated_state_records(context["state_root"], context["data"]["public_template"]["workpackage_id"])
    if len(values) != 1:
        raise RuntimeError("EXACT_CONTROLLER_EXECUTION_REQUIRED")
    return values[0]


def marker(docker, full_id, name):
    if name not in {"cancel", "stale"}:
        raise RuntimeError("PROBE_MARKER_DENIED")
    docker.run(["exec", full_id, "python3", "-c", "open('/execution/evidence/" + name + "','w').close()"])


def authenticated_operation(context, operation):
    if operation not in {"cancel", "cleanup"}:
        raise ValueError("PROBE_AUTHENTICATED_OPERATION_DENIED")
    import cdo_runtime_backend as backend
    state = exact_state(context)
    binding = state["binding"]
    credential = read(context["data"]["output"] / "backend-key.json")
    credential["url"] = "http://127.0.0.1:" + str(context["ready"]["port"])
    client = backend.AgentRuntimeClient.__new__(backend.AgentRuntimeClient)
    client.credentials = credential
    client.request = {"execution_id": binding["execution_id"], "attempt": binding["attempt"],
                      "runtime_context": binding["runtime_context"], "execution_deadline":
                      datetime.datetime.fromtimestamp(binding["execution_expires_at"], datetime.timezone.utc).isoformat().replace("+00:00", "Z")}
    client.host, client.port = backend.endpoint(credential["url"])
    return client.call(operation)


def passive_observation(context):
    import cdo_runtime_backend as backend
    credential = read(context["data"]["output"] / "backend-key.json")
    credential["url"] = "http://127.0.0.1:" + str(context["ready"]["port"])
    return backend.attestation_probe(credential)


def owned_process_rows():
    result = subprocess.run(["/bin/ps", "-axo", "pid=,ppid=,pgid=,uid=,lstart=,command="], capture_output=True, text=True, timeout=10)
    if result.returncode:
        raise RuntimeError("PROBE_PROCESS_OBSERVATION_DENIED")
    rows = []
    for line in result.stdout.splitlines():
        fields = line.strip().split(None, 9)
        if len(fields) == 10:
            rows.append({"pid": int(fields[0]), "ppid": int(fields[1]), "pgid": int(fields[2]),
                         "uid": int(fields[3]), "birth": " ".join(fields[4:9]), "command": fields[9]})
    return rows


def interrupt_broker(context):
    state = exact_state(context)
    root = context["state_root"] / state["binding"]["execution_id"]
    job = read(root / "job.json")
    private = Path(read(root / "evidence.json.execution.json")["private_root"])
    if not private.is_relative_to(root):
        raise RuntimeError("PROBE_BROKER_PRIVATE_ROOT_MISMATCH")
    expected = {"coordinator": str(Path(sys.executable).resolve()) + " " + str(context["bundle"] / "runner/bin/cdo_runtime_coordinator.py") + " " + str(root / "job.json"),
                "node": job["environment"]["CDO_RUNTIME_NODE_EXECUTABLE"] + " " + job["environment"]["CDO_RUNTIME_NODE_ENTRYPOINT"],
                "broker": job["environment"]["CDO_BROKER_PYTHON"] + " " + str(context["bundle"] / "runner/bin/cdo_broker.py") + " serve-http " + str(private / "control/policy.json")}
    rows = owned_process_rows()
    owners = {}
    for kind in ("coordinator", "node", "broker"):
        matches = [row for row in rows if row["command"] == expected[kind] and row["uid"] == os.geteuid()]
        if kind == "node":
            matches = [row for row in matches if row["ppid"] == owners["coordinator"]["pid"]]
        elif kind == "broker":
            matches = [row for row in matches if row["ppid"] == owners["node"]["pid"]]
        if len(matches) != 1:
            raise RuntimeError("PROBE_EXACT_BROKER_OWNERSHIP_UNPROVEN")
        owners[kind] = matches[0]
    fresh = owned_process_rows()
    if any(row not in fresh for row in owners.values()):
        raise RuntimeError("PROBE_BROKER_IDENTITY_CHANGED")
    # Future runtime certification only: signal the freshly observed exact MCP
    # broker child. An EPERM is terminal; there is no OCI or signal fallback.
    os.kill(owners["broker"]["pid"], signal.SIGKILL)
    wait_for(lambda: not any(row["pid"] == owners["broker"]["pid"] for row in owned_process_rows()), 15, "BROKER_PROCESS_ABSENCE_UNPROVEN")
    save(context["data"]["output"] / "broker-interruption.json", {"boundary": "HOST_MCP_BROKER_PROCESS", "owned_processes": owners, "signal": "SIGKILL"})
    return {"broker_process_absent": True}


def prepare_late_publisher(context):
    root = context["state_root"] / exact_state(context)["binding"]["execution_id"]
    private = Path(read(root / "evidence.json.execution.json")["private_root"])
    sys.path.insert(0, str(context["bundle"] / "runner/bin"))
    import cdo_broker as broker
    env = read(root / "job.json")["environment"]
    # Keep a valid real Publisher prepared before cancellation. Its later call
    # must reject the same invalidated execution even after private cleanup.
    previous = {name: os.environ.get(name) for name in env}
    os.environ.update(env)
    try:
        policy = broker.Policy(read(private / "control/policy.json"))
        staged = read(policy.state_dir / "state.json")
        return broker.Publisher(policy, staged["original"]), staged["proposals"], env, previous
    finally:
        for name, value in previous.items():
            if value is None:
                os.environ.pop(name, None)
            else:
                os.environ[name] = value


def reject_late_publisher(prepared):
    publisher, proposals, env, _ = prepared
    previous = {name: os.environ.get(name) for name in env}
    os.environ.update(env)
    try:
        try:
            publisher.publish(proposals)
        except Exception as error:
            code = str(error)
            if code not in {"FAILED_EXECUTION_PUBLICATION_DENIED", "PUBLICATION_ATTEMPT_ALREADY_USED", "EXECUTION_LEASE_EXPIRED_OR_INVALIDATED", "PUBLICATION_EXECUTION_INVALIDATED", "PUBLICATION_INVALIDATED"}:
                raise RuntimeError("LATE_PUBLICATION_NONSEMANTIC_FAILURE") from None
            return code
        raise RuntimeError("LATE_PUBLICATION_UNEXPECTED_SUCCESS")
    finally:
        for name, value in previous.items():
            if value is None:
                os.environ.pop(name, None)
            else:
                os.environ[name] = value


def perform_case(case, context):
    docker, full_id, image = context["docker"], context["full_id"], context["image"]
    phase = CASE_PHASES.get(case, "active")
    state = exact_state(context)
    binding = state["binding"]
    save(context["evidence"] / "transport-checkpoint.json", {"execution_id": binding["execution_id"], "runtime_context": binding["runtime_context"], "phase": phase})
    facts = {}
    if case in {"activity-cancel", "workflow-cancel", "active-cancel", "before-publication", "verification", "partial-publication"}:
        marker(docker, full_id, "cancel")
    elif case == "stale-result":
        marker(docker, full_id, "stale")
        wait_for(lambda: (context["copy_evidence"]() / "stale-observation.json").exists(), 30, "STALE_RESULT_OBSERVATION_MISSING")
        stale = read(context["evidence"] / "stale-observation.json")
        if stale.get("workflow_status") != "Running" or stale.get("task_status") != "running":
            raise RuntimeError("STALE_RESULT_CHANGED_ACTUAL_WORKFLOW")
        marker(docker, full_id, "cancel")
    elif case in {"controller-kill", "controller-restart"}:
        process = context["controller"]
        if process.pid != context["ready"]["pid"] or process.poll() is not None:
            raise RuntimeError("PROBE_CONTROLLER_OWNERSHIP_MISMATCH")
        process.kill(); process.wait(timeout=10)
        context["config"]["listen_port"] = context["ready"]["port"]
        save(context["config_path"], context["config"])
        Path(context["config"]["ready_file"]).unlink()
        context["controller"] = subprocess.Popen(context["controller_argv"], stdout=context["log"], stderr=context["log"], env=context["controller_env"], start_new_session=True)
        context["controller_processes"].append(context["controller"])
        context["ready"] = wait_for(lambda: read(Path(context["config"]["ready_file"])) if Path(context["config"]["ready_file"]).exists() else None, 45, "CONTROLLER_RESTART_FAILED")
        save(context["data"]["output"] / "controller-reconciliation.json", context["ready"]["reconciliation"])
        facts = {"controller_process_absent": process.poll() is not None,
                 "controller_restart_reconciled": bool(context["ready"]["reconciliation"])}
    elif case == "backend-kill":
        assert_owned_backend(docker, full_id, image)
        docker.run(["kill", "--signal", "KILL", full_id])
        wait_for(lambda: exact_state(context) if exact_state(context).get("invalid_reason") == "EXECUTION_LEASE_EXPIRED" and exact_state(context).get("cleanup_proven") is True else None, 30, "BACKEND_LOSS_LEASE_NOT_EXPIRED")
        context["copy_evidence"]()
        launch_terminal_observer(docker, image, context["driver"], context["evidence"], context["backend_ids"])
        facts = {"backend_process_absent": assert_owned_backend(docker, full_id, image)["State"]["Running"] is False, "lease_expired": True}
    elif case == "lease-expiry":
        renewal = {}
        pause_backend_for_lease_expiry(docker, full_id, image, lambda: exact_state(context), proof=renewal)
        after = exact_state(context)
        facts = {"lease_expired": after.get("invalid_reason") == "EXECUTION_LEASE_EXPIRED",
                 **renewal, "expiry_observed_after_last_renewal": time.time() >= renewal["last_authenticated_lease_expires_at"]}
    elif case == "broker-interruption":
        facts = interrupt_broker(context)
    elif case == "stale-publication":
        prepared = prepare_late_publisher(context)
        marker(docker, full_id, "cancel")
        wait_for(lambda: exact_state(context) if exact_state(context).get("cleanup_proven") else None, 40, "CANCELLED_PUBLICATION_CLEANUP_UNPROVEN")
        journals = list((context["state_root"] / binding["execution_id"]).glob("*.publication.json"))
        before = len(journals) == 1 and read(journals[0]).get("status") == "FAILED"
        code = reject_late_publisher(prepared)
        after = len(journals) == 1 and read(journals[0]).get("status") == "FAILED"
        facts = {"journal_failed_before_late_publish": before, "late_publication_rejected": True,
                 "journal_failed_after_late_publish": after, "no_verified_commit": True, "late_publication_error": code}
    elif case in {"transport-disconnect", "runtime-unknown"}:
        control = Path(context["config"]["fault_control"])
        save(control, {"operation": "suspend-listener" if case == "transport-disconnect" else "interrupt-cleanup-observer",
                       "execution_id": binding["execution_id"], "runtime_context": binding["runtime_context"], "nonce": str(uuid.uuid4())})
        if case == "runtime-unknown":
            try:
                authenticated_operation(context, "cleanup")
            except Exception as error:
                if str(error) != "AGENT_CLEANUP_UNKNOWN":
                    raise
                context["authenticated_unknown_error"] = str(error)
            else:
                raise RuntimeError("CLEANUP_OBSERVER_INTERRUPTION_NOT_REJECTED")
        wait_for(lambda: read(control.with_name("fault-observed.json")) if control.with_name("fault-observed.json").exists() else None, 30, "TRUSTED_FAULT_NOT_OBSERVED")
        fault = read(control.with_name("fault-observed.json"))
        if fault.get("execution_id") != binding["execution_id"] or fault.get("runtime_context") != binding["runtime_context"]:
            raise RuntimeError("TRUSTED_FAULT_IDENTITY_MISMATCH")
        wait_for(lambda: exact_state(context) if exact_state(context).get("invalid_reason") else None, 30, "FAULT_INVALIDATION_NOT_OBSERVED")
        if case == "transport-disconnect":
            try:
                passive_observation(context)
            except Exception as error:
                if str(error) != "RUNTIME_TRANSPORT_UNKNOWN":
                    raise
            else:
                raise RuntimeError("SUSPENDED_LISTENER_STILL_REACHABLE")
            if not exact_state(context).get("cleanup_proven"):
                raise RuntimeError("DISCONNECTED_LEASE_CLEANUP_UNPROVEN")
            save(control, {"operation": "restore-listener", "execution_id": binding["execution_id"], "runtime_context": binding["runtime_context"], "nonce": str(uuid.uuid4())})
            wait_for(lambda: read(control.with_name("fault-restored.json")) if control.with_name("fault-restored.json").exists() else None, 30, "LISTENER_NOT_RESTORED")
            passive_observation(context)
            facts = {"transport_listener_unreachable": fault.get("listener_closed") is True,
                     "transport_listener_restored": True, "lease_expired": exact_state(context).get("invalid_reason") == "EXECUTION_LEASE_EXPIRED"}
        else:
            unknown = exact_state(context)
            if unknown.get("state") != "UNKNOWN" or unknown.get("cleanup_proven") is not False:
                raise RuntimeError("CLEANUP_OBSERVER_UNKNOWN_NOT_LATCHED")
            control.unlink()
            context["unknown_before_reconciliation"] = unknown
            # Reconciliation uses the same observed runtime and ownership ledger;
            # it cannot change this failed execution into a successful result.
            context["controller"].terminate(); context["controller"].wait(timeout=30)
            context["config"]["listen_port"] = context["ready"]["port"]
            save(context["config_path"], context["config"])
            Path(context["config"]["ready_file"]).unlink()
            context["controller"] = subprocess.Popen(context["controller_argv"], stdout=context["log"], stderr=context["log"], env=context["controller_env"], start_new_session=True)
            context["controller_processes"].append(context["controller"])
            context["ready"] = wait_for(lambda: read(Path(context["config"]["ready_file"])) if Path(context["config"]["ready_file"]).exists() else None, 45, "UNKNOWN_RECONCILIATION_NOT_OBSERVED")
            facts = {"runtime_unobservable": True, "runtime_observable_after_reconciliation": True,
                     "observation_boundary": "CLEANUP_OBSERVER_UNKNOWN", "daemon_outage_claimed": False,
                     "cleanup_state_while_unobservable": "UNKNOWN", "cleanup_proven_while_unobservable": False,
                     "authenticated_unknown_error": context["authenticated_unknown_error"],
                     "observer_interrupted": fault.get("observer_interrupted") is True}
    elif case in {"timeout", "security-failure"}:
        pass  # Actual Temporal timeout / actual SDK quota causes the failure.
    else:
        raise ValueError("PROBE_CASE_CONTROL_DENIED")
    return facts


def finalize_case(context, case, facts, states, inspection, only_owned):
    if len(states) != 1:
        raise RuntimeError("EXACT_TERMINAL_STATE_REQUIRED")
    state = states[0]
    binding = state["binding"]
    root = context["state_root"] / binding["execution_id"]
    unknown = state.get("outcome") == "UNKNOWN" or state.get("state") == "UNKNOWN"
    receipt = state.get("reconciliation_evidence") if unknown else state.get("cleanup_evidence")
    receipt = receipt if isinstance(receipt, dict) else {}
    receipt_fields = {"version", "execution_id", "attempt", "job_sha256", "owned_config_sha256", "process_owner",
                      "coordinator_pid", "coordinator_reaped", "coordinator_absence_observed", "owned_oci_absent",
                      "coordinator_absent", "node_groups_absent", "private_storage_absent", "no_unknown", "teardown_sha256"}
    mandatory = ("coordinator_absence_observed", "owned_oci_absent", "coordinator_absent", "node_groups_absent", "private_storage_absent")
    receipt_bound = (set(receipt) == receipt_fields and type(receipt.get("version")) is int and receipt["version"] == 1
                     and receipt.get("execution_id") == binding["execution_id"]
                     and receipt.get("attempt") == binding["attempt"]
                     and receipt.get("job_sha256") == state.get("job_sha256")
                     and receipt.get("owned_config_sha256") == state.get("owned_config_sha256")
                     and receipt.get("process_owner") == state.get("process_owner")
                     and receipt.get("coordinator_pid") == state.get("coordinator_pid")
                     and type(receipt.get("coordinator_reaped")) is bool and receipt["coordinator_reaped"] == state.get("coordinator_reaped")
                     and all(receipt.get(key) is True for key in mandatory)
                     and receipt.get("no_unknown") is (False if unknown else True))
    owned = read(root / "owned-config.json")
    job = read(root / "job.json")
    if sha(canonical(owned)) != state.get("owned_config_sha256") or sha(canonical(job)) != state.get("job_sha256"):
        raise RuntimeError("TERMINAL_JOB_IDENTITY_CHANGED")
    teardown = read(root / "host-teardown.json")
    expected_binding = {"execution_id": binding["execution_id"], "attempt": binding["attempt"],
                        "runtime_context": binding["runtime_context"], "job_sha256": state["job_sha256"],
                        "owned_config_sha256": state["owned_config_sha256"], "process_owner": state.get("process_owner")}
    receipt_bound = (receipt_bound and teardown.get("binding") == expected_binding
                     and sha(canonical(teardown)) == receipt.get("teardown_sha256")
                     and teardown.get("node_group_absent") is True and teardown.get("no_unknown") is True)
    resources = list(read(root / "owned/owned-runtime.json").get("resources", {}).values())
    absence = {kind: bool([item for item in resources if item.get("resource") == kind])
               and all(item.get("status") == "REMOVED" and context["docker"].prove_absent(item.get("id", ""))
                       for item in resources if item.get("resource") == kind)
               for kind in ("command", "sdk")}
    process_owner = state.get("process_owner") or {}
    nonce = process_owner.get("owner_nonce")
    private_absent = (isinstance(nonce, str) and re.fullmatch(r"[a-f0-9]{32}", nonce) is not None
                      and not os.path.lexists(root / ("private-execution-" + nonce)) and absence["sdk"])
    journals = list(root.glob("*.publication.json"))
    publication = read(journals[0]) if len(journals) == 1 else {}
    publication_bound = publication.get("execution_id") == binding["execution_id"] and publication.get("attempt") == binding["attempt"]
    is_success = case in {"four-roles", "success", "adversarial"}
    verified = (is_success and state.get("state") == "CLEANED" and state.get("outcome") == "SUCCEEDED"
                and not state.get("invalid_reason") and state.get("cleanup_proven") is True
                and receipt_bound and all(absence.values()) and private_absent and publication_bound
                and publication.get("status") == "COMPLETE" and only_owned)
    partial = case in {"partial-publication", "stale-publication"} and context["data"]["fixture"].joinpath("internal/value.go").read_text().find("Version())*10+2") >= 0
    terminal = {"execution_id": binding["execution_id"], "runtime_context": binding["runtime_context"],
                "role": context["data"]["public_template"]["role"], "status": "VERIFIED" if verified else "UNKNOWN" if unknown else "NOT_VERIFIED",
                "invalid_reason": state.get("invalid_reason"), "unknown_latched": unknown,
                "real_model_calls": 0, "backend_authority": {
                    "no_docker_socket_mount": not any("docker.sock" in item.get("Source", "") or "docker.sock" in item.get("Destination", "") for item in inspection.get("Mounts", [])),
                    "no_remote_docker_authority": not any(value.startswith(("DOCKER_HOST=", "DOCKER_CONTEXT=", "DOCKER_CONFIG=")) for value in inspection.get("Config", {}).get("Env", []))},
                "cleanup": {"state": "CLEANED" if receipt_bound and all(absence.values()) and private_absent else "UNPROVEN",
                            "command_absent": absence["command"], "sdk_absent": absence["sdk"], "private_state_absent": private_absent,
                            "compound_receipt_authenticated": receipt_bound, "reconciled": unknown and receipt_bound},
                "publication": {"status": publication.get("status", "ABSENT") if publication_bound else "UNBOUND", "partial": partial},
                "cleanup_evidence": receipt}
    save(context["evidence"] / "transport-terminal.json", terminal)
    if case == "security-failure":
        facts["policy_rejected"] = teardown.get("terminal_cause") in {"SDK_PRIVATE_STATE_BYTES_EXCEEDED", "SDK_PRIVATE_STATE_FILES_EXCEEDED"}
    analyzer_case = "success" if is_success else "controller-kill" if case == "controller-restart" else case
    if facts:
        save(context["evidence"] / "control-completed.json", {**facts, "action": analyzer_case, "performed": True,
             "execution_id": binding["execution_id"], "runtime_context": binding["runtime_context"],
             "invalid_reason": state.get("invalid_reason")})
    spec = importlib.util.spec_from_file_location("complete_transport_analyzer", context["bundle"] / "lifecycle-analyzer.py")
    analyzer = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(analyzer)
    result = analyzer.analyze(context["evidence"], analyzer_case, context["data"]["public_template"]["role"])
    result["assertions"].update(compound_cleanup_receipt_authenticated=receipt_bound,
                                exact_owned_resources_absent=all(absence.values()), private_storage_absent=private_absent,
                                publication_identity_bound=publication_bound, source_sibling_frozen_unchanged=only_owned,
                                no_backend_daemon_authority=all(terminal["backend_authority"].values()))
    result["status"] = "PASS" if all(result["assertions"].values()) else "FAIL"
    save(context["evidence"] / "transport-temporal-result.json", result)
    return result


def controller_mode(config_file):
    config = read(config_file)
    bundle = Path(config["host_bundle"])
    sys.path.insert(0, str(bundle / "runner/bin"))
    import cdo_agent_runtime as agent
    import cdo_runtime_backend as backend
    import cdo_runtime_transport as transport
    # Load controller runtime dependencies before the first pinned observation.
    import cdo_runtime_owned, cdo_runtime_coordinator, cdo_broker, cdo_go_dependencies, cdo_reconcile, cdo_sdk_state, cdo_sdk_relay
    from cdo_runtime_attestation import TrustedAttestationConfig, capture_attestation, verify_attestation

    class DockerObservation:
        def active_attestation(self):
            def invoke(*argv):
                result = subprocess.run([config["docker_executable"], "--host", config["docker_endpoint"], *argv], capture_output=True, text=True, timeout=15,
                                        env={"PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": "/var/empty"})
                if result.returncode:
                    raise RuntimeError("DOCKER_ATTESTATION_UNKNOWN")
                return result.stdout.strip()
            images = {name: json.loads(invoke("image", "inspect", identity))[0]["Id"] for name, identity in config["images"].items()}
            daemon_id = invoke("info", "--format", "{{.ID}}")
            if daemon_id != config["daemon_id"]:
                raise RuntimeError("DOCKER_DAEMON_IDENTITY_CHANGED")
            return {"executable": config["docker_executable"], "endpoint": config["docker_endpoint"],
                    "daemon_id": daemon_id, "server_version": invoke("version", "--format", "{{.Server.Version}}"), "images": images}

    implementation = tuple(sorted((bundle / "runner/bin").glob("*.py"))) + (bundle / "controller-launcher.py", bundle / "lifecycle-analyzer.py")
    node_files = (bundle / "runner/package.json",) + tuple(sorted((bundle / "runner/dist").glob("*.js"))) + tuple(sorted((bundle / "runner/node_modules/@openai/codex-sdk").rglob("*.js"))) + tuple(sorted((bundle / "runner/node_modules/@openai/codex-sdk").rglob("package.json")))
    public = config["controller_configuration"]
    trusted = TrustedAttestationConfig(
        controller_path=bundle / "runner/bin/cdo_runtime_controller.py", transport_path=bundle / "runner/bin/cdo_runtime_transport.py",
        protocol_sha256=backend.protocol_sha256(), controller_configuration=public,
        signing_policy={"algorithm": "HMAC-SHA256", "request_domain": "cdo-agent-request-v1", "response_domain": "cdo-agent-response-v1", "execution_domain": "cdo-agent-execution-v1", "authority": "backend-template-key", "ownership": "private-host-key"},
        images=config["images"], security_resource_profiles={"agent": "agent-v1", "sdk": "network-none;ro;uid100;caps0;seccomp;tmpfs8MiB256inodes", "command": "network-none;ro;approved-go-test-only;caps0;seccomp"},
        implementation_paths=implementation, python_module_names=tuple(sorted(name for name, module in sys.modules.items()
            if module is not None and getattr(getattr(module, "__spec__", None), "origin", None))),
        runtime_dependency_paths=(Path("/bin/ps").resolve(strict=True),), bind_all_loaded_stdlib=True,
        node_executable=Path(config["node_executable"]), node_dependency_paths=node_files)
    observer = DockerObservation()
    expected_file = Path(config["attestation_file"])

    def file_pins(manifest):
        entries = manifest["implementation"] + manifest["runtime_dependencies"] + manifest["node"]["dependencies"] + manifest["native_runtime"]["files"] + manifest["node"]["native_dependencies"]["files"]
        entries += [manifest["controller"], manifest["transport"], manifest["python"]["executable"], manifest["node"]["executable"], manifest["docker"]["executable"]]
        for module in manifest["python"]["modules"]:
            entries += [module[name] for name in ("file", "bytecode") if name in module]
        return {item["path"]: item["sha256"] for item in entries}

    def precheck(pins):
        for path, digest in pins.items():
            if sha(regular(Path(path), 512 * 1024 * 1024)) != digest:
                raise RuntimeError("ACTIVE_IMPLEMENTATION_CHANGED")

    previous = read(expected_file) if expected_file.exists() else None
    if previous is not None:
        precheck(file_pins(previous))
    # Prewarm trusted observation dependencies, then include every active file
    # module in the immutable manifest used for admission and restart checks.
    capture_attestation(trusted, observer)
    trusted = dataclasses.replace(trusted, python_module_names=tuple(sorted(name for name, module in sys.modules.items()
        if module is not None and getattr(getattr(module, "__spec__", None), "origin", None))))
    expected = previous if previous is not None else capture_attestation(trusted, observer)
    if not expected_file.exists():
        save(expected_file, expected, 0o400)
    # Check observer executables and every supplied implementation before any
    # Node/Docker observation. A changed binary cannot be executed to attest itself.
    pinned_files = file_pins(expected)

    def active():
        precheck(pinned_files)
        actual = capture_attestation(trusted, observer)
        verify_attestation(expected, actual)
        return {"attestation": actual, "controller_configuration": public}

    # Restart must prove identity against the original pinned artifact before
    # reconciliation; it cannot replace the expected artifact with a new capture.
    active()

    templates = [agent.AgentTemplate(
        item["workpackage_id"], item["release_id"], item["role"], item["image_id"], item["scope_sha256"], "agent-v1",
        item["request"], bytes.fromhex(item["secret_hex"]), item["execution_expiry_max_seconds"], item["environment"],
        (("go", "test", "./..."),), item["owned_config"]) for item in config["templates"]]
    kwargs = {"state_root": Path(config["state_root"]), "templates": templates,
              "coordinator_command": (str(Path(sys.executable).resolve()), str(bundle / "runner/bin/cdo_runtime_coordinator.py")),
              "attestation": active, "controller_id": config["controller_id"], "attestation_sha256": sha(canonical(expected))}
    if "controller_configuration" in inspect.signature(agent.AgentController).parameters:
        kwargs["controller_configuration"] = public
    fault_control = Path(config["fault_control"])

    def cleanup(file):
        owned = read(Path(file))
        command = [str(Path(sys.executable).resolve()), str(bundle / "controller-launcher.py"), "--cleanup-observer", str(file)]
        child = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                 env={"PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": "/var/empty", "PYTHONDONTWRITEBYTECODE": "1"}, start_new_session=True)
        fault = read(fault_control) if fault_control.exists() else {}
        if fault.get("operation") == "interrupt-cleanup-observer" and fault.get("execution_id") == owned["execution_id"]:
            # This negative case interrupts an exact fresh observer child. It
            # proves missing observation, never a Docker daemon outage.
            child.kill(); child.wait(timeout=10)
            save(fault_control.with_name("fault-observed.json"), {**fault, "observer_pid": child.pid,
                 "observer_returncode": child.returncode, "observer_error": "CLEANUP_OBSERVER_UNKNOWN",
                 "observer_interrupted": True, "daemon_outage_claimed": False})
            return {"status": "UNKNOWN", "resources_absent": False, "code": "CLEANUP_OBSERVER_UNKNOWN"}
        try:
            output, error = child.communicate(timeout=40)
        except subprocess.TimeoutExpired:
            child.kill(); child.wait(timeout=10)
            raise agent.RuntimeUnknown("CLEANUP_OBSERVER_UNKNOWN") from None
        if child.returncode or len(output) > 1048576:
            raise agent.RuntimeUnknown("CLEANUP_OBSERVER_UNKNOWN")
        return backend.decode(output)

    kwargs["cleanup"] = cleanup
    controller = agent.AgentController(**kwargs)
    recovered = controller.startup_reconcile()
    controller.start_watchdog()
    server = transport.agent_listener(controller, address=("0.0.0.0", config.get("listen_port", 0)))
    save(Path(config["ready_file"]), {"port": server.server_port, "pid": os.getpid(), "controller_id": controller.config.controller_id,
                                      "protocol_sha256": controller.config.protocol_sha256, "attestation_sha256": controller.config.attestation_sha256,
                                      "reconciliation": recovered})

    stopping = threading.Event()
    def terminate(*_):
        stopping.set()
        threading.Thread(target=server.shutdown, daemon=True).start()

    signal.signal(signal.SIGTERM, terminate)
    signal.signal(signal.SIGINT, terminate)
    def listener_faults():
        while not stopping.wait(.05):
            fault = read(fault_control) if fault_control.exists() else {}
            if fault.get("operation") == "suspend-listener":
                server.shutdown()
                return
    threading.Thread(target=listener_faults, daemon=True).start()
    try:
        while not stopping.is_set():
            server.serve_forever(poll_interval=.1)
            if stopping.is_set():
                break
            fault = read(fault_control)
            if fault.get("operation") != "suspend-listener":
                raise RuntimeError("PROBE_LISTENER_FAULT_DENIED")
            port = server.server_port
            server.server_close()
            save(fault_control.with_name("fault-observed.json"), {**fault, "listener_closed": True,
                 "host_watchdog_running": controller._watchdog.is_alive()})
            wait_for(lambda: stopping.is_set() or (fault_control.exists() and read(fault_control).get("operation") == "restore-listener"), 90, "LISTENER_RESTORE_REQUIRED")
            if stopping.is_set():
                break
            server = transport.agent_listener(controller, address=("0.0.0.0", port))
            restored = read(fault_control)
            save(fault_control.with_name("fault-restored.json"), {**restored, "listener_restored": True})
            threading.Thread(target=listener_faults, daemon=True).start()
    finally:
        server.server_close()
        controller.close()


def cleanup_observer(file):
    # Host only; no alternative daemon endpoint or Docker command surface.
    sys.path.insert(0, str(Path(__file__).resolve().parent / "runner/bin"))
    from cdo_runtime_owned import cleanup_owned
    result = cleanup_owned(file)
    sys.stdout.buffer.write(canonical(result))
    return 0


def backend_adversarial(config_file, request_file, output):
    sys.path.insert(0, "/app/runner/bin")
    import cdo_runtime_backend as backend
    credential, request = backend.load_credentials(config_file), read(Path(request_file))
    request.update(execution_id=str(uuid.uuid4()), attempt=1, runtime_context={}, execution_deadline=(datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(seconds=60)).isoformat().replace("+00:00", "Z"))
    base = backend.agent_signed_request(credential, request, "inspect")
    host, port = backend.endpoint(credential["url"])

    def send(value):
        connection = http.client.HTTPConnection(host, port, timeout=10)
        try:
            connection.request("POST", backend.ENDPOINT, backend.canonical(value), {"Content-Type": "application/json"})
            response = connection.getresponse()
            body = response.read(1024 * 1024 + 1)
            if response.status != 200:
                return {"denied": True, "http_status": response.status}
            parsed = backend.decode(body)
            signed = parsed.pop("signature", None)
            if signed != backend.response_signature(backend.request_secret(bytes.fromhex(credential["secret_hex"]), value), parsed):
                raise RuntimeError("ADVERSARIAL_RESPONSE_NOT_AUTHENTICATED")
            return {"denied": parsed.get("ok") is False, "code": parsed.get("error"), "state": parsed.get("result", {}).get("state")}
        finally:
            connection.close()

    accepted = send(base)
    if accepted.get("denied"):
        raise RuntimeError("ADVERSARIAL_BASELINE_INSPECT_FAILED")
    evidence = [{"case": "nonce_replay", **send(base)}]
    mutations = {
        "invalid_signature": {"signature": "0" * 64}, "expired_request": {"request_expires_at": 0},
        "wrong_workpackage": {"workpackage_id": str(uuid.uuid4())}, "wrong_release": {"release_id": "0" * 64},
        "wrong_role": {"role": "reviewer" if credential["template"]["role"] != "reviewer" else "layer"},
        "wrong_scope": {"scope_sha256": "0" * 64}, "unapproved_image": {"image_id": "sha256:" + "0" * 64},
        "wrong_controller": {"controller_id": "0" * 64}, "wrong_protocol": {"protocol_sha256": "0" * 64},
        "stale_attestation": {"attestation_sha256": "0" * 64}, "wrong_request": {"request_sha256": "0" * 64},
        "unapproved_mount": {"mount": "/host"}, "privileged": {"privileged": True},
        "capabilities": {"cap_add": ["SYS_ADMIN"]}, "docker_socket": {"mount": "/var/run/docker.sock"},
        "generic_api": {"operation": "exec"}, "unrelated_inspect": {"target": "unrelated"},
        "unrelated_remove": {"operation": "remove", "target": "unrelated"},
        "frozen_mutation": {"write_paths": ["contracts/frozen.go"]}, "sibling_mutation": {"write_paths": ["internal/sibling.go"]},
        "network": {"network": "host"}, "credentials": {"auth": "/root/.codex/auth.json"},
        "git_lifecycle": {"argv": ["git", "worktree", "add", "/escape"]},
        "resource_escape": {"memory": "unlimited"}, "stale_green": {"result": {"status": "completed"}},
        "cross_execution_binding": {"attempt": 2},
    }
    for name, changes in mutations.items():
        changed = {**base, "nonce": str(uuid.uuid4()), **changes}
        if name != "invalid_signature":
            changed["signature"] = backend.request_signature(backend.request_secret(bytes.fromhex(credential["secret_hex"]), changed), {key: value for key, value in changed.items() if key != "signature"})
        evidence.append({"case": name, **send(changed)})
    save(Path(output), {"route": "SOCKET_FREE_BACKEND_AUTHENTICATED_CONTROLLER", "new_route_denials": evidence,
                        "pass": all(item["denied"] for item in evidence), "historical_finite_128_counted": False, "real_model_calls": 0})
    return 0 if all(item["denied"] for item in evidence) else 1


def make_fixture(role, root, bundle):
    fixture = root / role
    fixture.mkdir(mode=0o700)
    for name in ("internal", "contracts", "cmd"):
        (fixture / name).mkdir()
    manifest = read(bundle / "manifest.json")
    source = Path(manifest["repository"])
    for name in ("go.mod", "go.sum"):
        (fixture / name).write_bytes(regular(source / name))
    (fixture / "internal/value.go").write_text('package sample\nimport "github.com/google/uuid"\nfunc Value() int { _ = uuid.MustParse("11111111-1111-4111-8111-111111111111");return 0 }\n')
    expected = 42 if role == "layer" else 0
    (fixture / "internal/value_test.go").write_text('package sample\nimport "testing"\nfunc TestValue(t *testing.T){if Value()!=' + str(expected) + '{t.Fatal("expected' + str(expected) + '")}}\n')
    (fixture / "internal/sibling.go").write_text("package sample\n")
    (fixture / "contracts/frozen.go").write_text("package contracts\n")
    (fixture / "cmd/main.go").write_text("package main\n")
    return fixture


def load_dependency_bundle(args):
    sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "runner/bin"))
    import cdo_go_dependencies as dependencies
    bundle = Path(args.go_bundle)
    expected = args.go_bundle_manifest_sha256
    if not isinstance(expected, str) or not re.fullmatch("[a-f0-9]{64}", expected):
        raise RuntimeError("GO_DEPENDENCY_APPROVED_PIN_REQUIRED")
    manifest = dependencies.validate(bundle, expected, image=args.command_image)
    dependencies.validate(bundle, expected, source=manifest["repository"], image=args.command_image)
    return bundle, manifest


def main_probe(args):
    if args.case == "all":
        return run_all(args, main_probe)
    repo = Path(__file__).resolve().parents[1]
    output = Path(args.output).resolve()
    output.mkdir(mode=0o700, parents=False, exist_ok=False)
    sys.path.insert(0, str(repo / "runner/bin"))
    import cdo_runtime_backend as backend
    import cdo_release_identity as release
    if not all(value.startswith("sha256:") and len(value) == 71 for value in (args.runner_image, args.sdk_image, args.command_image)) or len(args.source_manifest) != 64:
        raise RuntimeError("IMMUTABLE_IMAGE_AND_SOURCE_REQUIRED")
    executable = Path(shutil.which("docker") or "").resolve(strict=True)
    context = subprocess.check_output([str(executable), "context", "show"], text=True, timeout=15).strip()
    if context != "desktop-linux":
        raise RuntimeError("DOCKER_CONTEXT_CHANGED")
    endpoint = subprocess.check_output([str(executable), "context", "inspect", context, "--format", "{{.Endpoints.docker.Host}}"], text=True, timeout=15).strip()
    if not endpoint.startswith("unix:///"):
        raise RuntimeError("DOCKER_ENDPOINT_DENIED")
    endpoint = "unix://" + str(Path(endpoint[7:]).resolve(strict=True))
    expected = tuple(getattr(args, name, None) for name in ("docker_executable", "docker_endpoint", "daemon_id"))
    if any(expected) and (not all(expected) or (str(executable), endpoint) != expected[:2]):
        raise RuntimeError("PROBE_HOST_CONFIGURATION_CHANGED")
    docker = HostDocker(executable, endpoint, output)
    daemon = docker.run(["info", "--format", "{{.ID}}"]).stdout.strip()
    if not daemon or (all(expected) and daemon != expected[2]):
        raise RuntimeError("PROBE_HOST_CONFIGURATION_CHANGED")
    docker.daemon = daemon
    before = docker.inventory()
    save(output / "unrelated-before.json", before, 0o400)
    bundle = copy_host_bundle(repo, output)
    node = Path(shutil.which("node") or "").resolve(strict=True)
    go_bundle, bundle_manifest = load_dependency_bundle(args)
    driver = Path(args.driver).resolve(strict=True)
    fixture_root = repo.parents[1] / (".cdo-sandbox-certification-20261009-complete-" + uuid.uuid4().hex)
    fixture_root.mkdir(mode=0o700)
    templates, providers, role_data = [], [], {}
    selected = ROLES if args.case == "four-roles" else ("layer",)
    hold = args.case not in {"four-roles", "success", "adversarial", "security-failure", *CASE_PHASES}
    controller = None
    controller_processes = []
    backend_ids = []
    try:
        for role in selected:
            load_dependency_bundle(args)
            role_output = output / role
            role_output.mkdir(mode=0o700)
            fixture = make_fixture(role, fixture_root, go_bundle)
            original = {str(path.relative_to(fixture)): sha(regular(path)) for path in fixture.rglob("*") if path.is_file()}
            provider = Provider(role, fixture, role_output, hold, security_stress=args.case == "security-failure")
            providers.append(provider)
            owned = {"contract": "contracts/current.go", "layer": "internal/value.go", "composition": "cmd/main.go", "reviewer": None}[role]
            request = {"role": "reviewer" if role == "reviewer" else "coder", "working_directory": str(fixture), "model": "gpt-5.4", "reasoning_effort": "low", "prompt": "Complete disposable fixture through the approved broker only.",
                       "output_schema": {"type": "object", "properties": {"status": {"type": "string"}}, "required": ["status"], "additionalProperties": False}, "sandbox_scope": {"profile": role, "write_paths": [] if owned is None else [owned]}}
            save(role_output / "request.json", request, 0o400)
            public_template = {"workpackage_id": str(uuid.uuid4()), "release_id": args.source_manifest, "role": role,
                               "image_id": args.sdk_image, "scope_sha256": sha(canonical(request["sandbox_scope"])), "resource_profile": "agent-v1", "request_sha256": backend.request_digest(request), "execution_expiry_max_seconds": 1200}
            template = {**public_template, "request": request, "secret_hex": secrets.token_hex(32),
                        "environment": {"CDO_SANDBOX_EXECUTION_MODE": "hardened-certification", "CDO_CERTIFICATION_ROOT": str(fixture), "CDO_CERTIFICATION_EVIDENCE": "${EXECUTION_ROOT}/evidence.json", "CDO_CERTIFICATION_PROVIDER_URL": "http://127.0.0.1:" + str(provider.server.server_port) + "/v1", "CDO_BROKER_IMAGE": args.command_image, "CDO_SDK_STATE_IMAGE": args.sdk_image, "CDO_GO_DEPENDENCY_BUNDLE": str(go_bundle), "CDO_GO_DEPENDENCY_MANIFEST_SHA256": args.go_bundle_manifest_sha256, "CDO_BROKER_PYTHON": str(Path(sys.executable).resolve()), "CDO_RUNTIME_NODE_EXECUTABLE": str(node), "CDO_RUNTIME_NODE_ENTRYPOINT": str(bundle / "runner/dist/index.js"), "CDO_DOCKER_EXECUTABLE": str(executable), "CDO_DOCKER_HOST": endpoint},
                        "owned_config": {"version": 1, "docker_executable": str(executable), "docker_endpoint": endpoint, "daemon_id": daemon, "command_image_id": args.command_image, "sdk_image_id": args.sdk_image,
                                         "allowed_mounts": {"/workspace": {"under": "${EXECUTION_ROOT}", "kind": "snapshot"}, "/dependency": str(go_bundle), "/reference/supervisor.py": str(bundle / "runner/bin/cdo_sdk_state.py"), "/reference/cdo_sdk_relay.py": str(bundle / "runner/bin/cdo_sdk_relay.py"), "/reference/schema.json": {"under": "${EXECUTION_ROOT}", "kind": "schema"}}}}
            gate = role_output / "phase-gate"
            gate.mkdir(mode=0o700)
            if args.case in CASE_PHASES:
                template["environment"].update(CDO_LIFECYCLE_GATE=str(gate), CDO_LIFECYCLE_PHASE=CASE_PHASES[args.case])
            templates.append(template)
            role_data[role] = {"output": role_output, "fixture": fixture, "original": original, "owned": owned, "public_template": public_template, "template": template, "provider": provider}
        state_root = output / "controller-state"
        state_root.mkdir(mode=0o700)
        if args.installed_build_manifest:
            installed = read(Path(args.installed_build_manifest).resolve(strict=True))
        else:
            inventory_id = docker.run(["create", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true", "--security-opt", "seccomp=builtin", "--entrypoint", "python3", args.runner_image, "-c", "import pathlib;print(pathlib.Path('/app/release/build.json').read_text())"]).stdout.strip()
            try:
                installed = json.loads(docker.run(["start", "--attach", inventory_id]).stdout)
            finally:
                docker.cleanup(inventory_id, args.runner_image)
            save(output / "installed-build-manifest.json", installed, 0o400)
        if installed["source_manifest_sha256"] != args.source_manifest:
            raise RuntimeError("INSTALLED_SOURCE_MANIFEST_MISMATCH")
        if (type(installed.get("identity")) is not dict or set(installed["identity"]) != release.STATIC_IDENTITY_KEYS
                or any(not isinstance(value, str) or not value or value == "UNKNOWN"
                       or key.endswith("_sha256") and not re.fullmatch(r"[a-f0-9]{64}", value)
                       for key, value in installed["identity"].items())):
            raise RuntimeError("INSTALLED_STATIC_RELEASE_IDENTITY_INCOMPLETE")
        release_identity = {**installed["identity"], "runner_image": args.runner_image, "sdk_image": args.sdk_image, "command_image": args.command_image, "docker_version": docker.run(["version", "--format", "{{.Server.Version}}"]).stdout.strip()}
        public_templates = [{**data["public_template"], "sandbox_scope": data["template"]["request"]["sandbox_scope"],
                             "environment": data["template"]["environment"], "owned_config": data["template"]["owned_config"],
                             "approved_commands": [["go", "test", "./..."]], "capability_key_id": sha(b"key-id-v1" + bytes.fromhex(data["template"]["secret_hex"]))}
                            for data in role_data.values()]
        public = {"release_identity": release_identity, "lease_seconds": 10, "production_admission": "DENIED", "budget": {"mode": "enforce", "max_runs_5h": 20}, "templates": public_templates}
        controller_id = subprocess.check_output([str(Path(sys.executable).resolve()), "-c", "import sys;sys.path.insert(0,sys.argv[1]);import cdo_agent_runtime;print(cdo_agent_runtime.controller_identity())", str(bundle / "runner/bin")], text=True, timeout=10, env={"PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": "/var/empty", "PYTHONDONTWRITEBYTECODE": "1"}).strip()
        if (controller_id != installed["identity"]["agent_runtime_controller_sha256"]
                or backend.protocol_sha256() != installed["identity"]["agent_runtime_protocol_sha256"]):
            raise RuntimeError("HOST_AND_INSTALLED_AGENT_IDENTITY_MISMATCH")
        config = {"host_bundle": str(bundle), "state_root": str(state_root), "docker_executable": str(executable), "docker_endpoint": endpoint, "daemon_id": daemon, "node_executable": str(node), "images": {"runner": args.runner_image, "sdk": args.sdk_image, "command": args.command_image}, "controller_id": controller_id, "controller_configuration": public, "templates": templates, "attestation_file": str(output / "host-attestation.json"), "ready_file": str(output / "controller-ready.json"), "listen_port": 0}
        config["fault_control"] = str(output / "fault-control.json")
        public["certification_fault_control"] = {"path": config["fault_control"], "case": args.case,
                                                "runtime_unknown_boundary": "CLEANUP_OBSERVER_UNKNOWN", "daemon_outage_claimed": False}
        config_path = output / "controller-config.json"
        save(config_path, config)
        log = open(output / "controller.log", "wb")
        controller = subprocess.Popen([str(Path(sys.executable).resolve()), str(bundle / "controller-launcher.py"), "--controller", str(config_path)], stdout=log, stderr=log,
                                      env={"PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": "/var/empty", "PYTHONDONTWRITEBYTECODE": "1"}, start_new_session=True)
        controller_processes.append(controller)
        ready = wait_for(lambda: read(output / "controller-ready.json") if (output / "controller-ready.json").exists() else None, 45, "CONTROLLER_START_FAILED")
        for role, data in role_data.items():
            credential = {"version": 1, "url": "http://host.docker.internal:" + str(ready["port"]), "controller_id": ready["controller_id"], "protocol_sha256": ready["protocol_sha256"], "attestation_sha256": ready["attestation_sha256"], "template": data["public_template"], "secret_hex": data["template"]["secret_hex"]}
            credential_path = data["output"] / "backend-key.json"
            save(credential_path, credential, 0o400)
            name = "cdo-complete-backend-" + uuid.uuid4().hex
            workflow = "cdo-complete-" + role + "-" + uuid.uuid4().hex
            # Trusted evidence lives in a HARD bounded tmpfs, not a host bind.
            # It is copied out by the host observer after each checkpoint.
            bootstrap = "import shutil,os;shutil.copyfile('/verification/request.json','/execution/evidence/request.json');os.execv('/verification/driver',['/verification/driver'])"
            uid, gid = os.geteuid(), os.getegid()
            run = ["create", "--name", name, "--read-only", "--user", str(uid) + ":" + str(gid), "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true", "--security-opt", "seccomp=builtin", "--pids-limit", "128", "--memory", "512m", "--memory-swap", "512m", "--cpus", "2", "--ulimit", "nofile=256:256", "--ulimit", "fsize=8388608:8388608", "--tmpfs", "/execution/evidence:rw,nosuid,nodev,noexec,size=33554432,nr_inodes=256,mode=0700,uid=" + str(uid) + ",gid=" + str(gid), "--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=1048576,nr_inodes=64,mode=1777", "--mount", "type=bind,src=" + str(driver) + ",dst=/verification/driver,readonly", "--mount", "type=bind,src=" + str(data["output"] / "request.json") + ",dst=/verification/request.json,readonly", "--mount", "type=bind,src=" + str(credential_path) + ",dst=/verification/backend-key.json,readonly", "--env", "CDO_SANDBOX_EXECUTION_MODE=hardened-certification", "--env", "CDO_RUNTIME_BACKEND_CREDENTIALS=/verification/backend-key.json", "--env", "CDO_LIFECYCLE_OUTPUT=/execution/evidence", "--env", "CDO_WORKFLOW_ID=" + workflow, "--env", "CDO_TEMPORAL_ADDRESS=host.docker.internal:17233", "--env", "CDO_RUNNER_COMMAND=python3 /app/runner/bin/cdo_runtime_backend.py", "--env", "CDO_CANCEL_KIND=" + ("workflow" if args.case == "workflow-cancel" else "timeout" if args.case == "timeout" else "activity"), "--env", "CODEX_SOL_MAX_RUNS_5H=20", "--env", "CODEX_BUDGET_MODE=enforce", "--entrypoint", "python3", args.runner_image, "-c", bootstrap]
            run[run.index("--entrypoint"):run.index("--entrypoint")] = ["--env", "CDO_ACTIVITY_TIMEOUT_SECONDS=" + ("60" if args.case == "timeout" else "120")]
            if args.case == "adversarial":
                launch_backend_adversarial(docker, run, args.runner_image, bundle / "controller-launcher.py", data["output"], backend_ids)
            full_id = docker.run(run).stdout.strip()
            backend_ids.append(full_id)
            inspected = json.loads(docker.run(["inspect", full_id]).stdout)[0]
            if inspected["Image"] != args.runner_image or any("docker.sock" in mount.get("Source", "") or "docker.sock" in mount.get("Destination", "") or mount.get("Source") == str(data["fixture"]) for mount in inspected["Mounts"]):
                raise RuntimeError("BACKEND_AUTHORITY_BOUNDARY_FAILED")
            save(data["output"] / "backend-inspection.json", inspected)
            docker.run(["start", full_id])
            local_evidence = data["output"] / "backend-evidence"
            local_evidence.mkdir(mode=0o700)

            def copy_evidence():
                docker.run(["cp", full_id + ":/execution/evidence/.", str(local_evidence)], check=False)
                return local_evidence

            context = {"docker": docker, "full_id": full_id, "image": args.runner_image, "data": data,
                       "state_root": state_root, "bundle": bundle, "config": config, "config_path": config_path,
                       "controller": controller, "ready": ready, "log": log, "driver": driver,
                       "controller_processes": controller_processes,
                       "backend_ids": backend_ids, "evidence": local_evidence, "copy_evidence": copy_evidence,
                       "controller_argv": [str(Path(sys.executable).resolve()), str(bundle / "controller-launcher.py"), "--controller", str(config_path)],
                       "controller_env": {"PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": "/var/empty", "PYTHONDONTWRITEBYTECODE": "1"}}
            facts = {}
            if hold or args.case in CASE_PHASES or args.case == "security-failure":
                wait_for(lambda: (copy_evidence() / "activity-start.json").exists() and data["provider"].seen, 60, "ACTIVE_EXECUTION_NOT_OBSERVED")
                if args.case in CASE_PHASES:
                    wait_for(lambda: (data["output"] / "phase-gate" / (CASE_PHASES[args.case] + ".ready")).exists(), 120, "ACTUAL_EXECUTION_PHASE_NOT_OBSERVED")
                facts = perform_case(args.case, context)
                controller, ready = context["controller"], context["ready"]
            wait_for(lambda: json.loads(docker.run(["inspect", full_id]).stdout)[0]["State"]["Running"] is False, 200, "BACKEND_EXECUTION_TIMEOUT")
            if args.case != "backend-kill":
                copy_evidence()
            logs = docker.run(["logs", full_id], check=False)
            save(data["output"] / "backend-output.json", {"stdout": logs.stdout, "stderr": logs.stderr})
            exit_code = json.loads(docker.run(["inspect", full_id]).stdout)[0]["State"]["ExitCode"]
            record = read(local_evidence / "runner-return.json") if (local_evidence / "runner-return.json").exists() else None
            state_records = authenticated_state_records(state_root, data["public_template"]["workpackage_id"])
            changes = {str(path.relative_to(data["fixture"])): sha(regular(path)) for path in data["fixture"].rglob("*") if path.is_file()}
            only_owned = all(changes.get(name) == digest for name, digest in data["original"].items() if name != data["owned"]) and not (set(changes) - set(data["original"]) - ({data["owned"]} if data["owned"] else set()))
            success_case = args.case in {"four-roles", "success", "adversarial"}
            assertions = {"backend_exit_expected": exit_code == (137 if args.case == "backend-kill" else 0), "temporal_identity_bound": read(local_evidence / "activity-start.json")["trusted_runtime_context_bound"] is True, "source_sibling_frozen_unchanged": only_owned, "actual_temporal_history": (local_evidence / "history.jsonl").stat().st_size > 0,
                          "result_success": bool(record and record["error"] == "<nil>") if success_case else record is None if args.case == "backend-kill" else bool(record and record["error"] != "<nil>"), "controller_cleanup": bool(state_records) and all(state["cleanup_proven"] for state in state_records), "real_model_calls_zero": True}
            if success_case:
                assertions.update(data["provider"].assertions())
            strict = finalize_case(context, args.case, facts, state_records, inspected, only_owned)
            assertions["strict_actual_temporal_analysis"] = strict["status"] == "PASS"
            save(data["output"] / "case-executed.json", {"case": args.case, "analyzer_case": strict["case"],
                 "execution_id": state_records[0]["binding"]["execution_id"], "actual_runtime_started": True,
                 "action": facts, "strict_status": strict["status"], "source_only_mock_evidence": False})
            save(data["output"] / "role-result.json", {"role": role, "assertions": assertions, "actual_runner_response": record, "controller_records": state_records, "pass": all(assertions.values())})
            docker.cleanup(full_id, args.runner_image)
            backend_ids.remove(full_id)
        after = docker.inventory()
        save(output / "unrelated-after.json", after, 0o400)
        roles = {role: read(data["output"] / "role-result.json") for role, data in role_data.items()}
        result = {"case": args.case, "scope": "FOUR_INITIAL_ROLE_COMPATIBILITY" if args.case == "four-roles" else "ONE_EXPLICIT_RUNTIME_CASE", "full_certification_claimed": False, "roles": roles, "backend_controller_route": all(item["pass"] for item in roles.values()), "unrelated_inventory_unchanged": before == after, "historical_finite_denials_counted": False, "real_model_calls": 0, "budget": "20/enforce", "production_admission": "DENIED", "sandbox_ready": "NO", "pass": all(item["pass"] for item in roles.values()) and before == after,
                  "required_runtime_capabilities": list(REQUIRED_RUNTIME_CAPABILITIES),
                  "runtime_unknown_boundary": "CLEANUP_OBSERVER_UNKNOWN", "actual_daemon_outage_claimed": False,
                  "real_daemon_unavailability_capability": "REQUIRES_SEPARATELY_AUTHORIZED_EXTERNAL_RUNTIME_FAULT"}
        save(output / "complete-transport-evidence.json", result)
        print(json.dumps({"pass": result["pass"], "output": str(output), "real_model_calls": 0}))
        return 0 if result["pass"] else 1
    finally:
        cleanup_errors = []
        # Retain exact fresh child handles across every controller restart.
        # A replaced local handle must not orphan the replacement process.
        # Never force-kill a coordinator before its own teardown proof finishes.
        for process in controller_processes:
            try:
                if process.poll() is None:
                    process.terminate()
                    process.wait(timeout=30)
            except Exception as error:
                cleanup_errors.append(type(error).__name__)
        for full_id in backend_ids:
            try:
                docker.cleanup(full_id, args.runner_image)
            except Exception as error:
                cleanup_errors.append(type(error).__name__)
        for provider in providers:
            try:
                provider.close()
            except Exception as error:
                cleanup_errors.append(type(error).__name__)
        if cleanup_errors:
            raise RuntimeError("PROBE_RESOURCE_CLEANUP_UNKNOWN:" + ",".join(cleanup_errors))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--controller")
    parser.add_argument("--cleanup-observer")
    parser.add_argument("--backend-adversarial", nargs=3, metavar=("CREDENTIALS", "REQUEST", "OUTPUT"))
    parser.add_argument("--output")
    parser.add_argument("--case", default="four-roles", choices=("all", "success", *ALL_CASES))
    parser.add_argument("--runner-image")
    parser.add_argument("--sdk-image")
    parser.add_argument("--command-image")
    parser.add_argument("--source-manifest")
    parser.add_argument("--go-bundle")
    parser.add_argument("--go-bundle-manifest-sha256")
    parser.add_argument("--driver")
    parser.add_argument("--installed-build-manifest")
    parser.add_argument("--docker-executable")
    parser.add_argument("--docker-endpoint")
    parser.add_argument("--daemon-id")
    args = parser.parse_args()
    if args.controller:
        return controller_mode(Path(args.controller)) or 0
    if args.cleanup_observer:
        return cleanup_observer(args.cleanup_observer)
    if args.backend_adversarial:
        return backend_adversarial(*args.backend_adversarial)
    if any((args.docker_executable, args.docker_endpoint, args.daemon_id)) and not all((args.docker_executable, args.docker_endpoint, args.daemon_id)):
        parser.error("Docker executable, endpoint and daemon pins must be supplied together")
    if not all((args.output, args.runner_image, args.sdk_image, args.command_image, args.source_manifest, args.go_bundle, args.go_bundle_manifest_sha256, args.driver)):
        parser.error("explicit output, immutable images, source manifest, approved Go bundle manifest pin and driver required")
    return main_probe(args)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as error:
        # This is trusted certification output, never a production grant.
        print(json.dumps({"pass": False, "error": type(error).__name__, "code": str(error)[:200], "real_model_calls": 0}), file=sys.stderr)
        raise SystemExit(1)
