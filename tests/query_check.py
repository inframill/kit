"""Verify scoped and all-namespace queries through the released RPC and CLI."""
import json
import subprocess
import urllib.error


def query_check(client, reference, arch):
    def cli(resource, *args):
        return subprocess.run(
            [client.binary, resource, 'list', '--endpoint', client.endpoint, *args],
            env=client.env, capture_output=True, text=True, timeout=30)

    # The retired resource paths must fail locally, even when requesting help.
    for resource in ['blueprint', 'command', 'instance', 'namespace', 'provider', 'registry', 'secret']:
        retired = subprocess.run([client.binary, 'server', resource, '--help'],
                                 env=client.env, capture_output=True, text=True, timeout=30)
        assert retired.returncode == 2 and not retired.stdout, retired

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
        client.rpc('ReplaceNamespaceSecrets', {'namespace': namespace,
                   'secrets': {'token': 'synthetic-query-secret'}})
        client.rpc('ReplaceNamespaceRegistries', {'namespace': namespace, 'registries': {
            'example.invalid': {'username': 'synthetic-query-user', 'password': 'synthetic-query-password'}}})

    namespaces = ['query-alpha', 'query-beta']
    expected = {
        'blueprints': [blueprints[name] for name in namespaces],
        'names': [{'namespace': name, 'name': 'token'} for name in namespaces],
        'hosts': [{'namespace': name, 'host': 'example.invalid'} for name in namespaces],
    }
    assert {i['id'] for i in client.rpc('ListInstances')['instances']} == set(instances.values())
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
        client.rpc('DeleteNamespace', {'name': namespace})
    assert client.rpc('ListNamespaces').get('namespaces', []) == []
    print('All-namespace and scoped RPC/CLI queries, metadata output and namespace columns passed', flush=True)
