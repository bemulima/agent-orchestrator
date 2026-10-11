#!/usr/bin/env python3
"""Operator-only disposable macOS host certification. Default is a pure plan.

Run --execute or the isolated --owned-signal-only probe on the supported host.
Never run either inside the restricted agent environment. No model, grant,
production admission, volumes or daemon reset.
A failed attempt is immutable history: resolve its exact owned cleanup first,
then use a fresh output directory. There is no continuation-to-PASS mode.
"""
from __future__ import annotations
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import signal
import socket
import stat
import subprocess
import sys
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
STAGES = ('host-observation', 'owned-group-signals', 'unix-loopback-bind', 'docker-identity',
          'temporal-cancellation-history', 'runner-build', 'build-release', 'source-before-runtime', 'complete-runtime-all',
          'sandbox-test', 'verify', 'go-vet', 'go-test', 'diff-check', 'source-after-checks', 'owned-temporal-cleanup')
IMAGE = re.compile(r'sha256:[a-f0-9]{64}\Z')


class Failure(RuntimeError):
    def __init__(self, code, *, unknown=False, detail=None):
        super().__init__(code)
        self.unknown = unknown
        self.detail = detail


def save(path, value):
    data = json.dumps(value, sort_keys=True, separators=(',', ':'), allow_nan=False).encode()
    if len(data) > 8 * 1024 * 1024:
        raise Failure('EVIDENCE_LIMIT')
    tmp = path.with_name(path.name + '.tmp')
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as stream:
        stream.write(data)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(tmp, path)


def read_json(path):
    info = path.lstat()
    if path.is_symlink() or not path.is_file() or info.st_nlink != 1 or info.st_size > 8 * 1024 * 1024:
        raise Failure('EVIDENCE_FILE_DENIED')
    return json.loads(path.read_bytes())


def plan():
    return {'version': 1, 'status': 'NOT_RUN', 'pass': False, 'stages': list(STAGES),
            'supported_host': 'macOS Docker Desktop desktop-linux',
            'required_capabilities': ['fixed /bin/ps full observation', 'fresh owned group signals and WNOWAIT',
                                      'AF_UNIX and loopback binds', 'fixed canonical Docker endpoint and daemon',
                                      'exact owned disposable Temporal OCI lifecycle', 'exact-run Temporal SDK cancellation/history'],
            'real_model_calls': 0, 'budget': '20/enforce', 'production_admission': 'DENIED',
            'sandbox_ready': 'NO', 'pilot': 'NO', 'actual_daemon_outage_claimed': False,
            'runtime_unknown_boundary': 'CLEANUP_OBSERVER_UNKNOWN',
            'fixed_commands': {
                'observer': ['/bin/ps', '-axo', 'pid=,ppid=,pgid=,uid=,stat='],
                'runner-build': ['npm', '--prefix', str(ROOT / 'runner'), 'run', 'build'],
                'build-release': ['python3', 'scripts/build-production-release.py', '--output', '<fresh-release>', '--tag', '<disposable-tag>', '--docker-executable', '<observed-executable>', '--docker-endpoint', '<observed-endpoint>', '--daemon-id', '<observed-daemon>'],
                'complete-runtime-all': ['python3', 'scripts/probe-complete-runtime-transport.py', '--case', 'all', '--output', '<fresh-suite>', '--runner-image', '<built-exact-image>', '--sdk-image', '<sdk-pin>', '--command-image', '<command-pin>', '--source-manifest', '<captured-source-pin>', '--go-bundle', '<exact-bundle>', '--go-bundle-manifest-sha256', '<approved-bundle-pin>', '--driver', '<frozen-linux-driver>', '--docker-executable', '<observed-executable>', '--docker-endpoint', '<observed-endpoint>', '--daemon-id', '<observed-daemon>'],
                'checks': [['make', 'sandbox-test'], ['make', 'verify'], ['go', 'vet', './...'], ['go', 'test', './...'], ['make', 'diff-check']]},
            'resume': 'Preserve failed attempt. Reconcile its exact authenticated ownership ledger; rerun in fresh output.'}


def parser():
    value = argparse.ArgumentParser(description=__doc__)
    mode = value.add_mutually_exclusive_group()
    mode.add_argument('--execute', action='store_true')
    mode.add_argument('--plan', '--source-only', action='store_true')
    mode.add_argument('--observe-only', action='store_true', help='Read-only trusted process observer diagnosis; no certification stages')
    mode.add_argument('--owned-signal-only', action='store_true', help='Reconcile old absence read-only, then probe one fresh owned group; no other runtime stages')
    mode.add_argument('--reconcile-temporal-only', action='store_true', help='Owner-authorized exact saved Temporal cleanup; separate immutable receipt, no certification')
    mode.add_argument('--observe-temporal-absence-only', action='store_true', help='Read-only exact saved Temporal absence and daemon verification; never removes a resource')
    value.add_argument('--reconcile-attempt')
    value.add_argument('--reconcile-attempt-id')
    value.add_argument('--approved-container-id')
    value.add_argument('--approved-container-image')
    value.add_argument('--approved-container-label')
    value.add_argument('--approved-docker-identity-sha256')
    value.add_argument('--output')
    value.add_argument('--go-bundle')
    value.add_argument('--command-image')
    value.add_argument('--sdk-image')
    value.add_argument('--temporal-image', help='Local Temporal CLI image reference or auto-local; resolved once, never pulled')
    value.add_argument('--go-bundle-manifest-sha256')
    return value


def run(argv, *, timeout=60, env=None, log=None, cwd=ROOT):
    # Every argv is authored below; the CLI never accepts commands or shell text.
    result = subprocess.run(argv, cwd=cwd, env=env, capture_output=True, text=True, timeout=timeout)
    if log is not None:
        save(log, {'argv': argv, 'exit_code': result.returncode,
                   'stdout': result.stdout[-1024*1024:], 'stderr': result.stderr[-65536:]})
    if result.returncode:
        raise Failure('COMMAND_FAILED:' + Path(argv[0]).name)
    return result


def validate_inputs(args):
    if sys.platform != 'darwin':
        raise Failure('SUPPORTED_MACOS_HOST_REQUIRED')
    for name in ('output', 'go_bundle', 'command_image', 'sdk_image', 'temporal_image', 'go_bundle_manifest_sha256'):
        if not getattr(args, name):
            raise Failure('EXPLICIT_INPUTS_REQUIRED')
    if not IMAGE.fullmatch(args.command_image) or not IMAGE.fullmatch(args.sdk_image):
        raise Failure('IMMUTABLE_COMMAND_SDK_IMAGE_REQUIRED')
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._/@:-]{0,255}', args.temporal_image):
        raise Failure('TEMPORAL_IMAGE_REFERENCE_DENIED')
    bundle = Path(args.go_bundle)
    if not bundle.is_absolute() or bundle.resolve(strict=True) != bundle or not bundle.is_dir():
        raise Failure('EXACT_EXISTING_GO_BUNDLE_REQUIRED')
    manifest_file = bundle / 'manifest.json'
    manifest = read_json(manifest_file)
    if not re.fullmatch(r'[a-f0-9]{64}', args.go_bundle_manifest_sha256) or hashlib.sha256(manifest_file.read_bytes()).hexdigest() != args.go_bundle_manifest_sha256:
        raise Failure('GO_BUNDLE_MANIFEST_PIN_MISMATCH')
    if manifest.get('toolchain_image') != args.command_image:
        raise Failure('GO_DEPENDENCY_IMAGE_MISMATCH')
    output = Path(args.output)
    if not output.is_absolute() or output.parent.resolve(strict=True) != output.parent:
        raise Failure('CANONICAL_FRESH_OUTPUT_REQUIRED')


def coordinator():
    # Imported only for explicit runtime/observer modes, never for the pure plan.
    sys.path.insert(0, str(ROOT / 'runner/bin'))
    import cdo_runtime_coordinator
    return cdo_runtime_coordinator


def observe_host():
    try:
        rows = coordinator().group_members(None)
        caller = next((row for row in rows if row['pid'] == os.getpid()), None)
        if caller is None:
            raise Failure('PROCESS_OBSERVATION_UNKNOWN', unknown=True, detail={'reason': 'CALLER_ANCHOR_MISSING'})
        if caller['uid'] != os.geteuid():
            raise Failure('PROCESS_OBSERVATION_UNKNOWN', unknown=True, detail={'reason': 'CALLER_UID_MISMATCH'})
        zero_pid = sum(row['pid'] == 0 for row in rows)
        zero_group = sum(row['pgid'] == 0 for row in rows)
        return {'complete_observation': True, 'caller_observed': True, 'observer_anchor_validated': True,
                'observer_parent_validated': True, 'record_count': len(rows),
                'zero_pid_records': zero_pid, 'zero_pgid_records': zero_group,
                'legacy_positive_ids_rule_would_reject': bool(zero_pid or zero_group),
                'runtime_context': observer_context()}
    except Exception as error:
        detail = getattr(error, 'detail', None) or {'reason': 'OBSERVER_FAILURE_WITHOUT_DETAIL',
                 'observer_code': str(error) if isinstance(error, Failure) or type(error).__name__ == 'CoordinatorDenied' else 'UNEXPECTED_OBSERVER_EXCEPTION',
                 'observer_errno': getattr(error, 'errno', None)}
        raise Failure('PROCESS_OBSERVATION_UNKNOWN', unknown=True,
                      detail={**detail, 'runtime_context': observer_context()}) from error


def observer_context():
    # No process rows, command lines, environment values or unrelated identities.
    return {'python_executable': str(Path(sys.executable).resolve()),
            'python_version': '.'.join(str(part) for part in sys.version_info[:3]),
            'platform': sys.platform, 'cwd_matches_repository': Path.cwd() == ROOT,
            'caller_real_uid': os.getuid(), 'caller_effective_uid': os.geteuid(),
            'python_flags': {name: getattr(sys.flags, name) for name in
                             ('isolated', 'ignore_environment', 'no_site', 'no_user_site', 'utf8_mode')},
            'ps_environment': 'inherited', 'ps_cwd': 'inherited', 'ps_stdin': 'DEVNULL',
            'ps_stdout_stderr': 'bounded byte pipes',
            'locale_environment_present': {name: name in os.environ for name in ('LANG', 'LC_ALL', 'LC_CTYPE')}}


def observe_only():
    # Keep this diagnostic read-only even when the operator omits python -B.
    sys.dont_write_bytecode = True
    result = {'scope': 'HOST_PROCESS_OBSERVER_DIAGNOSIS', 'read_only': True,
              'real_model_calls': 0, 'production_admission': 'DENIED'}
    try:
        result.update(status='PASS', evidence=observe_host())
        code = 0
    except Failure as error:
        result.update(status='UNKNOWN', error={'code': str(error), 'detail': error.detail})
        code = 1
    print(json.dumps(result, sort_keys=True, allow_nan=False))
    return code


def signal_error_detail(error, phase):
    # Never persist exception messages, process rows, commands or environment.
    nested = getattr(error, 'detail', None)
    detail = {'phase': phase, 'original_error_type': type(error).__name__,
              'os_errno': getattr(error, 'errno', None)}
    if isinstance(error, Failure) or type(error).__name__ == 'CoordinatorDenied':
        detail['original_error_code'] = str(error)[:160]
    if isinstance(nested, dict):
        keys = ('reason', 'operation', 'backend', 'observer_errno', 'observer_operation',
                'observer_exit_code', 'stdout_bytes', 'stderr_bytes', 'wnowait')
        detail['original_detail'] = {key: nested[key] for key in keys if key in nested and
                                     (nested[key] is None or type(nested[key]) in (int, bool) or
                                      isinstance(nested[key], str) and len(nested[key]) <= 160)}
        if detail['os_errno'] is None:
            detail['os_errno'] = detail['original_detail'].get('observer_errno')
    return detail


def signal_preflight(output):
    c = coordinator()
    process, failure, identity = None, None, None
    phase = 'caller-identity'
    observer_unknown, absence_checked = False, False
    evidence = {'version': 1, 'status': 'RUNNING', 'phase': phase,
                'owned_child_created': False, 'identity_validated': False,
                'identity_snapshots_validated': 0, 'signal_attempted': False,
                'signal_accepted': False, 'waitid_validated': False,
                'leader_reaped': False, 'group_absent': False, 'cleanup_state': 'NOT_CREATED'}
    def record(*, signal_pending=False):
        evidence['phase'] = phase
        durable = {**evidence, 'signal_attempted': None, 'signal_intent_pending': True} if signal_pending else evidence
        save(output / 'owned-signal-diagnostics.json', durable)
    def observe(group):
        nonlocal observer_unknown
        try:
            return c.group_members(group)
        except Exception:
            observer_unknown = True
            raise
    try:
        caller_pid, caller_uid, caller_euid = os.getpid(), os.getuid(), os.geteuid()
        if caller_uid != caller_euid:
            raise Failure('CALLER_REAL_EFFECTIVE_UID_MISMATCH', unknown=True)
        phase = 'process-creation'
        record()
        process = subprocess.Popen([str(Path(sys.executable).resolve()), '-c',
                                    'import time; time.sleep(4)'], start_new_session=True,
                                   stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        identity = {'pid': process.pid, 'pgid': process.pid, 'sid': process.pid,
                    'parent_pid': caller_pid, 'uid': caller_euid, 'real_uid': caller_uid}
        evidence.update(owned_child_created=True, identity=identity, cleanup_state='UNPROVEN')
        phase = 'ownership-ledger'
        save(output / 'owned-signal-process.json', identity)
        record()
        # No wait()/poll() here: retain the exact child's PID until signal/proof.
        for index in range(2):
            phase = 'identity-observation-' + str(index + 1)
            record()
            rows = observe(process.pid)
            phase = 'identity-validation-' + str(index + 1)
            checks = {'single_member': len(rows) == 1, 'caller_stable':
                      (os.getpid(), os.getuid(), os.geteuid()) == (caller_pid, caller_uid, caller_euid)}
            if len(rows) == 1:
                row = rows[0]
                checks.update(pid=row['pid'] == process.pid, parent=row['ppid'] == caller_pid,
                              uid=row['uid'] == caller_euid, pgid=row['pgid'] == process.pid,
                              live=not row['state'].startswith('Z'))
            evidence['identity_checks'] = checks
            if not all(checks.values()):
                raise Failure('FRESH_GROUP_IDENTITY_UNKNOWN', unknown=True)
            phase = 'getpgid-' + str(index + 1)
            if os.getpgid(process.pid) != process.pid:
                raise Failure('FRESH_PGID_IDENTITY_UNKNOWN', unknown=True)
            phase = 'getsid-' + str(index + 1)
            if os.getsid(process.pid) != process.pid:
                raise Failure('FRESH_SID_IDENTITY_UNKNOWN', unknown=True)
            evidence['identity_snapshots_validated'] += 1
        evidence['identity_validated'] = True
        phase = 'killpg'
        # Durable pending intent is distinct from an actual returned syscall.
        record(signal_pending=True)
        evidence['signal_attempted'] = True
        os.killpg(process.pid, signal.SIGTERM)
        evidence['signal_accepted'] = True
        phase = 'waitid'
        record()
        observed = c.wait_unreaped(process.pid)
        phase = 'waitid-validation'
        evidence['waitid_receipt'] = {key: observed.get(key) for key in
                                      ('pid', 'uid', 'code', 'status', 'uid_available', 'backend', 'wnowait')}
        if observed.get('pid') != process.pid:
            evidence['waitid_receipt']['pid'] = None  # never disclose a foreign PID
        darwin_uid_unavailable = sys.platform == 'darwin' and observed.get('backend') in ('darwin-libSystem-waitid', 'os-waitid') and observed.get('uid_available') is False and observed.get('uid') == 0
        native_uid_matches = sys.platform != 'darwin' and observed.get('backend') == 'os-waitid' and observed.get('uid_available') is True and observed.get('uid') == caller_uid
        checks = {'fields_valid': all(type(observed.get(key)) is int for key in ('pid', 'uid', 'code', 'status')),
                  'pid_matches': observed.get('pid') == process.pid, 'cause_matches': observed.get('code') == 2,
                  'signal_matches': observed.get('status') == signal.SIGTERM, 'wnowait': observed.get('wnowait') is True,
                  'uid_semantics_valid': darwin_uid_unavailable or native_uid_matches}
        evidence['waitid_validation'] = {'checks': checks, 'expected_real_uid': caller_uid,
                                        'expected_effective_uid': caller_euid,
                                        'uid_source': 'trusted-unreaped-zombie-effective-uid' if darwin_uid_unavailable else 'waitid-real-uid',
                                        'zombie_identity_validated': False}
        # CLD_KILLED=2 and SIGTERM prove signal exit. XNU's zero-filled UID
        # never proves ownership: require a fresh exact zombie identity below.
        if not all(checks.values()):
            raise Failure('FRESH_CHILD_SIGTERM_EXIT_UNKNOWN', unknown=True)
        phase = 'termination-observation'
        record()
        rows = observe(process.pid)
        zombie_checks = {'single_member': len(rows) == 1, 'caller_stable':
                         (os.getpid(), os.getuid(), os.geteuid()) == (caller_pid, caller_uid, caller_euid)}
        if len(rows) == 1:
            row = rows[0]
            zombie_checks.update(pid=row['pid'] == process.pid, parent=row['ppid'] == caller_pid,
                                 pgid=row['pgid'] == process.pid, effective_uid=row['uid'] == caller_euid,
                                 zombie=row['state'].startswith('Z'))
        evidence['waitid_validation']['zombie_checks'] = zombie_checks
        # WNOWAIT keeps the child unreaped. Darwin getpgid/getsid can reject
        # zombies; SID/PGID syscalls remain in the two LIVE identity checks.
        if not all(zombie_checks.values()):
            raise Failure('FRESH_GROUP_EXIT_UNKNOWN', unknown=True)
        evidence['waitid_validation']['zombie_identity_validated'] = True
        evidence['waitid_validated'] = True
        phase = 'reap'
        returncode = process.wait(timeout=5)
        evidence['leader_reaped'] = True
        evidence['reap_returncode'] = returncode if type(returncode) is int else None
        if type(returncode) is not int or returncode != -signal.SIGTERM:
            raise Failure('FRESH_CHILD_REAP_STATUS_UNKNOWN', unknown=True)
        phase = 'group-absence'
        record()
        rows = observe(None)
        absence_checked = True
        evidence['group_absent'] = not any(row['pid'] == process.pid or row['pgid'] == process.pid for row in rows)
        if not evidence['group_absent']:
            raise Failure('FRESH_GROUP_ABSENCE_UNKNOWN', unknown=True)
        evidence.update(status='PASS', cleanup_state='PROVEN_ABSENT')
        phase = 'complete'
        record()
        return evidence
    except Exception as error:
        detail = signal_error_detail(error, phase)
        detail.update(owned_child_created=evidence['owned_child_created'],
                      identity_validated=evidence['identity_validated'],
                      signal_attempted=evidence['signal_attempted'], signal_accepted=evidence['signal_accepted'])
        if 'waitid_validation' in evidence:
            detail['waitid_validation'] = evidence['waitid_validation']
        evidence.update(status='UNKNOWN', error=detail)
        failure = Failure('OWNED_SIGNAL_CAPABILITY_UNKNOWN', unknown=True, detail=detail)
        raise failure from error
    finally:
        if failure is not None:
            if process is not None:
                # Only wait on our live Popen handle. No saved-PID or fallback
                # signal. A read denial is latched, never retried for cleanup.
                try:
                    if not evidence['leader_reaped']:
                        process.wait(timeout=6)
                        evidence['leader_reaped'] = True
                    if not observer_unknown and not absence_checked:
                        rows = observe(None)
                        evidence['group_absent'] = not any(row['pid'] == process.pid or row['pgid'] == process.pid for row in rows)
                    evidence['cleanup_state'] = 'PROVEN_ABSENT' if evidence['leader_reaped'] and evidence['group_absent'] else 'UNKNOWN'
                except Exception as cleanup_error:
                    evidence['cleanup_state'] = 'UNKNOWN'
                    evidence['cleanup_error'] = signal_error_detail(cleanup_error, 'failure-cleanup')
                if observer_unknown:
                    evidence['cleanup_observation'] = 'UNKNOWN_NO_RETRY'
            failure.detail['cleanup_state'] = evidence['cleanup_state']
            try:
                record()
            except Exception as write_error:
                # Do not replace the original runtime denial with an IO error.
                failure.detail['diagnostic_write_error'] = signal_error_detail(write_error, 'evidence-write')


def read_signal_evidence(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or info.st_nlink != 1 or info.st_size > 65536:
            raise Failure('OLD_SIGNAL_EVIDENCE_FILE_DENIED', unknown=True)
        with os.fdopen(fd, 'rb', closefd=False) as stream:
            raw = stream.read(65537)
        final = os.fstat(fd)
        fields = ('st_dev', 'st_ino', 'st_size', 'st_mtime_ns', 'st_ctime_ns')
        if len(raw) > 65536 or any(getattr(info, key) != getattr(final, key) for key in fields):
            raise Failure('OLD_SIGNAL_EVIDENCE_CHANGED', unknown=True)
        value = json.loads(raw)
        if not isinstance(value, dict):
            raise Failure('OLD_SIGNAL_EVIDENCE_INVALID', unknown=True)
        return value, hashlib.sha256(raw).hexdigest()
    finally:
        os.close(fd)


def reconcile_signal_absence(directory, attempt_id):
    # Saved IDs are ONLY an absence selector, never permission to signal/reap.
    files = ('runtime-host-result.json', 'owned-signal-process.json')
    attempt, attempt_pin = read_signal_evidence(directory / files[0])
    identity, identity_pin = read_signal_evidence(directory / files[1])
    pins = dict(zip(files, (attempt_pin, identity_pin)))
    if attempt.get('attempt_id') != attempt_id or attempt.get('stages', {}).get('owned-group-signals', {}).get('status') != 'UNKNOWN':
        raise Failure('OLD_SIGNAL_ATTEMPT_MISMATCH', unknown=True)
    if set(identity) != {'pid', 'pgid', 'sid', 'parent_pid', 'uid'} or any(type(identity[key]) is not int or identity[key] < 1 for key in ('pid', 'pgid', 'sid', 'parent_pid')) or type(identity['uid']) is not int or identity['uid'] != os.geteuid() or identity['pid'] != identity['pgid'] or identity['pid'] != identity['sid']:
        raise Failure('OLD_SIGNAL_LEDGER_INVALID', unknown=True)
    try:
        rows = coordinator().group_members(None)
    except Exception as error:
        raise Failure('OLD_SIGNAL_RECONCILIATION_UNKNOWN', unknown=True,
                      detail=signal_error_detail(error, 'old-group-observation')) from error
    if any(row['pid'] == identity['pid'] or row['pgid'] == identity['pgid'] for row in rows):
        raise Failure('OLD_SIGNAL_RESOURCES_NOT_PROVEN_ABSENT', unknown=True,
                      detail={'phase': 'old-group-absence', 'reason': 'SAVED_PID_OR_PGID_PRESENT_NO_ACTION'})
    if pins != {name: read_signal_evidence(directory / name)[1] for name in files}:
        raise Failure('OLD_SIGNAL_EVIDENCE_CHANGED', unknown=True)
    return {'attempt_id': attempt_id, 'old_evidence_sha256': pins,
            'complete_observation': True, 'saved_pid_absent': True, 'saved_pgid_absent': True,
            'old_evidence_unchanged': True, 'signals_sent_to_old_resources': 0,
            'old_resources_deleted': 0, 'cleanup_state': 'PROVEN_ABSENT'}


def owned_signal_only(args):
    sys.dont_write_bytecode = True
    if sys.platform != 'darwin':
        raise Failure('SUPPORTED_MACOS_HOST_REQUIRED')
    if not args.output or not args.reconcile_attempt or not args.reconcile_attempt_id:
        raise Failure('EXPLICIT_SIGNAL_DIAGNOSIS_INPUTS_REQUIRED')
    directory, output = Path(args.reconcile_attempt), Path(args.output)
    parent = ROOT / '.cache/sandbox-certification'
    if not directory.is_absolute() or directory.resolve(strict=True) != directory or not directory.is_dir() or directory.parent != parent or not output.is_absolute() or output.parent.resolve(strict=True) != parent or output.parent != parent:
        raise Failure('CANONICAL_SIGNAL_EVIDENCE_PATHS_REQUIRED')
    output.mkdir(mode=0o700, parents=False, exist_ok=False)
    result = {'version': 1, 'scope': 'OWNED_SIGNAL_DIAGNOSIS_ONLY', 'status': 'RUNNING',
              'pass': False, 'attempt_id': str(uuid.uuid4()), 'real_model_calls': 0,
              'budget': '20/enforce', 'production_admission': 'DENIED', 'sandbox_ready': 'NO',
              'old_resource_reconciliation': 'NOT_RUN', 'fresh_owned_signal_probe': 'NOT_RUN'}
    save(output / 'owned-signal-only-result.json', result)
    try:
        result['old_resource_reconciliation'] = reconcile_signal_absence(directory, args.reconcile_attempt_id)
        save(output / 'owned-signal-only-result.json', result)
        result['fresh_owned_signal_probe'] = signal_preflight(output)
        result.update(status='PASS')
        result['pass'] = True
    except Exception as error:
        result['status'] = 'UNKNOWN'
        result['error'] = {'code': str(error)[:160] if isinstance(error, Failure) else 'SIGNAL_DIAGNOSIS_FAILED',
                           'detail': getattr(error, 'detail', None) or signal_error_detail(error, 'signal-only')}
    save(output / 'owned-signal-only-result.json', result)
    print(json.dumps(result, sort_keys=True, allow_nan=False))
    return 0 if result['pass'] else 1


def bind_preflight(output):
    # Darwin sockaddr_un limits the path length; evidence directories can be
    # longer. A fresh private fixed-root directory is owned only by this probe.
    directory = Path(tempfile.mkdtemp(prefix='cdo-host-bind-', dir='/private/tmp'))
    directory.chmod(0o700)
    path = directory / 'capability.sock'
    unix, loopback = None, None
    evidence = {'socket_path': str(path), 'private_directory': str(directory),
                'unix_bind': False, 'loopback_bind': False, 'socket_removed': False,
                'private_directory_removed': False}
    try:
        unix = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        loopback = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        unix.bind(str(path))
        unix.listen(1)
        evidence['unix_bind'] = True
        loopback.bind(('127.0.0.1', 0))
        loopback.listen(1)
        evidence['loopback_bind'] = True
        return evidence
    except OSError as error:
        raise Failure('HOST_BIND_CAPABILITY_UNKNOWN', unknown=True, detail=evidence) from error
    finally:
        if unix is not None:
            unix.close()
        if loopback is not None:
            loopback.close()
        try:
            if path.exists():
                path.unlink()
            evidence['socket_removed'] = not os.path.lexists(path)
            directory.rmdir()
            evidence['private_directory_removed'] = not os.path.lexists(directory)
            if not evidence['socket_removed'] or not evidence['private_directory_removed']:
                raise Failure('HOST_BIND_CLEANUP_UNKNOWN', unknown=True, detail=evidence)
        except OSError as error:
            raise Failure('HOST_BIND_CLEANUP_UNKNOWN', unknown=True, detail=evidence) from error


class Docker:
    def __init__(self, executable, endpoint, daemon, output):
        self.executable, self.endpoint, self.daemon, self.output = executable, endpoint, daemon, output
        self.actions = []

    def call(self, argv, *, check=True, timeout=30):
        if getattr(self, 'denied', False):
            raise Failure('DOCKER_CAPABILITY_DENIAL_LATCHED', unknown=True)
        try:
            if self.daemon and argv != ['info', '--format', '{{.ID}}']:
                current = self._invoke(['info', '--format', '{{.ID}}'], timeout)
                if current.returncode or current.stdout.strip() != self.daemon:
                    raise Failure('DOCKER_DAEMON_IDENTITY_CHANGED', unknown=True)
            result = self._invoke(argv, timeout)
            if result.returncode and (check or not (argv[0] == 'inspect' and len(argv) == 2 and absence(result, argv[1]))):
                raise Failure('DOCKER_OPERATION_UNKNOWN', unknown=True)
            return result
        except Exception as error:
            self.denied = True
            if isinstance(error, Failure):
                raise
            raise Failure('DOCKER_OPERATION_UNKNOWN', unknown=True) from error

    def _invoke(self, argv, timeout):
        result = subprocess.run([self.executable, '--host', self.endpoint, *argv],
                                capture_output=True, text=True, timeout=timeout,
                                env={'PATH': '/usr/local/bin:/usr/bin:/bin', 'HOME': '/var/empty'})
        self.actions.append({'argv': argv, 'exit_code': result.returncode,
                             'stdout': result.stdout[-1048576:], 'stderr': result.stderr[-65536:]})
        save(self.output / 'docker-actions.json', self.actions)
        return result


def local_temporal_image(docker):
    rows = docker.call(['image', 'ls', '--no-trunc', '--format', '{{.ID}}']).stdout.split()
    ids = sorted(set(rows))
    if len(ids) > 256 or any(not IMAGE.fullmatch(identity) for identity in ids):
        raise Failure('EXTERNAL_INFRASTRUCTURE_UNAVAILABLE', detail={'reason': 'LOCAL_IMAGE_INVENTORY_INVALID_OR_LIMITED'})
    values = json.loads(docker.call(['image', 'inspect', *ids]).stdout) if ids else []
    if len(values) != len(ids) or {item.get('Id') for item in values} != set(ids):
        raise Failure('DOCKER_IMAGE_INVENTORY_UNKNOWN', unknown=True)
    candidates = sorted({item['Id'] for item in values
                         if any(tag.startswith('temporalio/temporal:') for tag in (item.get('RepoTags') or []))})
    if len(candidates) != 1:
        raise Failure('EXTERNAL_INFRASTRUCTURE_UNAVAILABLE', detail={
            'reason': 'LOCAL_TEMPORAL_IMAGE_MISSING' if not candidates else 'LOCAL_TEMPORAL_IMAGE_AMBIGUOUS',
            'candidate_ids': candidates, 'local_image_count': len(ids), 'infra_changes': False})
    return candidates[0]


def docker_preflight(args, output):
    executable = str(Path(shutil.which('docker') or '').resolve(strict=True))
    # Read the operator's selected host configuration once. Lifecycle uses only
    # the resulting fixed canonical endpoint, with isolated HOME.
    env = dict(os.environ)
    context = run([executable, 'context', 'show'], env=env).stdout.strip()
    if context != 'desktop-linux':
        raise Failure('DOCKER_CONTEXT_CHANGED', unknown=True)
    endpoint = run([executable, 'context', 'inspect', context, '--format', '{{.Endpoints.docker.Host}}'], env=env).stdout.strip()
    if not endpoint.startswith('unix:///'):
        raise Failure('DOCKER_ENDPOINT_DENIED', unknown=True)
    endpoint = 'unix://' + str(Path(endpoint[7:]).resolve(strict=True))
    docker = Docker(executable, endpoint, None, output)
    daemon = docker.call(['info', '--format', '{{.ID}}']).stdout.strip()
    if not daemon:
        raise Failure('DOCKER_DAEMON_UNKNOWN', unknown=True)
    docker.daemon = daemon
    images = {}
    temporal_reference = local_temporal_image(docker) if args.temporal_image == 'auto-local' else args.temporal_image
    for role, reference in (('command', args.command_image), ('sdk', args.sdk_image), ('temporal', temporal_reference)):
        values = json.loads(docker.call(['image', 'inspect', reference]).stdout)
        identity = values[0]['Id'] if len(values) == 1 else ''
        if not IMAGE.fullmatch(identity) or (role != 'temporal' and identity != reference):
            raise Failure('DOCKER_IMAGE_IDENTITY_CHANGED', unknown=True)
        images[role] = identity
    save(output / 'docker-identity.json', {'executable': executable, 'endpoint': endpoint, 'daemon_id': daemon, 'images': images})
    return docker, images


def absence(result, identity):
    return (bool(re.fullmatch(r'[a-f0-9]{64}', identity)) and result.returncode == 1 and result.stdout.strip() in ('', '[]')
            and result.stderr.strip() in {prefix + identity for prefix in ('Error: No such object: ', 'Error: No such container: ',
                'Error response from daemon: No such object: ', 'Error response from daemon: No such container: ',
                'error: no such object: ')})


def inspect_owned(docker, owned):
    values = json.loads(docker.call(['inspect', owned['id']]).stdout)
    if len(values) != 1 or values[0]['Id'] != owned['id'] or values[0]['Image'] != owned['image'] or values[0]['Config'].get('Labels', {}).get('cdo.host-certification') != owned['label']:
        raise Failure('TEMPORAL_OWNERSHIP_UNKNOWN', unknown=True)
    return values[0]


def cleanup_temporal(docker, owned):
    if not re.fullmatch(r'[a-f0-9]{64}', owned.get('id', '')) or not IMAGE.fullmatch(owned.get('image', '')) or not isinstance(owned.get('label'), str) or not owned['label']:
        raise Failure('TEMPORAL_OWNERSHIP_UNKNOWN', unknown=True)
    observed = docker.call(['inspect', owned['id']], check=False)
    if absence(observed, owned['id']):
        if docker.call(['info', '--format', '{{.ID}}']).stdout.strip() != docker.daemon:
            raise Failure('DOCKER_DAEMON_IDENTITY_CHANGED', unknown=True)
        return {'exact_resource_absent': True}
    if observed.returncode:
        raise Failure('TEMPORAL_CLEANUP_UNKNOWN', unknown=True)
    values = json.loads(observed.stdout)
    if len(values) != 1 or values[0]['Id'] != owned['id'] or values[0]['Image'] != owned['image'] or values[0]['Config'].get('Labels', {}).get('cdo.host-certification') != owned['label'] or ('name' in owned and values[0].get('Name') != '/' + owned['name']):
        raise Failure('TEMPORAL_OWNERSHIP_UNKNOWN', unknown=True)
    if docker.call(['info', '--format', '{{.ID}}']).stdout.strip() != docker.daemon:
        raise Failure('DOCKER_DAEMON_IDENTITY_CHANGED', unknown=True)
    docker.call(['rm', '-f', owned['id']])
    if not absence(docker.call(['inspect', owned['id']], check=False), owned['id']):
        raise Failure('TEMPORAL_ABSENCE_UNKNOWN', unknown=True)
    if docker.call(['info', '--format', '{{.ID}}']).stdout.strip() != docker.daemon:
        raise Failure('DOCKER_DAEMON_IDENTITY_CHANGED', unknown=True)
    return {'exact_resource_absent': True}


def save_immutable_receipt(path, value):
    data = json.dumps(value, sort_keys=True, separators=(',', ':'), allow_nan=False).encode()
    if len(data) > 65536:
        raise Failure('RECONCILIATION_RECEIPT_LIMIT', unknown=True)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o400)
    with os.fdopen(fd, 'wb') as stream:
        stream.write(data)
        stream.flush()
        os.fsync(stream.fileno())


def reconcile_temporal_only(args):
    sys.dont_write_bytecode = True
    observation_only = getattr(args, 'observe_temporal_absence_only', False)
    if sys.platform != 'darwin':
        raise Failure('SUPPORTED_MACOS_HOST_REQUIRED')
    required = ('output', 'reconcile_attempt', 'reconcile_attempt_id', 'approved_container_id', 'approved_docker_identity_sha256',
                'approved_container_image', 'approved_container_label')
    if any(not getattr(args, name) for name in required):
        raise Failure('EXPLICIT_TEMPORAL_CLEANUP_APPROVAL_REQUIRED')
    if not re.fullmatch(r'[a-f0-9]{64}', args.approved_container_id) or not re.fullmatch(r'[a-f0-9]{64}', args.approved_docker_identity_sha256) or not IMAGE.fullmatch(args.approved_container_image) or str(uuid.UUID(args.approved_container_label)) != args.approved_container_label:
        raise Failure('TEMPORAL_CLEANUP_APPROVAL_INVALID')
    directory, output = Path(args.reconcile_attempt), Path(args.output)
    parent = ROOT / '.cache/sandbox-certification'
    if not directory.is_absolute() or directory.resolve(strict=True) != directory or not directory.is_dir() or directory.parent != parent or not output.is_absolute() or output.parent.resolve(strict=True) != parent or output.parent != parent:
        raise Failure('CANONICAL_TEMPORAL_EVIDENCE_PATHS_REQUIRED')
    output.mkdir(mode=0o700, parents=False, exist_ok=False)
    receipt = {'version': 1, 'scope': 'EXACT_TEMPORAL_ABSENCE_OBSERVATION' if observation_only else 'EXACT_OWNER_AUTHORIZED_TEMPORAL_RECONCILIATION',
               'read_only': observation_only,
               'receipt_id': str(uuid.uuid4()), 'original_attempt_id': args.reconcile_attempt_id,
               'status': 'UNKNOWN', 'exact_resource_absent': False, 'ownership_retained': True,
               'real_model_calls': 0, 'budget': '20/enforce', 'production_admission': 'DENIED',
               'sandbox_ready': 'NO', 'started_at': time.time()}
    docker, phase, pins = None, 'saved-evidence', {}
    try:
        files = ('runtime-host-result.json', 'temporal-owner.json', 'docker-identity.json')
        # Reuse the existing no-follow, owner-bound, bounded evidence reader.
        records = [read_signal_evidence(directory / name) for name in files]
        attempt, owned, identity = (record[0] for record in records)
        pins = {name: record[1] for name, record in zip(files, records)}
        receipt['original_evidence_sha256'] = pins
        if pins['docker-identity.json'] != args.approved_docker_identity_sha256:
            raise Failure('APPROVED_DOCKER_IDENTITY_MISMATCH', unknown=True)
        receipt['approved_docker_identity_sha256'] = args.approved_docker_identity_sha256
        if attempt.get('attempt_id') != args.reconcile_attempt_id or attempt.get('stages', {}).get('owned-temporal-cleanup', {}).get('status') != 'UNKNOWN' or owned.get('status') != 'REGISTERED' or any(owned.get(key) != value for key, value in {
                'id': args.approved_container_id, 'image': args.approved_container_image,
                'label': args.approved_container_label}.items()) or owned.get('name') != 'cdo-host-certification-' + args.approved_container_label:
            raise Failure('TEMPORAL_APPROVAL_LEDGER_MISMATCH', unknown=True)
        receipt['approved_owned_resource'] = owned
        executable, endpoint, daemon = (identity.get(name) for name in ('executable', 'endpoint', 'daemon_id'))
        if not isinstance(executable, str) or not Path(executable).is_absolute() or str(Path(executable).resolve(strict=True)) != executable or not isinstance(endpoint, str) or not endpoint.startswith('unix:///') or 'unix://' + str(Path(endpoint[7:]).resolve(strict=True)) != endpoint or not isinstance(daemon, str) or not daemon:
            raise Failure('SAVED_DOCKER_IDENTITY_INVALID', unknown=True)
        receipt['docker_identity'] = {'executable': executable, 'endpoint': endpoint, 'daemon_id': daemon}
        phase = 'selected-context'
        if run([executable, 'context', 'show'], timeout=15, log=output / 'selected-context.json').stdout.strip() != 'desktop-linux':
            raise Failure('DOCKER_CONTEXT_CHANGED', unknown=True)
        selected = run([executable, 'context', 'inspect', 'desktop-linux', '--format', '{{.Endpoints.docker.Host}}'], timeout=15, log=output / 'selected-endpoint.json').stdout.strip()
        if not selected.startswith('unix:///') or 'unix://' + str(Path(selected[7:]).resolve(strict=True)) != endpoint:
            raise Failure('DOCKER_ENDPOINT_CHANGED', unknown=True)
        docker = Docker(executable, endpoint, daemon, output)
        phase = 'owned-cleanup'
        if pins != {name: read_signal_evidence(directory / name)[1] for name in files}:
            raise Failure('ORIGINAL_TEMPORAL_EVIDENCE_CHANGED', unknown=True)
        if observation_only:
            # This branch has no mutation call, including when the ID is present.
            observed = docker.call(['inspect', owned['id']], check=False)
            if not absence(observed, owned['id']):
                raise Failure('TEMPORAL_ABSENCE_NOT_PROVEN', unknown=True)
            if docker.call(['info', '--format', '{{.ID}}']).stdout.strip() != docker.daemon:
                raise Failure('DOCKER_DAEMON_IDENTITY_CHANGED', unknown=True)
            cleanup = {'exact_resource_absent': True, 'mutations_attempted': 0}
        else:
            cleanup = cleanup_temporal(docker, owned)
        if cleanup.get('exact_resource_absent') is not True:
            raise Failure('TEMPORAL_ABSENCE_UNKNOWN', unknown=True)
        phase = 'original-evidence-preservation'
        if pins != {name: read_signal_evidence(directory / name)[1] for name in files}:
            raise Failure('ORIGINAL_TEMPORAL_EVIDENCE_CHANGED', unknown=True)
        receipt.update(status='PASS', exact_resource_absent=True, ownership_retained=False,
                       original_evidence_unchanged=True, cleanup=cleanup)
    except Exception as error:
        cause, errno = error, None
        for unused in range(5):
            if cause is None:
                break
            if getattr(cause, 'errno', None) is not None:
                errno = cause.errno
                break
            cause = cause.__cause__
        receipt['error'] = {'phase': phase, 'code': str(error)[:160] if isinstance(error, Failure) else 'TEMPORAL_RECONCILIATION_UNKNOWN',
                            'error_type': type(error).__name__, 'os_errno': errno}
        if docker is not None and docker.actions:
            last = docker.actions[-1]
            receipt['last_docker_observation'] = {'argv': last['argv'], 'exit_code': last['exit_code'],
                                                'stderr': last['stderr'][-2048:]}
        if pins:
            try:
                receipt['original_evidence_unchanged'] = pins == {name: read_signal_evidence(directory / name)[1] for name in pins}
            except Exception:
                receipt['original_evidence_unchanged'] = False
    receipt['finished_at'] = time.time()
    save_immutable_receipt(output / 'temporal-reconciliation-receipt.json', receipt)
    print(json.dumps(receipt, sort_keys=True))
    return 0 if receipt['status'] == 'PASS' else 1


def go_env():
    return {**os.environ, 'XDG_CACHE_HOME': str(ROOT / '.cache'), 'GOCACHE': str(ROOT / '.cache/go-build'),
            'GOMODCACHE': str(ROOT / '.cache/gomod'), 'GOBIN': str(ROOT / '.cache/bin')}


def temporal_preflight(docker, images, output, owned):
    # Port collisions fail without discovering or altering the existing service.
    port = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    try:
        port.bind(('127.0.0.1', 17233))
    except OSError as error:
        raise Failure('TEMPORAL_PORT_CONFLICT') from error
    finally:
        port.close()
    owned.update(image=images['temporal'], label=str(uuid.uuid4()))
    name = 'cdo-host-certification-' + owned['label']
    save(output / 'temporal-owner.json', {**owned, 'name': name, 'status': 'BEFORE_CREATE'})
    result = docker.call(['create', '--name', name, '--label', 'cdo.host-certification=' + owned['label'],
        '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges=true', '--pids-limit', '128',
        '--memory', '512m', '--memory-swap', '512m', '--cpus', '2', '--publish', '127.0.0.1:17233:7233',
        '--tmpfs', '/tmp:rw,nosuid,nodev,size=268435456', '--env', 'HOME=/tmp', '--entrypoint', 'temporal',
        owned['image'], 'server', 'start-dev', '--ip', '0.0.0.0', '--port', '7233', '--headless'])
    identity = result.stdout.strip()
    if not re.fullmatch(r'[a-f0-9]{64}', identity):
        raise Failure('TEMPORAL_CREATE_ID_UNKNOWN', unknown=True)
    owned['id'] = identity
    save(output / 'temporal-owner.json', {**owned, 'name': name, 'status': 'REGISTERED'})
    inspect_owned(docker, owned)
    docker.call(['start', identity])
    probe = output / 'temporal-capability-probe'
    run(['go', 'build', '-o', str(probe), str(ROOT / 'scripts/runtime-host-temporal-preflight.go')], env=go_env(), timeout=180, log=output / 'temporal-probe-build.json')
    run([str(probe), '--output', str(output / 'temporal-capability.json')], timeout=45, log=output / 'temporal-probe-run.json')
    evidence = read_json(output / 'temporal-capability.json')
    if evidence.get('pass') is not True or not evidence.get('workflow_id') or not evidence.get('run_id'):
        raise Failure('TEMPORAL_CAPABILITY_FAILED')
    return evidence


def build_release(docker, args, output):
    # The root-owned builder receives the exact observed executable, endpoint
    # and daemon, and reserves its immutable image identity with --iidfile.
    tag = 'cdo-host-certification:' + output.name.lower()
    if not re.fullmatch(r'[a-z0-9][a-z0-9_.-]{0,100}', output.name.lower()):
        tag = 'cdo-host-certification:' + uuid.uuid4().hex
    token = str(uuid.uuid4())
    try:
        run([str(Path(sys.executable).resolve()), str(ROOT / 'scripts/build-production-release.py'),
             '--output', str(output / 'release'), '--tag', tag,
             '--docker-executable', docker.executable, '--docker-endpoint', docker.endpoint,
             '--daemon-id', docker.daemon, '--failure-token', token],
            timeout=1800, log=output / 'release-build-command.json')
    except Exception as error:
        # Only a fresh trusted-builder receipt proving NO Docker invocation
        # permits normal exact-owned cleanup after a child source failure.
        try:
            receipt = read_signal_evidence(output / 'release/build-failure.json')[0]
            local = (set(receipt) == {'version', 'token', 'classification', 'phase', 'docker_calls_attempted', 'error_type'}
                     and type(receipt['version']) is int and receipt['version'] == 1 and receipt['token'] == token
                     and receipt['classification'] == 'LOCAL_BUILD_FAILURE'
                     and receipt['phase'] in ('source-capture', 'source-context')
                     and receipt['docker_calls_attempted'] is False
                     and isinstance(receipt['error_type'], str) and len(receipt['error_type']) <= 80)
        except Exception:
            local = False
        if local:
            raise Failure('RELEASE_LOCAL_BUILD_FAILED', detail=receipt) from error
        raise Failure('CHILD_RELEASE_RUNTIME_UNKNOWN', unknown=True) from error
    release = read_json(output / 'release/image-identity.json')
    if not IMAGE.fullmatch(release.get('image_id', '')) or not re.fullmatch(r'[a-f0-9]{64}', release.get('source_manifest_sha256', '')):
        raise Failure('RELEASE_BUILD_IDENTITY_UNKNOWN', unknown=True)
    values = json.loads(docker.call(['image', 'inspect', release['image_id']]).stdout)
    architecture = values[0].get('Architecture') if len(values) == 1 else ''
    if architecture not in ('arm64', 'amd64'):
        raise Failure('RELEASE_ARCHITECTURE_DENIED')
    driver = output / 'transport-temporal-probe-linux'
    run(['go', 'build', '-o', str(driver), str(output / 'release/source/scripts/transport-temporal-probe.go')],
        env={**go_env(), 'GOOS': 'linux', 'GOARCH': architecture, 'CGO_ENABLED': '0'}, timeout=180, log=output / 'transport-driver-build.json', cwd=output / 'release/source')
    return {**release, 'driver': str(driver)}


def complete_runtime(docker, args, output, release):
    run([str(Path(sys.executable).resolve()), str(ROOT / 'scripts/probe-complete-runtime-transport.py'),
        '--case', 'all', '--output', str(output / 'complete-runtime'), '--runner-image', release['image_id'],
        '--sdk-image', args.sdk_image, '--command-image', args.command_image,
        '--source-manifest', release['source_manifest_sha256'], '--go-bundle', args.go_bundle,
        '--go-bundle-manifest-sha256', args.go_bundle_manifest_sha256, '--driver', release['driver'], '--docker-executable', docker.executable,
        '--docker-endpoint', docker.endpoint, '--daemon-id', docker.daemon], timeout=7200, log=output / 'complete-runtime-command.json')
    evidence = read_json(output / 'complete-runtime/complete-transport-evidence.json')
    if evidence.get('pass') is not True or evidence.get('full_certification_claimed') is not True:
        raise Failure('COMPLETE_RUNTIME_NOT_CERTIFIED')
    return {'evidence_sha256': hashlib.sha256((output / 'complete-runtime/complete-transport-evidence.json').read_bytes()).hexdigest()}


def verify_current_source(manifest_file):
    # Reuse the root-owned import-safe exact source inventory rules.
    spec = importlib.util.spec_from_file_location('host_release_builder', ROOT / 'scripts/build-production-release.py')
    builder = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(builder)
    manifest = read_json(manifest_file)
    if manifest != builder.inventory(ROOT):
        raise Failure('CURRENT_SOURCE_SNAPSHOT_CHANGED')
    return {'source_manifest_sha256': hashlib.sha256(manifest_file.read_bytes()).hexdigest(), 'exact_current_source': True}


def execute(args):
    validate_inputs(args)
    output = Path(args.output)
    output.mkdir(mode=0o700, parents=False, exist_ok=False)
    result = {**plan(), 'status': 'RUNNING', 'stages': {name: {'status': 'NOT_RUN'} for name in STAGES}, 'attempt_id': str(uuid.uuid4()),
              'inputs': {'go_bundle': args.go_bundle, 'go_bundle_manifest_sha256': args.go_bundle_manifest_sha256,
                         'command_image': args.command_image, 'sdk_image': args.sdk_image, 'temporal_image_reference': args.temporal_image}}
    docker, owned = None, {}
    failed = False
    current = None
    child_cleanup_unknown = False
    def stage(name, call):
        nonlocal current
        current = name
        result['stages'][name] = {'status': 'RUNNING'}
        save(output / 'runtime-host-result.json', result)
        detail = call()
        result['stages'][name] = {'status': 'PASS', 'evidence': detail}
        save(output / 'runtime-host-result.json', result)
        return detail
    try:
        stage('host-observation', observe_host)
        stage('owned-group-signals', lambda: signal_preflight(output))
        stage('unix-loopback-bind', lambda: bind_preflight(output))
        def establish_docker():
            nonlocal docker
            docker, images = docker_preflight(args, output)
            return images
        images = stage('docker-identity', establish_docker)
        stage('temporal-cancellation-history', lambda: temporal_preflight(docker, images, output, owned))
        stage('runner-build', lambda: {'exit_code': run(['npm', '--prefix', str(ROOT / 'runner'), 'run', 'build'], timeout=180, log=output / 'runner-build.json').returncode})
        release = stage('build-release', lambda: build_release(docker, args, output))
        validate_inputs(args)
        stage('source-before-runtime', lambda: verify_current_source(output / 'release/source-manifest.json'))
        stage('complete-runtime-all', lambda: complete_runtime(docker, args, output, release))
        for name, command in (('sandbox-test', ['make', 'sandbox-test']), ('verify', ['make', 'verify']),
                              ('go-vet', ['go', 'vet', './...']), ('go-test', ['go', 'test', './...']), ('diff-check', ['make', 'diff-check'])):
            stage(name, lambda name=name, command=command: {'exit_code': run(command, env=go_env(), timeout=1800, log=output / (name + '.json')).returncode})
        validate_inputs(args)
        stage('source-after-checks', lambda: verify_current_source(output / 'release/source-manifest.json'))
        result['status'] = 'PASS'
    except Exception as error:
        failed = True
        # Child runtime uncertainty and observed Docker denial are distinct.
        # Local source failure is typed by the trusted builder before Docker.
        local_build_failure = (isinstance(error, Failure) and str(error) == 'RELEASE_LOCAL_BUILD_FAILED'
                               and error.unknown is False and isinstance(error.detail, dict)
                               and error.detail.get('classification') == 'LOCAL_BUILD_FAILURE'
                               and error.detail.get('docker_calls_attempted') is False
                               and error.detail.get('phase') in ('source-capture', 'source-context'))
        if (current == 'complete-runtime-all' or
                (current == 'build-release' and not local_build_failure)) and docker is not None:
            child_cleanup_unknown = True
            error = Failure('CHILD_RUNTIME_STAGE_UNKNOWN', unknown=True, detail={'stage': current, 'cause': str(error)[:200]})
        result['status'] = 'UNKNOWN' if getattr(error, 'unknown', False) else 'FAIL'
        result['error'] = {'type': type(error).__name__, 'code': str(error)[:200], 'detail': getattr(error, 'detail', None)}
        if current:
            result['stages'][current] = {'status': result['status'], 'error': result['error']}
    finally:
        if docker is not None and getattr(docker, 'denied', False) and owned:
            failed = True
            result['status'] = 'UNKNOWN'
            result['stages']['owned-temporal-cleanup'] = {'status': 'UNKNOWN', 'code': 'DOCKER_DENIED_NO_RETRY', 'ownership_retained': True}
        elif child_cleanup_unknown and owned:
            failed = True
            result['status'] = 'UNKNOWN'
            result['stages']['owned-temporal-cleanup'] = {'status': 'UNKNOWN', 'code': 'CHILD_RUNTIME_UNKNOWN_NO_RETRY', 'ownership_retained': True}
        elif docker is not None and owned.get('id'):
            try:
                stage('owned-temporal-cleanup', lambda: cleanup_temporal(docker, owned))
                save(output / 'temporal-owner.json', {**owned, 'status': 'REMOVED'})
            except Exception as error:
                failed = True
                result['status'] = 'UNKNOWN'
                result['stages']['owned-temporal-cleanup'] = {'status': 'UNKNOWN', 'code': str(error)[:200]}
        elif owned:
            failed = True
            result['status'] = 'UNKNOWN'
            result['stages']['owned-temporal-cleanup'] = {'status': 'UNKNOWN', 'code': 'CREATE_ID_UNRESOLVED'}
        else:
            result['stages']['owned-temporal-cleanup'] = {'status': 'NOT_CREATED'}
        result['pass'] = not failed and result['status'] == 'PASS' and all(item['status'] == 'PASS' for item in result['stages'].values())
        save(output / 'runtime-host-result.json', result)
    return 0 if result['pass'] else 1


def main(argv=None):
    args = parser().parse_args(argv)
    if args.observe_only:
        return observe_only()
    if args.reconcile_temporal_only or args.observe_temporal_absence_only:
        try:
            return reconcile_temporal_only(args)
        except Exception as error:
            print(json.dumps({'status': 'UNKNOWN', 'scope': 'EXACT_TEMPORAL_RECONCILIATION_ONLY',
                              'code': str(error)[:160] if isinstance(error, Failure) else 'RECONCILIATION_INPUT_OR_RECEIPT_FAILED',
                              'production_admission': 'DENIED', 'real_model_calls': 0}), file=sys.stderr)
            return 1
    if args.owned_signal_only:
        try:
            return owned_signal_only(args)
        except Exception as error:
            print(json.dumps({'status': 'UNKNOWN', 'pass': False, 'scope': 'OWNED_SIGNAL_DIAGNOSIS_ONLY',
                              'error': signal_error_detail(error, 'signal-only-inputs'),
                              'real_model_calls': 0, 'production_admission': 'DENIED'}), file=sys.stderr)
            return 1
    if not args.execute:
        print(json.dumps(plan(), sort_keys=True))
        return 0
    try:
        return execute(args)
    except Exception as error:
        print(json.dumps({'status': 'FAIL', 'pass': False, 'code': str(error)[:200], 'real_model_calls': 0,
                          'production_admission': 'DENIED'}), file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
