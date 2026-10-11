#!/usr/bin/env python3
"""Capture exact production inputs and build their frozen bytes. No Git mutation."""
import argparse
import hashlib
import io
import json
import os
import pathlib
import re
import shutil
import stat
import subprocess
import time
import tarfile
import uuid

INPUTS = ('cmd', 'internal', 'runner', 'docker', 'scripts', 'agent-system', '.ai', 'go.mod', 'go.sum')
EXCLUDED = {'node_modules', 'dist', '.cache', '__pycache__'}
MAX_CONTEXT_BYTES = 256 * 1024 * 1024
MAX_CONTEXT_FILE_BYTES = 32 * 1024 * 1024
MAX_CONTEXT_ENTRIES = 20000
BUILD_ARTIFACTS = ('build.log', 'build-result.json', 'image-identity.json', 'image.iid', 'build-reserved', 'build-failure.json')


class LocalBuildFailure(RuntimeError):
    """Trusted builder failed before its first Docker invocation."""
    def __init__(self, phase, error):
        super().__init__(str(error))
        self.phase = phase


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':')).encode()


def digest(data):
    return hashlib.sha256(data).hexdigest()


def write_new(path, data):
    with path.open('xb') as stream:
        stream.write(data)
        stream.flush()
        os.fsync(stream.fileno())


def safe_read(root, relative, limit=MAX_CONTEXT_FILE_BYTES):
    relative = str(relative)
    parts = relative.split('/')
    if not parts or any(part in ('', '.', '..') for part in parts) or relative.startswith('/') or '\\' in relative:
        raise RuntimeError('BUILD_INPUT_PATH_DENIED')
    if any(part == '.env' or part.startswith('.env.') for part in parts):
        raise RuntimeError('SECRET_BUILD_INPUT_DENIED')
    root = pathlib.Path(root)
    if any(part == '.env' or part.startswith('.env.') for part in root.parts):
        raise RuntimeError('SECRET_BUILD_INPUT_DENIED')
    if root.is_symlink():
        raise RuntimeError('BUILD_INPUT_ROOT_DENIED')
    root_before = root.lstat()
    directory = os.open(root.resolve(strict=True), os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        root_opened, root_after = os.fstat(directory), root.lstat()
        if not stat.S_ISDIR(root_before.st_mode) or any(
                (value.st_dev, value.st_ino) != (root_before.st_dev, root_before.st_ino)
                for value in (root_opened, root_after)):
            raise RuntimeError('BUILD_INPUT_ROOT_CHANGED')
        for part in parts[:-1]:
            child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=directory)
            os.close(directory)
            directory = child
        descriptor = os.open(parts[-1], os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory)
        with os.fdopen(descriptor, 'rb') as source:
            before = os.fstat(source.fileno())
            if not stat.S_ISREG(before.st_mode) or before.st_nlink != 1 or before.st_size > limit:
                raise RuntimeError('BUILD_INPUT_REGULAR_FILE_REQUIRED')
            data = source.read(limit + 1)
            after = os.fstat(source.fileno())
            fields = ('st_dev', 'st_ino', 'st_size', 'st_mtime_ns', 'st_ctime_ns', 'st_nlink')
            current = os.stat(parts[-1], dir_fd=directory, follow_symlinks=False)
            if len(data) != before.st_size or len(data) > limit or any(
                    getattr(before, key) != getattr(after, key) or getattr(before, key) != getattr(current, key)
                    for key in fields):
                raise RuntimeError('BUILD_INPUT_IDENTITY_CHANGED')
            return data
    finally:
        os.close(directory)


def inventory(root, captured=False, directories=None):
    records = {}
    def visit(path):
        relative = path.relative_to(root)
        if any(part in EXCLUDED or part.startswith('._') for part in relative.parts):
            if not captured:
                return
            raise RuntimeError('CAPTURE_INVENTORY_MISMATCH')
        mode = path.lstat().st_mode
        if stat.S_ISLNK(mode):
            raise RuntimeError('SYMLINK_BUILD_INPUT_DENIED')
        if path.name == '.env' or path.name.startswith('.env.'):
            raise RuntimeError('SECRET_BUILD_INPUT_DENIED')
        if stat.S_ISDIR(mode):
            if directories is not None:
                directories.append(str(relative))
            for child in sorted(path.iterdir()):
                visit(child)
        elif stat.S_ISREG(mode):
            records[str(relative)] = digest(safe_read(root, relative))
        else:
            raise RuntimeError('NONREGULAR_BUILD_INPUT_DENIED')
    if captured:
        for child in sorted(root.iterdir()):
            if child.name == 'release':
                continue
            visit(child)
    else:
        for name in INPUTS:
            visit(root / name)
    return records


def capture(repo, output):
    repo, output = pathlib.Path(repo).resolve(strict=True), pathlib.Path(output).resolve()
    output.mkdir(parents=True, exist_ok=False)
    snapshot = output / 'source'; snapshot.mkdir()
    directories = []
    manifest = inventory(repo, directories=directories)
    for relative in directories:
        (snapshot / relative).mkdir(parents=True, exist_ok=True)
    for relative, expected in manifest.items():
        source = repo / relative
        data = safe_read(repo, relative)
        if digest(data) != expected:
            raise RuntimeError('SOURCE_SNAPSHOT_MISMATCH')
        target = snapshot / relative; target.parent.mkdir(parents=True, exist_ok=True)
        write_new(target, data)
        if digest(safe_read(snapshot, relative)) != expected:
            raise RuntimeError('SOURCE_SNAPSHOT_MISMATCH')
    if inventory(repo) != manifest:
        raise RuntimeError('SOURCE_SNAPSHOT_MISMATCH')
    encoded = canonical(manifest); identity = digest(encoded)
    write_new(output / 'capture-inventory.json', canonical({'directories': sorted(directories)}))
    write_new(output / 'source-manifest.json', encoded)
    release = snapshot / 'release'; release.mkdir()
    write_new(release / 'source-manifest.json', encoded)
    write_new(release / 'source-identity', identity.encode())
    for path in snapshot.rglob('*'):
        if path.is_file():
            path.chmod(path.stat().st_mode & ~0o222)
    validate_capture(output)
    return identity


def regular(path):
    return safe_read(path.parent, path.name)


def validate_capture(output):
    output = pathlib.Path(output)
    if output.is_symlink() or not output.is_dir():
        raise RuntimeError('CAPTURE_DIRECTORY_REQUIRED')
    snapshot = output / 'source'
    if snapshot.is_symlink() or not snapshot.is_dir():
        raise RuntimeError('CAPTURE_DIRECTORY_REQUIRED')
    encoded = regular(output / 'source-manifest.json')
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise RuntimeError('CAPTURE_DUPLICATE_FIELD')
            result[key] = value
        return result
    manifest = json.loads(encoded, object_pairs_hook=unique)
    if not isinstance(manifest, dict) or canonical(manifest) != encoded or any(
            not isinstance(name, str) or not name or pathlib.PurePosixPath(name).is_absolute()
            or any(part in ('', '.', '..') for part in name.split('/'))
            or name.split('/')[0] not in INPUTS
            or not isinstance(value, str) or not re.fullmatch('[a-f0-9]{64}', value)
            for name, value in manifest.items()):
        raise RuntimeError('CAPTURE_MANIFEST_DENIED')
    directories = []
    directory_record = json.loads(regular(output / 'capture-inventory.json'), object_pairs_hook=unique)
    if (not isinstance(directory_record, dict) or set(directory_record) != {'directories'}
            or not isinstance(directory_record['directories'], list)):
        raise RuntimeError('CAPTURE_INVENTORY_MISMATCH')
    actual = inventory(snapshot, captured=True, directories=directories)
    if actual != manifest or directory_record['directories'] != sorted(directories):
        raise RuntimeError('CAPTURE_INVENTORY_MISMATCH')
    identity = digest(encoded)
    release = snapshot / 'release'
    if release.is_symlink() or not release.is_dir() or {p.name for p in release.iterdir()} != {'source-manifest.json', 'source-identity'}:
        raise RuntimeError('CAPTURE_RELEASE_METADATA_DENIED')
    if regular(release / 'source-manifest.json') != encoded or regular(release / 'source-identity') != identity.encode():
        raise RuntimeError('CAPTURE_RELEASE_METADATA_DENIED')
    return identity


def docker_configuration(expected=None):
    executable = shutil.which('docker')
    if not executable:
        raise RuntimeError('DOCKER_EXECUTABLE_REQUIRED')
    executable = str(pathlib.Path(executable).resolve(strict=True))
    context = subprocess.check_output([executable, 'context', 'show'], timeout=15).decode().strip()
    if context != 'desktop-linux':
        raise RuntimeError('DOCKER_CONTEXT_CHANGED')
    endpoint = subprocess.check_output([executable, 'context', 'inspect', context, '--format', '{{.Endpoints.docker.Host}}'], timeout=15).decode().strip()
    if not endpoint.startswith('unix:///'):
        raise RuntimeError('DOCKER_ENDPOINT_DENIED')
    endpoint = 'unix://' + str(pathlib.Path(endpoint[7:]).resolve(strict=True))
    daemon = subprocess.check_output([executable, '--host', endpoint, 'info', '--format', '{{.ID}}'], timeout=15).decode().strip()
    if not daemon:
        raise RuntimeError('DOCKER_DAEMON_IDENTITY_REQUIRED')
    if expected is not None and (executable, endpoint, daemon) != tuple(expected):
        raise RuntimeError('DOCKER_CONFIGURATION_CHANGED')
    return executable, endpoint


def immutable_context(output, identity):
    snapshot = output / 'source'
    encoded = regular(output / 'source-manifest.json')
    if digest(encoded) != identity:
        raise RuntimeError('CAPTURE_MANIFEST_CHANGED')
    manifest = json.loads(encoded)
    expected = dict(manifest)
    expected['release/source-manifest.json'] = digest(encoded)
    expected['release/source-identity'] = digest(identity.encode())
    if len(expected) > MAX_CONTEXT_ENTRIES:
        raise RuntimeError('BUILD_CONTEXT_LIMIT')
    stream = io.BytesIO()
    # PAX preserves long captured paths; USTAR cannot encode a leaf over 100
    # bytes. Only authored regular-file entries enter this immutable context.
    with tarfile.open(fileobj=stream, mode='w', format=tarfile.PAX_FORMAT,
                      encoding='utf-8', errors='strict') as archive:
        for name, expected_digest in sorted(expected.items()):
            data = safe_read(snapshot, name)
            if len(data) > MAX_CONTEXT_FILE_BYTES or digest(data) != expected_digest:
                raise RuntimeError('BUILD_CONTEXT_SOURCE_MISMATCH')
            entry = tarfile.TarInfo(name)
            entry.size = len(data)
            entry.mode = 0o644
            entry.uid = entry.gid = entry.mtime = 0
            entry.uname = entry.gname = ''
            # Include extended path headers and block padding BEFORE writing.
            # Reserve one record for the end markers and final record padding.
            header_bytes = len(entry.tobuf(archive.format, archive.encoding, archive.errors))
            data_bytes = ((entry.size + tarfile.BLOCKSIZE - 1) // tarfile.BLOCKSIZE) * tarfile.BLOCKSIZE
            if stream.tell() + header_bytes + data_bytes > MAX_CONTEXT_BYTES - tarfile.RECORDSIZE:
                raise RuntimeError('BUILD_CONTEXT_LIMIT')
            archive.addfile(entry, io.BytesIO(data))
    context = stream.getvalue()
    if len(context) > MAX_CONTEXT_BYTES:
        raise RuntimeError('BUILD_CONTEXT_LIMIT')
    return context


def build_capture(output, tag, expected_docker=None):
    output = pathlib.Path(output)
    try:
        identity = validate_capture(output)
        if not isinstance(tag, str) or not tag or tag.startswith('-') or any(c.isspace() for c in tag):
            raise RuntimeError('BUILD_TAG_DENIED')
        if any((output / name).exists() or (output / name).is_symlink() for name in BUILD_ARTIFACTS):
            raise RuntimeError('BUILD_OUTPUT_ALREADY_EXISTS')
        # Complete immutable local work before the first Docker invocation.
        write_new(output / 'build-reserved', canonical({'source_manifest_sha256': identity}))
        context = immutable_context(output, identity)
    except Exception as error:
        raise LocalBuildFailure('source-context', error) from error
    executable, endpoint = docker_configuration(expected_docker)
    validate_capture(output)
    with (output / 'build.log').open('x') as log:
        result = subprocess.run([executable, '--host', endpoint, 'build', '--iidfile', str(output / 'image.iid'),
                                 '--label', 'cdo.source_manifest=' + identity, '-t', tag,
                                 '-f', 'docker/ReleaseDockerfile', '-'],
                                input=context, stdout=log, stderr=subprocess.STDOUT)
    write_new(output / 'build-result.json', canonical({'source_manifest_sha256': identity, 'exit_code': result.returncode,
              'tag': tag, 'built_at': time.time(), 'docker_executable': executable, 'docker_endpoint': endpoint,
              'runtime_certified': False, 'production_admission': 'DENIED'}))
    if result.returncode:
        raise RuntimeError('DOCKER_BUILD_FAILED')
    image_id = regular(output / 'image.iid').decode().strip()
    if not re.fullmatch(r'sha256:[a-f0-9]{64}', image_id):
        raise RuntimeError('IMMUTABLE_BUILD_IMAGE_REQUIRED')
    images = json.loads(subprocess.check_output([executable, '--host', endpoint, 'image', 'inspect', image_id], timeout=30))
    if len(images) != 1 or images[0].get('Id') != image_id or images[0].get('Config', {}).get('Labels', {}).get('cdo.source_manifest') != identity:
        raise RuntimeError('BUILD_IMAGE_IDENTITY_MISMATCH')
    validate_capture(output)
    write_new(output / 'image-identity.json', canonical({'image_id': image_id, 'repo_digests': images[0].get('RepoDigests', []),
              'source_manifest_sha256': identity, 'runtime_certified': False, 'production_admission': 'DENIED'}))
    return image_id


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', required=True)
    parser.add_argument('--tag')
    parser.add_argument('--docker-executable')
    parser.add_argument('--docker-endpoint')
    parser.add_argument('--daemon-id')
    parser.add_argument('--failure-token', help='Parent-generated invocation identity for typed failure evidence')
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--capture-only', action='store_true')
    mode.add_argument('--build-existing', action='store_true')
    args = parser.parse_args(argv)
    if not args.capture_only and not args.tag:
        parser.error('--tag required for building')
    if args.capture_only and args.tag:
        parser.error('--capture-only does not accept --tag')
    pins = (args.docker_executable, args.docker_endpoint, args.daemon_id)
    if any(pins) and not all(pins):
        parser.error('Docker executable, endpoint and daemon pins require all three')
    output = pathlib.Path(args.output).absolute()
    if output.is_symlink():
        raise RuntimeError('CAPTURE_DIRECTORY_REQUIRED')
    if args.failure_token and str(uuid.UUID(args.failure_token)) != args.failure_token:
        raise RuntimeError('BUILDER_FAILURE_TOKEN_INVALID')
    try:
        if not args.build_existing:
            try:
                identity = capture(pathlib.Path(__file__).resolve().parents[1], output)
            except Exception as error:
                raise LocalBuildFailure('source-capture', error) from error
            if args.capture_only:
                print(identity)
                return 0
        print(build_capture(output, args.tag, pins if all(pins) else None))
    except Exception as error:
        if args.failure_token and output.is_dir() and not output.is_symlink():
            local = isinstance(error, LocalBuildFailure)
            evidence = {'version': 1, 'token': args.failure_token,
                        'classification': 'LOCAL_BUILD_FAILURE' if local else 'RUNTIME_UNKNOWN',
                        'phase': error.phase if local else 'docker-or-post-docker',
                        'docker_calls_attempted': not local,
                        'error_type': type(error.__cause__ if local else error).__name__}
            try:
                write_new(output / 'build-failure.json', canonical(evidence))
            except OSError:
                pass  # No valid receipt means UNKNOWN to the parent, never safe cleanup.
        raise
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
