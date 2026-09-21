#!/usr/bin/env python3
"""Assemble and publish Kit images from prebuilt driver and engine artifacts."""
import argparse
import hashlib
import io
import json
import os
import pathlib
import platform
import re
import shutil
import struct
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request
import zipfile

ROOT = pathlib.Path(__file__).resolve().parent
PLATFORMS = {'linux/amd64', 'linux/arm64'}


def run(args, **kwargs):
    print('+', ' '.join(map(str, args)), file=sys.stderr, flush=True)
    return subprocess.run(list(map(str, args)), check=True, text=True, **kwargs)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def python_requirements(lock):
    """Render pip input from the release; never resolve or update dependencies."""
    lines = []
    seen = set()
    for package in lock['python_dependencies']:
        name, version = package['name'], package['version']
        identity = re.sub(r'[-_.]+', '-', name).lower()
        if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.-]*', name) or identity in seen:
            raise ValueError('Invalid or duplicate Python dependency: ' + name)
        seen.add(identity)
        if not re.fullmatch(r'[0-9][A-Za-z0-9.+!-]*', version):
            raise ValueError('Python dependencies require exact versions')
        hashes = package['hashes']
        if not hashes or any(not re.fullmatch(r'[a-f0-9]{64}', h) for h in hashes):
            raise ValueError('Python dependencies require SHA-256 hashes')
        if package['source']['sha256'] not in hashes:
            raise ValueError('Python source checksum must be included in package hashes')
        if identity == 'ansible-core' and version != lock['ansible_core']:
            raise ValueError('Ansible version differs from Python dependencies')
        lines.append(name + '==' + version + ''.join(' \\\n    --hash=sha256:' + h for h in hashes))
    if 'ansible-core' not in seen:
        raise ValueError('Python dependencies must include ansible-core')
    return '\n'.join(lines) + '\n'


def native_platform():
    arch = {'aarch64': 'arm64', 'arm64': 'arm64', 'x86_64': 'amd64'}.get(platform.machine())
    return platform.system().lower() + '/' + str(arch)


def drivers_dist():
    return (ROOT / os.environ.get('DRIVERS_DIST_DIR', 'drivers/dist')).resolve()


def configuration(driver_root, workspace, tools=None, drivers=None, target=None):
    selected = {}
    for release in drivers or bundle_lock()['drivers']:
        name, version = release['name'], release['version']
        base, _, pre = version.partition('-')
        identity = name + '-' + base.replace('.', '-') + ('-pre-' + pre.encode().hex() if pre else '')
        path = pathlib.Path(driver_root) / name / version
        if target:
            path = path / target
        args = ['--tool', str(tools[name])] if tools and name in tools else []
        selected[identity] = {'path': str(path / 'driver'), 'args': args}
    return {'main': {'port': 8080}, 'logger': {'type': 'slog', 'settings': {'level': 'info', 'format': 'json'}},
            'engine': {'dataDir': str(workspace), 'drivers': selected}, 'handlers': [{'type': 'connect-engine'}]}


def local_tools():
    lock = bundle_lock()
    tofu = ROOT / f".cache/tools/opentofu/{lock['opentofu']['version']}/tofu"
    ansible = ROOT / f".cache/ansible-{lock['ansible_core']}/bin/ansible-playbook"
    return pathlib.Path(os.environ.get('OPENTOFU_PATH', tofu)).resolve(), pathlib.Path(os.environ.get('ANSIBLE_PATH', ansible)).resolve()


def bundle_output():
    return (ROOT / os.environ.get('KIT_DIST_DIR', 'dist')) / 'bundles' / project_version()


def find_python(override=None):
    candidates = [override] if override else [sys.executable, 'python3.14', 'python3.13', 'python3.12', 'python3']
    seen = set()
    for candidate in candidates:
        executable = shutil.which(candidate)
        if not executable or executable in seen:
            continue
        seen.add(executable)
        try:
            version = subprocess.check_output(
                [executable, '-c', 'import sys; print(".".join(map(str, sys.version_info[:3])))'],
                text=True, stderr=subprocess.DEVNULL, timeout=10,
            ).strip()
            parts = tuple(int(part) for part in version.split('.'))
        except (OSError, subprocess.SubprocessError, ValueError):
            continue
        if len(parts) == 3 and (3, 12) <= parts[:2] <= (3, 14):
            return executable, version
    if override:
        raise ValueError(f'PYTHON={override} must point to a working Python 3.12–3.14 interpreter.')
    raise ValueError(
        'Ansible requires Python 3.12–3.14; no supported interpreter was found. '
        'Install one alongside your system Python, then rerun task setup '
        'or set PYTHON=/path/to/python3.14.'
    )


def read_driver(dist, selection, target, lock):
    """Accept only an existing, complete driver build; never compile a fallback."""
    name, version = selection['name'], selection['version']
    if not re.fullmatch(r'[a-z][a-z0-9-]*', name) or not re.fullmatch(r'[0-9A-Za-z.-]+', version) or version in ('.', '..'):
        raise ValueError('Invalid driver selection')
    folder = pathlib.Path(dist) / name / version / target
    path = folder / 'build.json'
    if not path.is_file():
        raise ValueError(f'Missing prebuilt driver {name}:{version} for {target}; run task build in drivers/')
    record = json.loads(path.read_text())
    release = record['release']
    if record['schema'] != 1 or (release['name'], release['version']) != (name, version) or record['platform'] != target:
        raise ValueError('Driver build identity mismatch')
    expected_engine = {k: lock['inframill'][k] for k in ('version', 'revision')}
    if record['inframill'] != expected_engine:
        raise ValueError('Driver and image target different Inframill releases')
    tool_version = {'opentofu': lock['opentofu']['version'], 'ansible': lock['ansible_core']}.get(name)
    if release['tool_version'] != tool_version or release['protocol'] != 1 or set(release['platforms']) != set(lock['platforms']):
        raise ValueError('Driver tool/platform/protocol differs from the image release')
    actual = set()
    for file in folder.rglob('*'):
        if file.is_symlink():
            raise ValueError('Driver output contains a link')
        if file.is_file() and file != path:
            actual.add(str(file.relative_to(folder)))
    if actual != set(record['files']) or 'driver' not in actual or not any(p.startswith('sources/') for p in actual):
        raise ValueError('Driver output is incomplete or contains unrecorded files')
    for name, digest in record['files'].items():
        relative = pathlib.PurePosixPath(name)
        if relative.is_absolute() or '..' in relative.parts or not re.fullmatch(r'[0-9a-f]{64}', digest):
            raise ValueError('Invalid driver output record')
        if hashlib.sha256((folder / name).read_bytes()).hexdigest() != digest:
            raise ValueError('Driver output checksum mismatch: ' + name)
    for name, digest in record['source_files'].items():
        if record['files'].get('sources/' + name) != digest:
            raise ValueError('Driver source snapshot differs from the build record')
    data = read_binary(folder, {'path': 'driver', 'sha256': record['files']['driver']}, target.split('/')[1])
    return folder, record, data


def read_binary(dist, entry, arch, system='linux'):
    relative = pathlib.PurePosixPath(entry['path'])
    if relative.is_absolute() or '..' in relative.parts or not relative.parts:
        raise ValueError('Inframill binary path must be relative to its dist directory')
    expected = entry['sha256']
    if not re.fullmatch(r'[0-9a-f]{64}', expected):
        raise ValueError('Inframill binary requires a pinned SHA-256 checksum')
    path = pathlib.Path(dist) / relative
    if not path.is_file():
        raise ValueError(f'Missing prebuilt Inframill binary: {path}; provide both Linux releases in the sibling dist directory')
    with path.open('rb') as stream:
        data = stream.read((128 << 20) + 1)
    if len(data) > 128 << 20:
        raise ValueError('Inframill binary exceeds size limit')
    if hashlib.sha256(data).hexdigest() != expected:
        raise ValueError(f'Inframill binary checksum mismatch: {path}')
    if system == 'linux':
        if len(data) < 64 or data[:6] != b'\x7fELF\x02\x01' or data[7] not in (0, 3):
            raise ValueError(f'Expected a Linux 64-bit little-endian ELF binary: {path}')
        machine = struct.unpack_from('<H', data, 18)[0]
        expected_machine = {'amd64': 62, 'arm64': 183}[arch]
    elif system == 'darwin':
        if len(data) < 32 or data[:4] != b'\xcf\xfa\xed\xfe':
            raise ValueError(f'Expected a Darwin Mach-O binary: {path}')
        machine = struct.unpack_from('<I', data, 4)[0]
        expected_machine = {'amd64': 0x1000007, 'arm64': 0x100000c}[arch]
    else:
        raise ValueError('Unsupported binary platform: ' + system)
    if machine != expected_machine:
        raise ValueError(f'Inframill binary architecture mismatch: expected {arch}: {path}')
    return data


def valid_version(value, metadata=False):
    pattern = r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?'
    if metadata:
        pattern += r'(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?'
    if not isinstance(value, str) or not re.fullmatch(pattern, value):
        return False
    pre = value.split('+', 1)[0].partition('-')[2]
    return all(not (part.isdigit() and len(part) > 1 and part.startswith('0')) for part in pre.split('.'))


def project_version():
    try:
        version = (ROOT / 'VERSION').read_bytes().removesuffix(b'\n').decode('ascii')
    except (OSError, UnicodeError) as error:
        raise ValueError('VERSION must be a readable file containing one SemVer value') from error
    if not valid_version(version, metadata=True):
        raise ValueError('VERSION must contain exactly one SemVer value without whitespace or a v prefix')
    return version


def validate_release(lock, version):
    allowed = {'version', 'drivers', 'platforms', 'base_image', 'base_platform_digests',
               'source_date_epoch', 'inframill', 'opentofu', 'ansible_core', 'python',
               'python_dependencies', 'debian_snapshot'}
    if set(lock) - allowed:
        raise ValueError('Unknown image release fields: ' + ', '.join(sorted(set(lock) - allowed)))
    if not valid_version(version) or lock['version'] != version:
        raise ValueError('Release lock identity mismatch or invalid exact image version')
    engine = lock['inframill']
    if not valid_version(engine['version']):
        raise ValueError('Exact Inframill SemVer required in image release')
    if version.split('.')[:2] != engine['version'].split('.')[:2]:
        raise ValueError('Kit and Inframill must match major/minor versions')
    if set(engine) != {'version', 'revision', 'binaries'} or not re.fullmatch(r'[0-9a-f]{40}', engine['revision']):
        raise ValueError('Invalid Inframill release pin')
    if set(lock['platforms']) != PLATFORMS or len(lock['platforms']) != 2:
        raise ValueError('Both Linux platforms required')
    if not lock['drivers']:
        raise ValueError('Image release has no selected drivers')
    selected = set()
    for entry in lock['drivers']:
        if set(entry) != {'name', 'version'} or not re.fullmatch(r'[a-z][a-z0-9-]*', entry['name']) or not valid_version(entry['version']):
            raise ValueError('Invalid driver selection')
        key = (entry['name'], entry['version'])
        if key in selected:
            raise ValueError('Duplicate image driver requirement')
        selected.add(key)
    for target, entry in engine['binaries'].items():
        if target not in PLATFORMS | {'darwin/amd64', 'darwin/arm64'} or set(entry) != {'path', 'sha256'}:
            raise ValueError('Invalid engine binary selection')
        path = pathlib.PurePosixPath(entry['path'])
        if path.is_absolute() or '..' in path.parts or not path.parts or not re.fullmatch(r'[0-9a-f]{64}', entry['sha256']):
            raise ValueError('Invalid engine binary path or checksum')
    if not PLATFORMS <= set(engine['binaries']):
        raise ValueError('Both prebuilt Linux engine binaries required')
    python_requirements(lock)
    return lock


def bundle_lock(version=None):
    version = version or project_version()
    if not valid_version(version):
        raise ValueError('Exact image SemVer required; build metadata is not a registry tag')
    lock = json.loads((ROOT / f'releases/{version}.lock.json').read_text())
    return validate_release(lock, version)


def image_reference(version):
    registry = os.environ.get('REGISTRY', 'ghcr.io')
    namespace = os.environ.get('NAMESPACE', 'inframill')
    if not re.fullmatch(r'[a-z][a-z0-9-]*', namespace) or not re.fullmatch(r'[A-Za-z0-9.-]+(?::[0-9]+)?', registry):
        raise ValueError('Registry must be a hostname[:port] and namespace a lowercase name')
    return registry + '/' + namespace + '/kit:' + version


def build_image():
    lock = bundle_lock()
    version = lock['version']
    reference = image_reference(version)
    output = bundle_output()
    if output.exists():
        raise ValueError('Image output already exists; push it or explicitly remove unpublished development output before rebuilding')
    engine_dist = (ROOT / os.environ.get('INFRAMILL_DIST_DIR', '../inframill/dist')).resolve()
    binaries = {arch: read_binary(engine_dist, lock['inframill']['binaries']['linux/' + arch], arch)
                for arch in ('amd64', 'arm64')}
    drivers = {(entry['name'], entry['version'], target): read_driver(drivers_dist(), entry, target, lock)
               for entry in lock['drivers'] for target in lock['platforms']}
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.bundle-', dir=output.parent) as td:
        stage = pathlib.Path(td)
        context = stage / 'context'
        sources = context / 'sources'
        sources.mkdir(parents=True)
        for name in ['VERSION', 'build.py', 'LICENSE', 'README.md', 'docs', 'releases', 'runtime', 'tests', 'Taskfile.yml']:
            path = ROOT / name
            if path.is_dir():
                shutil.copytree(path, sources / name, ignore=shutil.ignore_patterns('__pycache__'))
            else:
                shutil.copy2(path, sources / name)
        for arch in ('amd64', 'arm64'):
            bindir = context / 'bin/linux' / arch
            bindir.mkdir(parents=True)
            (bindir / 'inframill').write_bytes(binaries[arch])
            (bindir / 'inframill').chmod(0o755)
            for entry in lock['drivers']:
                folder, record, data = drivers[(entry['name'], entry['version'], 'linux/' + arch)]
                destination = context / 'drivers' / arch / entry['name'] / entry['version']
                destination.mkdir(parents=True)
                (destination / 'driver').write_bytes(data)
                (destination / 'driver').chmod(0o755)
                shutil.copy2(folder / 'build.json', destination / 'build.json')
                for name in ('sources', 'notices'):
                    if (folder / name).is_dir():
                        shutil.copytree(folder / name, destination / name)
        for name in ('Dockerfile', 'NOTICES.md', 'inframill.yaml'):
            shutil.copy2(ROOT / 'runtime' / name, context / name)
        shutil.copy2(ROOT / 'build.py', context / 'build.py')
        (context / 'requirements.lock').write_text(python_requirements(lock))
        lock_bytes = (ROOT / f'releases/{version}.lock.json').read_bytes()
        if json.loads(lock_bytes) != lock:
            raise ValueError('Image release changed during build preparation')
        (context / 'bundle.lock.json').write_bytes(lock_bytes)
        archive_path = stage / 'image.tar'
        args = ['docker', 'buildx', 'build', '--platform', 'linux/amd64,linux/arm64',
                '--provenance=false', '--sbom=false']
        for name, value in {'OPENTOFU_VERSION': lock['opentofu']['version'], 'ANSIBLE_VERSION': lock['ansible_core'],
                            'INFRAMILL_VERSION': lock['inframill']['version'], 'DEBIAN_SNAPSHOT': lock['debian_snapshot'],
                            'BASE_IMAGE': lock['base_image'], 'SOURCE_DATE_EPOCH': lock['source_date_epoch']}.items():
            args.extend(['--build-arg', name + '=' + str(value)])
        run(args + ['--output', 'type=oci,dest=' + str(archive_path) + ',rewrite-timestamp=true', '--tag', reference, context])
        layout = stage / 'oci'
        layout.mkdir()
        with tarfile.open(archive_path) as archive:
            for item in archive:
                path = pathlib.PurePosixPath(item.name)
                if path.is_absolute() or '..' in path.parts or not (item.isfile() or item.isdir()):
                    raise ValueError('Unsafe OCI archive entry')
                archive.extract(item, layout)
        index = json.loads((layout / 'index.json').read_text())
        if len(index['manifests']) != 1:
            raise ValueError('Expected one combined image index')
        descriptor = index['manifests'][0]
        descriptor.setdefault('annotations', {})['org.opencontainers.image.ref.name'] = version
        (layout / 'index.json').write_text(json.dumps(index, separators=(',', ':')))
        manifest = json.loads((layout / 'blobs/sha256' / descriptor['digest'].split(':')[1]).read_text())
        if {(m.get('platform', {}).get('os'), m.get('platform', {}).get('architecture')) for m in manifest['manifests']} != {('linux', 'amd64'), ('linux', 'arm64')}:
            raise ValueError('Missing runtime platform')
        record = {'version': version, 'lock_sha256': digest(lock_bytes), 'descriptor': descriptor,
                  'dependency_revision': lock['inframill']['revision'], 'inframill': lock['inframill'],
                  'platforms': manifest['manifests'],
                  'source_files': {str(p.relative_to(sources)): digest(p.read_bytes()) for p in sources.rglob('*') if p.is_file()},
                  'drivers': {name + '@' + ver + '/' + target: record for (name, ver, target), (_, record, _) in drivers.items()},
                  'binaries': {str(p.relative_to(context)): digest(p.read_bytes()) for p in (context / 'bin').rglob('*') if p.is_file()}}
        completed = stage / 'complete'
        completed.mkdir()
        shutil.move(str(layout), completed / 'oci')
        (completed / 'build.json').write_text(json.dumps(record, indent=2) + '\n')
        verify_image(completed, lock)
        completed.rename(output)
    print('Built local image:', output, 'digest:', descriptor['digest'])
    return output


def verify_graph(layout, descriptor, seen=None):
    seen = set() if seen is None else seen
    checksum, size = descriptor['digest'], descriptor['size']
    if not re.fullmatch(r'sha256:[0-9a-f]{64}', checksum) or not isinstance(size, int) or not 0 <= size <= 16 << 30:
        raise ValueError('Invalid OCI descriptor')
    if (checksum, size) in seen:
        return
    path = layout / 'blobs/sha256' / checksum.split(':')[1]
    if path.is_symlink() or path.stat().st_size != size:
        raise ValueError('OCI blob size mismatch or link')
    checksummer = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b''):
            checksummer.update(chunk)
    if 'sha256:' + checksummer.hexdigest() != checksum:
        raise ValueError('OCI blob checksum mismatch')
    seen.add((checksum, size))
    media_type = descriptor['mediaType']
    if media_type in ('application/vnd.oci.image.index.v1+json', 'application/vnd.oci.image.manifest.v1+json'):
        if size > 1 << 20:
            raise ValueError('Oversized OCI manifest')
        manifest = json.loads(path.read_text())
        children = manifest['manifests'] if 'manifests' in manifest else [manifest['config'], *manifest['layers']]
        for child in children:
            verify_graph(layout, child, seen)


def verify_image(output=None, lock=None):
    lock = lock or bundle_lock()
    output = output or bundle_output()
    record = json.loads((output / 'build.json').read_text())
    lock_bytes = (ROOT / f"releases/{lock['version']}.lock.json").read_bytes()
    if record['version'] != lock['version'] or record['lock_sha256'] != digest(lock_bytes):
        raise ValueError('Image release lock differs from its build record')
    layout = output / 'oci'
    index = json.loads((layout / 'index.json').read_text())
    if len(index['manifests']) != 1:
        raise ValueError('Expected one OCI root descriptor')
    descriptor = index['manifests'][0]
    if any(descriptor[k] != record['descriptor'][k] for k in ('digest', 'size', 'mediaType')):
        raise ValueError('OCI root differs from build record')
    verify_graph(layout, descriptor)
    return descriptor


def load_image(output, reference):
    layout = output / 'oci'
    index = json.loads((layout / 'index.json').read_text())
    for descriptor in index['manifests']:
        descriptor['annotations'] = {'org.opencontainers.image.ref.name': reference.rsplit(':', 1)[1],
                                     'io.containerd.image.name': reference}
    data = json.dumps(index, separators=(',', ':')).encode()
    with tempfile.TemporaryFile() as stream:
        with tarfile.open(fileobj=stream, mode='w') as archive:
            for path in sorted(layout.rglob('*')):
                if path != layout / 'index.json':
                    archive.add(path, arcname=str(path.relative_to(layout)), recursive=False)
            header = tarfile.TarInfo('index.json')
            header.size = len(data)
            archive.addfile(header, io.BytesIO(data))
        stream.seek(0)
        run(['docker', 'load'], stdin=stream)


def remote_digest(reference):
    result = subprocess.run(['docker', 'buildx', 'imagetools', 'inspect', '--format', '{{json .Manifest}}', reference],
                            capture_output=True, text=True)
    if result.returncode:
        message = result.stderr.lower()
        if 'manifest unknown' in message or 'no such manifest' in message or ': not found' in message:
            return None
        raise RuntimeError('Cannot inspect remote image: ' + result.stderr.strip())
    value = json.loads(result.stdout)['digest']
    if not re.fullmatch(r'sha256:[0-9a-f]{64}', value):
        raise ValueError('Remote image has an invalid digest')
    return value


def push_image():
    lock = bundle_lock()
    output = bundle_output()
    reference = image_reference(lock['version'])
    descriptor = verify_image(output, lock)
    guard = output / '.push-lock'
    fd = os.open(guard, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    os.close(fd)
    try:
        existing = remote_digest(reference)
        if existing is not None and existing != descriptor['digest']:
            raise ValueError('Release tag already points to different content')
        if existing is None:
            load_image(output, reference)
            # Recheck after the load; cross-workstation publication still needs registry policy.
            existing = remote_digest(reference)
            if existing is not None and existing != descriptor['digest']:
                raise ValueError('Concurrent conflicting publication')
            if existing is None:
                run(['docker', 'push', reference])
            if remote_digest(reference) != descriptor['digest']:
                raise ValueError('Published digest mismatch')
    finally:
        guard.unlink()
    print(reference.rsplit(':', 1)[0] + '@' + descriptor['digest'])


def copy_engine():
    lock = bundle_lock()
    target = native_platform()
    if target not in lock['inframill']['binaries']:
        raise ValueError('Unsupported native engine platform: ' + target)
    system, arch = target.split('/')
    entry = lock['inframill']['binaries'][target]
    folder = (ROOT / os.environ.get('INFRAMILL_DIST_DIR', '../inframill/dist')).resolve()
    data = read_binary(folder, entry, arch, system=system)
    source = folder / entry['path']
    version = subprocess.check_output([str(source), '--version'], text=True).strip()
    if version != lock['inframill']['version']:
        raise ValueError('Native engine version mismatch')
    destination = ROOT / 'bin/inframill'
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_bytes(data)
    destination.chmod(0o755)
    print('Copied:', destination)


def download(url, checksum, destination):
    destination = pathlib.Path(destination)
    destination.parent.mkdir(parents=True, exist_ok=True)
    hasher = hashlib.sha256()
    with urllib.request.urlopen(url, timeout=120) as response, destination.open('wb') as stream:
        for chunk in iter(lambda: response.read(1 << 20), b''):
            hasher.update(chunk)
            stream.write(chunk)
    if hasher.hexdigest() != checksum:
        raise ValueError('Checksum mismatch for ' + url)
    return destination


def fetch_runtime(arch):
    lock = json.loads(pathlib.Path('/build/bundle.lock.json').read_text())
    asset = lock['opentofu']['assets']['linux/' + arch]
    archive_path = download(asset['url'], asset['sha256'], '/build/tofu.zip')
    output = pathlib.Path('/opt/inframill/tools/opentofu') / lock['opentofu']['version']
    output.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(archive_path) as archive:
        (output / 'tofu').write_bytes(archive.read('tofu'))
        for name in archive.namelist():
            if 'LICENSE' in name.upper() and '/' not in name:
                (output / name).write_bytes(archive.read(name))
    (output / 'tofu').chmod(0o755)
    for source in [p['source'] for p in lock['python_dependencies']] + [lock['opentofu']['source']]:
        download(source['url'], source['sha256'], pathlib.Path('/usr/share/inframill/sources') / source['url'].rsplit('/', 1)[-1])


def setup():
    python, version = find_python(os.environ.get('PYTHON'))
    lock = bundle_lock()
    print(f'Using Python {version}: {python}', flush=True)
    asset = lock['opentofu']['assets'][native_platform()]
    folder = ROOT / f".cache/tools/opentofu/{lock['opentofu']['version']}"
    archive_path = download(asset['url'], asset['sha256'], folder / 'download.zip')
    with zipfile.ZipFile(archive_path) as archive:
        (folder / 'tofu').write_bytes(archive.read('tofu'))
    (folder / 'tofu').chmod(0o755)
    archive_path.unlink()
    venv = ROOT / f".cache/ansible-{lock['ansible_core']}"
    run([python, '-m', 'venv', venv])
    with tempfile.TemporaryDirectory(prefix='kit-python-') as td:
        requirements = pathlib.Path(td) / 'requirements.lock'
        requirements.write_text(python_requirements(lock))
        run([venv / 'bin/python', '-m', 'pip', 'install', '--require-hashes', '-r', requirements])
    print('Pinned test tools installed under .cache')


def lock_python(report_path):
    path = ROOT / f'releases/{project_version()}.lock.json'
    lock = json.loads(path.read_text())
    report = json.loads(pathlib.Path(report_path).read_text())
    packages = []
    for item in sorted(report['install'], key=lambda x: x['metadata']['name'].lower()):
        name, version = item['metadata']['name'], item['metadata']['version']
        with urllib.request.urlopen(f'https://pypi.org/pypi/{name}/{version}/json', timeout=60) as response:
            metadata = json.load(response)
        source = next(f for f in metadata['urls'] if f['packagetype'] == 'sdist')
        packages.append({'name': name, 'version': version,
                         'hashes': sorted({f['digests']['sha256'] for f in metadata['urls']}),
                         'source': {'url': source['url'], 'sha256': source['digests']['sha256']}})
    lock['python_dependencies'] = packages
    python_requirements(lock)
    path.write_text(json.dumps(lock, indent=2) + '\n')


def verify_remote(reference):
    if not re.search(r'@sha256:[0-9a-f]{64}$', reference):
        raise ValueError('Reference must be digest-pinned')
    data = json.loads(run(['docker', 'buildx', 'imagetools', 'inspect', '--raw', reference], capture_output=True).stdout)
    if {(m.get('platform', {}).get('os'), m.get('platform', {}).get('architecture')) for m in data.get('manifests', [])} != {('linux', 'amd64'), ('linux', 'arm64')}:
        raise ValueError('Missing runtime architecture')
    lock = bundle_lock()
    for arch in ('arm64', 'amd64'):
        container = run(['docker', 'run', '-d', '--rm', '--platform', 'linux/' + arch, reference], capture_output=True).stdout.strip()
        try:
            deadline = time.monotonic() + 45
            while True:
                result = subprocess.run(['docker', 'exec', container, 'inframill', 'server', 'driver', 'list', '-o', 'json'], capture_output=True, text=True)
                if result.returncode == 0:
                    drivers = json.loads(result.stdout)['drivers']
                    expected = set(configuration('/opt/inframill/drivers', '/var/lib/inframill', drivers=lock['drivers'])['engine']['drivers'])
                    if {d['id'] for d in drivers} != expected or any(d['status'] != 'DRIVER_STATUS_AVAILABLE' for d in drivers):
                        raise ValueError('Published driver discovery differs from the release')
                    break
                if time.monotonic() >= deadline:
                    raise RuntimeError('Published image did not become ready: ' + result.stderr)
                time.sleep(0.1)
        finally:
            run(['docker', 'stop', container], stdout=subprocess.DEVNULL)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    for command in ('build', 'push', 'publish', 'validate', 'version', 'config', 'setup', 'copy-engine', 'test'):
        commands.add_parser(command)
    verify = commands.add_parser('verify', help='verify local output, or a published image by digest')
    verify.add_argument('--reference')
    pins = commands.add_parser('lock-python', help='explicitly update Python dependency pins from a pip report')
    pins.add_argument('report', type=pathlib.Path)
    fetch = commands.add_parser('fetch-runtime', help='internal Docker build step')
    fetch.add_argument('arch', choices=['amd64', 'arm64'])
    args = parser.parse_args(argv)
    if args.command == 'build':
        build_image()
    elif args.command == 'push':
        push_image()
    elif args.command == 'publish':
        build_image()
        push_image()
    elif args.command == 'validate':
        bundle_lock()
        print('Image release validated')
    elif args.command == 'version':
        print(project_version())
    elif args.command == 'verify':
        if args.reference:
            verify_remote(args.reference)
        else:
            print(verify_image()['digest'])
    elif args.command == 'copy-engine':
        copy_engine()
    elif args.command == 'setup':
        setup()
    elif args.command == 'config':
        tofu, ansible = local_tools()
        print(json.dumps(configuration(drivers_dist(), os.environ.get('DATA_DIR', str(ROOT / '.inframill')),
                                       {'opentofu': tofu, 'ansible': ansible}, target=native_platform()), indent=2))
    elif args.command == 'lock-python':
        lock_python(args.report)
    elif args.command == 'fetch-runtime':
        fetch_runtime(args.arch)
    elif args.command == 'test':
        run([sys.executable, '-m', 'unittest', 'discover', '-s', 'tests', '-p', 'test_*.py'], cwd=ROOT)


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, KeyError, RuntimeError, subprocess.CalledProcessError) as error:
        detail = error.stderr if isinstance(error, subprocess.CalledProcessError) else None
        sys.exit(detail.strip() if detail else str(error))
