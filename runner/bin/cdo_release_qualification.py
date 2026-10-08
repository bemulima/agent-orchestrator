"""Trusted qualification control, separate from the immutable approved release.

Only the previous-real-canary prerequisite may be deferred. Probe preparation
is not real-model admission. The model never supplies grant or evidence fields.
"""
from dataclasses import dataclass, asdict, fields
import datetime, hashlib, json, os, pathlib, re, uuid

SOURCE='37a92f8fb4031ea9c196f0a828fd280ea9fa5da79f791fa35a78c7be5b596a05'
IMAGES={'runner_image':'sha256:fd259c87c38d4744c3ecaca21815e67a1e025d1d0f3d856d89897ee403700a3b','command_image':'sha256:a63ad55c3990b47bbe62b51de214e3668936f79b3b55800ef2e3bf63c7591653','sdk_image':'sha256:7bc55a3d9834c84e3d411e2450338fb466a536f736869087515beec72d68cdfe'}
FIXTURE_ROOT='/Volumes/ZX10/Developments/.cdo-sandbox-certification-20261008/release-final-go'
OLD_ARTIFACT='8a574b23f5fa6211acb611d8921e2ca6cac06ee643a782d87b6b9118b36706eb'
IDENTITY_KEYS=('source_manifest_sha256','orchestrator_sha256','runner_manifest_sha256','runner_image','command_image','sdk_image','broker_sha256','publisher_sha256','dependency_preparer_sha256','reconciliation_sha256','profile_sha256','role_policy_sha256','sdk_version','cli_version','go_version','docker_version','security_policy_sha256','resource_policy_sha256')
DEFERRED='real_model_canary'
REQUIRED=('temporal_cancellation_certified','hardened_timeout_cleanup','late_result_rejection','unknown_runtime_fails_closed','lifecycle_reconciliation','broker_enabled','native_commands_disabled','native_filesystem_disabled','oci_seccomp','zero_capabilities','no_new_privileges','network_policy','role_scope','bounded_publication','isolated_scratch','lifecycle','cumulative_resources','sdk_adversarial','real_model_canary','role_compatibility','resume_fail_closed','release_binding','trusted_go_dependency_provisioning','go_dependency_manifest_verified','go_worker_network_blocked','go_dependency_input_readonly','sdk_private_state_isolated','sdk_private_state_bounded','sdk_private_state_cleanup','sdk_private_state_source_escape_blocked')

class QualificationDenied(Exception):
    def __init__(self, code):
        self.code=code
        super().__init__(code)

@dataclass(frozen=True)
class ReleaseQualificationGrant:
    kind: str
    id: str
    nonce: str
    release_attempt_id: str
    source_manifest_sha256: str
    identity: dict
    old_artifact_sha256: str
    fixture_root: str
    workpackage_id: str
    role: str
    write_paths: tuple
    expires_at: str
    deferred_prerequisite: str = DEFERRED
    product_repositories_allowed: bool = False
    real_pilot_allowed: bool = False


def create_grant(identity, root, workpackage, attempt, expires):
    return ReleaseQualificationGrant('RELEASE_QUALIFICATION_GRANT',str(uuid.uuid4()),str(uuid.uuid4()),attempt,SOURCE,dict(identity),OLD_ARTIFACT,str(root),workpackage,'layer',('internal/value.go',),expires)


def load_grant(file, pin):
    p=pathlib.Path(file);st=p.lstat()
    if p.is_symlink() or not p.is_file() or st.st_nlink!=1 or st.st_mode&0o222 or st.st_size>1048576:
        raise QualificationDenied('QUALIFICATION_GRANT_UNTRUSTED')
    data=p.read_bytes()
    if not re.fullmatch('[a-f0-9]{64}',pin) or hashlib.sha256(data).hexdigest()!=pin:
        raise QualificationDenied('QUALIFICATION_GRANT_STALE')
    value=json.loads(data)
    if value.pop('controller_sha256',None)!=hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest():
        raise QualificationDenied('QUALIFICATION_CONTROLLER_STALE')
    if set(value)!={f.name for f in fields(ReleaseQualificationGrant)}:
        raise QualificationDenied('QUALIFICATION_GRANT_INVALID')
    value['write_paths']=tuple(value['write_paths'])
    return ReleaseQualificationGrant(**value)


def validate(grant, actual, request, conditions, required, now=None):
    now=now or datetime.datetime.now(datetime.timezone.utc)
    if grant.kind!='RELEASE_QUALIFICATION_GRANT' or grant.deferred_prerequisite!=DEFERRED or grant.product_repositories_allowed is not False or grant.real_pilot_allowed is not False or grant.role!='layer' or tuple(grant.write_paths)!=('internal/value.go',):
        raise QualificationDenied('QUALIFICATION_SCOPE_INVALID')
    try:
        uuid.UUID(grant.id);uuid.UUID(grant.nonce);uuid.UUID(grant.workpackage_id);uuid.UUID(grant.release_attempt_id)
        expiry=datetime.datetime.fromisoformat(grant.expires_at)
        if expiry.tzinfo is None or expiry<=now:raise QualificationDenied('QUALIFICATION_EXPIRED')
    except (ValueError,TypeError):raise QualificationDenied('QUALIFICATION_GRANT_INVALID')
    if grant.source_manifest_sha256!=SOURCE or grant.identity.get('source_manifest_sha256')!=SOURCE or grant.old_artifact_sha256!=OLD_ARTIFACT:
        raise QualificationDenied('QUALIFICATION_SOURCE_MISMATCH')
    if any(grant.identity.get(k)!=v for k,v in IMAGES.items()):raise QualificationDenied('QUALIFICATION_IMAGE_MISMATCH')
    if set(grant.identity)!=set(IDENTITY_KEYS) or grant.identity.get('sdk_version')!='0.144.6' or grant.identity.get('cli_version')!='codex-cli 0.144.6' or grant.identity.get('docker_version')!='29.8.1':raise QualificationDenied('QUALIFICATION_IDENTITY_INCOMPLETE')
    if not grant.identity or set(actual)!=set(grant.identity) or any(not v or actual.get(k)!=v for k,v in grant.identity.items()):
        raise QualificationDenied('QUALIFICATION_RUNTIME_MISMATCH')
    root=pathlib.Path(grant.fixture_root)
    if str(root)!=FIXTURE_ROOT or not root.is_absolute() or not root.is_dir() or root.resolve()!=root or root.parent.name!='.cdo-sandbox-certification-20261008' or root.name!='release-final-go':
        raise QualificationDenied('QUALIFICATION_FIXTURE_DENIED')
    if request.get('purpose')!='release-qualification' or request.get('repository_kind')!='disposable_certification' or request.get('working_directory')!=str(root):
        raise QualificationDenied('QUALIFICATION_PRODUCT_REPOSITORY_DENIED')
    if request.get('workpackage_id')!=grant.workpackage_id or request.get('release_attempt_id')!=grant.release_attempt_id:
        raise QualificationDenied('QUALIFICATION_WORKPACKAGE_DENIED')
    if request.get('profile')!=grant.role or tuple(request.get('write_paths',()))!=grant.write_paths or request.get('resume_thread'):
        raise QualificationDenied('QUALIFICATION_SCOPE_MISMATCH')
    for name in grant.write_paths:
        p=root/name
        if p.is_symlink() or not p.resolve().is_relative_to(root) or any(parent.is_symlink() for parent in p.parents if parent!=root and parent.is_relative_to(root)) or not p.is_file() or p.stat().st_nlink!=1 or any(x in ('.git','..','.env') for x in p.relative_to(root).parts):raise QualificationDenied('QUALIFICATION_SCOPE_MISMATCH')
    if set(required)!=set(REQUIRED):raise QualificationDenied('QUALIFICATION_POLICY_MISMATCH')
    missing=[k for k in required if k!=DEFERRED and conditions.get(k) is not True]
    if missing:raise QualificationDenied('QUALIFICATION_PREREQUISITE_MISSING:'+','.join(missing))


def consume(grant, actual, request, conditions, required, store):
    # Failed identity/runtime/security checks never consume the model allowance.
    validate(grant,actual,request,conditions,required)
    store=pathlib.Path(store)
    if not store.is_dir() or store.is_symlink() or store.resolve()!=store or store.stat().st_mode&0o022:
        raise QualificationDenied('QUALIFICATION_STORE_DENIED')
    destination=store/(grant.release_attempt_id+'.consumed.json')
    try:fd=os.open(destination,os.O_WRONLY|os.O_CREAT|os.O_EXCL|getattr(os,'O_NOFOLLOW',0),0o600)
    except FileExistsError:raise QualificationDenied('QUALIFICATION_CONSUMED')
    value={'kind':grant.kind,'id':grant.id,'nonce':grant.nonce,'release_attempt_id':grant.release_attempt_id,'workpackage_id':grant.workpackage_id,'grant_sha256':hashlib.sha256(json.dumps(asdict(grant),sort_keys=True).encode()).hexdigest(),'consumed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'general_production_admission':'DENIED'}
    # Even an interrupted/failed write poisons the nonce. Never release consumption.
    with os.fdopen(fd,'w') as f:
        json.dump(value,f);f.flush();os.fsync(f.fileno())
    d=os.open(store,os.O_RDONLY|getattr(os,'O_DIRECTORY',0))
    try:os.fsync(d)
    finally:os.close(d)
    return value
