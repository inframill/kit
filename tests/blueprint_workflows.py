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
import urllib.error
import uuid

from events_check import provider_events

BLUEPRINTS = pathlib.Path(__file__).resolve().parents[1] / 'blueprints'
FIXTURES = pathlib.Path(__file__).resolve().parent / 'fixtures/blueprints'
LOCAL_REFERENCE = 'built.in/local-resource'


def run(args, **kwargs):
    print('+', ' '.join(map(str, args)), file=sys.stderr, flush=True)
    return subprocess.run(list(map(str, args)), check=True, text=True, **kwargs)


def local_paths():
    return [str(BLUEPRINTS), str(FIXTURES)]


def provider_ids(name):
    if name == 'hello-world':
        return {'debug': 'debug'}
    ids = {'opentofu': 'opentofu', 'ansible': 'ansible'}
    if name == 'pv-cloud':
        ids['dispatch'] = 'dispatch'
    return ids


def protocol_fixture(tmp):
    source = tmp / 'blueprints/protocol-fixture/latest'
    (source / 'templates/fixture').mkdir(parents=True)
    (source / 'templates/fixture/readme.txt').write_text('secret snapshot test\n')
    (source / 'blueprint.yaml').write_text(json.dumps({
        'api_version': 'inframill.dev/blueprint/v1',
        'description': 'protocol fixture',
        'values_schema': {'type': 'object', 'additionalProperties': False},
        'command_arguments': {'fixture.probe': {
            'type': 'object', 'additionalProperties': False, 'required': ['text'],
            'properties': {'text': {'type': 'string'}, 'wait': {'type': 'boolean'}},
        }},
        'provider_settings': {'fixture': {
            'version': '0.1', 'settings': {'id': 'fixture', 'contract': '0.1.0'},
        }},
    }))
    return str(source.parent.parent)


def command_contract_check(client, namespace, iid, expected, invalid):
    commands = client.rpc('ListInstanceCommandDefinitions', {
        'instance_id': iid,
    }, namespace=namespace)['commands']
    names = [c['name'] for c in commands]
    assert names == sorted(names), commands
    assert set(names) == set(expected) | {
        'builtin.cancel', 'builtin.delete', 'builtin.rerender'}, commands
    assert all(json.loads(c['argumentsSchema']['json'])['type'] == 'object'
               for c in commands), commands

    def history():
        return client.rpc('ListCommandExecutions', {
            'instance_id': iid,
        }, namespace=namespace).get('executions', [])

    before = history()
    for command, arguments in invalid:
        try:
            client.rpc('SubmitCommand', {
                'instance_id': iid, 'command_name': command,
                'arguments': {'json': json.dumps(arguments)},
            }, namespace=namespace)
        except urllib.error.HTTPError as error:
            failure = json.load(error)
            assert failure['code'] == 'invalid_argument', failure
        else:
            raise AssertionError(f'Blueprint admitted invalid arguments for {command}')
    assert history() == before, 'Rejected arguments created execution history'


def local_flow(client, reference=LOCAL_REFERENCE, name="test/local", fetch=True):
    architecture = 'arm64' if platform.machine() in ('arm64', 'aarch64') else 'amd64'
    data = {'architecture': architecture}
    namespace, local_name = name.split('/', 1)
    if fetch:
        client.fetch(reference, namespace)
    iid = client.initialize(namespace, local_name, reference, data)
    ids = provider_ids('local-resource')
    prefix, ansible = ids['opentofu'], ids['ansible']
    command_contract_check(client, namespace, iid,
        ['opentofu.' + c for c in ['init', 'validate', 'plan', 'apply', 'destroy']] +
        ['ansible.' + c for c in ['inventory', 'ping', 'syntax-check', 'check', 'apply']], [
            ('opentofu.init', {'unknown': True}),
            ('opentofu.plan', {'destroy': None}),
            ('opentofu.plan', {'destroy': True, 'refresh_only': True}),
            ('opentofu.apply', {'plan_id': '00000000000000000000000000'}),
            ('opentofu.destroy', {'plan_id': None}),
            ('ansible.apply', {'playbook': ''}),
            ('ansible.apply', {'playbook': None}),
            ('ansible.apply', {'playbook': 'missing'}),
            ('ansible.check', {}),
            ('ansible.syntax-check', {'playbook': 'site', 'hosts': []}),
            ('ansible.ping', {'hosts': ['localhost', 'localhost']}),
            ('ansible.ping', {'hosts': None}),
        ])

    def do(command, args=None):
        return client.execute(namespace, iid, command, args)
    do(prefix + '.init')
    do(prefix + '.validate')
    plan = do(prefix + '.plan')
    applied = do(prefix + '.apply', {'plan_id': plan['plan_id']})
    assert applied['valid'] and applied['nodes'] == 1, applied
    do(ansible + '.inventory')
    ping = do(ansible + '.ping')
    assert ping['hosts'] and all(host['ok'] == 1 for host in ping['hosts'].values()), ping
    do(ansible + '.syntax-check', {'playbook': 'site'})
    do(ansible + '.check', {'playbook': 'site'})
    do(ansible + '.apply', {'playbook': 'site'})
    ansible_failure_flow(client, namespace, iid)
    local_rerender_check(client, namespace, iid, data)
    destroy = do(prefix + '.plan', {'destroy': True})
    destroyed = do(prefix + '.destroy', {'plan_id': destroy['plan_id']})
    assert destroyed['destroyed'] and destroyed['managed_state_empty'], destroyed
    assert not destroyed['valid'] and destroyed['nodes'] == 0, destroyed
    local_direct_flow(client, namespace, iid, prefix)
    provider_events(client, namespace, iid)


def local_direct_flow(client, namespace, iid, prefix):
    # Exercise omitted arguments through the real daemon, Blueprint schema and
    # OpenTofu. The fixture manages only a disposable local terraform_data resource.
    state = pathlib.Path(client.config['engine']['dataDir']) / namespace / 'instances' / iid / 'artifacts' / (prefix + '-terraform.tfstate')
    applied = client.execute(namespace, iid, prefix + '.apply')
    assert applied['valid'] and applied['nodes'] == 1, applied
    resources = json.loads(state.read_text())['resources']
    assert len(resources) == 1 and resources[0]['type'] == 'terraform_data', resources
    destroyed = client.execute(namespace, iid, prefix + '.destroy')
    assert destroyed == {'destroyed': True, 'managed_state_empty': True,
                         'artifact': prefix + '-nodes-v1.json', 'nodes': 0, 'valid': False}, destroyed
    assert json.loads(state.read_text()).get('resources', []) == []
    print('OpenTofu apply and destroy without plan_id passed', flush=True)


def ansible_failure_flow(client, namespace, iid):
    # Alter only this disposable instance. Prove real Ansible failures finish
    # and --stream shows task progress without leaking module result text.
    site = pathlib.Path(client.config['engine']['dataDir']) / namespace / 'instances' / iid / 'workspace/ansible/site.yml'
    original = site.read_bytes()
    site.write_text('- hosts: all\n  gather_facts: false\n  tasks:\n'
                    '    - ansible.builtin.fail:\n        msg: synthetic-private-result\n')
    try:
        failed = subprocess.run(
            [client.binary, 'instance', 'exec', iid, 'ansible.apply', '--namespace', namespace,
             '--endpoint', client.endpoint, '--arguments', '{"playbook":"site"}', '--stream'],
            env=client.env, capture_output=True, text=True, timeout=30)
        assert failed.returncode == 1 and 'failed' in failed.stderr, failed
        assert 'ansible.builtin.fail' in failed.stderr and '"ansible": "failed"' in failed.stderr, failed
        assert 'synthetic-private-result' not in failed.stdout + failed.stderr, failed
        match = re.search(r'Execution admitted: ([0-7][0-9A-HJKMNP-TV-Z]{25})', failed.stderr)
        assert match, failed
        result = client.rpc('GetCommandExecution', {'instance_id': iid, 'execution_id': match[1]}, namespace=namespace)
        assert result['execution']['status'] == 'COMMAND_EXECUTION_STATUS_FAILED', result
    finally:
        site.write_bytes(original)


def hello_world_flow(client, reference, namespace='test'):
    values = json.loads((BLUEPRINTS / 'examples/hello-world.json').read_text())
    client.fetch(reference, namespace)
    iid = client.initialize(namespace, 'hello-world', reference, values)
    command_contract_check(client, namespace, iid,
        ['debug.' + c for c in ['echo', 'fail', 'logs', 'read-artifact', 'record', 'wait', 'write-artifact']], [
            ('debug.echo', {'message': 42}),
            ('debug.echo', {'unknown': True}),
            ('debug.wait', {'durationMs': -1}),
            ('debug.write-artifact', {'name': 'greeting.txt'}),
        ])
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
         '--endpoint', client.endpoint, '--arguments', arguments.read_text(), '--stream'],
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


def pv_required_secrets(client, namespace, reference, values, name):
    required = ['app1-password', 'app2-password', 'cloudflare', 'hetzner', 'inframill-root-token', 'millway-root-token',
                'pv-cloud-registry-token', 'pv-cloud-tls-private-key']
    instances = pathlib.Path(client.config['engine']['dataDir']) / namespace / 'instances'
    before_files = set(instances.iterdir()) if instances.exists() else set()
    before = client.rpc('ListInstances', namespace=namespace).get('instances', [])
    # Fetch succeeds without credentials; creation must report every missing name
    # without leaving a workspace or reserving the name used by the successful retry.
    for missing in [required[index:] for index in range(0, len(required), 2)]:
        try:
            client.initialize(namespace, name, reference, values)
        except urllib.error.HTTPError as error:
            failure = json.load(error)
            assert error.code == 400 and failure['code'] == 'failed_precondition', failure
            assert f"missing required secrets: {', '.join(missing)};" in failure['message'], failure
            assert 'secret upsert' in failure['message'], failure
        else:
            raise AssertionError('Instance creation accepted missing required secrets')
        assert client.rpc('ListInstances', namespace=namespace).get('instances', []) == before
        assert (set(instances.iterdir()) if instances.exists() else set()) == before_files
        for secret in missing[:2]:
            client.rpc('UpsertNamespaceSecret', {
                'secret': {'name': secret, 'value': 'synthetic-pv-' + secret},
            }, namespace=namespace)
    print('pv-cloud rejects missing secrets before creation and accepts namespace upserts', flush=True)


def pv_flow(client, reference, tofu):
    latest = client.fetch(reference, 'test')
    reference = 'built.in/pv-cloud:0.10.0'
    release = client.fetch(reference, 'test')
    assert release['digest'] == latest['digest'], (release, latest)
    ids = provider_ids('pv-cloud')
    instances = []
    cases = [
        ('arm64', FIXTURES / 'examples/pv-cloud-arm64.json'),
        ('amd64', FIXTURES / 'examples/pv-cloud-amd64.json'),
        ('docker', BLUEPRINTS / 'examples/pv-cloud.json'),
    ]
    pv_required_secrets(client, 'test', reference,
                        json.loads(cases[0][1].read_text()), 'pv-arm64')
    for name, path in cases:
        values = json.loads(path.read_text())
        iid = client.initialize('test', 'pv-' + name, reference, values)
        instances.append(iid)
        if name == 'arm64':
            command_contract_check(client, 'test', iid,
                ['opentofu.' + c for c in ['init', 'validate', 'plan', 'apply', 'destroy']] +
                ['ansible.' + c for c in ['inventory', 'ping', 'syntax-check', 'check', 'apply']] +
                ['dispatch.push', 'dispatch.make'], [
                    ('opentofu.apply', {'plan_id': ''}),
                    ('opentofu.plan', {'destroy': True, 'refresh_only': True}),
                    ('ansible.apply', {'playbook': 'missing'}),
                    ('dispatch.push', {'directory': ['missing']}),
                    ('dispatch.push', {'directory': []}),
                    ('dispatch.push', {'directory': ['postgres', 'postgres']}),
                    ('dispatch.push', {'directory': ['postgres'], 'target': 'install'}),
                    ('dispatch.make', {'directory': ['postgres', 'postgres'], 'target': 'start'}),
                    ('dispatch.make', {'directory': ['postgres']}),
                    ('dispatch.make', {'directory': ['inframill'], 'target': None}),
                    ('dispatch.make', {'directory': ['inframill'], 'target': '-f'}),
                    ('dispatch.make', {'directory': ['postgres'], 'target': 'missing'}),
                    ('dispatch.make', {'directory': ['postgres'], 'target': 'install'}),
                    ('dispatch.make', {'directory': ['postgres'], 'target': 'password'}),
                    ('dispatch.make', {'directory': ['inframill'], 'target': 'validate'}),
                    ('dispatch.make', {'directory': ['inframill'], 'target': 'login'}),
                    ('dispatch.make', {'directory': ['postgres'], 'target': 'setup'}),
                    ('dispatch.make', {'directory': ['postgres'], 'target': 'rotate-credentials'}),
                ])
        work = pathlib.Path(client.config['engine']['dataDir']) / 'test/instances' / iid / 'workspace/opentofu'
        assert f'app    = {json.dumps("pv-" + name)}' in (work / 'main.tf').read_text()
        assert f'instance_id = {json.dumps(iid)}' in (work / 'dns.tf').read_text()
        settings = json.loads((work / 'kit-provider.json').read_text())
        assert settings['environment'] == {'HCLOUD_TOKEN': 'hetzner', 'CLOUDFLARE_API_TOKEN': 'cloudflare'}
        assert json.loads((work / 'terraform.auto.tfvars.json').read_text()) == {
            'nodes': values['nodes'], 'private_networks': values['network']['private_networks'],
            'ssh_source_ips': values['ssh']['source_ips'],
            'cloudflare_zone_id': values['cloudflare']['zone_id'],
            'cloudflare_zone_name': values['cloudflare']['zone_name'],
            'postgres': values.get('postgres', {}),
        }
        assert f'loc    = {json.dumps(values["network"]["zone"])}' in (work / 'main.tf').read_text()
        cloud_init = (work / 'cloud-init.yaml').read_text()
        assert f'name: {json.dumps(values["ssh"]["user"])}' in cloud_init
        keys_json = json.dumps(values['ssh']['public_keys'], separators=(',', ':'))
        assert f'ssh_authorized_keys: {keys_json}' in cloud_init
        dispatch = work.parent / 'dispatch'
        delivery = json.loads((dispatch / 'kit-provider.json').read_text())
        assert delivery['address_mode'] == values['ansible']['address_mode']
        assert delivery['ssh_user'] == values['ssh']['user']
        postgres_mapping = {
            'source': 'postgres', 'destination': '/opt/pv-cloud/postgres',
            'select': {'roles': ['data']},
            'become': True,
            'secret_files': {'secrets/tls_private_key': 'pv-cloud-tls-private-key'},
        }
        databases = values.get('postgres', {}).get('databases', [
            {'name': 'app1', 'password_secret': 'app1-password'},
            {'name': 'app2', 'password_secret': 'app2-password'},
        ])
        for database in databases:
            postgres_mapping['secret_files'][f"secrets/{database['name']}_password"] = database['password_secret']
        inframill_mapping = {
            'source': 'inframill', 'destination': '/opt/pv-cloud/inframill',
            'select': {'roles': ['worker']},
            'secret_files': {'secrets/root_token': 'inframill-root-token',
                             'secrets/tls_private_key': 'pv-cloud-tls-private-key'},
            'become': True,
        }
        if values.get('registry'):
            postgres_mapping['secret_files']['secrets/registry_token'] = 'pv-cloud-registry-token'
            inframill_mapping['secret_files']['secrets/registry_token'] = 'pv-cloud-registry-token'
        assert delivery['directories']['postgres'] == postgres_mapping
        assert delivery['directories']['inframill'] == inframill_mapping
        millway_mapping = {
            'source': 'millway', 'destination': '/opt/pv-cloud/millway', 'select': {'roles': ['web']},
            'become': True,
            'secret_files': {'secrets/root_token': 'millway-root-token', 'secrets/database_password':
                            next(database['password_secret'] for database in databases if database['name'] == 'app1'),
                            'secrets/inframill_root_token': 'inframill-root-token',
                            'secrets/tls_private_key': 'pv-cloud-tls-private-key'},
        }
        if values.get('registry'):
            millway_mapping['secret_files']['secrets/registry_token'] = 'pv-cloud-registry-token'
        assert delivery['directories']['millway'] == millway_mapping
        assert json.loads((dispatch / 'millway/settings.json').read_text()) == {
            'instance_id': iid, 'zone_name': values['cloudflare']['zone_name'], 'nodes': values['nodes'],
            'millway': values['millway'], 'postgres': values.get('postgres', {}),
        }
        for service in ('inframill', 'postgres', 'millway'):
            assert (dispatch / service / 'tls/fullchain.pem').read_text().strip() == values['tls']['certificate'].strip()
        assert json.loads((dispatch / 'postgres/settings.json').read_text()) == values.get('postgres', {})
        assert json.dumps(values['inframill']['image']) in (dispatch / 'inframill/compose.yaml').read_text()
        assert not (dispatch / 'inframill/secrets').exists()
        for service in ('inframill', 'postgres', 'millway'):
            assert json.loads((dispatch / service / 'registry.json').read_text()) == values.get('registry', {})
        assert not any(b'synthetic-pv-' in path.read_bytes()
                       for path in work.parent.rglob('*') if path.is_file())
        assert (dispatch / 'postgres/compose.yaml').is_file()
        assert (dispatch / 'postgres/Makefile').is_file()
        assert not (work.parent / 'ansible/roles/data/tasks/main.yml').exists()
        client.execute('test', iid, ids['ansible'] + '.syntax-check', {'playbook': 'site'})
    pv_registry_flow(client, reference, values)
    pv_postgres_flow(client, reference, values)
    data = pathlib.Path(client.config['engine']['dataDir'])
    workspaces = [data / 'test/instances' / iid / 'workspace' / 'opentofu' for iid in instances]
    assert all((work / 'main.tf').is_file() for work in workspaces)
    # Network references must resolve through the new object group.
    network_values = {**values,
        'network': {**values['network'], 'private_networks': [{'name': 'app', 'cidr': '10.40.1.0/24'}]},
        'nodes': {name: {**node, 'private_networks': ['app']} for name, node in values['nodes'].items()},
    }
    client.initialize('test', 'pv-private-network', reference, network_values)
    invalid_groups = [
        {**values, 'ssh': values['ssh']['user']},
        {**values, 'ssh_user': values['ssh']['user']},
        {**values, 'cloud': {'secret_ref': 'hetzner'}},
        {**values, 'cloudflare': {**values['cloudflare'], 'secret_ref': 'cloudflare'}},
        {**values, 'inframill': {**values['inframill'], 'tls_cert': values['tls']['certificate']}},
        {**values, 'inframill': {**values['inframill'], 'tls_key_secret_ref': 'pv-cloud-tls-private-key'}},
        {**values, 'inframill': {**values['inframill'], 'root_token_secret_ref': 'inframill-root-token'}},
        {**network_values, 'network': values['network']},
        {**network_values, 'network': {**values['network'], 'private_networks': [
            {'name': 'app', 'cidr': '10.40.1.0/24'}, {'name': 'overlap', 'cidr': '10.40.1.0/25'},
        ]}},
    ]
    for index, invalid in enumerate(invalid_groups):
        try:
            client.initialize('test', 'pv-reject-group-' + str(index), reference, invalid)
        except urllib.error.HTTPError as error:
            assert error.code == 400, error
        else:
            raise AssertionError('Invalid grouped Values were accepted: ' + str(index))
    for reserved in ['all', 'ungrouped', 'bad_name', 'trailing-', 'a' * 37]:
        invalid = {**values, 'nodes': {reserved: next(iter(values['nodes'].values()))}}
        try:
            client.initialize('test', 'pv-reject-' + reserved, reference, invalid)
        except urllib.error.HTTPError as error:
            assert error.code == 400, error
        else:
            raise AssertionError('Invalid node name was accepted: ' + reserved)
    for role in ['data', 'worker', 'web']:
        invalid = {**values, 'nodes': {role + '-1': {
            **next(iter(values['nodes'].values())), 'roles': ['common', role],
        }}}
        try:
            client.initialize('test', 'pv-reject-' + role + '-without-docker', reference, invalid)
        except urllib.error.HTTPError as error:
            assert error.code == 400, error
        else:
            raise AssertionError(role + ' role without Docker was accepted')
    ansible = pathlib.Path(client.config['engine']['providers']['ansible']['args'][1])
    run([ansible.with_name('python3'), pathlib.Path(__file__).with_name('pv_roles_check.py'),
         ansible, workspaces[0].parent / 'ansible/site.yml'])
    # Provider init uses no credentials even after namespace secrets are removed.
    for secret in ['app1-password', 'app2-password', 'cloudflare', 'hetzner', 'inframill-root-token', 'millway-root-token',
                   'pv-cloud-registry-token', 'pv-cloud-tls-private-key']:
        client.rpc('DeleteNamespaceSecret', {'name': secret}, namespace='test')
    client.execute('test', instances[0], ids['opentofu'] + '.init')
    local_state_check(workspaces[0], tofu)
    for args in [['validate', '-no-color'], ['test', '-no-color']]:
        run([tofu, *args], cwd=workspaces[0], env={**os.environ, 'HCLOUD_TOKEN': 'mock-only'})


def pv_registry_flow(client, reference, values):
    registry = {'host': 'registry.example.invalid:5000', 'username': 'robot$pv-cloud'}
    # Token presence is required at creation; its contents are resolved during push.
    iid = client.initialize('test', 'pv-registry', reference, {**values, 'registry': registry})
    workspace = pathlib.Path(client.config['engine']['dataDir']) / 'test/instances' / iid / 'workspace/dispatch'
    delivery = json.loads((workspace / 'kit-provider.json').read_text())
    for service in ('inframill', 'postgres', 'millway'):
        assert json.loads((workspace / service / 'registry.json').read_text()) == registry
        assert delivery['directories'][service]['secret_files']['secrets/registry_token'] == 'pv-cloud-registry-token'
        assert not (workspace / service / 'secrets/registry_token').exists()
    for index, invalid in enumerate([
        {}, {'host': 'ghcr.io'}, {'username': 'example-user'},
        {**registry, 'host': 'https://ghcr.io'}, {**registry, 'host': 'ghcr.io/example'},
        {**registry, 'username': ''}, {**registry, 'username': 'two users'},
        {**registry, 'password': 'must-not-be-in-values'},
    ]):
        try:
            client.initialize('test', 'pv-reject-registry-' + str(index), reference,
                              {**values, 'registry': invalid})
        except urllib.error.HTTPError as error:
            assert error.code == 400, error
        else:
            raise AssertionError('Invalid registry Values were accepted')
    print('pv-cloud optional registry settings render login configuration and secret references', flush=True)


def pv_postgres_flow(client, reference, values):
    settings = {'port': 55432, 'source_ips': ['192.0.2.0/24', '2001:db8::/32'],
                'databases': [{'name': 'app1', 'password_secret': 'app1-password'},
                              {'name': 'app2', 'password_secret': 'app2-password'}]}
    iid = client.initialize('test', 'pv-postgres', reference, {**values, 'postgres': settings})
    work = pathlib.Path(client.config['engine']['dataDir']) / 'test/instances' / iid / 'workspace'
    delivery = json.loads((work / 'dispatch/kit-provider.json').read_text())
    assert delivery['directories']['postgres']['secret_files']['secrets/app1_password'] == 'app1-password'
    assert delivery['directories']['postgres']['secret_files']['secrets/app2_password'] == 'app2-password'
    assert json.loads((work / 'dispatch/postgres/settings.json').read_text()) == settings
    assert json.loads((work / 'opentofu/terraform.auto.tfvars.json').read_text())['postgres'] == settings
    for index, invalid in enumerate([
        {'databases': []}, {'databases': [{'name': 'app1'}, {'name': 'app1'}]},
        {'databases': [{'name': 'postgres'}]}, {'databases': [{'name': 'pg_admin'}]},
        {'databases': [{'name': 'app1', 'password': 'not-in-values'}]},
        {'databases': [{'name': 'app1', 'password_secret': '../secret'}]},
        {'databases': [{'name': 'app1'}]},
        {'databases': [{'name': 'app1', 'password_secret': 'app1-password'}, {'name': 'app2'}]},
        {'port': 0}, {'memory_mb': 128}, {'max_connections': True}, {'source_ips': []},
    ]):
        try:
            client.initialize('test', 'pv-reject-postgres-' + str(index), reference, {**values, 'postgres': invalid})
        except urllib.error.HTTPError as error:
            assert error.code == 400, error
        else:
            raise AssertionError('Invalid PostgreSQL Values were accepted')
    print('pv-cloud database list, shared TLS, credential references and public access settings passed', flush=True)


def local_rerender_check(client, namespace, iid, values):
    instance = pathlib.Path(client.config['engine']['dataDir']) / namespace / 'instances' / iid
    state = instance / 'artifacts/opentofu-terraform.tfstate'
    before = json.loads(state.read_text())
    assert before['resources'], before
    client.execute(namespace, iid, 'builtin.rerender', {'values': values})
    assert not (instance / 'workspace/opentofu/.terraform').exists()
    assert json.loads(state.read_text()) == before
    client.execute(namespace, iid, 'opentofu.init')
    plan = client.execute(namespace, iid, 'opentofu.plan')
    assert not plan['changes'], plan
    assert json.loads(state.read_text())['lineage'] == before['lineage']
    applied = client.execute(namespace, iid, 'opentofu.apply', {'plan_id': plan['plan_id']})
    assert applied['valid'] and applied['nodes'] == 1, applied


def local_state_check(work, tofu):
    backend = json.loads((work / '.terraform/terraform.tfstate').read_text())['backend']
    assert backend['type'] == 'local', backend
    state_path = work.parent.parent / 'artifacts/opentofu-terraform.tfstate'
    assert pathlib.Path(backend['config']['path']) == state_path, backend
    # Write an empty synthetic state; these commands never provision cloud resources.
    state = {'version': 4, 'serial': 1, 'lineage': str(uuid.uuid4()), 'outputs': {}, 'resources': []}
    run([tofu, 'state', 'push', '-'], cwd=work, input=json.dumps(state))
    stored = json.loads(state_path.read_text())
    assert not (work / 'terraform.tfstate').exists()
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
    client.execute('smoke', iid, ansible + '.ping')
    client.execute('smoke', iid, ansible + '.apply', {'playbook': 'site'})
    ansible_failure_flow(client, 'smoke', iid)
    local_rerender_check(client, 'smoke', iid, {'architecture': arch})
    plan = client.execute('smoke', iid, prefix + '.plan', {'destroy': True})
    client.execute('smoke', iid, prefix + '.destroy', {'plan_id': plan['plan_id']})
    local_direct_flow(client, 'smoke', iid, prefix)
    reference = 'built.in/pv-cloud:0.10.0'
    values = json.loads((BLUEPRINTS / 'examples/pv-cloud.json').read_text())
    client.fetch(reference, 'smoke')
    pv_required_secrets(client, 'smoke', reference, values, 'pv-cloud')
    iid = client.initialize('smoke', 'pv-cloud', reference, values)
    ids = provider_ids('pv-cloud')
    client.execute('smoke', iid, ids['ansible'] + '.syntax-check', {'playbook': 'site'})
    work = pathlib.Path(client.config['engine']['dataDir']) / 'smoke/instances' / iid / 'workspace' / 'opentofu'
    client.execute('smoke', iid, ids['opentofu'] + '.init')
    local_state_check(work, tofu)
    for args in [['validate', '-no-color'], ['test', '-no-color']]:
        subprocess.run([tofu, *args], cwd=work, env={**os.environ, 'HCLOUD_TOKEN': 'mock-only'}, check=True)
