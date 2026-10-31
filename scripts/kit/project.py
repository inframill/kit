"""Shared paths and runtime configuration; release identity comes from Toolai."""
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
LINUX = ['linux/amd64', 'linux/arm64']
PLATFORMS = LINUX + ['darwin/amd64', 'darwin/arm64']


def run(args, **kwargs):
    print('+', ' '.join(map(str, args)), file=sys.stderr, flush=True)
    return subprocess.run(list(map(str, args)), check=True, text=True, **kwargs)


def project_version():
    return json.loads((ROOT / 'toolai.json').read_text())['version']


def native_platform():
    arch = {'aarch64': 'arm64', 'arm64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}[platform.machine().lower()]
    return platform.system().lower() + '/' + arch


def runtime_settings():
    return json.loads((ROOT / 'runtime/dependencies.json').read_text())


def local_executable():
    return ROOT / 'bin/kit-exec'


def provider_releases():
    return [json.loads(path.read_text()) for path in sorted((ROOT / 'internal/providers').glob('*/release.json'))]


def image_reference(version=None):
    registry = os.environ.get('REGISTRY', 'ghcr.io')
    namespace = os.environ.get('NAMESPACE', 'inframill')
    if not re.fullmatch(r'[a-z][a-z0-9-]*', namespace) or not re.fullmatch(r'[A-Za-z0-9.-]+(?::[0-9]+)?', registry):
        raise ValueError('Invalid registry or namespace')
    return registry + '/' + namespace + '/kit:' + (version or project_version())


def bundle_output():
    return ROOT / 'dist/kit' / project_version()


def configuration(binary, workspace, tools=None):
    selected = {}
    for release in provider_releases():
        name = release['name']
        args = [name + '-' + release['version']]
        if tools and name in tools:
            args.extend(['--tool', str(tools[name])])
        selected[name] = {'path': str(binary), 'args': args}
    return {'main': {'port': 8080}, 'logger': {'type': 'slog', 'settings': {'level': 'info', 'format': 'json'}},
            'engine': {'dataDir': str(workspace), 'providers': selected}, 'handlers': [{'type': 'connect-engine'}]}


def local_tools():
    settings = runtime_settings()
    tofu = ROOT / f".cache/tools/opentofu/{settings['OPENTOFU_VERSION']}/tofu"
    ansible = ROOT / f".cache/ansible-{settings['ANSIBLE_VERSION']}/bin/ansible-playbook"
    return Path(os.environ.get('OPENTOFU_PATH', tofu)).resolve(), Path(os.environ.get('ANSIBLE_PATH', ansible)).resolve()


def engine_module():
    data = json.loads(subprocess.check_output(['go', 'list', '-m', '-json', 'github.com/inframill/inframill'], cwd=ROOT, text=True))
    module = Path(data.get('Replace', data)['Dir'])
    metadata = json.loads((module / 'toolai.json').read_text())
    return module, {'module': data['Path'], 'module_version': data.get('Version') or re.search(r'github.com/inframill/inframill\s+(\S+)', (ROOT / 'go.mod').read_text()).group(1), 'version': metadata['version'], 'sum': data.get('Sum', ''), 'local_source': bool(data.get('Main') or data.get('Replace'))}


def engine_version():
    return engine_module()[1]['version']
