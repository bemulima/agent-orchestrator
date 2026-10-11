"""One-use qualification under an explicitly pinned trusted owner policy.

Policy and receipt pins originate in the trusted host control plane, never model
input. This module verifies supplied authority; it does not establish runtime PASS
or provide a model execution entrypoint. Historical grants have no fallback.
"""
from dataclasses import dataclass, asdict, fields
import datetime
import hashlib
import json
import os
import pathlib
import re
import stat
import uuid

DEFERRED = "real_model_canary"
IDENTITY_KEYS = ("source_manifest_sha256", "orchestrator_sha256", "runner_manifest_sha256", "runner_image", "command_image", "sdk_image", "broker_sha256", "publisher_sha256", "dependency_preparer_sha256", "reconciliation_sha256", "runtime_controller_sha256", "runtime_protocol_sha256", "runtime_transport_sha256", "agent_runtime_controller_sha256", "agent_runtime_protocol_sha256", "agent_runtime_transport_sha256", "active_host_attestation_sha256", "profile_sha256", "role_policy_sha256", "sdk_version", "cli_version", "go_version", "docker_version", "security_policy_sha256", "resource_policy_sha256")
REQUIRED=('temporal_cancellation_certified','hardened_timeout_cleanup','late_result_rejection','unknown_runtime_fails_closed','lifecycle_reconciliation','broker_enabled','native_commands_disabled','native_filesystem_disabled','oci_seccomp','zero_capabilities','no_new_privileges','network_policy','role_scope','bounded_publication','isolated_scratch','lifecycle','cumulative_resources','sdk_adversarial','real_model_canary','role_compatibility','resume_fail_closed','release_binding','trusted_go_dependency_provisioning','go_dependency_manifest_verified','go_worker_network_blocked','go_dependency_input_readonly','sdk_private_state_isolated','sdk_private_state_bounded','sdk_private_state_cleanup','sdk_private_state_source_escape_blocked')


class QualificationDenied(Exception):
    def __init__(self, code):
        self.code = code
        super().__init__(code)


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()


def digest(value):
    return hashlib.sha256(canonical(value)).hexdigest()


def _hex(value):
    return isinstance(value, str) and re.fullmatch(r"[a-f0-9]{64}", value) is not None


def _uuid(value):
    try:
        return isinstance(value, str) and str(uuid.UUID(value)) == value
    except ValueError:
        return False


def _unique_pairs(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise QualificationDenied("QUALIFICATION_JSON_DUPLICATE")
        value[key] = item
    return value


def _read_pinned(file, pin, kind):
    if not _hex(pin):
        raise QualificationDenied("QUALIFICATION_" + kind + "_STALE")
    p = pathlib.Path(file)
    if not p.is_absolute() or p.resolve(strict=True) != p:
        raise QualificationDenied("QUALIFICATION_" + kind + "_UNTRUSTED")
    descriptor = os.open(p, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(descriptor, "rb") as source:
        info = os.fstat(source.fileno())
        if (not stat.S_ISREG(info.st_mode) or info.st_nlink != 1
                or info.st_mode & 0o222 or info.st_size > 1048576):
            raise QualificationDenied("QUALIFICATION_" + kind + "_UNTRUSTED")
        data = source.read(1048577)
    if len(data) > 1048576 or hashlib.sha256(data).hexdigest() != pin:
        raise QualificationDenied("QUALIFICATION_" + kind + "_STALE")
    try:
        value = json.loads(data, object_pairs_hook=_unique_pairs,
                           parse_constant=lambda _: (_ for _ in ()).throw(ValueError()))
    except (ValueError, UnicodeError):
        raise QualificationDenied("QUALIFICATION_" + kind + "_INVALID") from None
    if type(value) is not dict:
        raise QualificationDenied("QUALIFICATION_" + kind + "_INVALID")
    return value


@dataclass(frozen=True)
class TrustedQualificationPolicy:
    """Reference only. Every use rereads and validates pinned policy and receipts."""
    path: str
    sha256: str
    approved_sha256: str


POLICY_FIELDS = frozenset(("kind", "schema_version", "owner_approval_reference", "identity",
    "fixture_root", "repository_kind", "workpackage_id", "release_attempt_id", "role",
    "write_paths", "model", "reasoning", "max_model_runs", "budget", "expires_at", "consumption_store", "prerequisite_receipts"))
FIXED_BUDGET = {"max_runs_5h": 20, "mode": "enforce"}


def _policy(policy):
    if type(policy) is not TrustedQualificationPolicy:
        raise QualificationDenied("QUALIFICATION_TRUSTED_POLICY_REQUIRED")
    if not _hex(policy.approved_sha256) or policy.sha256 != policy.approved_sha256:
        raise QualificationDenied("QUALIFICATION_OWNER_APPROVAL_MISMATCH")
    value = _read_pinned(policy.path, policy.sha256, "POLICY")
    if (set(value) != POLICY_FIELDS or value["kind"] != "RELEASE_QUALIFICATION_POLICY"
            or type(value["schema_version"]) is not int or value["schema_version"] != 1
            or not isinstance(value["owner_approval_reference"], str)
            or not value["owner_approval_reference"].strip()
            or len(value["owner_approval_reference"]) > 512):
        raise QualificationDenied("QUALIFICATION_POLICY_INVALID")
    identity = value["identity"]
    if type(identity) is not dict or set(identity) != set(IDENTITY_KEYS):
        raise QualificationDenied("QUALIFICATION_IDENTITY_INCOMPLETE")
    for key, item in identity.items():
        if (not isinstance(item, str) or not item.strip() or item == "UNKNOWN" or len(item) > 256
                or key.endswith("_sha256") and not _hex(item)
                or key.endswith("_image") and not re.fullmatch(r"sha256:[a-f0-9]{64}", item)):
            raise QualificationDenied("QUALIFICATION_IDENTITY_INCOMPLETE")
    if (not _uuid(value["workpackage_id"]) or not _uuid(value["release_attempt_id"])
            or value["repository_kind"] != "disposable_certification"
            or value["role"] != "layer" or value["write_paths"] != ["internal/value.go"]
            or value["model"] != "gpt-5.6-sol" or value["reasoning"] != "low"
            or type(value["max_model_runs"]) is not int or value["max_model_runs"] != 1
            or type(value["budget"]) is not dict or value["budget"] != FIXED_BUDGET
            or type(value["budget"].get("max_runs_5h")) is not int):
        raise QualificationDenied("QUALIFICATION_POLICY_SCOPE_INVALID")
    try:
        expiry = datetime.datetime.fromisoformat(value["expires_at"])
        if expiry.tzinfo is None or expiry <= datetime.datetime.now(datetime.timezone.utc):
            raise QualificationDenied("QUALIFICATION_EXPIRED")
    except (ValueError, TypeError):
        raise QualificationDenied("QUALIFICATION_POLICY_INVALID") from None
    root = pathlib.Path(value["fixture_root"])
    if not root.is_absolute() or not root.is_dir() or root.resolve(strict=True) != root:
        raise QualificationDenied("QUALIFICATION_FIXTURE_DENIED")
    store = pathlib.Path(value["consumption_store"])
    if (not store.is_absolute() or not store.is_dir() or store.resolve(strict=True) != store
            or store.stat().st_mode & 0o077 or store.stat().st_uid != os.geteuid()):
        raise QualificationDenied("QUALIFICATION_STORE_DENIED")
    receipts = value["prerequisite_receipts"]
    if type(receipts) is not dict or set(receipts) != set(REQUIRED) - {DEFERRED}:
        raise QualificationDenied("QUALIFICATION_EVIDENCE_INCOMPLETE")
    expected = {"kind": "RELEASE_QUALIFICATION_PREREQUISITE", "result": "PASS",
                "identity_sha256": digest(identity), "release_attempt_id": value["release_attempt_id"],
                "workpackage_id": value["workpackage_id"]}
    for condition, ref in receipts.items():
        if type(ref) is not dict or set(ref) != {"path", "sha256"}:
            raise QualificationDenied("QUALIFICATION_EVIDENCE_INVALID")
        receipt = _read_pinned(ref["path"], ref["sha256"], "EVIDENCE")
        if receipt != {**expected, "condition": condition}:
            raise QualificationDenied("QUALIFICATION_EVIDENCE_MISMATCH")
    return value


def load_policy(file, pin, approved_policy_pin):
    """approved_policy_pin must come from trusted owner approval configuration.

    A policy claim or its computed file hash is not an approval source. This
    API requires the separate exact approved pin; production integration owns
    authentication of that source and must never accept it from model input.
    """
    policy = TrustedQualificationPolicy(str(file), pin, approved_policy_pin)
    _policy(policy)
    return policy


@dataclass(frozen=True)
class ReleaseQualificationGrant:
    kind: str
    id: str
    nonce: str
    release_attempt_id: str
    source_manifest_sha256: str
    identity: dict
    policy_sha256: str
    owner_approval_reference: str
    fixture_root: str
    workpackage_id: str
    role: str
    write_paths: tuple
    model: str
    reasoning: str
    max_model_runs: int
    budget: dict
    expires_at: str
    deferred_prerequisite: str = DEFERRED
    product_repositories_allowed: bool = False
    real_pilot_allowed: bool = False


def create_grant(policy, expires):
    value = _policy(policy)
    if expires != value["expires_at"]:
        raise QualificationDenied("QUALIFICATION_POLICY_MISMATCH")
    return ReleaseQualificationGrant(
        "RELEASE_QUALIFICATION_GRANT", str(uuid.uuid4()), str(uuid.uuid4()),
        value["release_attempt_id"], value["identity"]["source_manifest_sha256"],
        dict(value["identity"]), policy.sha256, value["owner_approval_reference"],
        value["fixture_root"], value["workpackage_id"], value["role"], tuple(value["write_paths"]),
        value["model"], value["reasoning"], value["max_model_runs"], dict(value["budget"]), expires)


def load_grant(file, pin):
    value = _read_pinned(file, pin, "GRANT")
    if value.pop("controller_sha256", None) != hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest():
        raise QualificationDenied("QUALIFICATION_CONTROLLER_STALE")
    if set(value) != {field.name for field in fields(ReleaseQualificationGrant)} or type(value["write_paths"]) is not list:
        raise QualificationDenied("QUALIFICATION_GRANT_INVALID")
    value["write_paths"] = tuple(value["write_paths"])
    return ReleaseQualificationGrant(**value)


def validate(grant, actual, request, conditions, required, policy, now=None):
    value = _policy(policy)
    now = now or datetime.datetime.now(datetime.timezone.utc)
    if (grant.kind != "RELEASE_QUALIFICATION_GRANT" or grant.deferred_prerequisite != DEFERRED
            or grant.product_repositories_allowed is not False or grant.real_pilot_allowed is not False
            or grant.role != "layer" or tuple(grant.write_paths) != ("internal/value.go",)):
        raise QualificationDenied("QUALIFICATION_SCOPE_INVALID")
    try:
        if not _uuid(grant.id) or not _uuid(grant.nonce):
            raise ValueError()
        expiry = datetime.datetime.fromisoformat(grant.expires_at)
        if expiry.tzinfo is None or expiry <= now:
            raise QualificationDenied("QUALIFICATION_EXPIRED")
    except (ValueError, TypeError):
        raise QualificationDenied("QUALIFICATION_GRANT_INVALID") from None
    if (grant.policy_sha256 != policy.sha256 or grant.owner_approval_reference != value["owner_approval_reference"]
            or grant.identity != value["identity"]
            or grant.source_manifest_sha256 != value["identity"]["source_manifest_sha256"]
            or any(getattr(grant, key) != value[key] for key in ("fixture_root", "workpackage_id", "release_attempt_id", "role", "model", "reasoning", "max_model_runs", "budget", "expires_at"))
            or type(grant.max_model_runs) is not int):
        raise QualificationDenied("QUALIFICATION_POLICY_MISMATCH")
    if type(actual) is not dict or actual != value["identity"]:
        raise QualificationDenied("QUALIFICATION_RUNTIME_MISMATCH")
    expected = {"purpose": "release-qualification", "repository_kind": "disposable_certification",
                "working_directory": grant.fixture_root, "workpackage_id": grant.workpackage_id,
                "release_attempt_id": grant.release_attempt_id, "profile": grant.role,
                "write_paths": list(grant.write_paths), "model": grant.model,
                "reasoning": grant.reasoning, "max_model_runs": 1, "budget": FIXED_BUDGET,
                "consumption_store": value["consumption_store"]}
    if (type(request) is not dict or request.get("resume_thread")
            or any(request.get(key) != item for key, item in expected.items())
            or type(request.get("max_model_runs")) is not int):
        raise QualificationDenied("QUALIFICATION_SCOPE_MISMATCH")
    root = pathlib.Path(grant.fixture_root)
    for name in grant.write_paths:
        p = root / name
        if (p.is_symlink() or not p.resolve().is_relative_to(root)
                or any(parent.is_symlink() for parent in p.parents if parent != root and parent.is_relative_to(root))
                or not p.is_file() or p.stat().st_nlink != 1):
            raise QualificationDenied("QUALIFICATION_SCOPE_MISMATCH")
    if set(required) != set(REQUIRED):
        raise QualificationDenied("QUALIFICATION_POLICY_MISMATCH")
    missing = [key for key in REQUIRED if key != DEFERRED and conditions.get(key) is not True]
    if missing:
        raise QualificationDenied("QUALIFICATION_PREREQUISITE_MISSING:" + ",".join(missing))


def consume(grant, actual, request, conditions, required, store, policy):
    # All policy/receipt/runtime/security failures precede irreversible consumption.
    validate(grant, actual, request, conditions, required, policy)
    approved = _policy(policy)["consumption_store"]
    store = pathlib.Path(store)
    if (str(store) != approved or not store.is_dir() or store.is_symlink()
            or store.resolve() != store or store.stat().st_mode & 0o077
            or store.stat().st_uid != os.geteuid()):
        raise QualificationDenied("QUALIFICATION_STORE_DENIED")
    destination = store / (grant.release_attempt_id + ".consumed.json")
    try:
        fd = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    except FileExistsError:
        raise QualificationDenied("QUALIFICATION_CONSUMED") from None
    value = {"kind": grant.kind, "id": grant.id, "nonce": grant.nonce,
             "release_attempt_id": grant.release_attempt_id, "workpackage_id": grant.workpackage_id,
             "grant_sha256": digest(asdict(grant)), "policy_sha256": policy.sha256,
             "consumed_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
             "general_production_admission": "DENIED"}
    # Failed/interrupted writes poison the attempt forever. No release/retry path.
    with os.fdopen(fd, "w") as f:
        json.dump(value, f)
        f.flush()
        os.fsync(f.fileno())
    descriptor = os.open(store, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    return value
