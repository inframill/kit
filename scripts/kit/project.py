"""Shared paths and runtime configuration; release identity comes from Toolai."""
import json
from pathlib import Path
import platform
import re

from ..core.context import Context

ROOT = Path(__file__).resolve().parents[2]
LINUX = ['linux/amd64', 'linux/arm64']


def get_context(context=None):
    return context if context is not None else Context(ROOT)


def project_version(*, context=None):
    return json.loads((get_context(context).root / 'toolai.json').read_text())['version']


def native_platform():
    arch = {'aarch64': 'arm64', 'arm64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}[platform.machine().lower()]
    return platform.system().lower() + '/' + arch


def runtime_settings(*, context=None):
    return json.loads((get_context(context).root / 'runtime/dependencies.json').read_text())


def local_executable(*, context=None):
    return get_context(context).root / 'bin/kit-exec'


def provider_releases(*, context=None):
    return [json.loads(path.read_text()) for path in sorted((get_context(context).root / 'internal/providers').glob('*/release.json'))]


def image_reference(version=None, *, context=None):
    context = get_context(context)
    registry = context.env.get('REGISTRY', 'ghcr.io')
    namespace = context.env.get('NAMESPACE', 'inframill')
    if not re.fullmatch(r'[a-z][a-z0-9-]*', namespace) or not re.fullmatch(r'[A-Za-z0-9.-]+(?::[0-9]+)?', registry):
        raise ValueError('Invalid registry or namespace')
    return registry + '/' + namespace + '/kit:' + (version or project_version(context=context))


def bundle_output(*, context=None):
    context = get_context(context)
    return context.root / 'dist/kit' / project_version(context=context)


def configuration(binary, workspace, tools=None, *, context=None):
    selected = {}
    for release in provider_releases(context=context):
        name = release['name']
        args = [name + '-' + release['version']]
        if tools and name in tools:
            args.extend(['--tool', str(tools[name])])
        selected[name] = {'path': str(binary), 'args': args}
    return {'main': {'port': 8080}, 'logger': {'type': 'slog', 'settings': {'level': 'info', 'format': 'json'}},
            'engine': {'dataDir': str(workspace), 'providers': selected}, 'handlers': [{'type': 'connect-engine'}]}


def local_tools(*, context=None):
    context = get_context(context)
    settings = runtime_settings(context=context)
    tofu = context.root / f".cache/tools/opentofu/{settings['OPENTOFU_VERSION']}/tofu"
    ansible = context.root / f".cache/ansible-{settings['ANSIBLE_VERSION']}/bin/ansible-playbook"
    return context.path(context.env.get('OPENTOFU_PATH', tofu)), context.path(context.env.get('ANSIBLE_PATH', ansible))


def engine_version(*, context=None):
    return runtime_settings(context=context)['INFRAMILL']['version']
