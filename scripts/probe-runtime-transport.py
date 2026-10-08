#!/usr/bin/env python3
"""Owner-approved, credential-free host-controller probe on disposable fixtures.

Never mounts a Docker socket, consumes a qualification grant or calls a model.
This is finite transport evidence, not final production coordinator certification.
"""
from __future__ import annotations
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import threading
import time
import uuid

REPO = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPO / "runner/bin"))
import cdo_runtime_controller as runtime
import cdo_runtime_transport as transport

COMMAND_IMAGE = "sha256:a63ad55c3990b47bbe62b51de214e3668936f79b3b55800ef2e3bf63c7591653"
SDK_IMAGE = "sha256:7bc55a3d9834c84e3d411e2450338fb466a536f736869087515beec72d68cdfe"
FIXTURE = Path("/Volumes/ZX10/Developments/.cdo-sandbox-certification-20261008/release-final-go")
DEPENDENCY = REPO / ".cache/go-dependencies/go-deps-fixture-pcs74yoa"


def host_read(*args):
    return subprocess.check_output(["/usr/local/bin/docker", *args], timeout=20, text=True).strip()


def inventory():
    # Status changes in unrelated naturally restarting containers are not caused
    # by this probe; compare identities and creation times, not uptime strings.
    rows = host_read("ps", "-a", "--format", "{{.ID}} {{.Names}} {{.CreatedAt}}")
    return sorted(rows.splitlines())


def readonly_copy(source, destination):
    file_inventory(source)
    # Copy links as links and reject them: never dereference into external data.
    shutil.copytree(source, destination, symlinks=True)
    for path in sorted(destination.rglob("*"), reverse=True):
        if path.is_symlink():
            raise RuntimeError("FIXTURE_LINK_DENIED")
        path.chmod(0o555 if path.is_dir() else 0o444)
    destination.chmod(0o555)


def file_inventory(source):
    if source.resolve(strict=True) != source or not source.is_dir():
        raise RuntimeError("FIXTURE_ROOT_DENIED")
    result = {}
    for path in source.rglob("*"):
        relative = path.relative_to(source)
        if any(part in runtime.PROTECTED or part.startswith(".env.") for part in relative.parts):
            raise RuntimeError("FIXTURE_PROTECTED_PATH_DENIED")
        if path.is_symlink() or path.resolve(strict=True) != path:
            raise RuntimeError("FIXTURE_LINK_DENIED")
        if path.is_file():
            if path.stat().st_nlink != 1:
                raise RuntimeError("FIXTURE_HARDLINK_DENIED")
            result[str(path.relative_to(source))] = hashlib.sha256(path.read_bytes()).hexdigest()
        elif not path.is_dir():
            raise RuntimeError("FIXTURE_SPECIAL_FILE_DENIED")
    return result


def production_source():
    manifest = {}
    for name in ("cmd", "internal", "runner", "docker", "scripts", "agent-system", ".ai", "go.mod", "go.sum"):
        source = REPO / name
        for path in ([source] if source.is_file() else sorted(source.rglob("*"))):
            if not path.is_file():
                continue
            relative = path.relative_to(REPO)
            if any(p in ("node_modules", "dist", ".cache", "__pycache__") or p.startswith("._") for p in relative.parts):
                continue
            if path.is_symlink() or path.name == ".env" or path.name.startswith(".env."):
                raise RuntimeError("SOURCE_INPUT_DENIED")
            manifest[str(relative)] = hashlib.sha256(path.read_bytes()).hexdigest()
    return hashlib.sha256(runtime.canonical(manifest)).hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    output = Path(args.output).resolve()
    if not output.is_relative_to(REPO / ".cache/sandbox-certification"):
        raise RuntimeError("EVIDENCE_LOCATION_DENIED")
    output.mkdir(mode=0o700, exist_ok=False)
    context = host_read("context", "show")
    if context != "desktop-linux":
        raise RuntimeError("DOCKER_CONTEXT_MISMATCH")
    endpoint = host_read("context", "inspect", "desktop-linux", "--format", "{{.Endpoints.docker.Host}}")
    if not endpoint.startswith("unix://"):
        raise RuntimeError("DOCKER_ENDPOINT_DENIED")
    endpoint = "unix://" + str(Path(endpoint[7:]).resolve(strict=True))
    daemon_id = host_read("info", "--format", "{{.ID}}")
    server_version = host_read("version", "--format", "{{.Server.Version}}")
    before = inventory()
    fixture_before = file_inventory(FIXTURE)
    state = output / "private-state"
    state.mkdir(mode=0o700)
    references = output / "references"
    references.mkdir(mode=0o755)
    source = references / "source"
    readonly_copy(FIXTURE, source)
    dependency = references / "dependency"
    readonly_copy(DEPENDENCY, dependency)
    source_pin, dependency_pin = runtime.source_digest(source), runtime.source_digest(dependency)
    source_manifest = production_source()
    controller_id = runtime.controller_identity()
    registrations = []
    scopes = {"contract": ["contracts/frozen.go"], "layer": ["internal/value.go"],
              "composition": ["cmd/main.go"], "reviewer": []}
    for recipe in ("command-runtime", "sdk-runtime"):
        helper = references / (recipe + ".py")
        helper.write_bytes(runtime.COMMAND_HELPER if recipe == "command-runtime" else runtime.SDK_HELPER)
        helper.chmod(0o444)
        for role in sorted(scopes):
            registrations.append(runtime.ExecutionRegistration(
                execution_id=str(uuid.uuid4()), release_id=source_manifest,
                workpackage_id=str(uuid.uuid4()), role=role, recipe=recipe,
                image_id=COMMAND_IMAGE if recipe == "command-runtime" else SDK_IMAGE,
                scope_sha256=hashlib.sha256(runtime.canonical(scopes[role])).hexdigest(),
                execution_expires_at=int(time.time()) + 900, secret=os.urandom(32),
                helper_path=helper, helper_sha256=runtime.helper_digest(recipe),
                source_root=source if recipe == "command-runtime" else None,
                source_sha256=source_pin if recipe == "command-runtime" else None,
                dependency_root=dependency if recipe == "command-runtime" else None,
                dependency_sha256=dependency_pin if recipe == "command-runtime" else None,
            ))
    audit = []

    class AuditedDocker(runtime.DockerCLI):
        def _run(self, arguments):
            audit.append(list(arguments))
            return super()._run(arguments)

    docker = AuditedDocker(executable=str(Path("/usr/local/bin/docker").resolve(strict=True)), endpoint=endpoint, expected_daemon_id=daemon_id)
    controller = runtime.RuntimeController(runtime.ControllerConfig(
        controller_id=controller_id, state_root=state,
        command_image_id=COMMAND_IMAGE, sdk_image_id=SDK_IMAGE), registrations, docker)
    controller.start_watchdog()
    server = transport.listener(controller, registrations)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    url = "http://127.0.0.1:" + str(server.server_port)
    evidence = {"schema_version": 1, "classification": "FINITE_TRANSPORT_PROBE_NOT_PRODUCTION_CERTIFICATION",
                "context": context, "daemon_id": daemon_id, "docker_version": server_version,
                "source_manifest_sha256": source_manifest, "controller_sha256": controller_id,
                "protocol_sha256": runtime.protocol_sha256(),
                "transport_sha256": hashlib.sha256((REPO / "runner/bin/cdo_runtime_transport.py").read_bytes()).hexdigest(),
                "model_calls": 0, "roles": [], "api_rejections": [], "cleanup": []}
    evidence["diagnostic_attempts"] = []
    try:
        for registration in registrations:
            client = transport.RuntimeClient(url, registration, controller_id)
            # Even authenticated callers cannot add flags or expand identities.
            base = runtime.signed_request(registration, "create", int(time.time()) + 30)
            attacks = [{**base, "operation": op} for op in ("exec", "build", "prune", "reset", "volume_delete", "image_delete")]
            attacks.extend({**base, key: value} for key, value in (
                ("mounts", ["/var/run/docker.sock"]), ("privileged", True), ("cap_add", ["SYS_ADMIN"]),
                ("security_opt", ["seccomp=unconfined"]), ("network", "host"),
                ("container_id", "unrelated"), ("argv", ["sh"]), ("entrypoint", "/bin/sh"),
                ("labels", {"cdo.execution_id": str(uuid.uuid4())}), ("memory", 1 << 40)))
            for attack in attacks:
                count = len(audit)
                try:
                    controller.handle(attack)
                    raise RuntimeError("ADVERSARIAL_REQUEST_ACCEPTED")
                except runtime.ControllerDenied as error:
                    if len(audit) != count:
                        raise RuntimeError("DENIED_REQUEST_REACHED_DOCKER")
                    evidence["api_rejections"].append({"execution_id": registration.execution_id, "code": str(error)})
            created = client.call("create")
            started = client.call("start")
            deadline = time.monotonic() + 75
            while True:
                observed = client.call("inspect")
                if observed["status"] == "EXITED":
                    break
                if time.monotonic() >= deadline:
                    raise RuntimeError("PROBE_DEADLINE_EXCEEDED")
                time.sleep(.2)
            diagnostics = client.call("diagnostics")
            evidence["diagnostic_attempts"].append({"execution_id": registration.execution_id,
                                                     "recipe": registration.recipe, "role": registration.role,
                                                     "diagnostics": diagnostics})
            data = json.loads(diagnostics["diagnostics"]["output"])
            if data.get("controls") != "PASS":
                raise RuntimeError("CONTROL_PROBE_FAILED")
            if registration.recipe == "command-runtime":
                if data.get("go_exit_code") != 1 or "TestValue" not in data.get("go_output", "") or "expected42" not in data.get("go_output", ""):
                    raise RuntimeError("SEMANTIC_PREFLIGHT_RED_UNPROVEN")
            elif data.get("cli_version") != "codex-cli 0.144.6":
                raise RuntimeError("SDK_CLI_IDENTITY_MISMATCH")
            evidence["roles"].append({"execution_id": registration.execution_id,
                                      "workpackage_id": registration.workpackage_id,
                                      "role": registration.role, "recipe": registration.recipe,
                                      "image_id": registration.image_id, "created": created,
                                      "started": started, "diagnostics": diagnostics})
            evidence["cleanup"].append(client.call("cleanup"))
    except Exception as error:
        evidence["failure"] = type(error).__name__ + ":" + str(error)
        raise
    finally:
        for registration in registrations:
            try:
                evidence["cleanup"].append(controller.handle(runtime.signed_request(registration, "cleanup", int(time.time()) + 30)))
            except runtime.ControllerDenied as error:
                evidence["cleanup"].append({"execution_id": registration.execution_id, "status": "UNKNOWN", "error": str(error)})
        server.shutdown()
        thread.join(timeout=2)
        server.server_close()
        controller.close()
        after = inventory()
        evidence["unrelated_container_inventory_unchanged"] = before == after
        evidence["docker_argv_audit"] = audit
        fixture_after = file_inventory(FIXTURE)
        evidence["source_sibling_contract_hashes_unchanged"] = fixture_before == fixture_after
        evidence["fixture_hashes"] = fixture_after
        evidence["no_reset_prune_volume_deletion"] = not any(
            any(word in ("prune", "reset", "volume", "rmi") for word in arguments) for arguments in audit)
        (output / "transport-evidence.json").write_bytes(runtime.canonical(evidence))
    if evidence.get("failure") or not evidence["unrelated_container_inventory_unchanged"] or not evidence["source_sibling_contract_hashes_unchanged"] or any(row["status"] == "UNKNOWN" for row in evidence["cleanup"]):
        raise RuntimeError("TRANSPORT_PROBE_FAILED")
    print(json.dumps({"result": "PASS_FINITE_TRANSPORT_ONLY", "evidence": str(output / "transport-evidence.json"),
                      "model_calls": 0, "production_admission": "DENIED"}))


if __name__ == "__main__":
    main()
