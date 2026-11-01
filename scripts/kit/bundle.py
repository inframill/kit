"""Package, verify and publish prepared Kit artifacts; never compile or test."""
import argparse
import hashlib
import gzip
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import tarfile
import tempfile

from .artifacts import verify_binary
from .engine import MODULE, verify_prepared
from .project import (ROOT, LINUX, bundle_output, image_reference,
                      local_executable, project_version, provider_releases, run,
                      runtime_settings)


def checksum(path):
    result = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b''):
            result.update(chunk)
    return result.hexdigest()


def write_checksums(folder):
    paths = sorted(p for p in folder.rglob('*') if p.is_file() and p != folder / 'SHA256SUMS')
    (folder / 'SHA256SUMS').write_text(''.join(checksum(p) + '  ' + p.relative_to(folder).as_posix() + '\n' for p in paths))


def verify(folder):
    folder = Path(folder)
    expected = {}
    for line in (folder / 'SHA256SUMS').read_text().splitlines():
        digest, name = line.split('  ', 1)
        path = PurePosixPath(name)
        if not re.fullmatch(r'[0-9a-f]{64}', digest) or path.is_absolute() or '..' in path.parts or name in expected:
            raise ValueError('Invalid checksum entry')
        expected[name] = digest
    actual = set()
    for path in folder.rglob('*'):
        if path.is_symlink():
            raise ValueError('Release bundle contains a link')
        if path.is_file() and path != folder / 'SHA256SUMS':
            name = path.relative_to(folder).as_posix()
            actual.add(name)
            if expected.get(name) != checksum(path):
                raise ValueError('Release checksum mismatch: ' + name)
    if actual != expected.keys() or not {'manifest.json', 'image.oci.tar', 'sources.tar.gz'} <= actual:
        raise ValueError('Incomplete release bundle')
    metadata = json.loads((folder / 'manifest.json').read_text())
    if metadata['schema'] != 1 or set(metadata['platforms']) != set(LINUX) or not re.fullmatch(r'sha256:[0-9a-f]{64}', metadata['image']['digest']):
        raise ValueError('Invalid release manifest')
    return metadata


def loaded_digest(reference):
    descriptor = json.loads(subprocess.check_output(['docker', 'image', 'inspect', reference, '--format', '{{json .Descriptor}}'], text=True))
    return descriptor['digest']


def load_image(folder, reference=None):
    metadata = verify(folder)
    original = metadata['image']['reference']
    run(['docker', 'load', '--input', Path(folder) / 'image.oci.tar'])
    if loaded_digest(original) != metadata['image']['digest']:
        raise ValueError('Loaded image digest differs from bundle')
    if reference and reference != original:
        run(['docker', 'tag', original, reference])
    return metadata


def source_files():
    paths = subprocess.check_output(['git', 'ls-files', '-co', '--exclude-standard', '-z'], cwd=ROOT).decode().split('\0')
    roots = {'cmd', 'internal', 'pkg', 'runtime', 'scripts', 'tests', 'blueprints', 'docs', '.agents'}
    files = {'go.mod', 'go.sum', 'toolai.json', 'Taskfile.yml', 'README.md', 'LICENSE'}
    result = {}
    for name in sorted(set(filter(None, paths))):
        path = ROOT / name
        if name not in files and PurePosixPath(name).parts[0] not in roots:
            continue
        if path.is_symlink():
            raise ValueError('Source snapshot contains a link: ' + name)
        if path.is_file():
            result[name] = checksum(path)
    return result


def capture():
    parent = ROOT / '.build'
    parent.mkdir(exist_ok=True)
    (parent / 'inputs.json').write_text(json.dumps(source_files(), sort_keys=True))


def check_inputs():
    if json.loads((ROOT / '.build/inputs.json').read_text()) != source_files():
        raise ValueError('Sources changed after build preparation; rerun toolai build')


def source_archive(target):
    check_inputs()
    epoch = int(runtime_settings()['SOURCE_DATE_EPOCH'])
    def normalize(info):
        info.uid = info.gid = 0
        info.uname = info.gname = ''
        info.mtime = epoch
        return info
    with target.open('wb') as stream, gzip.GzipFile(fileobj=stream, mode='wb', mtime=epoch, filename='') as compressed:
        with tarfile.open(fileobj=compressed, mode='w') as archive:
            for name in source_files():
                archive.add(ROOT / name, arcname='kit/' + name, recursive=False, filter=normalize)
    check_inputs()


def notices(target):
    shutil.copytree(ROOT / '.build/engine/notices', target)
    for pattern in ('LICENSE*', 'COPYING*', 'NOTICE*', 'AUTHORS*'):
        for path in ROOT.glob(pattern):
            if path.is_file() and not path.is_symlink():
                destination = target / ('github.com_inframill_kit@' + project_version()) / path.name
                destination.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(path, destination)


def verify_provider_build_info(info):
    prefix = '\tdep\t' + MODULE + '\t'
    if any(line.startswith(prefix) for line in info.splitlines()):
        raise ValueError('Provider binary still imports upstream Runmill; rerun toolai build')


def prepare(context):
    engine_root = ROOT / '.build/engine'
    metadata = json.loads((engine_root / 'engine.json').read_text())
    verify_prepared()
    providers = json.loads(subprocess.check_output([local_executable(), 'list'], text=True))
    expected = provider_releases()
    if providers != sorted(expected, key=lambda item: item['name'] + '-' + item['version']):
        raise ValueError('Compiled provider registry differs from sources')
    version = json.loads(subprocess.check_output([local_executable(), 'version'], text=True))['version']
    if version != project_version():
        raise ValueError('Provider binary version is stale; run toolai build')
    settings = runtime_settings()
    for provider in providers:
        key = {'opentofu': 'OPENTOFU_VERSION', 'ansible': 'ANSIBLE_VERSION'}.get(provider['name'])
        if provider['protocol'] != 1 or set(provider['platforms']) != set(LINUX) or key and provider['tool_version'] != settings[key]:
            raise ValueError('Provider runtime requirements differ from selected tools')
    binary_hashes = {}
    for platform in LINUX:
        system, arch = platform.split('/')
        folder = context / 'bin' / system / arch
        folder.mkdir(parents=True)
        for name, source in [('kit-exec', ROOT / f'bin/kit-exec-{system}-{arch}'), ('inframill', ROOT / f'bin/inframill-{system}-{arch}')]:
            data = source.read_bytes()
            verify_binary(data, platform)
            # Toolai embeds version injection and module identities in Go build info.
            info = subprocess.check_output(['go', 'version', '-m', source], text=True)
            expected_package = 'github.com/inframill/' + ('kit' if name == 'kit-exec' else 'inframill') + '/cmd'
            if expected_package not in info:
                raise ValueError('Unexpected binary package: ' + str(source))
            if name == 'kit-exec':
                verify_provider_build_info(info)
            destination = folder / name
            shutil.copy2(source, destination)
            binary_hashes[f'{platform}/{name}'] = checksum(destination)
    source_archive(context / 'sources.tar.gz')
    notices(context / 'notices')
    shutil.copytree(ROOT / 'runtime', context / 'runtime', ignore=shutil.ignore_patterns('__pycache__'))
    shutil.copy2(ROOT / 'runtime/inframill.yaml', context / 'inframill.yaml')
    record = {'schema': 1, 'version': project_version(), 'platforms': LINUX,
              'source_revision': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
              'source_dirty': bool(subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT, text=True).strip()),
              'inframill': metadata, 'providers': providers, 'runtime': settings,
              'go_version': subprocess.check_output(['go', 'env', 'GOVERSION'], text=True).strip(),
              'binaries': binary_hashes,
              'source_sha256': checksum(context / 'sources.tar.gz')}
    (context / 'manifest.json').write_text(json.dumps(record, indent=2) + '\n')
    return record


def build_image():
    parent = ROOT / '.build'
    parent.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='image-', dir=parent) as temporary:
        stage = Path(temporary)
        context = stage / 'context'
        context.mkdir()
        record = prepare(context)
        reference = image_reference()
        settings = runtime_settings()
        args = ['docker', 'buildx', 'build', '--platform', ','.join(LINUX), '--provenance=false', '--sbom=false',
                '--metadata-file', stage / 'buildx.json', '--tag', reference,
                '--build-arg', 'INFRAMILL_VERSION=' + record['inframill']['version'],
                '--build-arg', 'KIT_VERSION=' + record['version']]
        for name in ('BASE_IMAGE', 'SOURCE_DATE_EPOCH', 'DEBIAN_SNAPSHOT'):
            args += ['--build-arg', name + '=' + settings[name]]
        args += ['--file', context / 'runtime/Dockerfile', '--output', 'type=oci,dest=' + str(stage / 'image.oci.tar') + ',rewrite-timestamp=true', context]
        run(args)
        digest = json.loads((stage / 'buildx.json').read_text())['containerimage.digest']
        record['image'] = {'reference': reference, 'digest': digest}
        for name in ('sources.tar.gz', 'notices'):
            shutil.move(context / name, stage / name)
        shutil.rmtree(context)
        (stage / 'buildx.json').unlink()
        (stage / 'manifest.json').write_text(json.dumps(record, indent=2) + '\n')
        check_inputs()
        write_checksums(stage)
        verify(stage)
        destination = parent / 'image'
        if destination.exists():
            shutil.rmtree(destination)
        shutil.move(stage, destination)
    load_image(destination)
    print('Prepared image:', reference, digest)


def release():
    source = ROOT / '.build/image'
    metadata = verify(source)
    if metadata['version'] != project_version():
        raise ValueError('Prepared image version is stale')
    output = bundle_output()
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.release-', dir=output.parent) as temporary:
        stage = Path(temporary) / 'bundle'
        shutil.copytree(source, stage)
        verify(stage)
        if output.exists():
            shutil.rmtree(output)
        stage.rename(output)
    print('Release bundle:', output)


def remote_digest(reference):
    result = subprocess.run(['docker', 'buildx', 'imagetools', 'inspect', '--format', '{{json .Manifest}}', reference], capture_output=True, text=True)
    if result.returncode:
        if any(word in result.stderr.lower() for word in ('manifest unknown', 'no such manifest', ': not found')):
            return None
        raise RuntimeError('Cannot inspect remote image: ' + result.stderr.strip())
    return json.loads(result.stdout)['digest']


def push(folder):
    metadata = verify(folder)
    reference = image_reference(metadata['version'])
    digest = metadata['image']['digest']
    existing = remote_digest(reference)
    if existing == digest:
        return
    if existing is not None:
        raise ValueError('Release tag already points to different content')
    load_image(folder, reference)
    existing = remote_digest(reference)
    if existing is not None and existing != digest:
        raise ValueError('Concurrent conflicting publication')
    if existing is None:
        run(['docker', 'push', reference])
    if remote_digest(reference) != digest:
        raise ValueError('Published digest mismatch')
    print(reference.rsplit(':', 1)[0] + '@' + digest)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['capture', 'image', 'release', 'verify', 'load', 'push'])
    parser.add_argument('--bundle', type=Path)
    args = parser.parse_args()
    if args.action == 'capture': capture()
    elif args.action == 'image': build_image()
    elif args.action == 'release': release()
    else:
        folder = args.bundle or bundle_output()
        if args.action == 'verify': print(json.dumps(verify(folder), indent=2))
        elif args.action == 'load': load_image(folder)
        else: push(folder)


if __name__ == '__main__':
    main()
