"""Verify scoped and all-namespace queries through the released RPC and CLI."""
import json
import subprocess
import urllib.error


def query_check(client, reference, arch):
    def request(*args):
        result = subprocess.run(
            [client.binary, *args, '--endpoint', client.endpoint, '-o', 'json'],
            env=client.env, capture_output=True, text=True, timeout=30)
        assert result.returncode == 0, result.stderr
        return json.loads(result.stdout)

    def cli(resource, *args):
        return subprocess.run(
            [client.binary, resource, 'list', '--endpoint', client.endpoint, *args],
            env=client.env, capture_output=True, text=True, timeout=30)

    # The retired resource paths must fail locally, even when requesting help.
    for resource in ['blueprint', 'command', 'instance', 'namespace', 'provider', 'registry', 'secret']:
        retired = subprocess.run([client.binary, 'server', resource, '--help'],
                                 env=client.env, capture_output=True, text=True, timeout=30)
        assert retired.returncode == 2 and not retired.stdout, retired

    for args in [('command', '--help'), ('provider', 'commands', '--instance', 'query-alpha/same')]:
        retired = subprocess.run([client.binary, *args], env=client.env,
                                 capture_output=True, text=True, timeout=30)
        assert retired.returncode == 2 and not retired.stdout, retired

    # Discovery is global and available before any namespace or Instance exists.
    commands = request('provider', 'commands')['commands']
    wire_commands = client.rpc('ListCommandDefinitions')['commands']
    assert commands == [{**c, 'argumentsSchema': json.loads(c['argumentsSchema']['json'])}
                        for c in wire_commands], commands
    names = {c['name'] for c in commands}
    assert {'opentofu.plan', 'ansible.apply'} <= names, commands
    assert not any(name.startswith('builtin.') for name in names), commands
    for command in commands:
        assert request('provider', 'commands', command['name']) == command

    methods = [('blueprint', 'ListBlueprints', 'blueprints'),
               ('secret', 'ListNamespaceSecretNames', 'names'),
               ('registry', 'ListNamespaceRegistryHosts', 'hosts')]
    for _, method, field in methods:
        assert client.rpc(method).get(field, []) == []
    # Reverse creation order and duplicate local identities exercise stable namespace ordering.
    blueprints = {}
    instances = {}
    for namespace in ['query-beta', 'query-alpha', 'query-empty']:
        client.create_namespace(namespace)
        if namespace == 'query-empty':
            continue
        blueprints[namespace] = client.fetch(reference, namespace)
        instances[namespace] = client.initialize(namespace + '/same', reference, {'architecture': arch})
        secrets = client.root / 'query-secrets.json'
        secrets.write_text(json.dumps({'token': 'synthetic-query-secret'}))
        request('secret', 'replace', '--namespace', namespace, '--file', str(secrets))
        registries = client.root / 'query-registries.json'
        registries.write_text(json.dumps({
            'example.invalid': {'username': 'synthetic-query-user', 'password': 'synthetic-query-password'}}))
        request('registry', 'replace', '--namespace', namespace, '--file', str(registries))
        # Exercise renamed inspection and ready-only resolution against real cached sources.
        assert request('blueprint', 'get', reference, '--namespace', namespace) == blueprints[namespace]
        assert request('blueprint', 'resolve', reference, '--namespace', namespace)['blueprint'] == blueprints[namespace]
        assert request('instance', 'get', namespace + '/same')['instance']['id'] == instances[namespace]
        assert request('provider', 'commands')['commands'] == commands

    namespaces = ['query-alpha', 'query-beta']
    expected = {
        'blueprints': [blueprints[name] for name in namespaces],
        'names': [{'namespace': name, 'name': 'token'} for name in namespaces],
        'hosts': [{'namespace': name, 'host': 'example.invalid'} for name in namespaces],
    }
    assert {i['id'] for i in client.rpc('ListInstances')['instances']} == set(instances.values())
    # Page through multiple Instances, preserving a single JSON response document.
    assert {i['id'] for i in request('instance', 'list', '--all', '--limit', '1')['instances']} == set(instances.values())
    for resource, method, field in methods:
        assert client.rpc(method).get(field, []) == expected[field]
        result = cli(resource, '-o', 'json')
        assert result.returncode == 0, result.stderr
        assert json.loads(result.stdout).get(field, []) == expected[field], result.stdout
        for namespace in [*namespaces, 'query-empty']:
            scoped = [row for row in expected[field] if row['namespace'] == namespace]
            assert client.rpc(method, {'namespace': namespace}).get(field, []) == scoped
            result = cli(resource, '--namespace', namespace, '-o', 'json')
            assert result.returncode == 0, result.stderr
            assert json.loads(result.stdout).get(field, []) == scoped, result.stdout
        table = cli(resource)
        assert table.returncode == 0, table.stderr
        assert 'namespace' in table.stdout.lower() and all(name in table.stdout for name in namespaces), table.stdout
        assert 'synthetic-query-' not in table.stdout, table.stdout
        columns = cli(resource, '--columns', 'namespace')
        assert columns.returncode == 0 and all(name in columns.stdout for name in namespaces), columns
        assert cli(resource, 'query-alpha').returncode != 0, 'Positional namespace was accepted'
        try:
            client.rpc(method, {'namespace': 'missing'})
        except urllib.error.HTTPError as error:
            failure = json.load(error)
            assert failure['code'] == 'not_found', failure
        else:
            raise AssertionError('Missing namespace filter was accepted')

    for namespace in [*namespaces, 'query-empty']:
        request('secret', 'clear', '--namespace', namespace)
        request('registry', 'clear', '--namespace', namespace)
        assert client.rpc('ListNamespaceSecretNames', {'namespace': namespace}).get('names', []) == []
        assert client.rpc('ListNamespaceRegistryHosts', {'namespace': namespace}).get('hosts', []) == []
        client.rpc('DeleteNamespace', {'name': namespace})
    assert client.rpc('ListNamespaces').get('namespaces', []) == []
    print('CLI inspection, resolution, credential scopes, pagination and all-namespace RPC queries passed', flush=True)
