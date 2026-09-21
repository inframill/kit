"""Kit release selection, tool pins and runtime configuration."""
import json
import os
import pathlib
import platform
import re
import subprocess

ROOT = pathlib.Path(__file__).resolve().parent.parent


def project_version():
    return subprocess.check_output(['sh', str(ROOT / 'scripts/read-version.sh')], cwd=ROOT, text=True).removesuffix('\n')


def bundle_lock(version=None):
    version = version or project_version()
    if '/' in version or version in ('', '.', '..'):
        raise ValueError('Invalid bundle version')
    lock = json.loads((ROOT / f'releases/{version}.lock.json').read_text())
    if lock['version'] != version:
        raise ValueError('Release lock identity mismatch')
    python_requirements(lock)
    return lock


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
