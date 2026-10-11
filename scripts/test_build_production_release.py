import importlib.util
import pathlib
import tempfile
import io
import tarfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location('build_release', pathlib.Path(__file__).with_name('build-production-release.py'))
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)

class BuildReleaseTests(unittest.TestCase):
    def fixture(self, root):
        repo = root / 'repo'; repo.mkdir()
        for name in MODULE.INPUTS:
            path = repo / name
            if '.' in name and not name.startswith('.'):
                path.write_text('input')
            else:
                path.mkdir(); (path / 'input').write_text('input')
        (repo / 'docker/ReleaseDockerfile').write_text('FROM scratch')
        return repo

    def test_capture_and_revalidation(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory); repo = self.fixture(root)
            out = root / 'capture'
            identity = MODULE.capture(repo, out)
            self.assertEqual(identity, MODULE.validate_capture(out))
            (out / 'source/cmd/addition').write_text('extra')
            with self.assertRaisesRegex(RuntimeError, 'INVENTORY'):
                MODULE.validate_capture(out)

    def test_changes_removal_symlink_secret_denied(self):
        for case in ('changed', 'removed', 'symlink', 'secret'):
            with self.subTest(case=case), tempfile.TemporaryDirectory() as directory:
                root = pathlib.Path(directory); repo = self.fixture(root); out = root / 'capture'
                MODULE.capture(repo, out)
                target = out / 'source/go.mod'; target.chmod(0o600)
                if case == 'changed': target.write_text('changed')
                elif case == 'removed': target.unlink()
                elif case == 'symlink': target.unlink(); target.symlink_to(repo / 'go.mod')
                else: (out / 'source/cmd/.env').write_text('never read')
                with self.assertRaises(RuntimeError): MODULE.validate_capture(out)

    def test_empty_directory_addition_and_output_preserved(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory); repo = self.fixture(root); out = root / 'capture'
            MODULE.capture(repo, out)
            with self.assertRaises(FileExistsError): MODULE.capture(repo, out)
            (out / 'source/cmd/empty').mkdir()
            with self.assertRaisesRegex(RuntimeError, 'INVENTORY'): MODULE.validate_capture(out)

    def test_context_and_endpoint_denials(self):
        with tempfile.TemporaryDirectory() as directory:
            executable = pathlib.Path(directory) / 'docker'; executable.touch()
            for responses in ([b'other'], [b'desktop-linux', b'tcp://remote:2375']):
                with mock.patch.object(MODULE.shutil, 'which', return_value=str(executable)), mock.patch.object(MODULE.subprocess, 'check_output', side_effect=responses):
                    with self.assertRaises(RuntimeError): MODULE.docker_configuration()

    def test_docker_expected_pins_bind_observed_configuration(self):
        with tempfile.TemporaryDirectory() as directory:
            executable = pathlib.Path(directory) / 'docker'; executable.touch()
            socket = pathlib.Path(directory) / 'docker.sock'; socket.touch()
            pins = (str(executable.resolve()), 'unix://' + str(socket.resolve()), 'daemon-fixed')
            variants = [pins, ('/changed/docker', pins[1], pins[2]), (pins[0], 'unix:///changed', pins[2]), (pins[0], pins[1], 'changed-daemon')]
            for expected in variants:
                with mock.patch.object(MODULE.shutil, 'which', return_value=str(executable)), mock.patch.object(MODULE.subprocess, 'check_output', side_effect=[b'desktop-linux', pins[1].encode(), b'daemon-fixed']):
                    if expected == pins: self.assertEqual(MODULE.docker_configuration(expected), pins[:2])
                    else:
                        with self.assertRaisesRegex(RuntimeError, 'CONFIGURATION_CHANGED'): MODULE.docker_configuration(expected)

    def test_capture_rejects_symlink_and_secret_before_read(self):
        for case in ('symlink', 'secret'):
            with self.subTest(case=case), tempfile.TemporaryDirectory() as directory:
                root = pathlib.Path(directory); repo = self.fixture(root)
                if case == 'symlink': (repo / 'cmd/link').symlink_to(repo / 'internal', target_is_directory=True)
                else: (repo / 'cmd/.env').mkdir()
                with self.assertRaises(RuntimeError): MODULE.capture(repo, root / 'capture')

    def test_failed_build_preserves_evidence_and_never_inspects(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory); repo = self.fixture(root); out = root / 'capture'
            MODULE.capture(repo, out)
            with mock.patch.object(MODULE, 'docker_configuration', return_value=('/fixed/docker', 'unix:///fixed/socket')), mock.patch.object(MODULE.subprocess, 'run', return_value=mock.Mock(returncode=7)), mock.patch.object(MODULE.subprocess, 'check_output') as inspect:
                with self.assertRaisesRegex(RuntimeError, 'BUILD_FAILED'): MODULE.build_capture(out, 'tag')
                inspect.assert_not_called()
            self.assertEqual(__import__('json').loads((out / 'build-result.json').read_text())['exit_code'], 7)
            self.assertTrue((out / 'build.log').exists())
            with self.assertRaisesRegex(RuntimeError, 'ALREADY_EXISTS'): MODULE.build_capture(out, 'tag')

    def test_capture_rejects_hardlink_input(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory); repo = self.fixture(root)
            __import__('os').link(repo / 'go.mod', repo / 'cmd/linked-input')
            with self.assertRaisesRegex(RuntimeError, 'REGULAR'): MODULE.capture(repo, root / 'capture')

    def test_leaf_replaced_after_open_is_denied(self):
        import os
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory); (root / 'input').write_bytes(b'approved')
            (root / 'synthetic-private').write_bytes(b'synthetic secret')
            original_open = os.open
            def racing_open(path, flags, **kwargs):
                descriptor = original_open(path, flags, **kwargs)
                if path == 'input':
                    (root / 'input').rename(root / 'original')
                    (root / 'input').symlink_to(root / 'synthetic-private')
                return descriptor
            with mock.patch.object(MODULE.os, 'open', side_effect=racing_open):
                with self.assertRaisesRegex(RuntimeError, 'IDENTITY_CHANGED'): MODULE.safe_read(root, 'input')

    def test_safe_read_rejects_hardlink_and_intermediate_replacement(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory); (root / 'owned').mkdir(); (root / 'owned/file').write_bytes(b'approved')
            outside = root / 'synthetic-private'; outside.mkdir(); (outside / 'file').write_bytes(b'synthetic secret')
            __import__('os').link(outside / 'file', root / 'owned/hardlink')
            with self.assertRaises(RuntimeError): MODULE.safe_read(root, 'owned/hardlink')
            (root / 'owned/file').unlink(); (root / 'owned/hardlink').unlink(); (root / 'owned').rmdir()
            (root / 'owned').symlink_to(outside, target_is_directory=True)
            with self.assertRaises((RuntimeError, OSError)): MODULE.safe_read(root, 'owned/file')
            with self.assertRaisesRegex(RuntimeError, 'SECRET'): MODULE.safe_read(root, 'synthetic-private/.env')

    def test_context_is_deterministic_bounded_and_has_no_links(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory); repo = self.fixture(root); out = root / 'capture'
            identity = MODULE.capture(repo, out)
            context = MODULE.immutable_context(out, identity)
            self.assertEqual(context, MODULE.immutable_context(out, identity))
            with tarfile.open(fileobj=io.BytesIO(context)) as archive:
                self.assertTrue(all(item.isfile() and item.mode == 0o644 and item.mtime == 0 for item in archive))
            with mock.patch.object(MODULE, 'MAX_CONTEXT_BYTES', 1024):
                with self.assertRaisesRegex(RuntimeError, 'LIMIT'): MODULE.immutable_context(out, identity)
            target = out / 'source/go.mod'; target.unlink(); target.symlink_to(repo / 'go.mod')
            with self.assertRaises(OSError): MODULE.immutable_context(out, identity)

    def test_context_preserves_actual_release_long_leaf_names(self):
        stem = 'get-api-v1-architecture-platform-services-projectid-operations-operationid-presentation-2f5dcd3ddbf0'
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory); repo = self.fixture(root); out = root / 'capture'
            endpoints = repo / '.ai/architecture/endpoints'; endpoints.mkdir(parents=True)
            names = ['.ai/architecture/endpoints/' + stem + suffix for suffix in ('.mmd', '.yaml')]
            for name in names:
                (repo / name).write_bytes(b'exact captured source')
                with self.assertRaisesRegex(ValueError, 'name is too long'):
                    tarfile.TarInfo(name).tobuf(tarfile.USTAR_FORMAT)
            identity = MODULE.capture(repo, out)
            context = MODULE.immutable_context(out, identity)
            self.assertEqual(context, MODULE.immutable_context(out, identity))
            manifest = __import__('json').loads((out / 'source-manifest.json').read_bytes())
            with tarfile.open(fileobj=io.BytesIO(context)) as archive:
                self.assertEqual(archive.getnames(), sorted([*manifest, 'release/source-manifest.json', 'release/source-identity']))
                for item in archive:
                    self.assertTrue(item.isfile())
                    self.assertEqual((item.mode, item.uid, item.gid, item.mtime, item.uname, item.gname), (0o644, 0, 0, 0, '', ''))
                    self.assertEqual(archive.extractfile(item).read(), (out / 'source' / item.name).read_bytes())

    def test_context_preserves_unicode_paths(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory); repo = self.fixture(root); out = root / 'capture'
            name = 'cmd/' + 'путь' * 20 + '.go'
            (repo / name).write_bytes(b'unicode source')
            identity = MODULE.capture(repo, out)
            context = MODULE.immutable_context(out, identity)
            with tarfile.open(fileobj=io.BytesIO(context)) as archive:
                self.assertEqual(archive.extractfile(name).read(), b'unicode source')
            self.assertEqual(context, MODULE.immutable_context(out, identity))

    def test_pax_header_budget_checked_before_any_entry_write(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory); repo = self.fixture(root); out = root / 'capture'
            # Long relative path needs more than the old 1024-byte allowance.
            name = '.ai/' + '/'.join(['a' * 90] * 7) + '/file'
            target = repo / name; target.parent.mkdir(parents=True); target.write_bytes(b'input')
            identity = MODULE.capture(repo, out)
            entry = tarfile.TarInfo(name); entry.size = 5
            entry.mode = 0o644; entry.uid = entry.gid = entry.mtime = 0
            entry.uname = entry.gname = ''
            header = entry.tobuf(tarfile.PAX_FORMAT, 'utf-8', 'strict')
            self.assertGreater(len(header), 1024)
            cap = tarfile.RECORDSIZE + len(header) + tarfile.BLOCKSIZE - 1
            with mock.patch.object(MODULE, 'MAX_CONTEXT_BYTES', cap), mock.patch.object(tarfile.TarFile, 'addfile') as add:
                with self.assertRaisesRegex(RuntimeError, 'BUILD_CONTEXT_LIMIT'):
                    MODULE.immutable_context(out, identity)
                add.assert_not_called()

    def test_context_limits_prevent_docker_build(self):
        for limit, value in (('MAX_CONTEXT_BYTES', 1024), ('MAX_CONTEXT_FILE_BYTES', 1), ('MAX_CONTEXT_ENTRIES', 1)):
            with self.subTest(limit=limit), tempfile.TemporaryDirectory() as directory:
                root = pathlib.Path(directory); repo = self.fixture(root); out = root / 'capture'
                MODULE.capture(repo, out)
                with mock.patch.object(MODULE, limit, value), mock.patch.object(MODULE, 'docker_configuration', return_value=('/fixed/docker', 'unix:///fixed/socket')), mock.patch.object(MODULE.subprocess, 'run') as run:
                    with self.assertRaises(RuntimeError): MODULE.build_capture(out, 'tag')
                    run.assert_not_called()

    def test_builder_emits_local_failure_before_any_docker_invocation(self):
        token = 'cb51ae0e-e49f-4801-9b31-ce9394aaf4ab'
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory); repo = self.fixture(root); out = root / 'capture'
            capture = MODULE.capture
            with mock.patch.object(MODULE, 'capture', side_effect=lambda source, output: capture(repo, output)), mock.patch.object(MODULE, 'immutable_context', side_effect=ValueError('name is too long')), mock.patch.object(MODULE, 'docker_configuration', side_effect=AssertionError('Docker must not run')) as configuration:
                with self.assertRaises(MODULE.LocalBuildFailure):
                    MODULE.main(['--output', str(out), '--tag', 'fixture', '--failure-token', token])
                configuration.assert_not_called()
            receipt = __import__('json').loads((out / 'build-failure.json').read_bytes())
            self.assertEqual(receipt['token'], token)
            self.assertEqual(receipt['classification'], 'LOCAL_BUILD_FAILURE')
            self.assertEqual(receipt['phase'], 'source-context')
            self.assertIs(receipt['docker_calls_attempted'], False)
            self.assertEqual(receipt['error_type'], 'ValueError')

    def test_builder_runtime_failure_cannot_emit_local_cleanup_permission(self):
        token = 'cb51ae0e-e49f-4801-9b31-ce9394aaf4ab'
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory); repo = self.fixture(root); out = root / 'capture'
            capture = MODULE.capture
            with mock.patch.object(MODULE, 'capture', side_effect=lambda source, output: capture(repo, output)), mock.patch.object(MODULE, 'docker_configuration', side_effect=PermissionError(13, 'denied')):
                with self.assertRaises(PermissionError):
                    MODULE.main(['--output', str(out), '--tag', 'fixture', '--failure-token', token])
            receipt = __import__('json').loads((out / 'build-failure.json').read_bytes())
            self.assertEqual(receipt['classification'], 'RUNTIME_UNKNOWN')
            self.assertIs(receipt['docker_calls_attempted'], True)

    def test_historical_tag_replacement_characterization(self):
        # Original pre-remediation binding statement: a successful build can be
        # followed by another operator replacing its tag before inspection.
        import json
        expected = 'sha256:' + 'a' * 64
        replacement = 'sha256:' + 'b' * 64
        with mock.patch.object(MODULE.subprocess, 'check_output', return_value=json.dumps([{'Id': replacement, 'RepoDigests': []}]).encode()):
            image = json.loads(MODULE.subprocess.check_output(['docker', 'image', 'inspect', 'mutable-tag'], timeout=30))[0]
            self.assertNotEqual(image['Id'], expected)

    def test_immutable_image_inspection(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory); repo = self.fixture(root); out = root / 'capture'
            MODULE.capture(repo, out)
            endpoint = root / 'docker.sock'; endpoint.touch()
            executable = root / 'docker'; executable.touch()
            image = 'sha256:' + 'a' * 64
            calls = []
            def checked(argv, **kwargs):
                calls.append(argv)
                if argv[1:] == ['context', 'show']: return b'desktop-linux\n'
                if argv[1:3] == ['context', 'inspect']: return ('unix://' + str(endpoint)).encode()
                if 'info' in argv: return b'daemon-fixed'
                self.assertEqual(argv[-1], image)
                return ('[{"Id":"' + image + '","RepoDigests":[],"Config":{"Labels":{"cdo.source_manifest":"' + MODULE.validate_capture(out) + '"}}}]').encode()
            def run(argv, **kwargs):
                calls.append(argv)
                self.assertEqual(argv[-2:], ['docker/ReleaseDockerfile', '-'])
                target = out / 'source/go.mod'
                original = target.read_bytes(); target.chmod(0o600)
                target.write_bytes(b'transient replacement while Docker consumes context')
                try:
                    with tarfile.open(fileobj=io.BytesIO(kwargs['input'])) as archive:
                        self.assertEqual(archive.extractfile('go.mod').read(), original)
                finally:
                    target.write_bytes(original)
                pathlib.Path(argv[argv.index('--iidfile') + 1]).write_text(image)
                return mock.Mock(returncode=0)
            with mock.patch.object(MODULE.shutil, 'which', return_value=str(executable)), mock.patch.object(MODULE.subprocess, 'check_output', side_effect=checked), mock.patch.object(MODULE.subprocess, 'run', side_effect=run):
                self.assertEqual(MODULE.build_capture(out, 'mutable-tag'), image)
            self.assertTrue(all('--host' in call for call in calls if 'build' in call or 'image' in call))
            with self.assertRaises(RuntimeError): MODULE.build_capture(out, 'mutable-tag')

if __name__ == '__main__': unittest.main()
