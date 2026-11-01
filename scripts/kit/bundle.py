"""Package, verify and publish prepared Kit artifacts; never compile or test."""
import hashlib
import gzip
import json
from pathlib import Path, PurePosixPath
import re
import shutil
import tarfile

from ..core.workspace import install, workspace
from .artifacts import verify_binary
from .engine import MODULE, verify_prepared
from .project import (get_context, LINUX, bundle_output, image_reference,
                      local_executable, project_version, provider_releases,
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


def loaded_digest(reference, *, context=None):
    descriptor = json.loads(get_context(context).output(['docker', 'image', 'inspect', reference, '--format', '{{json .Descriptor}}']))
    return descriptor['digest']


def load_image(folder, reference=None, *, context=None):
    context = get_context(context)
    metadata = verify(folder)
    original = metadata['image']['reference']
    context.run(['docker', 'load', '--input', Path(folder) / 'image.oci.tar'], timeout=600)
    if loaded_digest(original, context=context) != metadata['image']['digest']:
        raise ValueError('Loaded image digest differs from bundle')
    if reference and reference != original:
        context.run(['docker', 'tag', original, reference])
    return metadata


def source_files(*, context=None):
    context = get_context(context)
    paths = context.output(['git', 'ls-files', '-co', '--exclude-standard', '-z']).split('\0')
    roots = {'cmd', 'internal', 'pkg', 'runtime', 'scripts', 'tests', 'blueprints', 'docs', '.agents'}
    files = {'go.mod', 'go.sum', 'toolai.json', 'Taskfile.yml', 'README.md', 'LICENSE'}
    result = {}
    for name in sorted(set(filter(None, paths))):
        path = context.root / name
        if name not in files and PurePosixPath(name).parts[0] not in roots:
            continue
        if path.is_symlink():
            raise ValueError('Source snapshot contains a link: ' + name)
        if path.is_file():
            result[name] = checksum(path)
    return result


def capture(*, context=None):
    context = get_context(context)
    parent = context.root / '.build'
    parent.mkdir(exist_ok=True)
    (parent / 'inputs.json').write_text(json.dumps(source_files(context=context), sort_keys=True))


def check_inputs(*, context=None):
    context = get_context(context)
    if json.loads((context.root / '.build/inputs.json').read_text()) != source_files(context=context):
        raise ValueError('Sources changed after build preparation; rerun toolai build')


def source_archive(target, *, context=None):
    context = get_context(context)
    check_inputs(context=context)
    epoch = int(runtime_settings(context=context)['SOURCE_DATE_EPOCH'])
    def normalize(info):
        info.uid = info.gid = 0
        info.uname = info.gname = ''
        info.mtime = epoch
        return info
    with target.open('wb') as stream, gzip.GzipFile(fileobj=stream, mode='wb', mtime=epoch, filename='') as compressed:
        with tarfile.open(fileobj=compressed, mode='w') as archive:
            for name in source_files(context=context):
                archive.add(context.root / name, arcname='kit/' + name, recursive=False, filter=normalize)
    check_inputs(context=context)


def notices(target, *, context=None):
    context = get_context(context)
    shutil.copytree(context.root / '.build/engine/notices', target)
    for pattern in ('LICENSE*', 'COPYING*', 'NOTICE*', 'AUTHORS*'):
        for path in context.root.glob(pattern):
            if path.is_file() and not path.is_symlink():
                destination = target / ('github.com_inframill_kit@' + project_version(context=context)) / path.name
                destination.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(path, destination)


def verify_provider_build_info(info):
    prefix = '\tdep\t' + MODULE + '\t'
    if any(line.startswith(prefix) for line in info.splitlines()):
        raise ValueError('Provider binary still imports upstream Runmill; rerun toolai build')


def prepare(target, *, context=None):
    context = get_context(context)
    engine_root = context.root / '.build/engine'
    metadata = json.loads((engine_root / 'engine.json').read_text())
    verify_prepared(context=context)
    providers = json.loads(context.output([local_executable(context=context), 'list']))
    expected = provider_releases(context=context)
    if providers != sorted(expected, key=lambda item: item['name'] + '-' + item['version']):
        raise ValueError('Compiled provider registry differs from sources')
    version = json.loads(context.output([local_executable(context=context), 'version']))['version']
    if version != project_version(context=context):
        raise ValueError('Provider binary version is stale; run toolai build')
    settings = runtime_settings(context=context)
    for provider in providers:
        key = {'opentofu': 'OPENTOFU_VERSION', 'ansible': 'ANSIBLE_VERSION'}.get(provider['name'])
        if provider['protocol'] != 1 or set(provider['platforms']) != set(LINUX) or key and provider['tool_version'] != settings[key]:
            raise ValueError('Provider runtime requirements differ from selected tools')
    binary_hashes = {}
    for platform in LINUX:
        system, arch = platform.split('/')
        folder = target / 'bin' / system / arch
        folder.mkdir(parents=True)
        for name, source in [('kit-exec', context.root / f'bin/kit-exec-{system}-{arch}'), ('inframill', context.root / f'bin/inframill-{system}-{arch}')]:
            data = source.read_bytes()
            verify_binary(data, platform)
            # Toolai embeds version injection and module identities in Go build info.
            info = context.output(['go', 'version', '-m', source])
            expected_package = 'github.com/inframill/' + ('kit' if name == 'kit-exec' else 'inframill') + '/cmd'
            if expected_package not in info:
                raise ValueError('Unexpected binary package: ' + str(source))
            if name == 'kit-exec':
                verify_provider_build_info(info)
            destination = folder / name
            shutil.copy2(source, destination)
            binary_hashes[f'{platform}/{name}'] = checksum(destination)
    source_archive(target / 'sources.tar.gz', context=context)
    notices(target / 'notices', context=context)
    shutil.copytree(context.root / 'runtime', target / 'runtime', ignore=shutil.ignore_patterns('__pycache__'))
    shutil.copy2(context.root / 'runtime/inframill.yaml', target / 'inframill.yaml')
    record = {'schema': 1, 'version': project_version(context=context), 'platforms': LINUX,
              'source_revision': context.output(['git', 'rev-parse', 'HEAD']).strip(),
              'source_dirty': bool(context.output(['git', 'status', '--porcelain']).strip()),
              'inframill': metadata, 'providers': providers, 'runtime': settings,
              'go_version': context.output(['go', 'env', 'GOVERSION']).strip(),
              'binaries': binary_hashes,
              'source_sha256': checksum(target / 'sources.tar.gz')}
    (target / 'manifest.json').write_text(json.dumps(record, indent=2) + '\n')
    return record


def build_image(*, context=None):
    context = get_context(context)
    parent = context.root / '.build'
    with workspace(parent, prefix='image-') as temporary:
        stage = temporary / 'bundle'
        stage.mkdir()
        target = temporary / 'context'
        target.mkdir()
        record = prepare(target, context=context)
        reference = image_reference(context=context)
        settings = runtime_settings(context=context)
        args = ['docker', 'buildx', 'build', '--platform', ','.join(LINUX), '--provenance=false', '--sbom=false',
                '--metadata-file', temporary / 'buildx.json', '--tag', reference,
                '--build-arg', 'INFRAMILL_VERSION=' + record['inframill']['version'],
                '--build-arg', 'KIT_VERSION=' + record['version']]
        for name in ('BASE_IMAGE', 'SOURCE_DATE_EPOCH', 'DEBIAN_SNAPSHOT'):
            args += ['--build-arg', name + '=' + settings[name]]
        args += ['--file', target / 'runtime/Dockerfile', '--output', 'type=oci,dest=' + str(stage / 'image.oci.tar') + ',rewrite-timestamp=true', target]
        context.run(args, timeout=1800)
        digest = json.loads((temporary / 'buildx.json').read_text())['containerimage.digest']
        record['image'] = {'reference': reference, 'digest': digest}
        for name in ('sources.tar.gz', 'notices'):
            shutil.move(target / name, stage / name)
        (stage / 'manifest.json').write_text(json.dumps(record, indent=2) + '\n')
        check_inputs(context=context)
        write_checksums(stage)
        verify(stage)
        load_image(stage, context=context)
        destination = parent / 'image'
        backup = temporary / 'backup'
        backup.mkdir()
        install([(stage, destination)], backup)
    print('Prepared image:', reference, digest)


def release(*, context=None):
    context = get_context(context)
    source = context.root / '.build/image'
    metadata = verify(source)
    if metadata['version'] != project_version(context=context):
        raise ValueError('Prepared image version is stale')
    output = bundle_output(context=context)
    with workspace(output.parent, prefix='.release-') as temporary:
        stage = temporary / 'bundle'
        shutil.copytree(source, stage)
        verify(stage)
        backup = temporary / 'backup'
        backup.mkdir()
        install([(stage, output)], backup)
    print('Release bundle:', output)


def remote_digest(reference, *, context=None):
    result = get_context(context).run(['docker', 'buildx', 'imagetools', 'inspect', '--format', '{{json .Manifest}}', reference],
                                      capture_output=True, check=False, log=False)
    if result.returncode:
        if any(word in result.stderr.lower() for word in ('manifest unknown', 'no such manifest', ': not found')):
            return None
        raise RuntimeError('Cannot inspect remote image: ' + result.stderr.strip())
    return json.loads(result.stdout)['digest']


def push(folder, *, context=None):
    context = get_context(context)
    metadata = verify(folder)
    reference = image_reference(metadata['version'], context=context)
    digest = metadata['image']['digest']
    existing = remote_digest(reference, context=context)
    if existing == digest:
        return
    if existing is not None:
        raise ValueError('Release tag already points to different content')
    load_image(folder, reference, context=context)
    existing = remote_digest(reference, context=context)
    if existing is not None and existing != digest:
        raise ValueError('Concurrent conflicting publication')
    if existing is None:
        context.run(['docker', 'push', reference], timeout=1800)
    if remote_digest(reference, context=context) != digest:
        raise ValueError('Published digest mismatch')
    print(reference.rsplit(':', 1)[0] + '@' + digest)


if __name__ == '__main__':
    import sys
    from ..cli import main
    raise SystemExit(main(['kit', 'bundle', *sys.argv[1:]]))
