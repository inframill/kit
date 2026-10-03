"""Blueprint fixtures and workflows used by the parent Kit integration harness.

Only tests import this module; production Blueprints are fetched from local sources.
"""
import json
import os
import pathlib
import platform
import re
import subprocess
import sys
import uuid

BLUEPRINTS = pathlib.Path(__file__).resolve().parents[1] / 'blueprints'
FIXTURES = pathlib.Path(__file__).resolve().parent / 'fixtures/blueprints'
LOCAL_REFERENCE = 'build.in/local-resource'


def run(args, **kwargs):
    print('+', ' '.join(map(str, args)), file=sys.stderr, flush=True)
    return subprocess.run(list(map(str, args)), check=True, text=True, **kwargs)


def local_paths():
    return [str(BLUEPRINTS), str(FIXTURES)]


def provider_ids(name):
    return {'debug': 'debug'} if name == 'hello-world' else {'opentofu': 'opentofu', 'ansible': 'ansible'}


def protocol_fixture(tmp):
    source = tmp / 'blueprints/protocol-fixture/latest'
    (source / 'templates/fixture').mkdir(parents=True)
    (source / 'templates/fixture/readme.txt').write_text('secret snapshot test\n')
    (source / 'blueprint.yaml').write_text(json.dumps({'api_version': 'inframill.dev/blueprint/v1', 'description': 'protocol fixture', 'values_schema': {'type': 'object', 'additionalProperties': False}, 'provider_settings': {'fixture': {'version': '0.1', 'settings': {'id': 'fixture', 'contract': '0.1.0'}}}}))
    return str(source.parent.parent)


def local_flow(client, reference=LOCAL_REFERENCE, name="test/local", fetch=True):
    architecture = 'arm64' if platform.machine() in ('arm64', 'aarch64') else 'amd64'
    data = {'architecture': architecture}
    namespace, local_name = name.split('/', 1)
    if fetch:
        client.fetch(reference, namespace)
    iid = client.initialize(namespace, local_name, reference, data)
    ids = provider_ids('local-resource')
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


def hello_world_flow(client, reference, namespace='test'):
    values = json.loads((BLUEPRINTS / 'examples/hello-world.json').read_text())
    client.fetch(reference, namespace)
    iid = client.initialize(namespace, 'hello-world', reference, values)
    workspace = pathlib.Path(client.config['engine']['dataDir']) / namespace / 'instances' / iid / 'workspace'
    assert {path.name for path in workspace.iterdir()} == {'debug'}
    message = f"{values['greeting']}, {values['name']}!"
    assert (workspace / 'debug/message.txt').read_text() == message + '\n'
    assert client.execute(namespace, iid, 'debug.echo', {'message': message}) == {'message': message}
    # Exercise the public CLI's new combined submit/follow path against real saved logs.
    arguments = client.root / 'stream-arguments.json'
    arguments.write_text(json.dumps({'stdout': 'stream-out-marker\n', 'stderr': 'stream-err-marker\n'}))
    streamed = subprocess.run(
        [client.binary, 'instance', 'exec', iid, 'debug.logs', '--namespace', namespace,
         '--endpoint', client.endpoint, '--arguments-file', str(arguments), '--stream'],
        env=client.env, capture_output=True, text=True, timeout=30)
    assert streamed.returncode == 0, streamed.stderr
    match = re.search(r'Execution admitted: ([0-7][0-9A-HJKMNP-TV-Z]{25})', streamed.stderr)
    assert match and 'succeeded' in streamed.stderr, streamed.stderr
    assert 'stream-out-marker' in streamed.stderr and 'stream-err-marker' in streamed.stderr
    for stream in ['stdout', 'stderr']:
        saved = subprocess.run(
            [client.binary, 'execution', 'logs', match[1], '--instance', iid, '--namespace', namespace,
             '--endpoint', client.endpoint, '--stream', stream],
            env=client.env, capture_output=True, text=True, timeout=30)
        assert saved.returncode == 0, saved.stderr
        if stream == 'stdout':
            assert streamed.stdout == saved.stdout
        else:
            assert saved.stdout in streamed.stderr
    arguments.write_text(json.dumps({'message': 'expected-stream-failure'}))
    failed = subprocess.run(
        [client.binary, 'instance', 'exec', iid, 'debug.fail', '--namespace', namespace,
         '--endpoint', client.endpoint, '--arguments-file', str(arguments), '--stream'],
        env=client.env, capture_output=True, text=True, timeout=30)
    assert failed.returncode == 1 and 'failed' in failed.stderr, failed
    artifact = {'name': 'greeting.txt', 'content': message}
    assert client.execute(namespace, iid, 'debug.write-artifact', artifact) == artifact
    assert client.execute(namespace, iid, 'debug.read-artifact', {'name': 'greeting.txt'}) == artifact
    print('Hello-world rendering and debug-only commands passed', flush=True)


def pv_flow(client, reference, tofu):
    client.fetch(reference, 'test')
    ids = provider_ids('pv-cloud')
    instances = []
    cases = [
        ('arm64', FIXTURES / 'examples/pv-cloud-arm64.json'),
        ('amd64', FIXTURES / 'examples/pv-cloud-amd64.json'),
        ('mixed', BLUEPRINTS / 'examples/pv-cloud.json'),
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


def runtime_flows(client, arch, tofu):
    client.create_namespace('smoke')
    reference = LOCAL_REFERENCE
    client.fetch(reference, 'smoke')
    iid = client.initialize('smoke', 'local', reference, {'architecture': arch})
    ids = provider_ids('local-resource')
    prefix, ansible = ids['opentofu'], ids['ansible']
    client.execute('smoke', iid, prefix + '.init')
    plan = client.execute('smoke', iid, prefix + '.plan')
    client.execute('smoke', iid, prefix + '.apply', {'plan_id': plan['plan_id']})
    client.execute('smoke', iid, ansible + '.apply', {'playbook': 'site'})
    plan = client.execute('smoke', iid, prefix + '.plan', {'destroy': True})
    client.execute('smoke', iid, prefix + '.destroy', {'plan_id': plan['plan_id']})
    reference = 'build.in/pv-cloud'
    values = json.loads((BLUEPRINTS / 'examples/pv-cloud.json').read_text())
    client.fetch(reference, 'smoke')
    iid = client.initialize('smoke', 'pv-cloud', reference, values)
    ids = provider_ids('pv-cloud')
    client.execute('smoke', iid, ids['ansible'] + '.syntax-check', {'playbook': 'site'})
    work = pathlib.Path(client.config['engine']['dataDir']) / 'smoke/instances' / iid / 'workspace' / 'opentofu'
    client.execute('smoke', iid, ids['opentofu'] + '.init')
    local_state_check(work, tofu)
    for args in [['validate', '-no-color'], ['test', '-no-color']]:
        subprocess.run([tofu, *args], cwd=work, env={**os.environ, 'HCLOUD_TOKEN': 'mock-only'}, check=True)
