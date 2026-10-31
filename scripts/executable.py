"""Build and verify the unified Kit executable without Docker or publication."""
from contextlib import contextmanager
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import time

from scripts.artifacts import LINUX, PLATFORMS, digest, native_platform, valid_version, verify_binary, verify_files

ROOT = Path(__file__).resolve().parent.parent


def run(args, **kwargs):
    print('+', ' '.join(map(str, args)), file=sys.stderr, flush=True)
    return subprocess.run(list(map(str, args)), check=True, text=True, **kwargs)


def releases(root=ROOT):
    config = json.loads((root / 'releases.json').read_text())
    if config['schema'] != 1 or not re.fullmatch(r'[0-9a-f]{40}', config['inframill']['revision']):
        raise ValueError('Invalid executable build configuration')
    if not valid_version(config['inframill']['version']):
        raise ValueError('Exact Inframill version required')
    selected, seen = [], set()
    for entry in config['providers']:
        key = (entry['name'], entry['version'])
        if not re.fullmatch(r'[a-z][a-z0-9-]*', key[0]) or not valid_version(key[1]) or key in seen:
            raise ValueError('Invalid or duplicate provider selection')
        seen.add(key)
        directory = 'v' + key[1].replace('.', '_')
        release = json.loads((root / 'pkg/providers' / key[0] / directory / 'release.json').read_text())
        if (release['name'], release['version']) != key:
            raise ValueError('Provider release identity differs from its directory')
        tool_version = release.get('tool_version')
        if (release['protocol'] != 1 or set(release['platforms']) != set(LINUX)
                or tool_version is not None and not valid_version(tool_version)):
            raise ValueError('Provider requires protocol 1, both Linux platforms and an exact version for pinned tools')
        selected.append(release)
    if not selected:
        raise ValueError('No provider releases selected')
    return config, selected


def verify_source(source, expected):
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=source, text=True).strip()
    changed = subprocess.check_output([
        'git', 'status', '--porcelain', '--untracked-files=all', '--',
        'cmd', 'internal', 'pkg', 'go.mod', 'go.sum', 'LICENSE*', 'NOTICE*',
    ], cwd=source, text=True)
    if revision != expected or changed.strip():
        raise ValueError('Inframill source differs from the revision pinned in releases.json')
    return revision


@contextmanager
def go_environment(root, source, config):
    if subprocess.check_output(['go', 'env', 'GOVERSION'], cwd=root, text=True).strip() != config['go_version']:
        raise ValueError('Go toolchain differs from releases.json')
    verify_source(source, config['inframill']['revision'])
    with tempfile.TemporaryDirectory(prefix='kit-go-') as td:
        modfile = Path(td) / 'go.mod'
        shutil.copy2(root / 'go.mod', modfile)
        shutil.copy2(root / 'go.sum', modfile.with_suffix('.sum'))
        run(['go', 'mod', 'edit', '-modfile=' + str(modfile),
             '-replace=github.com/sir-hassan/inframill=' + str(source)], cwd=root)
        env = {**os.environ, 'GOWORK': 'off', 'GOFLAGS': '-mod=readonly ' + shlex.quote('-modfile=' + str(modfile))}
        yield env
    verify_source(source, config['inframill']['revision'])


def source_files(root):
    paths = [root / name for name in ('go.mod', 'go.sum', 'LICENSE', 'README.md', 'build.py', 'releases.json', 'VERSION', 'Taskfile.yml')]
    for folder in ('cmd', 'internal', 'pkg', 'scripts', 'runtime'):
        paths.extend((root / folder).rglob('*'))
    result = {}
    for path in sorted(paths):
        relative = path.relative_to(root)
        if '__pycache__' in relative.parts:
            continue
        if path.is_symlink():
            raise ValueError('Executable sources must not contain links: ' + str(relative))
        if path.is_file():
            result[str(relative)] = digest(path.read_bytes())
    return result


def verify_output(folder, version, target, config=None, selected=None, sources=None):
    record = json.loads((folder / 'build.json').read_text())
    if record['schema'] != 2 or record['version'] != version or record['platform'] != target:
        raise ValueError('Executable output identity mismatch: ' + str(folder))
    if config and (record['inframill'] != config['inframill'] or record['go_version'] != config['go_version']):
        raise ValueError('Executable dependency pins changed; use --refresh for unpublished output')
    if selected is not None and record['providers'] != selected:
        raise ValueError('Executable provider selections changed; use --refresh for unpublished output')
    if sources is not None and record['source_files'] != sources:
        raise ValueError('Executable sources changed; use --refresh for unpublished output')
    verify_files(folder, record)
    return record


def build(root, dist, version, target, config, selected, source, env, refresh=False):
    sources = source_files(root)
    output = dist / version / target
    if output.exists():
        try:
            verify_output(output, version, target, config, selected, sources)
        except (ValueError, OSError, KeyError):
            if not refresh:
                raise
        else:
            print('Already built:', output)
            return
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.exec-', dir=output.parent) as td:
        stage = Path(td)
        system, arch = target.split('/')
        env = {**env, 'GOOS': system, 'GOARCH': arch, 'CGO_ENABLED': '0'}
        package = './cmd/exec'
        run(['go', 'build', '-trimpath', '-buildvcs=false', '-ldflags=-X main.version=' + version,
             '-o', stage / 'exec', package], cwd=root, env=env)
        verify_binary((stage / 'exec').read_bytes(), target)
        # A native binary can report its compiled registry; reject drift from release selections.
        if target == native_platform():
            actual = json.loads(subprocess.check_output([stage / 'exec', 'list'], text=True))
            if sorted(actual, key=lambda r: (r['name'], r['version'])) != sorted(selected, key=lambda r: (r['name'], r['version'])):
                raise ValueError('Compiled provider registry differs from releases.json')
        for name in sources:
            destination = stage / 'sources' / name
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(root / name, destination)
        # Resolve notices once per binary, including the engine dependencies bundled by the image.
        directories = subprocess.check_output([
            'go', 'list', '-deps', '-f', '{{if .Module}}{{.Module.Dir}}{{end}}', package,
        ], cwd=root, env=env, text=True)
        engine_env = {**env}
        engine_env.pop('GOFLAGS', None)
        engine_directories = subprocess.check_output([
            'go', 'list', '-mod=readonly', '-deps', '-f', '{{if .Module}}{{.Module.Dir}}{{end}}', './cmd',
        ], cwd=source, env=engine_env, text=True)
        for directory in sorted(set((directories + engine_directories).splitlines())):
            if not directory or Path(directory) == root:
                continue
            module = Path(directory)
            identity = module.name + '-' + digest(directory.encode())[:8]
            for pattern in ['LICENSE*', 'COPYING*', 'NOTICE*', 'AUTHORS*']:
                for path in module.glob(pattern):
                    if path.is_file():
                        destination = stage / 'notices' / identity / path.name
                        destination.parent.mkdir(parents=True, exist_ok=True)
                        shutil.copy2(path, destination)
        if sources != source_files(root):
            raise ValueError('Executable sources changed during the build')
        verify_source(source, config['inframill']['revision'])
        record = {'schema': 2, 'version': version, 'providers': selected, 'platform': target,
                  'go_version': config['go_version'], 'inframill': config['inframill'],
                  'source_files': sources,
                  'files': {str(p.relative_to(stage)): digest(p.read_bytes()) for p in sorted(stage.rglob('*')) if p.is_file()}}
        (stage / 'build.json').write_text(json.dumps(record, indent=2) + '\n')
        verify_output(stage, version, target, config, selected, sources)
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


def add_arguments(parser):
    parser.add_argument('--dist', type=Path)
    parser.add_argument('--platform', action='append', choices=PLATFORMS)
    parser.add_argument('--native', action='store_true', help='select the host platform instead of both Linux architectures')
    parser.add_argument('--refresh', action='store_true', help='rebuild stale outputs, preserving previous artifacts')
    parser.add_argument('--inframill-source', type=Path)


def execute(args, root, dist, version):
    if args.native and args.platform:
        raise ValueError('--native and --platform are mutually exclusive')
    config, selected = releases(root)
    targets = [native_platform()] if args.native else (args.platform or LINUX)
    dist = (args.dist or dist).resolve()
    if args.command == 'verify-exec':
        for target in targets:
            verify_output(dist / version / target, version, target, config, selected, source_files(root))
        print('Executable outputs verified')
        return
    source = Path(args.inframill_source or os.environ.get('INFRAMILL_SOURCE', root.parent / 'inframill')).resolve()
    with go_environment(root, source, config) as env:
        # Check the compiled registry even when all requested targets are cross builds.
        run(['go', 'test', './internal/catalog'], cwd=root, env=env)
        for target in targets:
            build(root, dist, version, target, config, selected, source, env, refresh=args.refresh)


def test(root, source=None):
    config, _ = releases(root)
    source = Path(source or os.environ.get('INFRAMILL_SOURCE', root.parent / 'inframill')).resolve()
    with go_environment(root, source, config) as env:
        run(['go', 'test', './...'], cwd=root, env=env)
