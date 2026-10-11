#!/usr/bin/env python3
"""Record actual installed build bytes; this is not runtime certification."""
import hashlib
import importlib.util
import json
import pathlib
import subprocess
import sys
from contextlib import contextmanager, ExitStack


CONTROLLER_SOURCE_FILES = {"finite_controller": "cdo_runtime_controller.py",
                           "agent_controller": "cdo_agent_runtime.py",
                           "owned_runtime": "cdo_runtime_owned.py",
                           "coordinator": "cdo_runtime_coordinator.py",
                           "attestation": "cdo_runtime_attestation.py", "broker": "cdo_broker.py"}
TRANSPORT_SOURCE_FILES = {"backend_client": "cdo_runtime_backend.py",
                         "http_transport": "cdo_runtime_transport.py",
                         "sdk_relay": "cdo_sdk_relay.py", "sdk_state": "cdo_sdk_state.py"}


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


@contextmanager
def installed_module(file, name):
    # Import only the inventoried installed file, never a cwd/PYTHONPATH module.
    spec = importlib.util.spec_from_file_location(name, file)
    if spec is None or spec.loader is None:
        raise RuntimeError("RUNTIME_PROTOCOL_IDENTITY_UNAVAILABLE")
    module = importlib.util.module_from_spec(spec)
    previous = sys.modules.get(name)
    previous_bytecode = sys.dont_write_bytecode
    sys.modules[name] = module
    sys.dont_write_bytecode = True
    try:
        spec.loader.exec_module(module)
        yield module
    finally:
        sys.dont_write_bytecode = previous_bytecode
        if previous is None:
            del sys.modules[name]
        else:
            sys.modules[name] = previous


def installed_protocol_sha256(controller):
    with installed_module(controller, "_cdo_installed_runtime_controller") as module:
        canonical = json.dumps(module.PROTOCOL_DESCRIPTOR, sort_keys=True,
                               separators=(",", ":"), ensure_ascii=True,
                               allow_nan=False).encode("utf-8")
        digest = hashlib.sha256(canonical).hexdigest()
        if module.protocol_sha256() != digest:
            raise RuntimeError("RUNTIME_PROTOCOL_IDENTITY_MISMATCH")
        return digest


def composite_sha256(files, sources):
    values = {name: files["runner/bin/" + file] for name, file in sources.items()}
    return hashlib.sha256(json.dumps(values, sort_keys=True,
                                    separators=(",", ":")).encode()).hexdigest()


def installed_agent_identities(root, files):
    bin_root = root / "runner/bin"
    controller = composite_sha256(files, CONTROLLER_SOURCE_FILES)
    transport = composite_sha256(files, TRANSPORT_SOURCE_FILES)
    with ExitStack() as installed:
        installed.enter_context(installed_module(bin_root / "cdo_runtime_controller.py", "cdo_runtime_controller"))
        backend = installed.enter_context(installed_module(bin_root / "cdo_runtime_backend.py", "cdo_runtime_backend"))
        installed.enter_context(installed_module(bin_root / "cdo_runtime_attestation.py", "cdo_runtime_attestation"))
        agent = installed.enter_context(installed_module(bin_root / "cdo_agent_runtime.py", "cdo_agent_runtime"))
        if (agent.CONTROLLER_SOURCE_FILES != CONTROLLER_SOURCE_FILES
                or agent.TRANSPORT_SOURCE_FILES != TRANSPORT_SOURCE_FILES
                or agent.controller_identity() != controller
                or agent.transport_identity() != transport):
            raise RuntimeError("AGENT_RUNTIME_IMPLEMENTATION_IDENTITY_MISMATCH")
        protocol = installed_protocol_sha256(bin_root / "cdo_runtime_backend.py")
        if backend.protocol_sha256() != protocol or agent.agent_protocol_sha256() != protocol:
            raise RuntimeError("AGENT_RUNTIME_PROTOCOL_IDENTITY_MISMATCH")
    return {"agent_runtime_controller_sha256": controller,
            "agent_runtime_transport_sha256": transport,
            "agent_runtime_protocol_sha256": protocol}


def build_identity(root):
    runner = root / "runner"
    files = {str(p.relative_to(root)): sha(p) for p in sorted(runner.rglob("*"))
             if p.is_file() and not p.is_symlink() and "__pycache__" not in p.parts}
    for name in ("course-dev-orchestrator", "release/profile.v1.json",
                 "release/role-policy.json", "release/source-manifest.json"):
        files[name] = sha(root / name)
    symlinks = {str(p.relative_to(root)): str(p.readlink())
                for p in runner.rglob("*") if p.is_symlink()}
    source = files["release/source-manifest.json"]
    # Missing/symlinked controller or transport bytes are absent from inventory
    # and fail this build instead of producing an unbound release candidate.
    controller = "runner/bin/cdo_runtime_controller.py"
    transport = "runner/bin/cdo_runtime_transport.py"
    controller_sha256 = files[controller]
    transport_sha256 = files[transport]
    protocol_sha256 = installed_protocol_sha256(root / controller)
    agent_identity = installed_agent_identities(root, files)
    identity = {
        "source_manifest_sha256": source,
        "orchestrator_sha256": files["course-dev-orchestrator"],
        "runner_manifest_sha256": hashlib.sha256(json.dumps(
            {"installed_files": files, "installed_symlinks": symlinks},
            sort_keys=True, separators=(",", ":")).encode()).hexdigest(),
        "broker_sha256": sha(runner / "bin/cdo_broker.py"),
        "publisher_sha256": sha(runner / "bin/cdo_broker.py"),
        "dependency_preparer_sha256": sha(runner / "bin/cdo_go_dependencies.py"),
        "reconciliation_sha256": sha(runner / "bin/cdo_reconcile.py"),
        "runtime_controller_sha256": controller_sha256,
        "runtime_protocol_sha256": protocol_sha256,
        "runtime_transport_sha256": transport_sha256,
        "profile_sha256": sha(root / "release/profile.v1.json"),
        "role_policy_sha256": sha(root / "release/role-policy.json"),
        "sdk_version": json.loads((runner / "node_modules/@openai/codex-sdk/package.json").read_text())["version"],
        "cli_version": subprocess.check_output(
            [str(runner / "node_modules/.bin/codex"), "--version"], text=True).strip(),
        "go_version": subprocess.check_output(["go", "version"], text=True).strip(),
        "security_policy_sha256": sha(root / "release/profile.v1.json"),
        "resource_policy_sha256": sha(root / "release/profile.v1.json"),
        **agent_identity,
    }
    return {"identity": identity, "installed_files": files,
            "installed_symlinks": symlinks,
            "source_manifest_sha256": source}


if __name__ == "__main__":
    root = pathlib.Path("/app")
    (root / "release/build.json").write_text(json.dumps(build_identity(root), sort_keys=True))
