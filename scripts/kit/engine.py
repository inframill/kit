"""Verify and import sibling engine binaries; retain no engine implementation source."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

from .artifacts import verify_binary
from .project import ROOT, native_platform, run, runtime_settings

MODULE = 'github.com/inframill/inframill'
LOCAL_RUNMILL = 'github.com/inframill/kit/pkg/runmill'
PLATFORMS = ('linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64')


def engine_checkout():
    return Path(os.environ.get('INFRAMILL_SOURCE') or os.environ.get('INFRAMILL_DIR')
                or ROOT.parent / 'inframill').resolve()


def source_revision(source):
    if not re.search(r'^module\s+' + re.escape(MODULE) + r'\s*$',
                     (source / 'go.mod').read_text(), re.MULTILINE):
        raise ValueError('INFRAMILL_DIR must be the Inframill module root')
    root = subprocess.check_output(['git', 'rev-parse', '--show-toplevel'], cwd=source, text=True).strip()
    if Path(root).resolve() != source:
        raise ValueError('INFRAMILL_DIR must be the Inframill Git checkout root')
    if subprocess.check_output(['git', 'status', '--porcelain'], cwd=source, text=True).strip():
        raise ValueError('Commit or set aside sibling changes first; the engine pin requires a clean checkout')
    return subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=source, text=True).strip()


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


def verify_engine_binary(path, platform, pin):
    if path.is_symlink() or not path.is_file():
        raise ValueError('Missing regular engine binary: ' + str(path))
    verify_binary(path.read_bytes(), platform)
    info = subprocess.check_output(['go', 'version', '-m', path], text=True)
    required = ['\tpath\t' + MODULE + '/cmd', '\tbuild\tvcs.revision=' + pin['revision'],
                '\tbuild\tvcs.modified=false', '\tbuild\tGOOS=' + platform.split('/')[0],
                '\tbuild\tGOARCH=' + platform.split('/')[1]]
    if not all(line in info.splitlines() for line in required):
        raise ValueError('Engine binary build identity differs from clean sibling HEAD; rebuild in the sibling: ' + str(path))
    return hashlib.sha256(path.read_bytes()).hexdigest()


def collect_notices(source, target, revision):
    target.mkdir()
    output = subprocess.check_output(
        ['go', 'list', '-deps', '-f', '{{if .Module}}{{.Module.Path}}|{{.Module.Version}}|{{.Module.Dir}}{{end}}', './cmd'],
        cwd=source, env={**os.environ, 'GOWORK': 'off', 'CGO_ENABLED': '0'}, text=True)
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


def prepare(source, stage, expected=None):
    revision = source_revision(source)
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
        pin['binaries'][platform] = verify_engine_binary(target, platform, pin)
    native = binaries / binary_name(native_platform())
    if subprocess.check_output([native, '--version'], text=True).strip() != pin['version']:
        raise ValueError('Engine binary version differs from sibling metadata')
    shutil.copy2(native, binaries / 'inframill')
    if expected is not None and pin != expected:
        raise ValueError('Sibling engine artifacts differ from runtime pin; run task dev:sync')
    metadata = stage / 'engine'
    metadata.mkdir()
    collect_notices(source, metadata / 'notices', revision)
    if source_revision(source) != revision or runmill_digest(source / 'pkg/runmill') != pin['runmill_sha256']:
        raise ValueError('Sibling changed during import; rerun task dev:sync')
    (metadata / 'engine.json').write_text(json.dumps(pin, indent=2) + '\n')
    return pin


def install(items, backup):
    """Install the prepared package, pins and artifacts together, restoring on failure."""
    previous = []
    try:
        for index, (prepared, target) in enumerate(items):
            if target.is_symlink():
                raise ValueError('Import target must not be a symlink: ' + str(target))
            target.parent.mkdir(parents=True, exist_ok=True)
            saved = backup / str(index)
            if target.exists():
                target.rename(saved)
            previous.append((target, saved))
            prepared.rename(target)
    except BaseException:
        for target, saved in reversed(previous):
            if target.is_dir():
                shutil.rmtree(target)
            elif target.exists():
                target.unlink()
            if saved.exists():
                saved.rename(target)
        raise


def artifact_items(stage):
    return [(path, ROOT / 'bin' / path.name) for path in sorted((stage / 'bin').iterdir())] + [
        (stage / 'engine', ROOT / '.build/engine')]


def verify_prepared():
    pin = runtime_settings()['INFRAMILL']
    if json.loads((ROOT / '.build/engine/engine.json').read_text()) != pin:
        raise ValueError('Prepared engine pin is stale; rerun task build:engine')
    if runmill_digest(ROOT / 'pkg/runmill') != pin['runmill_sha256']:
        raise ValueError('Kit Runmill source differs from runtime pin')
    for platform in PLATFORMS:
        if verify_engine_binary(ROOT / 'bin' / binary_name(platform), platform, pin) != pin['binaries'][platform]:
            raise ValueError('Engine binary checksum mismatch: ' + platform)
    if hashlib.sha256((ROOT / 'bin/inframill').read_bytes()).hexdigest() != pin['binaries'][native_platform()]:
        raise ValueError('Native engine binary checksum mismatch')


def copy():
    parent = ROOT / '.build'
    parent.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='engine-', dir=parent) as temporary:
        stage = Path(temporary)
        prepare(engine_checkout(), stage, runtime_settings()['INFRAMILL'])
        backup = stage / 'backup'
        backup.mkdir()
        install(artifact_items(stage), backup)


def build_sibling():
    source = engine_checkout()
    revision = source_revision(source)
    run([os.environ.get('TOOLAI', 'toolai'), 'go', 'build', source], env={**os.environ, 'GOWORK': 'off'})
    if source_revision(source) != revision:
        raise ValueError('Sibling changed during build; rerun task dev:prepare')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', nargs='?', choices=['copy', 'build'], default='copy')
    if parser.parse_args().action == 'build':
        build_sibling()
    else:
        copy()
