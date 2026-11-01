"""Verify and import sibling engine binaries; retain no engine implementation source."""
import hashlib
import json
from pathlib import Path
import re
import shutil

from ..core.workspace import workspace
from .workspace import install
from .artifacts import verify_binary
from .project import get_context, native_platform, runtime_settings

MODULE = 'github.com/inframill/inframill'
LOCAL_RUNMILL = 'github.com/inframill/kit/pkg/runmill'
PLATFORMS = ('linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64')


def engine_checkout(*, context=None):
    context = get_context(context)
    return context.path(context.env.get('INFRAMILL_SOURCE') or context.env.get('INFRAMILL_DIR')
                        or context.root.parent / 'inframill')


def source_revision(source, *, context=None):
    context = get_context(context)
    if not re.search(r'^module\s+' + re.escape(MODULE) + r'\s*$',
                     (source / 'go.mod').read_text(), re.MULTILINE):
        raise ValueError('INFRAMILL_DIR must be the Inframill module root')
    root = context.output(['git', 'rev-parse', '--show-toplevel'], cwd=source).strip()
    if Path(root).resolve() != source:
        raise ValueError('INFRAMILL_DIR must be the Inframill Git checkout root')
    if context.output(['git', 'status', '--porcelain'], cwd=source).strip():
        raise ValueError('Commit or set aside sibling changes first; the engine pin requires a clean checkout')
    return context.output(['git', 'rev-parse', 'HEAD'], cwd=source).strip()


def runmill_digest(package):
    paths = sorted(package.glob('*.go'))
    if not paths or package.is_symlink():
        raise ValueError('Runmill Go sources are missing or linked')
    digest = hashlib.sha256()
    for path in paths:
        if path.is_symlink() or not path.is_file():
            raise ValueError('Runmill Go sources must be regular files')
        data = path.read_bytes().replace((MODULE + '/pkg/runmill').encode(), LOCAL_RUNMILL.encode())
        digest.update(path.name.encode() + b'\0' + hashlib.sha256(data).digest())
    return digest.hexdigest()


def binary_name(platform):
    return 'inframill-' + platform.replace('/', '-')


def verify_engine_binary(path, platform, pin, *, context=None):
    if path.is_symlink() or not path.is_file():
        raise ValueError('Missing regular engine binary: ' + str(path))
    verify_binary(path.read_bytes(), platform)
    info = get_context(context).output(['go', 'version', '-m', path])
    required = ['\tpath\t' + MODULE + '/cmd', '\tbuild\tvcs.revision=' + pin['revision'],
                '\tbuild\tvcs.modified=false', '\tbuild\tGOOS=' + platform.split('/')[0],
                '\tbuild\tGOARCH=' + platform.split('/')[1]]
    if not all(line in info.splitlines() for line in required):
        raise ValueError('Engine binary build identity differs from clean sibling HEAD; rebuild in the sibling: ' + str(path))
    return hashlib.sha256(path.read_bytes()).hexdigest()


def collect_notices(source, target, revision, *, context=None):
    target.mkdir()
    output = get_context(context).output(
        ['go', 'list', '-deps', '-f', '{{if .Module}}{{.Module.Path}}|{{.Module.Version}}|{{.Module.Dir}}{{end}}', './cmd'],
        cwd=source, env={'GOWORK': 'off', 'CGO_ENABLED': '0'}, timeout=300)
    modules = {}
    for line in filter(None, output.splitlines()):
        name, version, folder = line.split('|', 2)
        identity = name.replace('/', '_') + '@' + (revision if name == MODULE else version)
        modules[identity] = Path(folder)
    for identity, folder in sorted(modules.items()):
        for pattern in ('LICENSE*', 'COPYING*', 'NOTICE*', 'AUTHORS*'):
            for path in folder.glob(pattern):
                if path.is_file() and not path.is_symlink():
                    destination = target / identity / path.name
                    destination.parent.mkdir(parents=True, exist_ok=True)
                    shutil.copy2(path, destination)


def prepare(source, stage, expected=None, *, context=None):
    context = get_context(context)
    revision = source_revision(source, context=context)
    pin = {'version': json.loads((source / 'toolai.json').read_text())['version'],
           'revision': revision, 'runmill_sha256': runmill_digest(source / 'pkg/runmill'), 'binaries': {}}
    binaries = stage / 'bin'
    binaries.mkdir()
    for platform in PLATFORMS:
        name = binary_name(platform)
        original = source / 'bin' / name
        if original.is_symlink() or not original.is_file():
            raise ValueError('Missing sibling binaries; run task dev:prepare or build Inframill first: ' + str(original))
        target = binaries / name
        shutil.copy2(original, target)
        pin['binaries'][platform] = verify_engine_binary(target, platform, pin, context=context)
    native = binaries / binary_name(native_platform())
    if context.output([native, '--version']).strip() != pin['version']:
        raise ValueError('Engine binary version differs from sibling metadata')
    shutil.copy2(native, binaries / 'inframill')
    if expected is not None and pin != expected:
        raise ValueError('Sibling engine artifacts differ from runtime pin; run task dev:sync')
    metadata = stage / 'engine'
    metadata.mkdir()
    collect_notices(source, metadata / 'notices', revision, context=context)
    if source_revision(source, context=context) != revision or runmill_digest(source / 'pkg/runmill') != pin['runmill_sha256']:
        raise ValueError('Sibling changed during import; rerun task dev:sync')
    (metadata / 'engine.json').write_text(json.dumps(pin, indent=2) + '\n')
    return pin


def artifact_items(stage, *, context=None):
    root = get_context(context).root
    return [(path, root / 'bin' / path.name) for path in sorted((stage / 'bin').iterdir())] + [
        (stage / 'engine', root / '.build/engine')]


def verify_prepared(*, context=None):
    context = get_context(context)
    root = context.root
    pin = runtime_settings(context=context)['INFRAMILL']
    if json.loads((root / '.build/engine/engine.json').read_text()) != pin:
        raise ValueError('Prepared engine pin is stale; rerun task build:engine')
    if runmill_digest(root / 'pkg/runmill') != pin['runmill_sha256']:
        raise ValueError('Kit Runmill source differs from runtime pin')
    for platform in PLATFORMS:
        if verify_engine_binary(root / 'bin' / binary_name(platform), platform, pin, context=context) != pin['binaries'][platform]:
            raise ValueError('Engine binary checksum mismatch: ' + platform)
    if hashlib.sha256((root / 'bin/inframill').read_bytes()).hexdigest() != pin['binaries'][native_platform()]:
        raise ValueError('Native engine binary checksum mismatch')


def copy(*, context=None):
    context = get_context(context)
    with workspace(context.root / '.build', prefix='engine-') as stage:
        prepare(engine_checkout(context=context), stage, runtime_settings(context=context)['INFRAMILL'], context=context)
        backup = stage / 'backup'
        backup.mkdir()
        install(artifact_items(stage, context=context), backup)


def build_sibling(*, context=None):
    context = get_context(context)
    source = engine_checkout(context=context)
    revision = source_revision(source, context=context)
    context.run([context.env.get('TOOLAI', 'toolai'), 'go', 'build', source],
                env={'GOWORK': 'off'}, timeout=1800)
    if source_revision(source, context=context) != revision:
        raise ValueError('Sibling changed during build; rerun task dev:prepare')


if __name__ == '__main__':
    import sys
    from ..cli import main
    raise SystemExit(main(['kit', 'engine', *(sys.argv[1:] or ['copy'])]))
