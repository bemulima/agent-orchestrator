#!/usr/bin/env python3
"""Positive, credential-free observation of the active trusted host runtime.

This observer is a controller-internal dependency, not a public Docker API. The
administrator supplies its immutable configuration; an execution request cannot
choose files, modules, executables, images, endpoints, or security profiles.
``verify_attestation`` must succeed before execution is admitted. Observation
failure is UNKNOWN and never an empty or matching identity.
"""
from __future__ import annotations

from dataclasses import dataclass
import ctypes
import hashlib
import hmac
import json
import os
from pathlib import Path
import plistlib
import re
import selectors
import ssl
import stat
import struct
import subprocess
import sys
import sysconfig
import time


SCHEMA = "cdo.runtime-attestation.v1"
MAX_FILE_BYTES = 512 * 1024 * 1024
MAX_MANIFEST_BYTES = 1024 * 1024
MAX_DEPENDENCIES = 4096
MAX_NATIVE_BYTES = 64 * 1024 * 1024
MAX_JSON_DEPTH = 16
REQUIRED_MODULES = ("_hashlib", "_ssl", "hashlib", "hmac", "json", "os",
                    "selectors", "socket", "ssl", "stat", "subprocess",
                    "sys", "threading", "time")
MANIFEST_FIELDS = frozenset({"schema", "controller", "transport", "implementation",
                            "protocol_sha256", "python", "node", "runtime_dependencies",
                            "native_runtime", "controller_configuration_sha256",
                            "signing_policy_sha256", "security_resource_profiles_sha256",
                            "docker", "attestation_sha256"})
DOCKER_FIELDS = frozenset({"executable", "endpoint", "daemon_id", "server_version", "images"})
IMAGE_ROLES = frozenset({"runner", "sdk", "command"})
PROTECTED_NAMES = frozenset({".env", ".git", ".codex", ".ssh", ".aws", "auth.json",
                             "credentials", "id_rsa", "id_ed25519"})
SECRET_KEYS = frozenset({"secret", "secrets", "password", "token", "credentials", "credential",
                        "private_key", "signing_key", "api_key", "auth", "authorization"})


class AttestationDenied(Exception):
    """A bounded public code; never include file contents or exception text."""
    def __init__(self, code="RUNTIME_ATTESTATION_DENIED"):
        self.code = code
        super().__init__(code)


class AttestationUnknown(AttestationDenied):
    def __init__(self, code="RUNTIME_ATTESTATION_UNKNOWN"):
        super().__init__(code)


@dataclass(frozen=True)
class TrustedAttestationConfig:
    controller_path: Path
    transport_path: Path
    protocol_sha256: str
    controller_configuration: dict
    signing_policy: dict
    images: dict
    security_resource_profiles: dict
    implementation_paths: tuple = ()
    python_module_names: tuple = REQUIRED_MODULES
    runtime_dependency_paths: tuple = ()
    node_executable: Path | None = None
    node_dependency_paths: tuple = ()
    bind_all_loaded_stdlib: bool = False


def canonical(value):
    try:
        return json.dumps(value, sort_keys=True, separators=(",", ":"),
                          ensure_ascii=True, allow_nan=False).encode("ascii")
    except (TypeError, ValueError, RecursionError):
        raise AttestationDenied("ATTESTATION_JSON_DENIED") from None


def _hex(value, image=False):
    return isinstance(value, str) and bool(re.fullmatch(("sha256:" if image else "") + "[a-f0-9]{64}", value))


def _public_json(value, depth=0):
    if depth > MAX_JSON_DEPTH:
        raise AttestationDenied("ATTESTATION_CONFIG_LIMIT")
    if type(value) is dict:
        if len(value) > MAX_DEPENDENCIES:
            raise AttestationDenied("ATTESTATION_CONFIG_LIMIT")
        for name, item in value.items():
            if (not isinstance(name, str) or not name or len(name) > 128
                    or name.lower().replace("-", "_") in SECRET_KEYS
                    or re.search(r"(?:^|_)(?:secret|password|token|credentials?|private_key|api_key)(?:_|$)", name.lower().replace("-", "_"))):
                raise AttestationDenied("ATTESTATION_PRIVATE_CONFIG_DENIED")
            _public_json(item, depth + 1)
    elif type(value) is list:
        if len(value) > MAX_DEPENDENCIES:
            raise AttestationDenied("ATTESTATION_CONFIG_LIMIT")
        for item in value:
            _public_json(item, depth + 1)
    elif type(value) is str:
        if len(value) > 8192 or any(ord(char) < 32 for char in value):
            raise AttestationDenied("ATTESTATION_CONFIG_LIMIT")
    elif value is not None and type(value) not in (bool, int):
        raise AttestationDenied("ATTESTATION_CONFIG_DENIED")


def _canonical_path(value, resolve_system=False):
    try:
        path = Path(value)
        if (not path.is_absolute() or re.search(r"[\x00-\x1f\x7f]", str(path))
                or any(part in PROTECTED_NAMES or part.startswith(".env.") for part in path.parts)):
            raise AttestationDenied("ATTESTATION_PATH_DENIED")
        resolved = path.resolve(strict=True)
        if any(part in PROTECTED_NAMES or part.startswith(".env.") for part in resolved.parts):
            raise AttestationDenied("ATTESTATION_PATH_DENIED")
        if not resolve_system and resolved != path:
            raise AttestationDenied("ATTESTATION_PATH_DENIED")
        return resolved
    except (OSError, RuntimeError, TypeError, ValueError):
        raise AttestationUnknown("ATTESTATION_PATH_UNKNOWN") from None


def _file_identity(value, executable=False, resolve_system=False, limit=MAX_FILE_BYTES):
    path = _canonical_path(value, resolve_system=resolve_system)
    descriptor = None
    try:
        descriptor = os.open(str(path), os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
        before = os.fstat(descriptor)
        if (not stat.S_ISREG(before.st_mode) or before.st_nlink != 1
                or before.st_uid not in {0, os.geteuid()} or before.st_mode & 0o022
                or before.st_size < 0 or before.st_size > limit
                or executable and not before.st_mode & 0o111):
            raise AttestationDenied("ATTESTATION_FILE_DENIED")
        digest = hashlib.sha256()
        acquired = 0
        while True:
            part = os.read(descriptor, 1024 * 1024)
            if not part:
                break
            acquired += len(part)
            if acquired > limit:
                raise AttestationDenied("ATTESTATION_FILE_LIMIT")
            digest.update(part)
        after = os.fstat(descriptor)
        observed = path.lstat()
        fields = ("st_dev", "st_ino", "st_size", "st_mtime_ns", "st_ctime_ns", "st_mode", "st_uid", "st_gid", "st_nlink")
        if (acquired != before.st_size or any(getattr(before, key) != getattr(after, key)
                                             or getattr(after, key) != getattr(observed, key) for key in fields)
                or path.resolve(strict=True) != path):
            raise AttestationUnknown("ATTESTATION_FILE_CHANGED")
        return {"path": str(path), "sha256": digest.hexdigest(), "bytes": acquired,
                "mode": stat.S_IMODE(before.st_mode), "uid": before.st_uid, "gid": before.st_gid}
    except (OSError, RuntimeError):
        raise AttestationUnknown("ATTESTATION_FILE_UNKNOWN") from None
    finally:
        if descriptor is not None:
            os.close(descriptor)


def _files(paths, resolve_system=False):
    if type(paths) not in (tuple, list) or len(paths) > MAX_DEPENDENCIES:
        raise AttestationDenied("ATTESTATION_DEPENDENCY_LIMIT")
    values = [_file_identity(path, resolve_system=resolve_system) for path in paths]
    values.sort(key=lambda value: value["path"])
    if len({value["path"] for value in values}) != len(values):
        raise AttestationDenied("ATTESTATION_DUPLICATE_DEPENDENCY")
    return values


def _modules(names):
    if (type(names) not in (tuple, list) or not set(REQUIRED_MODULES).issubset(names)
            or len(names) > MAX_DEPENDENCIES or len(set(names)) != len(names)):
        raise AttestationDenied("ATTESTATION_MODULE_CONFIG_DENIED")
    values = []
    for name in sorted(names):
        if not isinstance(name, str) or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_.]{0,127}", name):
            raise AttestationDenied("ATTESTATION_MODULE_CONFIG_DENIED")
        module = sys.modules.get(name)
        if module is None:
            raise AttestationUnknown("ATTESTATION_ACTIVE_MODULE_UNKNOWN")
        spec = getattr(module, "__spec__", None)
        origin = getattr(spec, "origin", None)
        if origin in ("built-in", "frozen"):
            values.append({"name": name, "kind": origin})
            continue
        filename = getattr(module, "__file__", None)
        if not filename or not origin or _canonical_path(origin, True) != _canonical_path(filename, True):
            raise AttestationUnknown("ATTESTATION_MODULE_ORIGIN_UNKNOWN")
        value = {"name": name, "kind": "file", "file": _file_identity(filename, resolve_system=True)}
        cached = getattr(module, "__cached__", None)
        if cached and Path(cached).exists():
            value["bytecode"] = _file_identity(cached, resolve_system=True)
        values.append(value)
    return values


def _bounded_command(executable, arguments):
    process = None
    selector = None
    try:
        process = subprocess.Popen([str(executable), *arguments], stdin=subprocess.DEVNULL,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   env={"PATH": "/usr/bin:/bin", "HOME": "/var/empty"}, start_new_session=True)
        selector = selectors.DefaultSelector()
        selector.register(process.stdout, selectors.EVENT_READ, "stdout")
        selector.register(process.stderr, selectors.EVENT_READ, "stderr")
        output = {"stdout": bytearray(), "stderr": bytearray()}
        deadline = time.monotonic() + 5
        while selector.get_map():
            if time.monotonic() >= deadline:
                raise AttestationUnknown("ATTESTATION_EXECUTABLE_UNKNOWN")
            for key, _ in selector.select(min(.1, deadline - time.monotonic())):
                part = os.read(key.fileobj.fileno(), 4096)
                if not part:
                    selector.unregister(key.fileobj)
                else:
                    output[key.data].extend(part)
                    if sum(map(len, output.values())) > 65536:
                        raise AttestationUnknown("ATTESTATION_EXECUTABLE_LIMIT")
        if process.wait(timeout=max(.01, deadline - time.monotonic())) or output["stderr"]:
            raise AttestationUnknown("ATTESTATION_EXECUTABLE_UNKNOWN")
        return bytes(output["stdout"])
    except (OSError, subprocess.TimeoutExpired, ValueError):
        raise AttestationUnknown("ATTESTATION_EXECUTABLE_UNKNOWN") from None
    finally:
        if selector:
            selector.close()
        if process:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=2)
            for stream in (process.stdout, process.stderr):
                if stream:
                    stream.close()


def _node(config):
    if config.node_executable is None:
        if config.node_dependency_paths:
            raise AttestationDenied("ATTESTATION_NODE_CONFIG_DENIED")
        return None
    if not config.node_dependency_paths:
        raise AttestationDenied("ATTESTATION_NODE_DEPENDENCIES_REQUIRED")
    executable = _file_identity(config.node_executable, executable=True)
    # A single literal observer. No request field can provide source or argv.
    source = "console.log(JSON.stringify({version:process.version,versions:process.versions,shared_objects:process.report.getReport().sharedObjects}))"
    try:
        observed = json.loads(_bounded_command(executable["path"], ["--no-addons", "-e", source]))
    except (ValueError, UnicodeError, RecursionError):
        raise AttestationUnknown("ATTESTATION_NODE_UNKNOWN") from None
    if (type(observed) is not dict or set(observed) != {"version", "versions", "shared_objects"}
            or not isinstance(observed["version"], str) or not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", observed["version"])
            or type(observed["versions"]) is not dict or type(observed["shared_objects"]) is not list
            or len(observed["shared_objects"]) > MAX_DEPENDENCIES):
        raise AttestationUnknown("ATTESTATION_NODE_UNKNOWN")
    _public_json(observed["versions"])
    if _file_identity(executable["path"], executable=True) != executable:
        raise AttestationUnknown("ATTESTATION_NODE_EXECUTABLE_CHANGED")
    return {"executable": executable, "version": observed["version"], "versions": observed["versions"],
            "dependencies": _files(config.node_dependency_paths), "native_dependencies": _native_files(observed["shared_objects"])}


def _native_files(paths):
    if len(paths) > MAX_DEPENDENCIES or any(not isinstance(path, str) for path in paths):
        raise AttestationUnknown("ATTESTATION_NATIVE_LIMIT")
    readable, shared_cache = {}, []
    for path in sorted(set(paths)):
        if not Path(path).exists():
            if sys.platform == "darwin" and path.startswith(("/usr/lib/", "/System/Library/")):
                shared_cache.append(path)
                continue
            raise AttestationUnknown("ATTESTATION_NATIVE_DEPENDENCY_UNKNOWN")
        identity = _file_identity(path, resolve_system=True)
        if identity["path"] in readable and readable[identity["path"]] != identity:
            raise AttestationUnknown("ATTESTATION_NATIVE_DEPENDENCY_CHANGED")
        readable[identity["path"]] = identity
    return {"files": [readable[path] for path in sorted(readable)], "system_shared_cache_paths": shared_cache}


def _darwin_cache():
    """Bind actual dyld cache UUID and signed cache files; never invent dylib hashes.

    macOS executes sealed, signed shared-cache code without standalone dylib files.
    The cache's code signature identifies its code pages. This records and hashes
    that signature and the exact cache header, rather than hashing a nonexistent
    /usr/lib file or presenting a partial-file hash as a whole-file digest.
    """
    try:
        dyld = ctypes.CDLL(None)
        uuid_bytes = (ctypes.c_ubyte * 16)()
        method = dyld._dyld_get_shared_cache_uuid
        method.argtypes = [ctypes.POINTER(ctypes.c_ubyte)]
        method.restype = ctypes.c_bool
        if not method(uuid_bytes):
            raise AttestationUnknown("ATTESTATION_SHARED_CACHE_UNKNOWN")
        active_uuid = bytes(uuid_bytes).hex()
        dyld._dyld_image_count.restype = ctypes.c_uint32
        dyld._dyld_get_image_name.argtypes = [ctypes.c_uint32]
        dyld._dyld_get_image_name.restype = ctypes.c_char_p
        dyld._dyld_get_image_header.argtypes = [ctypes.c_uint32]
        dyld._dyld_get_image_header.restype = ctypes.c_void_p
        count = dyld._dyld_image_count()
        if count > MAX_DEPENDENCIES:
            raise AttestationUnknown("ATTESTATION_NATIVE_LIMIT")
        active_images = []
        for index in range(count):
            name = dyld._dyld_get_image_name(index).decode("utf-8")
            address = dyld._dyld_get_image_header(index)
            if not address:
                raise AttestationUnknown("ATTESTATION_NATIVE_HEADER_UNKNOWN")
            head = ctypes.string_at(address, 32)
            magic, _, _, _, commands, command_bytes, flags = struct.unpack_from("<7I", head)
            if magic not in (0xfeedfacf, 0xfeedface) or commands > 4096 or command_bytes > MAX_MANIFEST_BYTES:
                raise AttestationUnknown("ATTESTATION_NATIVE_HEADER_UNKNOWN")
            in_cache = bool(flags & 0x80000000)  # MH_DYLIB_IN_CACHE, from the actual loaded image.
            if not Path(name).exists() and not in_cache:
                raise AttestationDenied("ATTESTATION_SYSTEM_LIBRARY_OVERRIDE")
            if not in_cache:
                continue
            header_size = 32 if magic == 0xfeedfacf else 28
            load_commands = ctypes.string_at(address + header_size, command_bytes)
            offset = 0
            image_uuid = None
            for _ in range(commands):
                if offset + 8 > len(load_commands):
                    raise AttestationUnknown("ATTESTATION_NATIVE_HEADER_UNKNOWN")
                command, size = struct.unpack_from("<II", load_commands, offset)
                if size < 8 or offset + size > len(load_commands):
                    raise AttestationUnknown("ATTESTATION_NATIVE_HEADER_UNKNOWN")
                if command == 0x1b:  # LC_UUID
                    if size != 24 or image_uuid is not None:
                        raise AttestationUnknown("ATTESTATION_NATIVE_UUID_UNKNOWN")
                    image_uuid = load_commands[offset + 8:offset + 24].hex()
                offset += size
            if not image_uuid:
                raise AttestationUnknown("ATTESTATION_NATIVE_UUID_UNKNOWN")
            active_images.append({"path": name, "uuid": image_uuid})
        active_images.sort(key=lambda item: item["path"])
        candidates = (Path("/System/Volumes/Preboot/Cryptexes/OS/System/Library/dyld"), Path("/System/Library/dyld"))
        found = []
        for folder in candidates:
            if not folder.is_dir():
                continue
            selected = None
            for path in sorted(folder.glob("dyld_shared_cache_*")):
                if "." in path.name:
                    continue
                with path.open("rb") as stream:
                    header = stream.read(104)
                if header.startswith(b"dyld_v1") and len(header) == 104 and header[88:104].hex() == active_uuid:
                    selected = path.name
                    break
            if selected is None:
                continue
            for path in sorted(folder.glob(selected + "*")):
                if not re.fullmatch(re.escape(selected) + r"(?:\.\d+)?", path.name):
                    continue
                descriptor = os.open(str(path), os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
                try:
                    before = os.fstat(descriptor)
                    if (not stat.S_ISREG(before.st_mode) or before.st_uid != 0 or before.st_mode & 0o022
                            or before.st_nlink != 1):
                        raise AttestationDenied("ATTESTATION_SHARED_CACHE_DENIED")
                    header = os.read(descriptor, 4096)
                    if not header.startswith(b"dyld_v1") or len(header) < 104:
                        raise AttestationUnknown("ATTESTATION_SHARED_CACHE_UNKNOWN")
                    cache_uuid = header[88:104].hex()
                    offset, length = struct.unpack_from("<QQ", header, 40)
                    if length <= 0 or length > MAX_NATIVE_BYTES or offset + length > before.st_size:
                        raise AttestationUnknown("ATTESTATION_SHARED_CACHE_SIGNATURE_UNKNOWN")
                    os.lseek(descriptor, offset, os.SEEK_SET)
                    digest = hashlib.sha256()
                    acquired = 0
                    while acquired < length:
                        part = os.read(descriptor, min(1024 * 1024, length - acquired))
                        if not part:
                            raise AttestationUnknown("ATTESTATION_SHARED_CACHE_SIGNATURE_UNKNOWN")
                        acquired += len(part)
                        digest.update(part)
                    after = os.fstat(descriptor)
                    if any(getattr(before, key) != getattr(after, key) or getattr(after, key) != getattr(path.lstat(), key)
                           for key in ("st_dev", "st_ino", "st_size", "st_mtime_ns", "st_ctime_ns", "st_mode", "st_uid")):
                        raise AttestationUnknown("ATTESTATION_SHARED_CACHE_CHANGED")
                    found.append({"path": str(_canonical_path(path)), "uuid": cache_uuid,
                                  "bytes": before.st_size, "header_sha256": hashlib.sha256(header).hexdigest(),
                                  "code_signature_sha256": digest.hexdigest(), "code_signature_bytes": length,
                                  "mtime_ns": before.st_mtime_ns, "ctime_ns": before.st_ctime_ns,
                                  "mode": stat.S_IMODE(before.st_mode), "uid": before.st_uid, "gid": before.st_gid})
                finally:
                    os.close(descriptor)
        if not found or active_uuid not in {item["uuid"] for item in found}:
            raise AttestationUnknown("ATTESTATION_ACTIVE_SHARED_CACHE_MISMATCH")
        system_path = Path("/System/Library/CoreServices/SystemVersion.plist")
        system_identity = _file_identity(system_path)
        system_bytes = system_path.read_bytes()
        if hashlib.sha256(system_bytes).hexdigest() != system_identity["sha256"]:
            raise AttestationUnknown("ATTESTATION_OS_BUILD_CHANGED")
        system = plistlib.loads(system_bytes)
        if not all(isinstance(system.get(key), str) and system[key] for key in ("ProductBuildVersion", "ProductVersion")):
            raise AttestationUnknown("ATTESTATION_OS_BUILD_UNKNOWN")
        return {"kind": "darwin-signed-dyld-cache", "active_uuid": active_uuid, "files": found, "active_images": active_images,
                "os_build": system["ProductBuildVersion"], "os_version": system["ProductVersion"],
                "system_version_file": system_identity}
    except (AttributeError, OSError, ValueError, TypeError):
        raise AttestationUnknown("ATTESTATION_SHARED_CACHE_UNKNOWN") from None


def _native_runtime():
    if sys.platform == "darwin":
        try:
            dyld = ctypes.CDLL(None)
            dyld._dyld_image_count.argtypes = []
            dyld._dyld_image_count.restype = ctypes.c_uint32
            dyld._dyld_get_image_name.argtypes = [ctypes.c_uint32]
            dyld._dyld_get_image_name.restype = ctypes.c_char_p
            count = dyld._dyld_image_count()
            if count > MAX_DEPENDENCIES:
                raise AttestationUnknown("ATTESTATION_NATIVE_LIMIT")
            paths = [dyld._dyld_get_image_name(index).decode("utf-8") for index in range(count)]
            result = _native_files(paths)
            result["system_shared_cache"] = _darwin_cache()
            result["kernel"] = os.uname().release
            return result
        except (AttributeError, UnicodeError):
            raise AttestationUnknown("ATTESTATION_NATIVE_RUNTIME_UNKNOWN") from None
    if sys.platform.startswith("linux"):
        try:
            maps = Path("/proc/self/maps").read_bytes()
            if len(maps) > MAX_MANIFEST_BYTES:
                raise AttestationUnknown("ATTESTATION_NATIVE_LIMIT")
            paths = set()
            for line in maps.decode("utf-8").splitlines():
                values = line.split(maxsplit=5)
                if len(values) == 6 and values[5].startswith("/"):
                    if values[5].endswith(" (deleted)"):
                        raise AttestationUnknown("ATTESTATION_DELETED_NATIVE_DEPENDENCY")
                    paths.add(values[5])
            if not paths:
                raise AttestationUnknown("ATTESTATION_NATIVE_RUNTIME_UNKNOWN")
            result = _native_files(paths)
            result["system_shared_cache"] = None
            result["kernel"] = os.uname().release
            return result
        except (OSError, UnicodeError):
            raise AttestationUnknown("ATTESTATION_NATIVE_RUNTIME_UNKNOWN") from None
    raise AttestationUnknown("ATTESTATION_NATIVE_PLATFORM_UNKNOWN")


def _docker(config, adapter):
    try:
        observed = adapter.active_attestation()
    except Exception:
        raise AttestationUnknown("ATTESTATION_DOCKER_UNKNOWN") from None
    if (type(observed) is not dict or set(observed) != DOCKER_FIELDS
            or type(observed["images"]) is not dict or set(observed["images"]) != IMAGE_ROLES
            or observed["images"] != config.images
            or not all(_hex(image, image=True) for image in observed["images"].values())
            or not isinstance(observed["daemon_id"], str) or not re.fullmatch(r"[a-zA-Z0-9:_-]{8,128}", observed["daemon_id"])
            or not isinstance(observed["server_version"], str) or not re.fullmatch(r"[0-9]+(?:\.[0-9]+){1,3}(?:[-+][A-Za-z0-9.]+)?", observed["server_version"])
            or not isinstance(observed["endpoint"], str) or not observed["endpoint"].startswith("unix://")):
        raise AttestationUnknown("ATTESTATION_DOCKER_IDENTITY_UNKNOWN")
    endpoint = _canonical_path(observed["endpoint"][7:])
    info = endpoint.lstat()
    if not stat.S_ISSOCK(info.st_mode) or info.st_uid not in {0, os.geteuid()}:
        raise AttestationDenied("ATTESTATION_DOCKER_ENDPOINT_DENIED")
    return {"executable": _file_identity(observed["executable"], executable=True),
            "endpoint": "unix://" + str(endpoint), "daemon_id": observed["daemon_id"],
            "server_version": observed["server_version"], "images": dict(observed["images"])}


def _capture_attestation(trusted_config, docker_adapter):
    """Observe the live trusted runtime; failure is typed and has no fallback."""
    config = trusted_config
    if (not isinstance(config, TrustedAttestationConfig) or not _hex(config.protocol_sha256)
            or type(config.bind_all_loaded_stdlib) is not bool):
        raise AttestationDenied("ATTESTATION_CONFIG_DENIED")
    for value in (config.controller_configuration, config.signing_policy, config.security_resource_profiles, config.images):
        if type(value) is not dict or not value:
            raise AttestationDenied("ATTESTATION_CONFIG_DENIED")
        _public_json(value)
    if (config.signing_policy.get("algorithm") != "HMAC-SHA256" or set(config.images) != IMAGE_ROLES
            or not all(_hex(value, image=True) for value in config.images.values())):
        raise AttestationDenied("ATTESTATION_POLICY_DENIED")
    # Force relevant extension imports before native-loader observation.
    _ = ssl.OPENSSL_VERSION
    names = config.python_module_names
    if config.bind_all_loaded_stdlib:
        roots = tuple(Path(sysconfig.get_path(key)).resolve(strict=True) for key in ("stdlib", "platstdlib"))
        expanded = set(names)
        for name, module in tuple(sys.modules.items()):
            if name == "test" or name.startswith("test.") or name == "unittest" or name.startswith("unittest."):
                continue
            origin = getattr(getattr(module, "__spec__", None), "origin", None)
            if origin in ("built-in", "frozen"):
                expanded.add(name)
            elif isinstance(origin, str) and Path(origin).is_absolute():
                path = Path(origin).resolve(strict=True)
                if not any(part in {"site-packages", "dist-packages"} for part in path.parts) and any(root == path or root in path.parents for root in roots):
                    expanded.add(name)
        names = tuple(sorted(expanded))
    modules = _modules(names)
    result = {"schema": SCHEMA, "controller": _file_identity(config.controller_path),
              "transport": _file_identity(config.transport_path), "implementation": _files(config.implementation_paths),
              "protocol_sha256": config.protocol_sha256,
              "python": {"executable": _file_identity(sys.executable, executable=True, resolve_system=True),
                         "version": " ".join(sys.version.split()), "implementation": sys.implementation.name,
                         "cache_tag": sys.implementation.cache_tag, "openssl_version": ssl.OPENSSL_VERSION,
                         "modules": modules},
              "node": _node(config), "runtime_dependencies": _files(config.runtime_dependency_paths, resolve_system=True),
              "native_runtime": _native_runtime(),
              "controller_configuration_sha256": hashlib.sha256(canonical(config.controller_configuration)).hexdigest(),
              "signing_policy_sha256": hashlib.sha256(canonical(config.signing_policy)).hexdigest(),
              "security_resource_profiles_sha256": hashlib.sha256(canonical(config.security_resource_profiles)).hexdigest(),
              "docker": _docker(config, docker_adapter)}
    # Detach the evidence from observer-owned dictionaries before pinning it.
    result = json.loads(canonical(result))
    result["attestation_sha256"] = hashlib.sha256(canonical(result)).hexdigest()
    _validate_manifest(result)
    return result


def capture_attestation(trusted_config, docker_adapter):
    """Typed public observation API; malformed or unavailable input cannot pass."""
    try:
        return _capture_attestation(trusted_config, docker_adapter)
    except AttestationDenied:
        raise
    except (TypeError, ValueError, KeyError, AttributeError, OSError, OverflowError, RecursionError):
        raise AttestationUnknown() from None


def _validate_manifest_inner(value):
    if (type(value) is not dict or set(value) != MANIFEST_FIELDS or value.get("schema") != SCHEMA
            or not _hex(value.get("attestation_sha256"))):
        raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
    _public_json(value)
    unsigned = {key: item for key, item in value.items() if key != "attestation_sha256"}
    encoded = canonical(unsigned)
    if len(encoded) > MAX_MANIFEST_BYTES:
        raise AttestationDenied("ATTESTATION_MANIFEST_LIMIT")
    if not hmac.compare_digest(hashlib.sha256(encoded).hexdigest(), value["attestation_sha256"]):
        raise AttestationDenied("ATTESTATION_MANIFEST_DIGEST_DENIED")
    for key in ("controller_configuration_sha256", "signing_policy_sha256", "security_resource_profiles_sha256", "protocol_sha256"):
        if not _hex(value.get(key)):
            raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
    for key in ("controller", "transport", "python", "native_runtime", "docker"):
        if type(value.get(key)) is not dict or not value[key]:
            raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
    for key in ("implementation", "runtime_dependencies"):
        if type(value.get(key)) is not list:
            raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
    def file_identity(item):
        if (type(item) is not dict or set(item) != {"path", "sha256", "bytes", "mode", "uid", "gid"}
                or not isinstance(item["path"], str) or not item["path"].startswith("/")
                or not _hex(item["sha256"]) or any(type(item[key]) is not int or item[key] < 0 for key in ("bytes", "mode", "uid", "gid"))
                or item["mode"] > 0o7777 or item["mode"] & 0o022):
            raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
    def file_list(items):
        if type(items) is not list or len(items) > MAX_DEPENDENCIES:
            raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
        for item in items:
            file_identity(item)
        if [item["path"] for item in items] != sorted({item["path"] for item in items}):
            raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
    file_identity(value["controller"])
    file_identity(value["transport"])
    file_list(value["implementation"])
    file_list(value["runtime_dependencies"])
    python = value["python"]
    if (set(python) != {"executable", "version", "implementation", "cache_tag", "openssl_version", "modules"}
            or any(not isinstance(python[key], str) or not python[key] or python[key] == "UNKNOWN"
                   for key in ("version", "implementation", "cache_tag", "openssl_version"))
            or type(python["modules"]) is not list):
        raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
    file_identity(python["executable"])
    names = []
    for module in python["modules"]:
        if (type(module) is not dict or not isinstance(module.get("name"), str)
                or module.get("kind") not in ("built-in", "frozen", "file")):
            raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
        names.append(module["name"])
        if module["kind"] == "file":
            if set(module) not in ({"name", "kind", "file"}, {"name", "kind", "file", "bytecode"}):
                raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
            file_identity(module["file"])
            if "bytecode" in module:
                file_identity(module["bytecode"])
        elif set(module) != {"name", "kind"}:
            raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
    if names != sorted(set(names)) or not set(REQUIRED_MODULES).issubset(names):
        raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
    def native_files(item):
        if type(item) is not dict or not {"files", "system_shared_cache_paths"}.issubset(item):
            raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
        file_list(item["files"])
        paths = item["system_shared_cache_paths"]
        if (type(paths) is not list or paths != sorted(set(paths))
                or any(not isinstance(path, str) or not path.startswith(("/usr/lib/", "/System/Library/")) for path in paths)):
            raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
    native = value["native_runtime"]
    if (set(native) != {"files", "system_shared_cache_paths", "system_shared_cache", "kernel"}
            or not isinstance(native.get("kernel"), str) or not native["kernel"] or native["kernel"] == "UNKNOWN"
            or not native.get("files")):
        raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
    native_files(native)
    cache = native["system_shared_cache"]
    if cache is None:
        if native["system_shared_cache_paths"]:
            raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
    else:
        if (type(cache) is not dict or set(cache) != {"kind", "active_uuid", "files", "active_images", "os_build", "os_version", "system_version_file"}
                or cache["kind"] != "darwin-signed-dyld-cache" or not re.fullmatch(r"[a-f0-9]{32}", cache["active_uuid"])
                or not cache["os_build"] or not cache["os_version"] or type(cache["files"]) is not list or not cache["files"]):
            raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
        file_identity(cache["system_version_file"])
        if type(cache["active_images"]) is not list or not cache["active_images"]:
            raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
        for item in cache["active_images"]:
            if (type(item) is not dict or set(item) != {"path", "uuid"} or not isinstance(item["path"], str)
                    or not item["path"].startswith(("/usr/lib/", "/System/Library/")) or not re.fullmatch(r"[a-f0-9]{32}", item["uuid"])):
                raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
        for item in cache["files"]:
            if (type(item) is not dict or set(item) != {"path", "uuid", "bytes", "header_sha256", "code_signature_sha256", "code_signature_bytes",
                                                      "mtime_ns", "ctime_ns", "mode", "uid", "gid"}
                    or not re.fullmatch(r"[a-f0-9]{32}", item["uuid"]) or not _hex(item["header_sha256"]) or not _hex(item["code_signature_sha256"])
                    or type(item["bytes"]) is not int or item["bytes"] <= 0
                    or type(item["code_signature_bytes"]) is not int or not 0 < item["code_signature_bytes"] <= MAX_NATIVE_BYTES
                    or any(type(item[key]) is not int or item[key] < 0 for key in ("mtime_ns", "ctime_ns", "mode", "uid", "gid"))
                    or item["uid"] != 0 or item["mode"] & 0o022):
                raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
        if cache["active_uuid"] not in {item["uuid"] for item in cache["files"]}:
            raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
    node = value["node"]
    if node is not None:
        if (type(node) is not dict or set(node) != {"executable", "version", "versions", "dependencies", "native_dependencies"}
                or not isinstance(node["version"], str) or not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", node["version"])
                or type(node["versions"]) is not dict or not node["versions"] or not node["dependencies"]):
            raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
        file_identity(node["executable"])
        file_list(node["dependencies"])
        if set(node["native_dependencies"]) != {"files", "system_shared_cache_paths"}:
            raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
        native_files(node["native_dependencies"])
        if node["native_dependencies"]["system_shared_cache_paths"] and cache is None:
            raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
    docker = value["docker"]
    if (set(docker) != DOCKER_FIELDS or type(docker["images"]) is not dict
            or set(docker["images"]) != IMAGE_ROLES or not all(_hex(image, image=True) for image in docker["images"].values())):
        raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")
    file_identity(docker["executable"])
    if (not isinstance(docker["endpoint"], str) or not docker["endpoint"].startswith("unix:///")
            or not isinstance(docker["daemon_id"], str) or not re.fullmatch(r"[a-zA-Z0-9:_-]{8,128}", docker["daemon_id"])
            or not isinstance(docker["server_version"], str) or not re.fullmatch(r"[0-9]+(?:\.[0-9]+){1,3}(?:[-+][A-Za-z0-9.]+)?", docker["server_version"])):
        raise AttestationDenied("ATTESTATION_MANIFEST_DENIED")


def _validate_manifest(value):
    try:
        _validate_manifest_inner(value)
    except AttestationDenied:
        raise
    except (TypeError, ValueError, KeyError, AttributeError, OverflowError, RecursionError):
        raise AttestationDenied("ATTESTATION_MANIFEST_DENIED") from None


def verify_attestation(expected, actual):
    """Compare positively observed complete identity to the trusted pinned manifest."""
    _validate_manifest(expected)
    _validate_manifest(actual)
    if not hmac.compare_digest(canonical(expected), canonical(actual)):
        raise AttestationDenied("RUNTIME_ATTESTATION_CHANGED")
    return True
