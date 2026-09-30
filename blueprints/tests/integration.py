"""Blueprint fixtures and workflows used by the parent Kit integration harness.

Only tests import this module; build.py remains the standalone publisher.
"""
import json
import os
import pathlib
import platform
import re
import subprocess
import sys
import uuid

BLUEPRINTS = pathlib.Path(__file__).resolve().parents[1]
FIXTURES = BLUEPRINTS / 'tests/fixtures'
BLUEPRINT_PYTHON = BLUEPRINTS / '.venv/bin/python'


def run(args, **kwargs):
    print('+', ' '.join(map(str, args)), file=sys.stderr, flush=True)
    return subprocess.run(list(map(str, args)), check=True, text=True, **kwargs)


def blueprint_root(name):
    return FIXTURES if (FIXTURES / name).is_dir() else BLUEPRINTS


def requirements(name, version):
    root = blueprint_root(name)
    release = json.loads((root / 'releases.json').read_text())
    return next(b['providers'] for b in release['blueprints'] if b['name'] == name and b['version'] == version)


def provider_ids(name, version):
    return {d['name']: d['id'] for d in requirements(name, version)}


def registry_image():
    image = json.loads((FIXTURES / 'suite.json').read_text())['registry_image']
    if not re.fullmatch(r'.+@sha256:[0-9a-f]{64}', image):
        raise ValueError('Registry image must be digest pinned')
    return image


def fixture_cases():
    return json.loads((FIXTURES / 'suite.json').read_text())['blueprints']


def blueprint_releases():
    return json.loads((BLUEPRINTS / 'releases.json').read_text())['blueprints']


def publish(name, version, env):
    for action in ['build', 'push', 'push']:
        run([BLUEPRINT_PYTHON, BLUEPRINTS / 'build.py', action,
             '--root', blueprint_root(name), '--name', name, '--version', version,
             '--dist', pathlib.Path(env['KIT_DIST_DIR']) / 'blueprints'], cwd=BLUEPRINTS, env=env)


def publish_protocol_fixture(tmp, env, engine_version):
    # This blueprint and provider exist only in test builds, never the release set.
    source = tmp / 'blueprints/protocol-fixture/0.1.0'
    (source / 'templates/fixture').mkdir(parents=True)
    (source / 'templates/fixture/readme.txt').write_text('secret snapshot test\n')
    (source / 'blueprint.yaml').write_text(json.dumps({'api_version': 'inframill.dev/blueprint/v1', 'description': 'protocol fixture', 'values_schema': {'type': 'object', 'additionalProperties': False}, 'provider_settings': {'fixture': {'version': '0.1', 'settings': {'id': 'fixture', 'contract': '0.1.0'}}}}))
    prefix = [str(BLUEPRINT_PYTHON), str(BLUEPRINTS / 'build.py')]
    selectors = ['--root', str(tmp / 'blueprints'), '--name', 'protocol-fixture', '--version', '0.1.0',
                 '--dist', str(tmp / 'dist')]
    digest = subprocess.check_output(prefix + ['source-digest'] + selectors, env=env, text=True).strip()
    release = tmp / 'blueprints/releases.json'
    release.write_text(json.dumps({'schema': 1, 'blueprints': [{'name': 'protocol-fixture', 'version': '0.1.0',
        'inframill_version': engine_version, 'protocol': 1,
        'platforms': ['linux/amd64', 'linux/arm64'], 'architectures': ['amd64', 'arm64'],
        'providers': [{'id': 'fixture', 'name': 'fixture', 'version': '0.1.0'}], 'source_sha256': digest, 'status': 'test'}]}))
    for action in ['build', 'push']:
        subprocess.run(prefix + [action] + selectors, env=env, check=True)


def local_flow(client, version, reference, name=None, fetch=True):
    data = json.loads((FIXTURES / f'examples/local-resource-{version}.json').read_text())
    data['architecture'] = 'arm64' if platform.machine() in ('arm64', 'aarch64') else 'amd64'
    namespace, local_name = (name or 'test/local-' + version.replace('.', '-')).split('/', 1)
    if fetch:
        client.fetch(reference, namespace)
    iid = client.initialize(namespace, local_name, reference, data)
    ids = provider_ids('local-resource', version)
    prefix, ansible = ids['opentofu'], ids['ansible']

    def do(command, args=None):
        return client.execute(namespace, iid, command, args)
    do(prefix + '.init')
    do(prefix + '.validate')
    plan = do(prefix + '.plan')
    do(prefix + '.apply', {'plan_id': plan['plan_id']})
    do(ansible + '.inventory')
    do(ansible + '.syntax-check', {'playbook': 'site'})
    do(ansible + '.check', {'playbook': 'site'})
    do(ansible + '.apply', {'playbook': 'site'})
    destroy = do(prefix + '.plan', {'destroy': True})
    do(prefix + '.destroy', {'plan_id': destroy['plan_id']})


def pv_flow(client, reference, tofu):
    client.fetch(reference, 'test')
    ids = provider_ids('pv-cloud', '0.3.0')
    instances = []
    cases = [
        ('arm64', FIXTURES / 'examples/pv-cloud-arm64.json'),
        ('amd64', FIXTURES / 'examples/pv-cloud-amd64.json'),
        ('mixed', BLUEPRINTS / 'examples/pv-cloud-0.3.0.json'),
    ]
    for name, path in cases:
        values = json.loads(path.read_text())
        iid = client.initialize('test', 'pv-' + name, reference, values)
        instances.append(iid)
        client.execute('test', iid, ids['ansible'] + '.syntax-check', {'playbook': 'site'})
    data = pathlib.Path(client.config['engine']['dataDir'])
    workspaces = [data / 'test/instances' / iid / 'workspace' / 'opentofu' for iid in instances]
    assert all((work / 'main.tf').is_file() for work in workspaces)
    # Init must work without any namespace credentials and initialize the real backend.
    client.execute('test', instances[0], ids['opentofu'] + '.init')
    local_state_check(workspaces[0], tofu)
    for args in [['validate', '-no-color'], ['test', '-no-color']]:
        run([tofu, *args], cwd=workspaces[0], env={**os.environ, 'HCLOUD_TOKEN': 'mock-only'})


def local_state_check(work, tofu):
    backend = json.loads((work / '.terraform/terraform.tfstate').read_text())['backend']
    assert backend['type'] == 'local', backend
    # Write an empty synthetic state; these commands never provision cloud resources.
    state = {'version': 4, 'serial': 1, 'lineage': str(uuid.uuid4()), 'outputs': {}, 'resources': []}
    run([tofu, 'state', 'push', '-'], cwd=work, input=json.dumps(state))
    stored = json.loads((work / 'terraform.tfstate').read_text())
    pulled = json.loads(run([tofu, 'state', 'pull'], cwd=work, capture_output=True).stdout)
    assert stored['lineage'] == pulled['lineage'] == state['lineage']
    assert stored['resources'] == pulled['resources'] == []


def verify(client, name, version, reference):
    values = json.loads((blueprint_root(name) / f'examples/{name}-{version}.json').read_text())
    release = next(b for b in json.loads((blueprint_root(name) / 'releases.json').read_text())['blueprints']
                   if b['name'] == name and b['version'] == version)
    candidates = [d['id'] for d in release['providers'] if d['name'] == 'ansible']
    if len(candidates) != 1:
        raise RuntimeError('Verification requires a fixture selecting one Ansible provider')
    client.create_namespace('verify')
    client.fetch(reference, 'verify')
    iid = client.initialize('verify', 'blueprint', reference, values)
    client.execute('verify', iid, candidates[0] + '.syntax-check', {'playbook': 'site'})


def runtime_flows(client, registry, arch, tofu):
    client.create_namespace('smoke')
    for case in fixture_cases():
        version = case['version']
        reference = f'{registry}/inframill/blueprints/local-resource:{version}'
        client.fetch(reference, 'smoke')
        iid = client.initialize('smoke', 'v' + version.replace('.', '-'), reference, {'architecture': arch})
        ids = provider_ids(case['name'], version)
        prefix, ansible = ids['opentofu'], ids['ansible']
        client.execute('smoke', iid, prefix + '.init')
        plan = client.execute('smoke', iid, prefix + '.plan')
        client.execute('smoke', iid, prefix + '.apply', {'plan_id': plan['plan_id']})
        client.execute('smoke', iid, ansible + '.apply', {'playbook': 'site'})
        plan = client.execute('smoke', iid, prefix + '.plan', {'destroy': True})
        client.execute('smoke', iid, prefix + '.destroy', {'plan_id': plan['plan_id']})
    reference = registry + '/inframill/blueprints/pv-cloud:0.3.0'
    values = json.loads((BLUEPRINTS / 'examples/pv-cloud-0.3.0.json').read_text())
    client.fetch(reference, 'smoke')
    iid = client.initialize('smoke', 'pv-cloud', reference, values)
    release = next(b for b in blueprint_releases() if b['name'] == 'pv-cloud' and b['version'] == '0.3.0')
    ids = {d['name']: d['id'] for d in release['providers']}
    client.execute('smoke', iid, ids['ansible'] + '.syntax-check', {'playbook': 'site'})
    work = pathlib.Path(client.config['engine']['dataDir']) / 'smoke/instances' / iid / 'workspace' / 'opentofu'
    client.execute('smoke', iid, ids['opentofu'] + '.init')
    local_state_check(work, tofu)
    for args in [['validate', '-no-color'], ['test', '-no-color']]:
        subprocess.run([tofu, *args], cwd=work, env={**os.environ, 'HCLOUD_TOKEN': 'mock-only'}, check=True)
