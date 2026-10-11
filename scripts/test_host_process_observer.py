"""Deterministic observer regressions; never execute ps, signals or sockets."""
import importlib.util
from contextlib import contextmanager
import io
import json
from pathlib import Path
from types import SimpleNamespace
import sys
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'runner/bin'))
import cdo_runtime_coordinator as c


def host_module():
    spec = importlib.util.spec_from_file_location('observer_host', ROOT / 'scripts/certify-runtime-host.py')
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


class ProcessObserverTests(unittest.TestCase):
    anchors = b'111 1 111 501 S\n222 111 111 501 R\n'

    def test_setuid_ps_uses_observer_euid_without_changing_caller_uid(self):
        m = host_module()
        snapshot = b'111 1 111 501 S\n222 111 111 0 R\n'
        with mock.patch.object(m, 'coordinator', return_value=c), self.ps_output(snapshot), mock.patch.object(c, 'observer_executable_identity', return_value={'expected_uid':0, 'setuid':True, 'file_uid':0, 'fingerprint':('trusted-ps',)}, create=True):
            result = m.observe_host()
        self.assertTrue(result['caller_observed'])

    def test_missing_and_uid_mismatch_have_distinct_reasons(self):
        cases = [(b'111 1 111 501 S\n', 'OBSERVER_ANCHOR_MISSING'),
                 (b'222 111 111 501 R\n', 'CALLER_ANCHOR_MISSING'),
                 (b'111 1 111 501 S\n222 111 111 0 R\n', 'OBSERVER_UID_MISMATCH'),
                 (b'111 1 111 0 S\n222 111 111 501 R\n', 'CALLER_UID_MISMATCH')]
        for snapshot, reason in cases:
            with self.subTest(reason=reason), self.assertRaises(c.CoordinatorDenied) as raised:
                c.parse_process_snapshot(snapshot, 222, 111, 501)
            self.assertEqual(raised.exception.detail['reason'], reason)

    def test_observer_parent_must_be_the_caller(self):
        snapshot = b'111 1 111 501 S\n222 999 111 501 R\n'
        with self.assertRaises(c.CoordinatorDenied) as raised:
            c.parse_process_snapshot(snapshot, 222, 111, 501)
        self.assertEqual(raised.exception.detail['reason'], 'PARENT_IDENTITY_MISMATCH')

    def test_setuid_identity_comes_only_from_trusted_fixed_executable(self):
        def file_info(mode=0o4755, uid=0):
            return SimpleNamespace(st_mode=c.stat.S_IFREG|mode, st_uid=uid, st_gid=0,
                   st_dev=1, st_ino=2, st_size=32, st_mtime_ns=3, st_ctime_ns=4)
        directory=SimpleNamespace(st_mode=c.stat.S_IFDIR|0o755, st_uid=0)
        for mode, expected in [(0o4755,0),(0o755,501)]:
            with self.subTest(mode=mode), mock.patch.object(c.pathlib.Path, 'lstat', return_value=file_info(mode)), mock.patch.object(c.pathlib.Path, 'resolve', return_value=Path('/bin/ps')), mock.patch.object(c.pathlib.Path, 'stat', return_value=directory):
                self.assertEqual(c.observer_executable_identity(501)['expected_uid'], expected)
        for info, parent in [(file_info(0o4777), directory), (file_info(uid=501), directory),
                             (file_info(), SimpleNamespace(st_mode=c.stat.S_IFDIR|0o777,st_uid=0)),
                             (SimpleNamespace(st_mode=c.stat.S_IFLNK|0o777,st_uid=0), directory)]:
            with self.subTest(info=info), mock.patch.object(c.pathlib.Path, 'lstat', return_value=info), mock.patch.object(c.pathlib.Path, 'resolve', return_value=Path('/bin/ps')), mock.patch.object(c.pathlib.Path, 'stat', return_value=parent), self.assertRaises(c.CoordinatorDenied) as raised:
                c.observer_executable_identity(501)
            self.assertEqual(raised.exception.detail['reason'], 'OBSERVER_EXECUTABLE_UNTRUSTED')

    def test_setuid_observer_cannot_fallback_to_caller_uid(self):
        identity={'expected_uid':0,'setuid':True,'file_uid':0,'fingerprint':('trusted-ps',)}
        with self.ps_output(self.anchors), mock.patch.object(c, 'observer_executable_identity', return_value=identity), self.assertRaises(c.CoordinatorDenied) as raised:
            c.group_members(None)
        self.assertEqual(raised.exception.detail['reason'], 'OBSERVER_UID_MISMATCH')
        self.assertTrue(raised.exception.detail['anchors']['caller']['uid_matches'])
        self.assertTrue(raised.exception.detail['anchors']['observer']['present'])
        self.assertFalse(raised.exception.detail['anchors']['observer']['uid_matches'])

    def test_missing_observer_is_unknown_even_for_setuid_executable(self):
        identity={'expected_uid':0,'setuid':True,'file_uid':0,'fingerprint':('trusted-ps',)}
        with self.ps_output(self.anchors.splitlines()[0]+b'\n'), mock.patch.object(c, 'observer_executable_identity', return_value=identity), self.assertRaises(c.CoordinatorDenied) as raised:
            c.group_members(None)
        self.assertEqual(raised.exception.detail['reason'], 'OBSERVER_ANCHOR_MISSING')
        self.assertFalse(raised.exception.detail['anchors']['observer']['present'])

    def test_caller_parent_and_uid_must_remain_stable(self):
        for field, sequence, reason in [('getppid',[1,2],'PARENT_IDENTITY_MISMATCH'),
                                        ('geteuid',[501,0],'CALLER_UID_MISMATCH')]:
            with self.subTest(field=field), self.ps_output(self.anchors), mock.patch.object(c.os, field, side_effect=sequence), self.assertRaises(c.CoordinatorDenied) as raised:
                c.group_members(None)
            self.assertEqual(raised.exception.detail['reason'], reason)
            self.assertEqual(raised.exception.detail['affected_anchor'], 'caller')

    def test_caller_snapshot_parent_mismatch_is_unknown(self):
        with self.assertRaises(c.CoordinatorDenied) as raised:
            c.parse_process_snapshot(self.anchors,222,111,501,caller_parent_pid=999)
        self.assertEqual(raised.exception.detail['reason'],'PARENT_IDENTITY_MISMATCH')
        self.assertEqual(raised.exception.detail['affected_anchor'],'caller')

    def test_executable_identity_change_and_denial_prevent_anchor_pass(self):
        first={'expected_uid':501,'setuid':False,'file_uid':0,'fingerprint':('trusted-ps',)}
        other={**first,'fingerprint':('changed-ps',)}
        with self.ps_output(self.anchors), mock.patch.object(c,'observer_executable_identity',side_effect=[first,other]), self.assertRaises(c.CoordinatorDenied) as raised:
            c.group_members(None)
        self.assertEqual(raised.exception.detail['reason'],'OBSERVER_EXECUTABLE_IDENTITY_CHANGED')
        with self.ps_output(self.anchors) as (_, spawn), mock.patch.object(c,'observer_executable_identity',side_effect=PermissionError(1,'denied')), self.assertRaises(c.CoordinatorDenied) as raised:
            c.group_members(None)
        spawn.assert_not_called()
        self.assertEqual(raised.exception.detail['observer_errno'],1)

    def test_diagnostics_report_both_anchors_without_raw_pid_records(self):
        with self.assertRaises(c.CoordinatorDenied) as raised:
            c.parse_process_snapshot(b'111 1 111 501 S\n222 111 111 0 R\n',222,111,501)
        detail=raised.exception.detail
        self.assertEqual(detail['reason'],'OBSERVER_UID_MISMATCH')
        self.assertTrue(detail['anchors']['caller']['present'])
        self.assertTrue(detail['anchors']['caller']['uid_matches'])
        self.assertTrue(detail['anchors']['observer']['parent_matches'])
        serialized=json.dumps(detail)
        for hidden in ('111','222','501','stdout','stderr','ppid','pgid'):
            self.assertNotIn(hidden,serialized)

    @contextmanager
    def ps_output(self, raw, *, code=0, stderr=b'', spawn_error=None, wait_error=None):
        process = mock.Mock(pid=222)
        process.stdout.fileno.return_value = 101
        process.stderr.fileno.return_value = 102
        process.wait.return_value = code
        process.wait.side_effect = wait_error
        process.poll.return_value = code
        streams = {101: [raw, b''], 102: [stderr, b'']}
        class Selector:
            def __init__(self): self.keys = {}
            def register(self, fileobj, events, name):
                self.keys[fileobj] = SimpleNamespace(fileobj=fileobj, data=name)
            def get_map(self): return self.keys
            def select(self, timeout): return [(key, 1) for key in list(self.keys.values())]
            def unregister(self, fileobj): del self.keys[fileobj]
            def close(self): pass
        with mock.patch.object(c.sys, 'platform', 'darwin'), mock.patch.object(c.os, 'getpid', return_value=111), mock.patch.object(c.os, 'geteuid', return_value=501), mock.patch.object(c.os, 'getppid', return_value=1), mock.patch.object(c, 'observer_executable_identity', return_value={'expected_uid':501,'setuid':False,'file_uid':0,'fingerprint':('trusted-ps',)}), mock.patch.object(c.subprocess, 'Popen', return_value=process, side_effect=spawn_error) as spawn, mock.patch.object(c.selectors, 'DefaultSelector', Selector), mock.patch.object(c.os, 'read', side_effect=lambda fd, size: streams[fd].pop(0)):
            yield process, spawn

    def test_darwin_kernel_record_does_not_invalidate_complete_snapshot(self):
        with mock.patch.object(c.sys, 'platform', 'darwin'):
            rows = c.parse_process_snapshot(b'0 0 0 0 R\n' + self.anchors, 222, 111, 501)
        self.assertEqual(len(rows), 3)
        self.assertEqual(rows[0], {'pid': 0, 'ppid': 0, 'pgid': 0, 'uid': 0, 'state': 'R'})

    def test_successful_ps_with_darwin_kernel_record_passes_host_observation(self):
        m = host_module()
        with mock.patch.object(m, 'coordinator', return_value=c), self.ps_output(b'0 0 0 0 R\n' + self.anchors) as (process, spawn):
            result = m.observe_host()
        self.assertTrue(result['caller_observed'])
        self.assertEqual(result['zero_pid_records'], 1)
        self.assertTrue(result['legacy_positive_ids_rule_would_reject'])
        self.assertEqual(spawn.call_args.args[0], ['/bin/ps', '-axo', 'pid=,ppid=,pgid=,uid=,stat='])
        self.assertNotIn('env', spawn.call_args.kwargs)
        process.kill.assert_not_called()

    def test_detail_none_has_concrete_fallback(self):
        m = host_module()
        observer = mock.Mock()
        observer.group_members.side_effect = c.CoordinatorDenied('COORDINATOR_GROUP_OBSERVATION_UNKNOWN')
        with mock.patch.object(m, 'coordinator', return_value=observer), self.assertRaises(m.Failure) as raised:
            m.observe_host()
        self.assertIsInstance(raised.exception.detail, dict)
        self.assertEqual(raised.exception.detail['reason'], 'OBSERVER_FAILURE_WITHOUT_DETAIL')
        self.assertEqual(raised.exception.detail['observer_code'], 'COORDINATOR_GROUP_OBSERVATION_UNKNOWN')

    def test_successful_ps_parse_failure_keeps_exit_zero_and_specific_reason(self):
        m = host_module()
        with mock.patch.object(m, 'coordinator', return_value=c), self.ps_output(self.anchors + b'not a complete row\n'), self.assertRaises(m.Failure) as raised:
            m.observe_host()
        detail = raised.exception.detail
        self.assertEqual(detail['reason'], 'RECORD_FIELD_COUNT_INVALID')
        self.assertEqual(detail['observer_exit_code'], 0)
        self.assertEqual(detail['stderr_bytes'], 0)
        self.assertEqual(detail['record_index'], 3)
        self.assertNotIn('not a complete row', json.dumps(detail))

    def test_darwin_zero_group_observation_is_not_owned_selector_authority(self):
        with mock.patch.object(c.sys, 'platform', 'darwin'):
            rows = c.parse_process_snapshot(self.anchors + b'333 1 0 0 S\n', 222, 111, 501)
        self.assertEqual(rows[-1]['pgid'], 0)
        with mock.patch.object(c.subprocess, 'Popen', side_effect=AssertionError('spawn')), self.assertRaises(c.CoordinatorDenied) as raised:
            c.group_members(0)
        self.assertEqual(raised.exception.detail['reason'], 'OWNED_GROUP_SELECTOR_INVALID')

    def test_invalid_darwin_kernel_identities_remain_unknown(self):
        for row in (b'0 1 0 0 R\n', b'0 0 1 0 R\n', b'0 0 0 501 R\n'):
            with self.subTest(row=row), mock.patch.object(c.sys, 'platform', 'darwin'), self.assertRaises(c.CoordinatorDenied) as raised:
                c.parse_process_snapshot(row + self.anchors, 222, 111, 501)
            self.assertEqual(raised.exception.detail['reason'], 'RECORD_IDENTITY_RANGE_INVALID')

    def test_zero_ids_remain_invalid_on_non_darwin(self):
        for row in (b'0 0 0 0 R\n', b'333 1 0 0 S\n'):
            with self.subTest(row=row), mock.patch.object(c.sys, 'platform', 'linux'), self.assertRaises(c.CoordinatorDenied):
                c.parse_process_snapshot(row + self.anchors, 222, 111, 501)

    def test_malformed_snapshots_have_reason_without_process_rows(self):
        cases = [(b'', 'SNAPSHOT_EMPTY'), (b'\xff', 'SNAPSHOT_NON_ASCII'),
                 (self.anchors + b'333 1 333 0 S EXTRA\n', 'RECORD_FIELD_COUNT_INVALID'),
                 (self.anchors + b'333 x 333 0 S\n', 'RECORD_NUMERIC_FIELDS_INVALID'),
                 (self.anchors + b'333 -1 333 0 S\n', 'RECORD_NUMERIC_FIELDS_INVALID'),
                 (self.anchors + b'333 1 333 0 S!\n', 'RECORD_STATE_INVALID'),
                 (self.anchors + self.anchors[:self.anchors.index(b'\n')+1], 'RECORD_DUPLICATE_PID'),
                 (self.anchors.splitlines()[0]+b'\n', 'OBSERVER_ANCHOR_MISSING'),
                 (self.anchors.splitlines()[1]+b'\n', 'CALLER_ANCHOR_MISSING')]
        for raw, reason in cases:
            with self.subTest(reason=reason), self.assertRaises(c.CoordinatorDenied) as raised:
                c.parse_process_snapshot(raw, 222, 111, 501)
            self.assertEqual(raised.exception.detail['reason'], reason)
            self.assertFalse({'stdout', 'stderr', 'pid', 'ppid', 'pgid', 'uid'} & raised.exception.detail.keys())

    def test_zero_anchor_cannot_be_satisfied_by_kernel_record(self):
        with self.assertRaises(c.CoordinatorDenied) as raised:
            c.parse_process_snapshot(b'0 0 0 0 R\n' + self.anchors, 0, 111, 0)
        self.assertEqual(raised.exception.detail['reason'], 'ANCHOR_IDENTITY_INVALID')

    def test_spawn_eperm_is_unknown_and_has_errno_without_retry(self):
        with self.ps_output(self.anchors, spawn_error=PermissionError(1, 'denied')) as (_, spawn), self.assertRaises(c.CoordinatorDenied) as raised:
            c.group_members(None)
        self.assertEqual(raised.exception.detail['reason'], 'OBSERVER_IO_ERROR')
        self.assertEqual(raised.exception.detail['observer_errno'], 1)
        self.assertEqual(raised.exception.detail['observer_operation'], 'spawn')
        self.assertEqual(spawn.call_count, 1)

    def test_nonzero_exit_and_stderr_fail_closed_without_raw_output(self):
        for code, stderr, reason in [(1, b'private observer message', 'OBSERVER_NONZERO_EXIT'), (0, b'private observer message', 'OBSERVER_STDERR_NOT_EMPTY')]:
            with self.subTest(reason=reason), self.ps_output(self.anchors, code=code, stderr=stderr), self.assertRaises(c.CoordinatorDenied) as raised:
                c.group_members(None)
            self.assertEqual(raised.exception.detail['reason'], reason)
            self.assertEqual(raised.exception.detail['observer_exit_code'], code)
            self.assertNotIn('private observer message', json.dumps(raised.exception.detail))

    def test_output_quota_and_timeout_stay_unknown(self):
        with self.ps_output(b'x'*131073), self.assertRaises(c.CoordinatorDenied) as raised:
            c.group_members(None)
        self.assertEqual(raised.exception.detail['reason'], 'OBSERVER_OUTPUT_LIMIT')
        with self.ps_output(self.anchors, wait_error=c.subprocess.TimeoutExpired('/bin/ps', 1)), self.assertRaises(c.CoordinatorDenied) as raised:
            c.group_members(None)
        self.assertEqual(raised.exception.detail['reason'], 'OBSERVER_TIMEOUT')

    def test_caller_check_has_reason_without_existing_owned_child(self):
        m = host_module()
        observer = mock.Mock()
        observer.group_members.return_value = []
        with mock.patch.object(m, 'coordinator', return_value=observer), self.assertRaises(m.Failure) as raised:
            m.observe_host()
        self.assertEqual(raised.exception.detail['reason'], 'CALLER_ANCHOR_MISSING')
        observer.group_members.assert_called_once_with(None)

    def test_observe_only_is_read_only_and_never_enters_certification(self):
        m = host_module()
        output = io.StringIO()
        initial_bytecode = sys.dont_write_bytecode
        try:
            with mock.patch.object(m, 'observe_host', return_value={'caller_observed': True}), mock.patch.object(m, 'execute', side_effect=AssertionError('certification')), mock.patch.object(m, 'save', side_effect=AssertionError('write')), mock.patch.object(m, 'validate_inputs', side_effect=AssertionError('certification inputs')), mock.patch('sys.stdout', output):
                self.assertEqual(m.main(['--observe-only']), 0)
            result = json.loads(output.getvalue())
            self.assertTrue(result['read_only'])
            self.assertTrue(sys.dont_write_bytecode)
            self.assertEqual(result['production_admission'], 'DENIED')
        finally:
            sys.dont_write_bytecode = initial_bytecode

    def test_observe_only_prints_specific_unknown_and_never_enters_certification(self):
        m = host_module()
        output = io.StringIO()
        initial_bytecode = sys.dont_write_bytecode
        try:
            with mock.patch.object(m, 'observe_host', side_effect=m.Failure('PROCESS_OBSERVATION_UNKNOWN', unknown=True, detail={'reason':'SNAPSHOT_EMPTY'})), mock.patch.object(m, 'execute', side_effect=AssertionError('certification')), mock.patch('sys.stdout', output):
                self.assertEqual(m.main(['--observe-only']), 1)
            self.assertEqual(json.loads(output.getvalue())['error']['detail']['reason'], 'SNAPSHOT_EMPTY')
        finally:
            sys.dont_write_bytecode = initial_bytecode


if __name__ == '__main__':
    unittest.main()
