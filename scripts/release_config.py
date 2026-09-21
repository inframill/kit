"""Kit release selection, tool pins and runtime configuration."""
import json
import os
import pathlib
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



def configuration(executable, workspace, tools=None, drivers=None, format='json'):
    env = {**os.environ, 'KIT_EXECUTABLE': str(executable), 'DATA_DIR': str(workspace), 'CONFIG_FORMAT': format}
    for name in ('opentofu', 'ansible'):
        env.pop(name.upper() + '_PATH', None)
    for name, path in (tools or {}).items():
        env[name.upper() + '_PATH'] = str(path)
    env['CONFIG_DRIVERS'] = json.dumps(drivers or bundle_lock()['drivers'])
    result = subprocess.check_output(['go', 'run', './cmd/kit-build', 'config'], cwd=ROOT, env=env, text=True)
    return json.loads(result) if format == 'json' else result


def local_tools():
    lock = bundle_lock()
    tofu = ROOT / f".cache/tools/opentofu/{lock['opentofu']['version']}/tofu"
    ansible = ROOT / f".cache/ansible-{lock['ansible_core']}/bin/ansible-playbook"
    return pathlib.Path(os.environ.get('OPENTOFU_PATH', tofu)).resolve(), pathlib.Path(os.environ.get('ANSIBLE_PATH', ansible)).resolve()


def bundle_output():
    return (ROOT / os.environ.get('KIT_DIST_DIR', 'dist')) / 'bundles' / project_version()
