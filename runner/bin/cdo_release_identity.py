#!/usr/bin/env python3
"""Verify installed bytes and observe the authenticated host; no Docker authority.

The three image pins identify the approved immutable execution/packaging images
positively observed by the host. Local inventory verifies this installed payload;
it is not an independent claim about the backend container's Docker deployment.
"""
from __future__ import annotations

import hashlib
import json
import os
import pathlib
import re
import stat

import cdo_runtime_backend as backend
import cdo_runtime_attestation as attestation


MAX_BUILD_BYTES = 1048576
STATIC_IDENTITY_KEYS = frozenset({
    "source_manifest_sha256", "orchestrator_sha256", "runner_manifest_sha256",
    "broker_sha256", "publisher_sha256", "dependency_preparer_sha256",
    "reconciliation_sha256", "runtime_controller_sha256", "runtime_protocol_sha256",
    "runtime_transport_sha256", "agent_runtime_controller_sha256",
    "agent_runtime_protocol_sha256", "agent_runtime_transport_sha256",
    "profile_sha256", "role_policy_sha256", "sdk_version", "cli_version", "go_version",
    "security_policy_sha256", "resource_policy_sha256",
})
IMAGE_KEYS = {"runner_image": "runner", "command_image": "command", "sdk_image": "sdk"}
RELEASE_IDENTITY_KEYS = STATIC_IDENTITY_KEYS | set(IMAGE_KEYS) | {"docker_version", "active_host_attestation_sha256"}
CONTROLLER_SOURCE_FILES = {"finite_controller": "cdo_runtime_controller.py",
                           "agent_controller": "cdo_agent_runtime.py",
                           "owned_runtime": "cdo_runtime_owned.py",
                           "coordinator": "cdo_runtime_coordinator.py",
                           "attestation": "cdo_runtime_attestation.py", "broker": "cdo_broker.py"}
TRANSPORT_SOURCE_FILES = {"backend_client": "cdo_runtime_backend.py",
                         "http_transport": "cdo_runtime_transport.py",
                         "sdk_relay": "cdo_sdk_relay.py", "sdk_state": "cdo_sdk_state.py"}
FILE_IDENTITIES = {
    "source_manifest_sha256": "release/source-manifest.json",
    "orchestrator_sha256": "course-dev-orchestrator",
    "broker_sha256": "runner/bin/cdo_broker.py", "publisher_sha256": "runner/bin/cdo_broker.py",
    "dependency_preparer_sha256": "runner/bin/cdo_go_dependencies.py",
    "reconciliation_sha256": "runner/bin/cdo_reconcile.py",
    "runtime_controller_sha256": "runner/bin/cdo_runtime_controller.py",
    "runtime_transport_sha256": "runner/bin/cdo_runtime_transport.py",
    "profile_sha256": "release/profile.v1.json", "role_policy_sha256": "release/role-policy.json",
    "security_policy_sha256": "release/profile.v1.json", "resource_policy_sha256": "release/profile.v1.json",
}
REQUIRED_FILES = frozenset(FILE_IDENTITIES.values()) | {
    "runner/bin/" + name for name in (*CONTROLLER_SOURCE_FILES.values(), *TRANSPORT_SOURCE_FILES.values())
} | {"runner/bin/cdo_release_identity.py", "runner/node_modules/@openai/codex-sdk/package.json"}


class IdentityDenied(RuntimeError):
    pass


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()


def digest(value):
    return hashlib.sha256(canonical(value)).hexdigest()


def _hex(value):
    return isinstance(value, str) and re.fullmatch(r"[a-f0-9]{64}", value) is not None


def sha(path):
    return hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()


def _path(root, name):
    if (not isinstance(name, str) or not name or "\\" in name
            or any(ord(char) < 32 or ord(char) == 127 for char in name)
            or any(part in {"", ".", "..", ".git", ".codex", ".aws", ".ssh", ".env"}
                   or part.startswith(".env.") for part in name.split("/"))):
        raise IdentityDenied("BUILD_PATH_DENIED")
    current = root
    for part in name.split("/")[:-1]:
        current = current / part
        info = current.lstat()
        if not stat.S_ISDIR(info.st_mode) or stat.S_ISLNK(info.st_mode):
            raise IdentityDenied("BUILD_PATH_DENIED")
    return root / name


def installed_identity(root):
    root = pathlib.Path(root)
    if not root.is_absolute() or root.resolve(strict=True) != root:
        raise IdentityDenied("BUILD_ROOT_DENIED")
    file = _path(root, "release/build.json")
    descriptor = os.open(file, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(descriptor, "rb") as source:
        info = os.fstat(source.fileno())
        if (not stat.S_ISREG(info.st_mode) or info.st_nlink != 1
                or info.st_mode & 0o222 or info.st_size > MAX_BUILD_BYTES):
            raise IdentityDenied("BUILD_INVENTORY_DENIED")
        build = backend.decode(source.read(MAX_BUILD_BYTES + 1))
    if (type(build) is not dict or set(build) != {"identity", "installed_files", "installed_symlinks", "source_manifest_sha256"}
            or type(build["identity"]) is not dict or set(build["identity"]) != STATIC_IDENTITY_KEYS
            or type(build["installed_files"]) is not dict or not REQUIRED_FILES.issubset(build["installed_files"])
            or type(build["installed_symlinks"]) is not dict):
        raise IdentityDenied("BUILD_INVENTORY_DENIED")
    identity, files = build["identity"], build["installed_files"]
    for key, value in identity.items():
        if (not isinstance(value, str) or not value or value == "UNKNOWN" or len(value) > 256
                or key.endswith("_sha256") and not _hex(value)):
            raise IdentityDenied("BUILD_IDENTITY_DENIED")
    if digest({"installed_files": files, "installed_symlinks": build["installed_symlinks"]}) != identity["runner_manifest_sha256"]:
        raise IdentityDenied("BUILD_INVENTORY_MISMATCH")
    for name, value in files.items():
        file = _path(root, name)
        info = file.lstat()
        if (not _hex(value) or not stat.S_ISREG(info.st_mode) or info.st_nlink != 1
                or sha(file) != value):
            raise IdentityDenied("BUILD_FILE_MISMATCH")
    for name, target in build["installed_symlinks"].items():
        file = _path(root, name)
        if (name in files or not isinstance(target, str) or not file.is_symlink()
                or str(file.readlink()) != target or not file.resolve(strict=True).is_relative_to(root)):
            raise IdentityDenied("BUILD_LINK_MISMATCH")
    if build["source_manifest_sha256"] != identity["source_manifest_sha256"]:
        raise IdentityDenied("SOURCE_BUILD_MISMATCH")
    for key, name in FILE_IDENTITIES.items():
        if files[name] != identity[key]:
            raise IdentityDenied("BUILD_IDENTITY_MISMATCH")
    for key, sources in (("agent_runtime_controller_sha256", CONTROLLER_SOURCE_FILES),
                         ("agent_runtime_transport_sha256", TRANSPORT_SOURCE_FILES)):
        if digest({name: files["runner/bin/" + file] for name, file in sources.items()}) != identity[key]:
            raise IdentityDenied("BUILD_IDENTITY_MISMATCH")
    if backend.protocol_sha256() != identity["agent_runtime_protocol_sha256"]:
        raise IdentityDenied("BUILD_PROTOCOL_MISMATCH")
    return dict(identity)


def _host_files(manifest):
    values = {}
    for item in [manifest["controller"], manifest["transport"], *manifest["implementation"]]:
        name = pathlib.Path(item["path"]).name
        if name in values and values[name] != item:
            raise IdentityDenied("HOST_IMPLEMENTATION_AMBIGUOUS")
        values[name] = item
    return values


def observe(root=pathlib.Path("/app")):
    identity = installed_identity(root)
    credentials = backend.load_credentials(os.environ.get("CDO_RUNTIME_BACKEND_CREDENTIALS"))
    if (credentials["controller_id"] != identity["agent_runtime_controller_sha256"]
            or credentials["protocol_sha256"] != identity["agent_runtime_protocol_sha256"]
            or credentials["template"]["release_id"] != identity["source_manifest_sha256"]):
        raise IdentityDenied("HOST_RELEASE_IDENTITY_MISMATCH")
    result = backend.attestation_probe(credentials)
    if (type(result) is not dict or result.get("state") != "UNCREATED"
            or result.get("invalid_reason") not in (None, "")
            or result.get("attestation_verified") is not True
            or result.get("attestation_sha256") != credentials["attestation_sha256"]):
        raise IdentityDenied("HOST_ATTESTATION_DENIED")
    manifest, config = result.get("attestation"), result.get("controller_configuration")
    attestation.verify_attestation(manifest, manifest)
    if (digest(manifest) != credentials["attestation_sha256"] or type(config) is not dict
            or digest(config) != manifest["controller_configuration_sha256"]
            or manifest["protocol_sha256"] != identity["agent_runtime_protocol_sha256"]
            or not manifest["node"] or not manifest["node"]["dependencies"]):
        raise IdentityDenied("HOST_ATTESTATION_MISMATCH")
    host_identity = config.get("release_identity")
    if (type(host_identity) is not dict or not STATIC_IDENTITY_KEYS.issubset(host_identity)
            or set(host_identity) - (STATIC_IDENTITY_KEYS | set(IMAGE_KEYS) | {"docker_version"})
            or any(host_identity[key] != value for key, value in identity.items())):
        raise IdentityDenied("HOST_RELEASE_IDENTITY_MISMATCH")
    actual_files = _host_files(manifest)
    for key, sources in (("agent_runtime_controller_sha256", CONTROLLER_SOURCE_FILES),
                         ("agent_runtime_transport_sha256", TRANSPORT_SOURCE_FILES)):
        if digest({name: actual_files[file]["sha256"] for name, file in sources.items()}) != identity[key]:
            raise IdentityDenied("HOST_IMPLEMENTATION_MISMATCH")
    for key in ("broker_sha256", "publisher_sha256", "dependency_preparer_sha256", "reconciliation_sha256",
                "runtime_controller_sha256", "runtime_transport_sha256"):
        if actual_files[pathlib.Path(FILE_IDENTITIES[key]).name]["sha256"] != identity[key]:
            raise IdentityDenied("HOST_IMPLEMENTATION_MISMATCH")
    identity.update({key: manifest["docker"]["images"][role] for key, role in IMAGE_KEYS.items()})
    identity.update(docker_version=manifest["docker"]["server_version"],
                    active_host_attestation_sha256=credentials["attestation_sha256"])
    if any(key in host_identity and host_identity[key] != identity[key]
           for key in (*IMAGE_KEYS, "docker_version")) or set(identity) != RELEASE_IDENTITY_KEYS:
        raise IdentityDenied("HOST_IMAGE_IDENTITY_MISMATCH")
    return identity


if __name__ == "__main__":
    try:
        print(json.dumps(observe(), sort_keys=True))
    except Exception:
        raise SystemExit("CERTIFIED_RUNTIME_MISMATCH: RUNTIME_IDENTITY_UNKNOWN")
