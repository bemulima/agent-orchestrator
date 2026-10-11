"""Exact cleanup tests; every Docker/context operation is mocked."""
import contextlib
import hashlib
import importlib.util
import io
import json
import pathlib
import subprocess
import tempfile
import unittest
from unittest import mock

SOURCE = pathlib.Path(__file__).with_name('certify-runtime-host.py')


def module():
    spec = importlib.util.spec_from_file_location('host_cleanup', SOURCE)
    value = importlib.util.module_from_spec(spec); spec.loader.exec_module(value)
    return value


class ExactTemporalReconciliationTests(unittest.TestCase):
    def fixture(self, base):
        m = module(); root = pathlib.Path(base).resolve() / 'repo'
        parent = root / '.cache/sandbox-certification'; parent.mkdir(parents=True)
        old = parent / 'old'; old.mkdir(); output = parent / 'new'
        executable = root / 'docker'; executable.touch()
        socket = root / 'docker.sock'; socket.touch()
        label = 'e58e96da-d8c4-460b-b0a3-489d3687379c'
        owned = {'id': 'a'*64, 'image': 'sha256:'+'b'*64, 'label': label,
                 'name': 'cdo-host-certification-'+label, 'status': 'REGISTERED'}
        identity = {'executable': str(executable), 'endpoint': 'unix://'+str(socket), 'daemon_id': 'daemon'}
        attempt = {'attempt_id': 'd92da144-20af-4a7f-b02f-d175383e526a',
                   'stages': {'owned-temporal-cleanup': {'status': 'UNKNOWN'}}}
        for name, data in (('runtime-host-result.json', attempt), ('temporal-owner.json', owned), ('docker-identity.json', identity)):
            m.save(old / name, data)
        argv = ['--reconcile-temporal-only', '--output', str(output), '--reconcile-attempt', str(old),
                '--reconcile-attempt-id', attempt['attempt_id'], '--approved-container-id', owned['id'],
                '--approved-container-image', owned['image'], '--approved-container-label', owned['label'],
                '--approved-docker-identity-sha256', hashlib.sha256((old / 'docker-identity.json').read_bytes()).hexdigest()]
        return m, root, old, output, owned, identity, m.parser().parse_args(argv)

    def invoke(self, case='valid', approved_mismatch=None, identity_tamper=None, observation_only=False):
        with tempfile.TemporaryDirectory() as base:
            m, root, old, output, owned, identity, args = self.fixture(base)
            if observation_only:
                args.reconcile_temporal_only = False
                args.observe_temporal_absence_only = True
            if identity_tamper:
                altered = {**identity, identity_tamper: '/unapproved' if identity_tamper == 'executable' else 'unapproved'}
                m.save(old / 'docker-identity.json', altered)
            original = {p.name: p.read_bytes() for p in old.iterdir()}
            if approved_mismatch: setattr(args, approved_mismatch, 'c'*64 if approved_mismatch in ('approved_container_id', 'approved_docker_identity_sha256') else 'different-attempt')
            calls = []; removed = False; inspections = 0
            def docker(argv, **kwargs):
                nonlocal removed, inspections
                calls.append(argv)
                self.assertFalse(kwargs.get('shell', False))
                if argv[1:] == ['context', 'show']: return subprocess.CompletedProcess(argv, 0, 'desktop-linux', '')
                if argv[1:3] == ['context', 'inspect']: return subprocess.CompletedProcess(argv, 0, identity['endpoint'], '')
                self.assertEqual(argv[1:3], ['--host', identity['endpoint']])
                operation = argv[3:]
                if operation[0] == 'info':
                    if case == 'permission' or (case == 'absent-post-denied' and inspections): raise PermissionError(1, 'denied')
                    changed = case == 'daemon' or (case == 'absent-post-daemon' and inspections)
                    return subprocess.CompletedProcess(argv, 0, 'replacement' if changed else 'daemon', '')
                if operation[0] == 'inspect':
                    inspections += 1
                    self.assertEqual(operation, ['inspect', owned['id']])
                    if case.startswith('absent') or (removed and case != 'late-present'):
                        prefix = 'error: no such object: ' if case in ('valid-lowercase', 'absent-lowercase') else 'Error: No such object: '
                        return subprocess.CompletedProcess(argv, 1, '[]\n', prefix+owned['id']+'\n')
                    if case == 'inspect-denied': return subprocess.CompletedProcess(argv, 1, '', 'permission denied')
                    value = {'Id': owned['id'], 'Image': owned['image'], 'Name': '/'+owned['name'],
                             'Config': {'Labels': {'cdo.host-certification': owned['label']}}}
                    if case == 'image': value['Image'] = 'sha256:'+'c'*64
                    if case == 'label': value['Config']['Labels']['cdo.host-certification'] = 'wrong'
                    if case == 'id': value['Id'] = 'c'*64
                    if case == 'name': value['Name'] = '/wrong'
                    return subprocess.CompletedProcess(argv, 0, json.dumps([value]), '')
                self.assertEqual(operation, ['rm', '-f', owned['id']])
                if case == 'remove-denied': return subprocess.CompletedProcess(argv, 1, '', 'permission denied')
                removed = True
                return subprocess.CompletedProcess(argv, 0, owned['id'], '')
            with mock.patch.object(m, 'ROOT', root), mock.patch.object(m.sys, 'platform', 'darwin'), mock.patch.object(m.subprocess, 'run', side_effect=docker), contextlib.redirect_stdout(io.StringIO()):
                code = m.reconcile_temporal_only(args)
            self.assertEqual({p.name: p.read_bytes() for p in old.iterdir()}, original)
            receipt = m.read_json(output / 'temporal-reconciliation-receipt.json')
            self.assertEqual((output / 'temporal-reconciliation-receipt.json').stat().st_mode & 0o777, 0o400)
            self.assertTrue(receipt['original_evidence_unchanged'])
            return code, receipt, calls, removed

    def test_exact_owned_cleanup_and_positive_absence(self):
        code, receipt, calls, removed = self.invoke()
        self.assertEqual(code, 0); self.assertEqual(receipt['status'], 'PASS')
        self.assertTrue(receipt['exact_resource_absent']); self.assertTrue(removed)
        self.assertFalse(receipt['ownership_retained'])
        remove = next(i for i, argv in enumerate(calls) if argv[3:5] == ['rm', '-f'])
        self.assertEqual(calls[remove-1][3:], ['info', '--format', '{{.ID}}'])
        self.assertEqual(calls[-1][3:], ['info', '--format', '{{.ID}}'])

    def test_already_absent_has_no_mutation(self):
        code, receipt, calls, removed = self.invoke('absent')
        self.assertEqual(code, 0); self.assertTrue(receipt['exact_resource_absent'])
        self.assertFalse(removed); self.assertFalse(any('rm' in argv for argv in calls))

    def test_recorded_lowercase_post_remove_absence_completes_with_daemon_check(self):
        code, receipt, calls, removed = self.invoke('valid-lowercase')
        self.assertEqual(code, 0); self.assertEqual(receipt['status'], 'PASS')
        self.assertTrue(removed); self.assertTrue(receipt['exact_resource_absent'])
        self.assertEqual(sum(argv[3:5] == ['rm', '-f'] for argv in calls), 1)
        self.assertEqual(calls[-1][3:], ['info', '--format', '{{.ID}}'])

    def test_absence_observation_passes_without_any_mutation(self):
        for case in ('absent', 'absent-lowercase'):
            with self.subTest(case=case):
                code, receipt, calls, removed = self.invoke(case, observation_only=True)
                self.assertEqual(code, 0); self.assertTrue(receipt['read_only'])
                self.assertEqual(receipt['scope'], 'EXACT_TEMPORAL_ABSENCE_OBSERVATION')
                self.assertTrue(receipt['exact_resource_absent'])
                self.assertEqual(receipt['cleanup']['mutations_attempted'], 0)
                self.assertFalse(removed); self.assertFalse(any('rm' in argv for argv in calls))
                self.assertEqual(calls[-1][3:], ['info', '--format', '{{.ID}}'])

    def test_absence_observation_never_removes_present_resource(self):
        code, receipt, calls, removed = self.invoke(observation_only=True)
        self.assertEqual(code, 1); self.assertEqual(receipt['error']['code'], 'TEMPORAL_ABSENCE_NOT_PROVEN')
        self.assertTrue(receipt['ownership_retained']); self.assertFalse(receipt['exact_resource_absent'])
        self.assertFalse(removed); self.assertFalse(any('rm' in argv for argv in calls))

    def test_absence_observation_requires_successful_post_observation_same_daemon(self):
        for case in ('absent-post-daemon', 'absent-post-denied', 'inspect-denied'):
            with self.subTest(case=case):
                code, receipt, calls, removed = self.invoke(case, observation_only=True)
                self.assertEqual(code, 1); self.assertEqual(receipt['status'], 'UNKNOWN')
                self.assertTrue(receipt['ownership_retained']); self.assertFalse(receipt['exact_resource_absent'])
                self.assertFalse(removed); self.assertFalse(any('rm' in argv for argv in calls))

    def test_read_only_cli_dispatch_never_enters_certification_or_cleanup(self):
        m = module()
        with mock.patch.object(m, 'reconcile_temporal_only', return_value=0) as reconcile, mock.patch.object(m, 'execute', side_effect=AssertionError('certification')), mock.patch.object(m, 'cleanup_temporal', side_effect=AssertionError('cleanup')):
            self.assertEqual(m.main(['--observe-temporal-absence-only']), 0)
            self.assertTrue(reconcile.call_args.args[0].observe_temporal_absence_only)
            self.assertFalse(reconcile.call_args.args[0].reconcile_temporal_only)

    def test_denial_and_wrong_daemon_are_unknown_without_mutation(self):
        for case in ('permission', 'daemon', 'inspect-denied'):
            with self.subTest(case=case):
                code, receipt, calls, removed = self.invoke(case)
                self.assertEqual(code, 1); self.assertEqual(receipt['status'], 'UNKNOWN')
                self.assertFalse(receipt['exact_resource_absent']); self.assertFalse(removed)
                self.assertFalse(any('rm' in argv for argv in calls))
                if case == 'permission': self.assertEqual(receipt['error']['os_errno'], 1)

    def test_owned_identity_mismatch_has_no_mutation(self):
        for case in ('id', 'image', 'label', 'name'):
            with self.subTest(case=case):
                code, receipt, calls, removed = self.invoke(case)
                self.assertEqual(code, 1); self.assertFalse(removed)
                self.assertEqual(receipt['error']['code'], 'TEMPORAL_OWNERSHIP_UNKNOWN')
                self.assertFalse(any('rm' in argv for argv in calls))

    def test_failed_remove_or_missing_absence_never_passes(self):
        for case in ('remove-denied', 'late-present'):
            with self.subTest(case=case):
                code, receipt, calls, removed = self.invoke(case)
                self.assertEqual(code, 1); self.assertFalse(receipt['exact_resource_absent'])
                self.assertTrue(receipt['ownership_retained'])
                self.assertEqual(sum(argv[3:5] == ['rm', '-f'] for argv in calls), 1)

    def test_approval_and_attempt_mismatch_stop_before_docker(self):
        for field in ('approved_container_id', 'reconcile_attempt_id', 'approved_docker_identity_sha256'):
            with self.subTest(field=field):
                code, receipt, calls, removed = self.invoke(approved_mismatch=field)
                self.assertEqual(code, 1); self.assertEqual(calls, [])
                self.assertFalse(removed)

    def test_tampered_runtime_identity_stops_before_any_subprocess(self):
        for field in ('executable', 'endpoint', 'daemon_id'):
            with self.subTest(field=field):
                code, receipt, calls, removed = self.invoke(identity_tamper=field)
                self.assertEqual(code, 1); self.assertEqual(calls, [])
                self.assertFalse(removed)
                self.assertEqual(receipt['error']['code'], 'APPROVED_DOCKER_IDENTITY_MISMATCH')

    def test_existing_receipt_directory_and_missing_approval_are_rejected(self):
        with tempfile.TemporaryDirectory() as base:
            m, root, old, output, owned, identity, args = self.fixture(base)
            output.mkdir()
            with mock.patch.object(m, 'ROOT', root), mock.patch.object(m.sys, 'platform', 'darwin'), mock.patch.object(m.subprocess, 'run') as run:
                with self.assertRaises(FileExistsError): m.reconcile_temporal_only(args)
                args.approved_container_id = None
                with self.assertRaises(m.Failure): m.reconcile_temporal_only(args)
                run.assert_not_called()


if __name__ == '__main__':
    unittest.main()
