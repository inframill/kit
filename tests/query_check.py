"""Verify namespace isolation and current RPC/CLI resource contracts."""
import json
import pathlib
import re
import subprocess
import urllib.error


def query_check(client, reference, arch):
    def invoke(*args):
        return subprocess.run(
            [client.binary, *args, '--endpoint', client.endpoint, '-o', 'json'],
            env=client.env, capture_output=True, text=True, timeout=30)

    def request(*args):
        result = invoke(*args)
        assert result.returncode == 0, result.stderr
        return json.loads(result.stdout)

    def rejected(method, body=None, namespace=None, code='invalid_argument'):
        try:
            client.rpc(method, body, namespace=namespace)
        except urllib.error.HTTPError as error:
            failure = json.load(error)
            assert failure['code'] == code, failure
        else:
            raise AssertionError(method + ' unexpectedly succeeded')

    for resource in ['blueprint', 'command', 'instance', 'namespace', 'provider', 'registry', 'secret']:
        retired = subprocess.run([client.binary, 'server', resource, '--help'],
                                 env=client.env, capture_output=True, text=True, timeout=30)
        assert retired.returncode == 2 and not retired.stdout, retired
    for args in [('command', '--help'), ('provider', 'commands', '--instance', 'same')]:
        retired = invoke(*args)
        assert retired.returncode == 2 and not retired.stdout, retired

    commands = request('provider', 'commands')['commands']
    wire_commands = client.rpc('ListCommandDefinitions')['commands']
    assert commands == [{**c, 'argumentsSchema': json.loads(c['argumentsSchema']['json'])}
                        for c in wire_commands], commands
    assert {'opentofu.plan', 'ansible.apply'} <= {c['name'] for c in commands}
    assert not any(c['name'].startswith('builtin.') for c in commands)
    for command in commands:
        assert request('provider', 'commands', command['name']) == command

    methods = [('blueprint', 'ListBlueprints', 'blueprints'),
               ('secret', 'ListNamespaceSecretNames', 'names'),
               ('registry', 'ListNamespaceRegistryHosts', 'hosts'),
               ('instance', 'ListInstances', 'instances')]
    for resource, method, _ in methods:
        rejected(method)
        rejected(method, {'namespace': 'query-alpha'})
        assert invoke(resource, 'list').returncode == 2
    for method in ['GetServerVersion', 'ListProviders', 'ListCommandDefinitions', 'ListNamespaces']:
        rejected(method, namespace='query-alpha')
    assert invoke('provider', 'list', '--namespace', 'query-alpha').returncode == 2

    # Inframill 0.20 exposes namespace IDs end-to-end, preserving case and hyphens.
    for namespace_id in ['Query-Team', '-Team-']:
        created = client.create_namespace(namespace_id)
        assert created == {'id': namespace_id, 'status': 'NAMESPACE_STATUS_ACTIVE'}, created
        assert client.rpc('GetNamespace', {'id': namespace_id})['namespace'] == created
        metadata = client.config['engine']['dataDir']
        stored = json.loads((pathlib.Path(metadata) / namespace_id / 'namespace.json').read_text())
        assert stored['id'] == namespace_id and 'name' not in stored, stored
        result = subprocess.run(
            [client.binary, 'namespace', 'get', '--endpoint', client.endpoint,
             '-o', 'json', '--', namespace_id], env=client.env, capture_output=True, text=True, timeout=30)
        assert result.returncode == 0 and json.loads(result.stdout)['namespace'] == created, result
        client.fetch(reference, namespace_id)
        instance_id = client.initialize(namespace_id, 'Web-Prod', reference, {'architecture': arch})
        assert request('instance', 'get', 'Web-Prod', '--namespace=' + namespace_id)['instance']['id'] == instance_id
        client.rpc('DeleteNamespace', {'id': namespace_id})
    client.poll(lambda: not client.rpc('ListNamespaces').get('namespaces', []), timeout=30)
    for invalid in ['', 'bad_name', 'has space', 'team/name']:
        rejected('CreateNamespace', {'id': invalid})
    for method in ['CreateNamespace', 'GetNamespace', 'DeleteNamespace']:
        rejected(method, {'name': 'obsolete'})

    instances = {}
    blueprints = {}
    for namespace in ['query-beta', 'query-alpha', 'query-empty']:
        created = client.create_namespace(namespace)
        assert client.rpc('GetNamespace', {'id': namespace})['namespace'] == created
        assert request('namespace', 'get', namespace)['namespace'] == created
        assert invoke('namespace', 'get', namespace, '--namespace', namespace).returncode == 2
        if namespace == 'query-empty':
            continue
        blueprints[namespace] = client.fetch(reference, namespace)
        assert 'namespace' not in blueprints[namespace]
        instances[namespace] = [client.initialize(namespace, name, reference, {'architecture': arch})
                                for name in ['same', 'second']]
        assert all(re.fullmatch(r'[0-7][0-9A-HJKMNP-TV-Z]{25}', iid) for iid in instances[namespace])
        secrets = client.root / 'query-secrets.json'
        secrets.write_text(json.dumps({'token': 'synthetic-query-secret'}))
        request('secret', 'replace', '--namespace', namespace, '--file', str(secrets))
        registries = client.root / 'query-registries.json'
        registries.write_text(json.dumps({
            'example.invalid': {'username': 'synthetic-query-user', 'password': 'synthetic-query-password'}}))
        request('registry', 'replace', '--namespace', namespace, '--file', str(registries))
        assert request('blueprint', 'get', reference, '--namespace', namespace) == blueprints[namespace]
        resolved = request('blueprint', 'resolve', reference, '--namespace', namespace)
        wire_blueprint = client.rpc('GetBlueprint', {'reference': reference}, namespace=namespace)
        assert resolved['blueprint'] == wire_blueprint['blueprint'] == blueprints[namespace]
        schema = json.loads(wire_blueprint['valuesSchema']['json'])
        assert resolved['valuesSchema'] == schema, resolved
        assert schema['required'] == ['architecture'], schema
        assert schema['properties']['architecture']['enum'] == ['amd64', 'arm64'], schema
        instance = request('instance', 'get', 'same', '--namespace', namespace)['instance']
        assert instance['id'] == instances[namespace][0] and instance['name'] == 'same', instance
        assert 'namespace' not in instance
        assert invoke('instance', 'get', namespace + '/same', '--namespace', namespace).returncode == 2
        paged = request('instance', 'list', '--namespace', namespace, '--all', '--limit', '1')['instances']
        assert {i['id'] for i in paged} == set(instances[namespace])

    rejected('GetNamespace', {'id': 'missing'}, code='not_found')
    foreign = instances['query-beta'][0]
    for method, body in [('GetInstance', {'instance_id': foreign}),
                         ('SubmitCommand', {'instance_id': foreign, 'command_name': 'builtin.delete',
                                            'arguments': {'json': '{}'}}),
                         ('ListCommandExecutions', {'instance_id': foreign})]:
        rejected(method, body, namespace='query-alpha', code='not_found')
    assert invoke('instance', 'get', foreign, '--namespace', 'query-alpha').returncode != 0
    page = client.rpc('ListInstances', {'limit': 1}, namespace='query-alpha')
    rejected('ListInstances', {'cursor': page['nextCursor']}, namespace='query-beta')

    for resource, method, field in methods:
        for namespace in ['query-alpha', 'query-beta', 'query-empty']:
            rows = client.rpc(method, namespace=namespace).get(field, [])
            if namespace == 'query-empty':
                assert rows == [], rows
            elif field == 'blueprints':
                assert rows == [blueprints[namespace]], rows
            elif field == 'names':
                assert rows == [{'name': 'token'}], rows
            elif field == 'hosts':
                assert rows == [{'host': 'example.invalid'}], rows
            else:
                assert {i['id'] for i in rows} == set(instances[namespace]), rows
            assert all('namespace' not in row for row in rows), rows
            cli_rows = request(resource, 'list', '--namespace', namespace).get(field, [])
            expected_rows = ([{**row, 'values': json.loads(row['values']['json'])} for row in rows]
                             if resource == 'instance' else rows)
            assert cli_rows == expected_rows, (resource, cli_rows, expected_rows)
        table = subprocess.run(
            [client.binary, resource, 'list', '--namespace', 'query-alpha', '--endpoint', client.endpoint],
            env=client.env, capture_output=True, text=True, timeout=30)
        assert table.returncode == 0, table.stderr
        assert 'namespace' not in table.stdout.lower() and 'synthetic-query-' not in table.stdout, table.stdout
        assert invoke(resource, 'list', 'query-alpha').returncode == 2
        rejected(method, namespace='missing', code='not_found')
        assert invoke(resource, 'list', '--namespace', 'query-alpha', '--columns', 'namespace').returncode == 2

    for namespace in ['query-alpha', 'query-beta', 'query-empty']:
        request('secret', 'clear', '--namespace', namespace)
        request('registry', 'clear', '--namespace', namespace)
        assert client.rpc('ListNamespaceSecretNames', namespace=namespace).get('names', []) == []
        assert client.rpc('ListNamespaceRegistryHosts', namespace=namespace).get('hosts', []) == []
        client.rpc('DeleteNamespace', {'id': namespace})
    client.poll(lambda: not client.rpc('ListNamespaces').get('namespaces', []), timeout=30)
    print('Namespace inspection, header isolation, local selectors, ULIDs and scoped pagination passed', flush=True)
