"""Shared release selection, tool pins, configuration and fixture requirements."""
import hashlib
import json
import os
import pathlib
import subprocess

ROOT = pathlib.Path(__file__).resolve().parent.parent
FIXTURES = ROOT / 'tests/fixtures'


def bundle_lock(version=None):
    version = version or os.environ.get('BUNDLE_VERSION', '0.2.0')
    if '/' in version or version in ('', '.', '..'):
        raise ValueError('Invalid bundle version')
    lock = json.loads((ROOT / f'releases/bundles/{version}.lock.yaml').read_text())
    path = pathlib.PurePosixPath(lock['toolchain']['path'])
    if path.is_absolute() or '..' in path.parts:
        raise ValueError('Toolchain path must be local')
    data = (ROOT / path).read_bytes()
    if hashlib.sha256(data).hexdigest() != lock['toolchain']['sha256']:
        raise ValueError('Toolchain lock checksum mismatch')
    tools = json.loads(data)
    if set(lock) & set(tools):
        raise ValueError('Duplicate toolchain fields in bundle lock')
    for file, key in [('runtime/requirements.lock', 'python_requirements_sha256'), ('runtime/python-sources.lock.json', 'python_sources_sha256')]:
        if hashlib.sha256((ROOT / file).read_bytes()).hexdigest() != tools[key]:
            raise ValueError('Dependency lock checksum mismatch: ' + file)
    return {**lock, **tools}


def configuration(executable, workspace, tools=None, drivers=None):
    env = {**os.environ, 'KIT_EXECUTABLE': str(executable), 'DATA_DIR': str(workspace)}
    for name in ('opentofu', 'ansible'):
        env.pop(name.upper() + '_PATH', None)
    for name, path in (tools or {}).items():
        env[name.upper() + '_PATH'] = str(path)
    env['CONFIG_DRIVERS'] = json.dumps(drivers or bundle_lock()['drivers'])
    result = subprocess.check_output(['go', 'run', './cmd/kit-build', 'config'], cwd=ROOT, env=env, text=True)
    return json.loads(result)


def blueprint_root(name):
    return FIXTURES if (FIXTURES / 'blueprints' / name).is_dir() else ROOT


def requirements(name, version):
    return json.loads((blueprint_root(name) / f'releases/blueprints/{name}/{version}.yaml').read_text())['drivers']


def driver_ids(name, version):
    return {d['name']: d['id'] for d in requirements(name, version)}


def local_tools():
    lock = bundle_lock()
    tofu = ROOT / f".cache/tools/opentofu/{lock['opentofu']['version']}/tofu"
    ansible = ROOT / f".cache/ansible-{lock['ansible_core']}/bin/ansible-playbook"
    return pathlib.Path(os.environ.get('OPENTOFU_PATH', tofu)).resolve(), pathlib.Path(os.environ.get('ANSIBLE_PATH', ansible)).resolve()


def registry_image():
    # Compose is the single source for the disposable registry's image pin too.
    for line in (ROOT / 'runtime/registry.compose.yaml').read_text().splitlines():
        if line.strip().startswith('image:'):
            image = line.split('image:', 1)[1].strip()
            if '@sha256:' not in image:
                raise ValueError('Registry image must be digest pinned')
            return image
    raise ValueError('Registry image missing')


def fixture_cases():
    return json.loads((FIXTURES / 'suite.json').read_text())['blueprints']


def bundle_output():
    return (ROOT / os.environ.get('KIT_DIST_DIR', 'dist')) / 'bundles' / os.environ.get('BUNDLE_VERSION', '0.2.0')
