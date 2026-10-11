"""Offline tests: all privileged/runtime operations are mocked."""
import importlib.util
import pathlib
import tempfile
import unittest
from unittest import mock

ROOT = pathlib.Path(__file__).resolve().parents[1]


def module():
    spec = importlib.util.spec_from_file_location('certify_runtime_host', ROOT / 'scripts/certify-runtime-host.py')
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


class CertificationTests(unittest.TestCase):
    def test_import_has_no_runtime_calls(self):
        with mock.patch('subprocess.run', side_effect=AssertionError('runtime')), mock.patch('subprocess.Popen', side_effect=AssertionError('runtime')):
            self.assertIsNotNone(module())

    def test_plan_has_no_runtime_and_denies_production(self):
        m = module()
        with mock.patch.object(m, 'run', side_effect=AssertionError('runtime')):
            result = m.plan()
        self.assertEqual(result['status'], 'NOT_RUN')
        self.assertEqual(result['production_admission'], 'DENIED')
        self.assertEqual(result['real_model_calls'], 0)
        self.assertIn('complete-runtime-all', result['stages'])
        self.assertFalse(result['actual_daemon_outage_claimed'])

    def test_observer_denial_stops_before_other_capabilities(self):
        m = module()
        with tempfile.TemporaryDirectory() as base:
            output = pathlib.Path(base) / 'new'
            args = m.parser().parse_args(['--execute', '--output', str(output), '--go-bundle', base,
                 '--command-image', 'sha256:' + 'a'*64, '--sdk-image', 'sha256:' + 'b'*64, '--temporal-image', 'temporal:test'])
            with mock.patch.object(m, 'validate_inputs'), mock.patch.object(m, 'observe_host', side_effect=m.Failure('PROCESS_OBSERVATION_UNKNOWN', unknown=True)), mock.patch.object(m, 'signal_preflight', side_effect=AssertionError('signal')), mock.patch.object(m, 'bind_preflight', side_effect=AssertionError('bind')), mock.patch.object(m, 'docker_preflight', side_effect=AssertionError('docker')):
                self.assertEqual(m.execute(args), 1)
            result = m.read_json(output / 'runtime-host-result.json')
            self.assertEqual(result['status'], 'UNKNOWN')
            self.assertEqual(result['stages']['build-release']['status'], 'NOT_RUN')
            self.assertFalse(result['pass'])

    def test_execute_requires_fresh_output(self):
        m = module()
        with tempfile.TemporaryDirectory() as base:
            args = m.parser().parse_args(['--execute', '--output', base, '--go-bundle', base,
                 '--command-image', 'sha256:' + 'a'*64, '--sdk-image', 'sha256:' + 'b'*64, '--temporal-image', 'temporal:test'])
            with mock.patch.object(m, 'validate_inputs'), mock.patch.object(m, 'observe_host', side_effect=AssertionError('runtime')):
                with self.assertRaises(FileExistsError):
                    m.execute(args)

    def test_temporal_cleanup_refuses_wrong_identity(self):
        m = module()
        owned = {'id': 'a'*64, 'image': 'sha256:'+'b'*64, 'label': 'owner'}
        wrong = [{'Id': 'a'*64, 'Image': 'sha256:'+'c'*64, 'Config': {'Labels': {'cdo.host-certification': 'owner'}}}]
        docker = mock.Mock()
        docker.call.return_value = mock.Mock(returncode=0, stdout=__import__('json').dumps(wrong), stderr='')
        with self.assertRaises(m.Failure):
            m.cleanup_temporal(docker, owned)
        self.assertEqual(docker.call.call_count, 1)

    def test_exact_absence_does_not_accept_daemon_error(self):
        m = module()
        self.assertFalse(m.absence(mock.Mock(returncode=1, stdout='', stderr='Cannot connect to Docker daemon'), 'a'*64))
        self.assertTrue(m.absence(mock.Mock(returncode=1, stdout='[]', stderr='Error: No such object: '+'a'*64), 'a'*64))

    def test_recorded_lowercase_exact_absence_is_recognized(self):
        m = module(); identity = 'a'*64
        # Actual host Docker CLI format after successful exact-ID removal.
        result = mock.Mock(returncode=1, stdout='[]\n', stderr='error: no such object: '+identity+'\n')
        self.assertTrue(m.absence(result, identity))

    def test_lowercase_absence_rejects_inexact_or_ambiguous_outputs(self):
        m = module(); identity = 'a'*64; message = 'error: no such object: '+identity
        for code, stdout, stderr in ((0, '[]', message), (2, '[]', message), (1, '{}', message),
                (1, '', 'error: no such object: '+'b'*64), (1, '', message[:-1]),
                (1, '', message+'\npermission denied'), (1, '', 'permission denied: '+identity),
                (1, '', 'cannot connect to daemon: '+identity)):
            with self.subTest(code=code, stdout=stdout, stderr=stderr):
                self.assertFalse(m.absence(mock.Mock(returncode=code, stdout=stdout, stderr=stderr), identity))

    def test_docker_adapter_does_not_latch_denial_for_exact_lowercase_absence(self):
        m = module(); identity = 'a'*64
        with tempfile.TemporaryDirectory() as base:
            docker = m.Docker('/fixed/docker', 'unix:///fixed/socket', 'daemon', pathlib.Path(base))
            missing = mock.Mock(returncode=1, stdout='[]\n', stderr='error: no such object: '+identity+'\n')
            with mock.patch.object(docker, '_invoke', side_effect=[mock.Mock(returncode=0, stdout='daemon', stderr=''), missing]):
                self.assertIs(docker.call(['inspect', identity], check=False), missing)
                self.assertFalse(getattr(docker, 'denied', False))

    def test_docker_denial_latches_and_prevents_next_operation(self):
        m = module()
        with tempfile.TemporaryDirectory() as base:
            docker = m.Docker('/fixed/docker', 'unix:///fixed/socket', 'daemon', pathlib.Path(base))
            good = mock.Mock(returncode=0, stdout='daemon', stderr='')
            denied = mock.Mock(returncode=1, stdout='', stderr='denied')
            with mock.patch.object(docker, '_invoke', side_effect=[good, denied]) as invoke:
                with self.assertRaises(m.Failure):
                    docker.call(['start', 'a'*64])
                with self.assertRaises(m.Failure):
                    docker.call(['rm', '-f', 'a'*64])
                self.assertEqual(invoke.call_count, 2)
                self.assertTrue(docker.denied)

    def test_replaced_daemon_cannot_prove_absence(self):
        m = module()
        with tempfile.TemporaryDirectory() as base:
            docker = m.Docker('/fixed/docker', 'unix:///fixed/socket', 'original', pathlib.Path(base))
            with mock.patch.object(docker, '_invoke', return_value=mock.Mock(returncode=0, stdout='replacement', stderr='')) as invoke:
                with self.assertRaises(m.Failure):
                    m.cleanup_temporal(docker, {'id': 'a'*64, 'image': 'sha256:'+'b'*64, 'label': 'owner'})
                self.assertEqual(invoke.call_count, 1)
                self.assertTrue(docker.denied)

    def test_failed_start_does_not_enter_docker_cleanup(self):
        m = module()
        with tempfile.TemporaryDirectory() as base:
            output = pathlib.Path(base) / 'new'
            args = m.parser().parse_args(['--execute', '--output', str(output)])
            docker = mock.Mock(denied=False)
            def temporal(docker, images, output, owned):
                owned.update(id='a'*64, image='sha256:'+'b'*64, label='owner')
                docker.denied = True
                raise m.Failure('DOCKER_OPERATION_UNKNOWN', unknown=True)
            with mock.patch.object(m, 'validate_inputs'), mock.patch.object(m, 'observe_host', return_value={}), mock.patch.object(m, 'signal_preflight', return_value={}), mock.patch.object(m, 'bind_preflight', return_value={}), mock.patch.object(m, 'docker_preflight', return_value=(docker, {})), mock.patch.object(m, 'temporal_preflight', side_effect=temporal), mock.patch.object(m, 'cleanup_temporal', side_effect=AssertionError('retry')), mock.patch.object(m, 'build_release', side_effect=AssertionError('build')):
                self.assertEqual(m.execute(args), 1)
            result = m.read_json(output / 'runtime-host-result.json')
            self.assertEqual(result['status'], 'UNKNOWN')
            self.assertEqual(result['stages']['owned-temporal-cleanup']['code'], 'DOCKER_DENIED_NO_RETRY')
            self.assertFalse(result['pass'])

    def test_signal_denial_never_sends_alternative_signal(self):
        m = module()
        c = mock.Mock()
        process = mock.Mock(pid=98765)
        member = {'pid': process.pid, 'ppid': __import__('os').getpid(), 'pgid': process.pid,
                  'uid': __import__('os').geteuid(), 'state': 'S'}
        c.group_members.return_value = [member]
        with tempfile.TemporaryDirectory() as base, mock.patch.object(m, 'coordinator', return_value=c), mock.patch.object(m.subprocess, 'Popen', return_value=process), mock.patch.object(m.os, 'getpgid', return_value=process.pid), mock.patch.object(m.os, 'getsid', return_value=process.pid), mock.patch.object(m.os, 'killpg', side_effect=PermissionError(1, 'denied')) as kill:
            with self.assertRaises(m.Failure) as error:
                m.signal_preflight(pathlib.Path(base))
            self.assertTrue(error.exception.unknown)
            self.assertEqual(kill.call_count, 1)
            c.wait_unreaped.assert_not_called()
            process.wait.assert_called_once_with(timeout=6)

    def test_signal_pass_requires_complete_post_reap_absence(self):
        m = module()
        c = mock.Mock()
        process = mock.Mock(pid=98765)
        member = {'pid': process.pid, 'ppid': __import__('os').getpid(), 'pgid': process.pid,
                  'uid': __import__('os').geteuid(), 'state': 'S'}
        c.group_members.side_effect = [[member], [member], [{**member, 'state': 'Z'}], [member]]
        c.wait_unreaped.return_value = {'pid': process.pid, 'uid': 0 if m.sys.platform == 'darwin' else __import__('os').getuid(), 'code': 2, 'status': int(__import__('signal').SIGTERM), 'uid_available': m.sys.platform != 'darwin', 'backend': 'os-waitid', 'wnowait': True}
        process.wait.return_value = -__import__('signal').SIGTERM
        with tempfile.TemporaryDirectory() as base, mock.patch.object(m, 'coordinator', return_value=c), mock.patch.object(m.subprocess, 'Popen', return_value=process), mock.patch.object(m.os, 'getpgid', return_value=process.pid), mock.patch.object(m.os, 'getsid', return_value=process.pid), mock.patch.object(m.os, 'killpg'):
            with self.assertRaises(m.Failure):
                m.signal_preflight(pathlib.Path(base))
            self.assertEqual(c.group_members.call_count, 4)

    def test_complete_stage_requires_positive_full_evidence(self):
        m = module()
        with tempfile.TemporaryDirectory() as base:
            out = pathlib.Path(base)
            (out / 'complete-runtime').mkdir()
            m.save(out / 'complete-runtime/complete-transport-evidence.json', {'pass': True, 'full_certification_claimed': False})
            args = m.parser().parse_args(['--sdk-image', 'sdk', '--command-image', 'command', '--go-bundle', base, '--go-bundle-manifest-sha256', 'd'*64])
            with mock.patch.object(m, 'run') as run:
                with self.assertRaises(m.Failure):
                    m.complete_runtime(mock.Mock(executable='/fixed/docker', endpoint='unix:///fixed/socket', daemon='fixed-daemon'), args, out, {'image_id': 'runner', 'source_manifest_sha256': 'manifest', 'driver': 'driver'})
            self.assertIn('--case', run.call_args.args[0])
            self.assertIn('all', run.call_args.args[0])
            argv = run.call_args.args[0]
            self.assertEqual(argv[argv.index('--docker-executable') + 1], '/fixed/docker')
            self.assertEqual(argv[argv.index('--docker-endpoint') + 1], 'unix:///fixed/socket')
            self.assertEqual(argv[argv.index('--daemon-id') + 1], 'fixed-daemon')
            self.assertEqual(argv[argv.index('--go-bundle-manifest-sha256') + 1], 'd'*64)

    def test_all_pass_requires_every_stage_and_owned_cleanup(self):
        m = module()
        with tempfile.TemporaryDirectory() as base:
            output = pathlib.Path(base) / 'new'
            args = m.parser().parse_args(['--execute', '--output', str(output)])
            docker = mock.Mock(denied=False)
            def temporal(docker, images, output, owned):
                owned.update(id='a'*64, image='sha256:'+'b'*64, label='owner')
                return {'pass': True}
            release = {'image_id': 'sha256:'+'c'*64, 'source_manifest_sha256': 'd'*64, 'driver': 'driver'}
            with mock.patch.object(m, 'validate_inputs') as validate_inputs, mock.patch.object(m, 'observe_host', return_value={}), mock.patch.object(m, 'signal_preflight', return_value={}), mock.patch.object(m, 'bind_preflight', return_value={}), mock.patch.object(m, 'docker_preflight', return_value=(docker, {})), mock.patch.object(m, 'temporal_preflight', side_effect=temporal), mock.patch.object(m, 'build_release', return_value=release), mock.patch.object(m, 'verify_current_source', return_value={}) as verify_source, mock.patch.object(m, 'complete_runtime', return_value={}), mock.patch.object(m, 'run', return_value=mock.Mock(returncode=0)), mock.patch.object(m, 'cleanup_temporal', return_value={'exact_resource_absent': True}):
                self.assertEqual(m.execute(args), 0)
                self.assertEqual(verify_source.call_count, 2)
                self.assertEqual(validate_inputs.call_count, 3)
            result = m.read_json(output / 'runtime-host-result.json')
            self.assertEqual(result['status'], 'PASS')
            self.assertTrue(result['pass'])
            self.assertEqual(set(result['stages']), set(m.STAGES))
            self.assertTrue(all(stage['status'] == 'PASS' for stage in result['stages'].values()))
            self.assertEqual(result['production_admission'], 'DENIED')
            self.assertFalse(result['actual_daemon_outage_claimed'])

    def test_cleanup_unknown_prevents_overall_pass(self):
        m = module()
        with tempfile.TemporaryDirectory() as base:
            output = pathlib.Path(base) / 'new'
            args = m.parser().parse_args(['--execute', '--output', str(output)])
            docker = mock.Mock(denied=False)
            def temporal(docker, images, output, owned):
                owned.update(id='a'*64, image='sha256:'+'b'*64, label='owner')
                raise m.Failure('TEMPORAL_CAPABILITY_FAILED')
            with mock.patch.object(m, 'validate_inputs'), mock.patch.object(m, 'observe_host', return_value={}), mock.patch.object(m, 'signal_preflight', return_value={}), mock.patch.object(m, 'bind_preflight', return_value={}), mock.patch.object(m, 'docker_preflight', return_value=(docker, {})), mock.patch.object(m, 'temporal_preflight', side_effect=temporal), mock.patch.object(m, 'cleanup_temporal', side_effect=m.Failure('TEMPORAL_CLEANUP_UNKNOWN', unknown=True)), mock.patch.object(m, 'build_release', side_effect=AssertionError('build')):
                self.assertEqual(m.execute(args), 1)
            result = m.read_json(output / 'runtime-host-result.json')
            self.assertEqual(result['status'], 'UNKNOWN')
            self.assertFalse(result['pass'])
            self.assertEqual(result['stages']['build-release']['status'], 'NOT_RUN')

    def test_observer_original_denial_detail_retained(self):
        m = module()
        c = mock.Mock()
        denied = RuntimeError('COORDINATOR_GROUP_OBSERVATION_UNKNOWN')
        denied.detail = {'observer_errno': 1}
        c.group_members.side_effect = denied
        with mock.patch.object(m, 'coordinator', return_value=c):
            with self.assertRaises(m.Failure) as error:
                m.observe_host()
        self.assertEqual(error.exception.detail['observer_errno'], 1)
        self.assertIn('runtime_context', error.exception.detail)

    def test_bundle_manifest_pin_is_required_before_runtime(self):
        m = module()
        with tempfile.TemporaryDirectory() as base:
            bundle = pathlib.Path(base).resolve()
            m.save(bundle / 'manifest.json', {'toolchain_image': 'sha256:'+'a'*64})
            args = m.parser().parse_args(['--execute', '--output', str(bundle / 'new'), '--go-bundle', str(bundle),
                 '--command-image', 'sha256:'+'a'*64, '--sdk-image', 'sha256:'+'b'*64,
                 '--temporal-image', 'temporal:test', '--go-bundle-manifest-sha256', 'c'*64])
            with mock.patch.object(m.sys, 'platform', 'darwin'):
                with self.assertRaises(m.Failure) as error:
                    m.validate_inputs(args)
            self.assertEqual(str(error.exception), 'GO_BUNDLE_MANIFEST_PIN_MISMATCH')

    def test_failed_runtime_child_latches_unknown_without_cleanup_retry(self):
        m = module()
        with tempfile.TemporaryDirectory() as base:
            output = pathlib.Path(base) / 'new'
            args = m.parser().parse_args(['--execute', '--output', str(output)])
            docker = mock.Mock(denied=False)
            def temporal(docker, images, output, owned):
                owned.update(id='a'*64, image='sha256:'+'b'*64, label='owner')
                return {'pass': True}
            with mock.patch.object(m, 'validate_inputs'), mock.patch.object(m, 'observe_host', return_value={}), mock.patch.object(m, 'signal_preflight', return_value={}), mock.patch.object(m, 'bind_preflight', return_value={}), mock.patch.object(m, 'docker_preflight', return_value=(docker, {})), mock.patch.object(m, 'temporal_preflight', side_effect=temporal), mock.patch.object(m, 'run', return_value=mock.Mock(returncode=0)), mock.patch.object(m, 'build_release', side_effect=m.Failure('COMMAND_FAILED:docker')), mock.patch.object(m, 'cleanup_temporal', side_effect=AssertionError('retry')):
                self.assertEqual(m.execute(args), 1)
            result = m.read_json(output / 'runtime-host-result.json')
            self.assertFalse(docker.denied)
            self.assertEqual(result['status'], 'UNKNOWN')
            self.assertEqual(result['stages']['owned-temporal-cleanup']['code'], 'CHILD_RUNTIME_UNKNOWN_NO_RETRY')
            self.assertEqual(result['stages']['complete-runtime-all']['status'], 'NOT_RUN')

    def test_local_release_source_failure_still_cleans_exact_owned_temporal(self):
        m = module()
        with tempfile.TemporaryDirectory() as base:
            output = pathlib.Path(base) / 'new'
            args = m.parser().parse_args(['--execute', '--output', str(output)])
            docker = mock.Mock(denied=False)
            def temporal(docker, images, output, owned):
                owned.update(id='a'*64, image='sha256:'+'b'*64, label='owner')
                return {'pass': True}
            local_error = m.Failure('RELEASE_LOCAL_BUILD_FAILED', detail={'classification': 'LOCAL_BUILD_FAILURE', 'phase': 'source-context', 'docker_calls_attempted': False})
            with mock.patch.object(m, 'validate_inputs'), mock.patch.object(m, 'observe_host', return_value={}), mock.patch.object(m, 'signal_preflight', return_value={}), mock.patch.object(m, 'bind_preflight', return_value={}), mock.patch.object(m, 'docker_preflight', return_value=(docker, {})), mock.patch.object(m, 'temporal_preflight', side_effect=temporal), mock.patch.object(m, 'run', return_value=mock.Mock(returncode=0)), mock.patch.object(m, 'build_release', side_effect=local_error), mock.patch.object(m, 'cleanup_temporal', return_value={'exact_resource_absent': True}) as cleanup:
                self.assertEqual(m.execute(args), 1)
            cleanup.assert_called_once_with(docker, {'id': 'a'*64, 'image': 'sha256:'+'b'*64, 'label': 'owner'})
            self.assertFalse(docker.denied)
            result = m.read_json(output / 'runtime-host-result.json')
            self.assertEqual(result['stages']['build-release']['status'], 'FAIL')
            self.assertEqual(result['stages']['owned-temporal-cleanup']['status'], 'PASS')
            self.assertFalse(result['pass'])
            self.assertEqual(m.read_json(output / 'temporal-owner.json')['status'], 'REMOVED')

    def test_child_failure_receipt_requires_fresh_token_and_local_phase(self):
        for case in ('valid', 'stale', 'runtime', 'phase', 'missing', 'boolean-version'):
            with self.subTest(case=case), tempfile.TemporaryDirectory() as base:
                m = module(); output = pathlib.Path(base)
                docker = mock.Mock(executable='/fixed/docker', endpoint='unix:///fixed/socket', daemon='daemon')
                def child(argv, **kwargs):
                    if case != 'missing':
                        (output / 'release').mkdir()
                        record = {'version': 1, 'token': argv[argv.index('--failure-token') + 1],
                                  'classification': 'LOCAL_BUILD_FAILURE', 'phase': 'source-context',
                                  'docker_calls_attempted': False, 'error_type': 'ValueError'}
                        if case == 'stale': record['token'] = 'old-token'
                        if case == 'runtime': record.update(classification='RUNTIME_UNKNOWN', docker_calls_attempted=True)
                        if case == 'phase': record['phase'] = 'docker-build'
                        if case == 'boolean-version': record['version'] = True
                        m.save(output / 'release/build-failure.json', record)
                    raise m.Failure('COMMAND_FAILED:python3.14')
                with mock.patch.object(m, 'run', side_effect=child):
                    with self.assertRaises(m.Failure) as error: m.build_release(docker, mock.Mock(), output)
                self.assertEqual(str(error.exception), 'RELEASE_LOCAL_BUILD_FAILED' if case == 'valid' else 'CHILD_RELEASE_RUNTIME_UNKNOWN')
                self.assertEqual(error.exception.unknown, case != 'valid')

    def test_cleanup_requires_absence_and_post_observation_same_daemon(self):
        m = module()
        owned = {'id': 'a'*64, 'image': 'sha256:'+'b'*64, 'label': 'owner', 'name': 'owned'}
        present = mock.Mock(returncode=0, stdout=__import__('json').dumps([{'Id': owned['id'], 'Image': owned['image'], 'Name': '/owned', 'Config': {'Labels': {'cdo.host-certification': 'owner'}}}]), stderr='')
        missing = mock.Mock(returncode=1, stdout='', stderr='Error: No such object: '+owned['id'])
        for case in ('valid', 'present', 'wrong-daemon', 'denied'):
            with self.subTest(case=case):
                docker = mock.Mock(daemon='daemon')
                final = present if case == 'present' else mock.Mock(returncode=1, stdout='', stderr='permission denied') if case == 'denied' else missing
                docker.call.side_effect = [present, mock.Mock(stdout='daemon'), mock.Mock(returncode=0), final, mock.Mock(stdout='changed' if case == 'wrong-daemon' else 'daemon')]
                if case == 'valid': self.assertTrue(m.cleanup_temporal(docker, owned)['exact_resource_absent'])
                else:
                    with self.assertRaises(m.Failure): m.cleanup_temporal(docker, owned)
                self.assertEqual(docker.call.call_args_list[2].args[0], ['rm', '-f', owned['id']])

    def test_immutable_reconciliation_receipt_cannot_be_overwritten(self):
        m = module()
        with tempfile.TemporaryDirectory() as base:
            path = pathlib.Path(base) / 'receipt.json'
            m.save_immutable_receipt(path, {'status': 'UNKNOWN'})
            original = path.read_bytes()
            self.assertEqual(path.stat().st_mode & 0o777, 0o400)
            with self.assertRaises(FileExistsError): m.save_immutable_receipt(path, {'status': 'PASS'})
            self.assertEqual(path.read_bytes(), original)

    def test_unix_bind_uses_short_exact_owned_path(self):
        m = module()
        unix, loopback = mock.Mock(), mock.Mock()
        with mock.patch.object(m.socket, 'socket', side_effect=[unix, loopback]):
            result = m.bind_preflight(pathlib.Path('/very/long/evidence/' + 'x'*200))
        path = unix.bind.call_args.args[0]
        self.assertLess(len(path.encode()), 104)
        self.assertTrue(path.startswith('/private/tmp/cdo-host-bind-'))
        self.assertFalse(pathlib.Path(path).parent.exists())
        self.assertTrue(result['unix_bind'])
        self.assertEqual(result['socket_path'], path)
        self.assertTrue(result['socket_removed'])
        self.assertTrue(result['private_directory_removed'])
        unix.close.assert_called_once()
        loopback.close.assert_called_once()

    def test_normal_repository_evidence_path_exceeds_unix_limit(self):
        m = module()
        long_output = ROOT / '.cache/sandbox-certification/host-certification-20261009-complete'
        self.assertGreater(len(str(long_output / 'capability.sock').encode()), 104)
        unix, loopback = mock.Mock(), mock.Mock()
        def bounded_bind(path):
            if len(path.encode()) >= 104:
                raise OSError(36, 'AF_UNIX path too long')
        unix.bind.side_effect = bounded_bind
        with mock.patch.object(m.socket, 'socket', side_effect=[unix, loopback]):
            evidence = m.bind_preflight(long_output)
        self.assertTrue(evidence['unix_bind'])
        self.assertTrue(evidence['private_directory_removed'])

    def test_auto_local_deduplicates_tags_to_exact_one_image(self):
        m = module()
        docker = mock.Mock()
        identity = 'sha256:'+'a'*64
        other = 'sha256:'+'b'*64
        docker.call.side_effect = [mock.Mock(stdout=identity+'\n'+identity+'\n'+other),
            mock.Mock(stdout=__import__('json').dumps([
                {'Id': identity, 'RepoTags': ['temporalio/temporal:latest', 'temporalio/temporal:stable']},
                {'Id': other, 'RepoTags': ['other:local']}]))]
        self.assertEqual(m.local_temporal_image(docker), identity)
        self.assertEqual(docker.call.call_args_list[0].args[0], ['image', 'ls', '--no-trunc', '--format', '{{.ID}}'])
        self.assertEqual(docker.call.call_args_list[1].args[0], ['image', 'inspect', identity, other])
        self.assertEqual(docker.call.call_count, 2)

    def test_auto_local_missing_reports_infrastructure_without_changes(self):
        m = module()
        docker = mock.Mock()
        docker.call.return_value = mock.Mock(stdout='')
        with self.assertRaises(m.Failure) as error:
            m.local_temporal_image(docker)
        self.assertEqual(str(error.exception), 'EXTERNAL_INFRASTRUCTURE_UNAVAILABLE')
        self.assertEqual(error.exception.detail['reason'], 'LOCAL_TEMPORAL_IMAGE_MISSING')
        self.assertFalse(error.exception.detail['infra_changes'])
        self.assertEqual(docker.call.call_count, 1)

    def test_auto_local_ambiguity_does_not_guess_or_pull(self):
        m = module()
        docker = mock.Mock()
        ids = ['sha256:'+'a'*64, 'sha256:'+'b'*64]
        docker.call.side_effect = [mock.Mock(stdout='\n'.join(ids)), mock.Mock(stdout=__import__('json').dumps([
            {'Id': ids[0], 'RepoTags': ['temporalio/temporal:one']},
            {'Id': ids[1], 'RepoTags': ['temporalio/temporal:two']}]))]
        with self.assertRaises(m.Failure) as error:
            m.local_temporal_image(docker)
        self.assertEqual(error.exception.detail['reason'], 'LOCAL_TEMPORAL_IMAGE_AMBIGUOUS')
        self.assertEqual(error.exception.detail['candidate_ids'], ids)
        self.assertFalse(error.exception.detail['infra_changes'])
        self.assertEqual(docker.call.call_count, 2)


if __name__ == '__main__':
    unittest.main()
