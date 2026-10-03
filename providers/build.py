#!/usr/bin/env python3
"""Build independently versioned provider executables; never build or publish images."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shlex
import shutil
import struct
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parent
LINUX = ['linux/amd64', 'linux/arm64']
PLATFORMS = LINUX + ['darwin/amd64', 'darwin/arm64']


def run(args, **kwargs):
    print('+', ' '.join(map(str, args)), file=sys.stderr, flush=True)
    return subprocess.run(list(map(str, args)), check=True, text=True, **kwargs)


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def valid_version(value):
    if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?', value):
        return False
    return all(not (part.isdigit() and len(part) > 1 and part.startswith('0'))
               for part in value.partition('-')[2].split('.'))


def native_platform():
    arch = {'aarch64': 'arm64', 'arm64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}.get(platform.machine().lower())
    target = platform.system().lower() + '/' + str(arch)
    if target not in PLATFORMS:
        raise ValueError('Unsupported native platform: ' + target)
    return target


def releases(root=ROOT, name=None, version=None):
    config = json.loads((root / 'releases.json').read_text())
    if config['schema'] != 1 or not re.fullmatch(r'[0-9a-f]{40}', config['inframill']['revision']):
        raise ValueError('Invalid provider build configuration')
    if not valid_version(config['inframill']['version']):
        raise ValueError('Exact Inframill version required')
    selected, seen = [], set()
    for entry in config['providers']:
        key = (entry['name'], entry['version'])
        if not re.fullmatch(r'[a-z][a-z0-9-]*', key[0]) or not valid_version(key[1]) or key in seen:
            raise ValueError('Invalid or duplicate provider selection')
        seen.add(key)
        release = json.loads((root / 'roots' / key[0] / key[1] / 'release.json').read_text())
        if (release['name'], release['version']) != key:
            raise ValueError('Provider release identity differs from its directory')
        if release['protocol'] != 1 or set(release['platforms']) != set(LINUX) or not valid_version(release['tool_version']):
            raise ValueError('Provider requires protocol 1, an exact tool version and both Linux platforms')
        if (name is None or name == key[0]) and (version is None or version == key[1]):
            selected.append(release)
    if not selected:
        raise ValueError('No matching provider releases')
    return config, selected


def verify_source(source, expected):
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=source, text=True).strip()
    changed = subprocess.check_output([
        'git', 'status', '--porcelain', '--untracked-files=all', '--',
        'cmd', 'internal', 'pkg', 'go.mod', 'go.sum', 'LICENSE*', 'NOTICE*',
    ], cwd=source, text=True)
    if revision != expected or changed.strip():
        raise ValueError('Inframill source differs from the revision pinned in providers/releases.json')
    return revision


def source_files(root, release):
    paths = [root / name for name in ('go.mod', 'go.sum', 'LICENSE', 'README.md', 'build.py', 'releases.json')]
    for folder in [root / 'roots/internal', root / 'roots' / release['name'] / release['version']]:
        paths.extend(p for p in folder.rglob('*') if p.is_file())
    result = {}
    for path in sorted(paths):
        relative = path.relative_to(root)
        if '__pycache__' in relative.parts:
            continue
        if path.is_symlink():
            raise ValueError('Provider sources must not contain links: ' + str(relative))
        result[str(relative)] = sha256(path.read_bytes())
    return result


def verify_binary(data, target):
    system, arch = target.split('/')
    if system == 'linux':
        if len(data) < 64 or data[:6] != b'\x7fELF\x02\x01' or data[7] not in (0, 3):
            raise ValueError('Expected a Linux ELF provider')
        machine = struct.unpack_from('<H', data, 18)[0]
        expected = {'amd64': 62, 'arm64': 183}[arch]
    else:
        if len(data) < 32 or data[:4] != b'\xcf\xfa\xed\xfe':
            raise ValueError('Expected a Darwin Mach-O provider')
        machine = struct.unpack_from('<I', data, 4)[0]
        expected = {'amd64': 0x1000007, 'arm64': 0x100000c}[arch]
    if machine != expected:
        raise ValueError('Provider architecture mismatch: ' + target)


def verify_output(folder, release, target, config=None, sources=None):
    record = json.loads((folder / 'build.json').read_text())
    if record['schema'] != 1 or record['release'] != release or record['platform'] != target:
        raise ValueError('Provider output identity mismatch: ' + str(folder))
    if config and (record['inframill'] != config['inframill'] or record['go_version'] != config['go_version']):
        raise ValueError('Provider dependency pins changed; select a new release or remove unpublished output')
    if sources is not None and record['source_files'] != sources:
        raise ValueError('Provider sources changed; select a new release or remove unpublished output')
    actual = set()
    for path in folder.rglob('*'):
        if path.is_symlink():
            raise ValueError('Provider output contains a link')
        if path.is_file() and path != folder / 'build.json':
            actual.add(str(path.relative_to(folder)))
    if actual != set(record['files']):
        raise ValueError('Provider output contains missing or unrecorded files')
    for name, digest in record['files'].items():
        path = Path(name)
        if path.is_absolute() or '..' in path.parts or sha256((folder / path).read_bytes()) != digest:
            raise ValueError('Provider output checksum mismatch: ' + name)
    if 'provider' not in record['files']:
        raise ValueError('Provider output is missing its executable')
    verify_binary((folder / 'provider').read_bytes(), target)
    return record


def build(root, dist, release, target, config, source, modfile, refresh=False):
    sources = source_files(root, release)
    output = dist / release['name'] / release['version'] / target
    if output.exists():
        try:
            verify_output(output, release, target, config, sources)
        except (ValueError, OSError, KeyError):
            if not refresh:
                raise
        else:
            print('Already built:', output)
            return
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.provider-', dir=output.parent) as td:
        stage = Path(td)
        system, arch = target.split('/')
        env = {**os.environ, 'GOOS': system, 'GOARCH': arch, 'CGO_ENABLED': '0', 'GOWORK': 'off'}
        package = './roots/' + release['name'] + '/' + release['version'] + '/cmd'
        run(['go', 'build', '-mod=readonly', '-modfile=' + str(modfile), '-trimpath', '-buildvcs=false',
             '-o', stage / 'provider', package], cwd=root, env=env)
        verify_binary((stage / 'provider').read_bytes(), target)
        for name in sources:
            destination = stage / 'sources' / name
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(root / name, destination)
        # Resolve notices at build time. Image assembly needs only dist, not Go or the SDK sources.
        directories = subprocess.check_output([
            'go', 'list', '-mod=readonly', '-modfile=' + str(modfile), '-deps',
            '-f', '{{if .Module}}{{.Module.Dir}}{{end}}', package,
        ], cwd=root, env=env, text=True)
        engine_directories = subprocess.check_output([
            'go', 'list', '-mod=readonly', '-deps', '-f', '{{if .Module}}{{.Module.Dir}}{{end}}', './cmd',
        ], cwd=source, env=env, text=True)
        for directory in sorted(set((directories + engine_directories).splitlines())):
            if not directory or Path(directory) == root:
                continue
            module = Path(directory)
            identity = module.name + '-' + sha256(directory.encode())[:8]
            for pattern in ['LICENSE*', 'COPYING*', 'NOTICE*', 'AUTHORS*']:
                for path in module.glob(pattern):
                    if path.is_file():
                        destination = stage / 'notices' / identity / path.name
                        destination.parent.mkdir(parents=True, exist_ok=True)
                        shutil.copy2(path, destination)
        if sources != source_files(root, release):
            raise ValueError('Provider sources changed during the build')
        verify_source(source, config['inframill']['revision'])
        record = {'schema': 1, 'release': release, 'platform': target,
                  'go_version': config['go_version'], 'inframill': config['inframill'],
                  'source_files': sources,
                  'files': {str(p.relative_to(stage)): sha256(p.read_bytes()) for p in sorted(stage.rglob('*')) if p.is_file()}}
        (stage / 'build.json').write_text(json.dumps(record, indent=2) + '\n')
        backup = None
        if output.exists():
            backup = output.with_name('.previous-' + output.name + '-' + str(time.time_ns()))
            output.rename(backup)
        try:
            stage.rename(output)
        except OSError:
            if backup is not None:
                backup.rename(output)
            raise
        if backup is not None:
            print('Previous output saved:', backup)
    print('Built:', output)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['build', 'test', 'list', 'verify'])
    parser.add_argument('--name')
    parser.add_argument('--version')
    parser.add_argument('--dist', type=Path, default=ROOT / 'dist')
    parser.add_argument('--platform', action='append', choices=PLATFORMS)
    parser.add_argument('--native', action='store_true', help='select the host platform instead of both Linux architectures')
    parser.add_argument('--refresh', action='store_true', help='rebuild stale outputs, preserving previous artifacts')
    parser.add_argument('--inframill-source', type=Path,
                        default=Path(os.environ.get('INFRAMILL_SOURCE', ROOT.parent.parent / 'inframill')))
    args = parser.parse_args(argv)
    if args.native and args.platform:
        parser.error('--native and --platform are mutually exclusive')
    if args.version and not args.name:
        parser.error('--version requires --name')
    config, selected = releases(ROOT, args.name, args.version)
    targets = [native_platform()] if args.native else (args.platform or LINUX)
    dist = args.dist.resolve()
    if args.command == 'list':
        for release in selected:
            print(release['name'] + ':' + release['version'])
        return
    if args.command == 'verify':
        for release in selected:
            for target in targets:
                verify_output(dist / release['name'] / release['version'] / target, release, target, config)
        print('Provider outputs verified')
        return
    if subprocess.check_output(['go', 'env', 'GOVERSION'], text=True).strip() != config['go_version']:
        raise ValueError('Go toolchain differs from providers/releases.json')
    source = args.inframill_source.resolve()
    verify_source(source, config['inframill']['revision'])
    with tempfile.TemporaryDirectory(prefix='provider-go-') as td:
        modfile = Path(td) / 'go.mod'
        shutil.copy2(ROOT / 'go.mod', modfile)
        shutil.copy2(ROOT / 'go.sum', modfile.with_suffix('.sum'))
        run(['go', 'mod', 'edit', '-modfile=' + str(modfile),
             '-replace=github.com/sir-hassan/inframill=' + str(source)], cwd=ROOT)
        if args.command == 'test':
            run([sys.executable, '-m', 'unittest', 'discover', '-s', 'tests', '-p', 'test_*.py'], cwd=ROOT)
            env = {**os.environ, 'GOWORK': 'off', 'GOFLAGS': '-mod=readonly ' + shlex.quote('-modfile=' + str(modfile))}
            packages = ['./roots/' + r['name'] + '/' + r['version'] + '/...' for r in selected]
            run(['go', 'test', '-vet=off', *packages, './roots/internal/...', './tests/...'], cwd=ROOT, env=env)
        else:
            for release in selected:
                for target in targets:
                    build(ROOT, dist, release, target, config, source, modfile, refresh=args.refresh)


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, KeyError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))
