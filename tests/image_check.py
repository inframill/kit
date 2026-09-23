"""Verify the image's default command, mounted configuration and persistent storage."""
import hashlib
import json
import pathlib
import subprocess
import time
import urllib.error
import urllib.request
import uuid

from blueprints.tests.integration import provider_ids
from build import runtime_settings, ROOT, bundle_lock, configuration

CONFIG = '/etc/inframill/inframill.yaml'
DATA = '/var/lib/inframill'


def image_check(tmp, image, arch, network):
    root = pathlib.Path(tmp) / ('image-' + arch)
    root.mkdir()
    lock = bundle_lock()
    python = '/opt/inframill/tools/ansible/' + runtime_settings()['ANSIBLE_VERSION'] + '/bin/python3'
    prefix = ['docker', 'run', '--rm', '--pull=never', '--platform', 'linux/' + arch]
    containers = set()
    volume = 'kit-data-' + uuid.uuid4().hex
    reference = 'registry:5000/inframill/blueprints/local-resource:0.1.0'
    ids = provider_ids('local-resource', '0.1.0')

    def docker(args, check=True, timeout=90):
        return subprocess.run(list(map(str, args)), check=check, text=True, capture_output=True, timeout=timeout)

    def rpc(endpoint, method, body=None):
        request = urllib.request.Request(endpoint + method, json.dumps(body or {}).encode(), {
            'Content-Type': 'application/json', 'Connect-Protocol-Version': '1',
        })
        with urllib.request.urlopen(request, timeout=10) as response:
            return json.load(response)

    def poll(fn):
        deadline = time.monotonic() + 45
        while time.monotonic() < deadline:
            result = fn()
            if result:
                return result
            time.sleep(0.1)
        raise RuntimeError('Image acceptance timed out on ' + arch)

    def start(mounts, port=8080, workdir=DATA):
        name = 'kit-server-' + uuid.uuid4().hex
        containers.add(name)
        docker(prefix + ['-d', '--name', name, '--network', network, '--workdir', workdir,
                         '-p', '127.0.0.1::' + str(port), '-e', 'INFRAMILL_HTTP_REGISTRIES=registry:5000',
                         *mounts, image])  # Exercise the image's ENTRYPOINT and CMD unchanged.
        address = docker(['docker', 'port', name, str(port) + '/tcp']).stdout.strip()
        endpoint = 'http://' + address + '/inframill.v1.EngineService/'

        def ready():
            state = docker(['docker', 'inspect', '--format', '{{.State.Running}}', name], check=False)
            if state.returncode or state.stdout.strip() != 'true':
                raise RuntimeError('Image server exited: ' + docker(['docker', 'logs', name], check=False).stderr)
            try:
                return rpc(endpoint, 'ListProviders')
            except (urllib.error.URLError, ConnectionError, TimeoutError):
                return None
        providers = poll(ready)['providers']
        assert {d['id'] for d in providers} == set(ids.values()), providers
        assert all(d['availability'] == 'PROVIDER_AVAILABILITY_AVAILABLE' for d in providers), providers
        expected_version = {'version': lock['inframill']['version']}
        assert rpc(endpoint, 'GetServerVersion') == expected_version
        version_cli = ['docker', 'exec', name, 'inframill', 'server', 'version',
                       '--endpoint', 'http://127.0.0.1:' + str(port)]
        assert docker(version_cli).stdout == expected_version['version'] + '\n'
        assert json.loads(docker(version_cli + ['-o', 'json']).stdout) == expected_version
        return name, endpoint

    def stop(name):
        docker(['docker', 'stop', '--time', '10', name])
        containers.discard(name)

    def persist(mounts, port=8080, workdir=DATA):
        name, endpoint = start(mounts, port, workdir)
        namespace_cli = ['docker', 'exec', name, 'inframill', 'server', 'namespace',
                         'create', 'image', '--endpoint', 'http://127.0.0.1:' + str(port), '-o', 'json']
        assert json.loads(docker(namespace_cli).stdout)['namespace'] == {'name': 'image'}
        rpc(endpoint, 'FetchBlueprint', {'reference': reference, 'namespace': 'image'})
        instance = rpc(endpoint, 'InitInstance', {'name': 'image/persistence',
            'blueprint_reference': reference, 'values': {'json': json.dumps({'architecture': arch})},
        })['instance']['id']
        command = rpc(endpoint, 'SubmitCommand', {'instance_id': instance,
                      'command_name': ids['ansible'] + '.syntax-check', 'arguments': {'json': json.dumps({'playbook': 'site'})}})['executionId']
        assert uuid.UUID(instance).version == 7 and uuid.UUID(command).version == 7

        def completed():
            result = rpc(endpoint, 'GetCommandExecution', {'instance_id': instance, 'execution_id': command})['execution']
            return result if result['status'] in ('COMMAND_EXECUTION_STATUS_SUCCEEDED', 'COMMAND_EXECUTION_STATUS_FAILED', 'COMMAND_EXECUTION_STATUS_INTERRUPTED') else None
        result = poll(completed)
        assert result['status'] == 'COMMAND_EXECUTION_STATUS_SUCCEEDED', result
        mounts_state = json.loads(docker(['docker', 'inspect', name]).stdout)[0]['Mounts']
        stop(name)
        name, endpoint = start(mounts, port, workdir)
        assert rpc(endpoint, 'ListNamespaces')['namespaces'] == [{'name': 'image'}]
        instances = rpc(endpoint, 'ListInstances')['instances']
        assert any(i['id'] == instance for i in instances), instances
        history = rpc(endpoint, 'ListCommandExecutions', {'instance_id': instance})['executions']
        assert len(history) == 1 and history[0]['status'] == 'COMMAND_EXECUTION_STATUS_SUCCEEDED', history
        cli = ['docker', 'exec', name, 'inframill']
        namespaces = json.loads(docker(cli + ['server', 'namespace', 'list', '--endpoint',
                                'http://127.0.0.1:' + str(port), '-o', 'json']).stdout)['namespaces']
        assert namespaces == [{'name': 'image'}], namespaces
        history_cli = json.loads(docker(cli + ['server', 'command', 'execution', 'list',
                                 '--instance-id', instance, '--endpoint',
                                 'http://127.0.0.1:' + str(port), '-o', 'json']).stdout)['executions']
        assert len(history_cli) == 1 and history_cli[0]['request']['executionId'] == command, history_cli
        remote = json.loads(docker(cli + ['server', 'instance', 'list', '--endpoint',
                            'http://127.0.0.1:' + str(port), '-o', 'json']).stdout)['instances']
        assert any(i['id'] == instance for i in remote), remote
        remote_providers = json.loads(docker(cli + ['server', 'provider', 'list', '--endpoint',
                                    'http://127.0.0.1:' + str(port), '-o', 'json']).stdout)['providers']
        assert {d['id'] for d in remote_providers} == set(ids.values()), remote_providers
        assert all(d['availability'] == 'PROVIDER_AVAILABILITY_AVAILABLE' for d in remote_providers), remote_providers
        stop(name)
        return instance, mounts_state

    docker(['docker', 'volume', 'create', volume])
    try:
        bundled = docker(prefix + ['--entrypoint', 'cat', image, CONFIG]).stdout
        assert bundled == (ROOT / 'runtime/inframill.yaml').read_text(), 'Bundled configuration differs from runtime/inframill.yaml'
        # Parse the actual bundled YAML using the image's already-pinned PyYAML.
        raw = docker(prefix + ['--entrypoint', python, image, '-c',
                     'import json,yaml; print(json.dumps(yaml.safe_load(open("' + CONFIG + '"))))']).stdout
        config = json.loads(raw)
        assert config['handlers'] == [{'type': 'connect-engine'}], config
        assert config['engine']['dataDir'] == DATA and config['main']['port'] == 8080, config
        assert {d['path'] for d in config['engine']['providers'].values()} == {
            '/opt/inframill/providers/' + d['name'] + '/' + d['version'] + '/provider' for d in lock['providers']}
        assert all(d['args'][0] == '--tool' for d in config['engine']['providers'].values())
        persist(['--mount', 'type=volume,src=' + volume + ',dst=' + DATA])
        print('Default YAML, named volume and container replacement passed on ' + arch, flush=True)

        # A different working directory proves the config-relative data rule.
        custom = root / 'inframill.yaml'
        custom_config = configuration('/opt/inframill/providers', './data', providers=lock['providers'])
        custom_config['main']['port'] = 8081
        custom.write_text(json.dumps(custom_config))
        original = hashlib.sha256(custom.read_bytes()).hexdigest()
        data = root / 'data'
        data.mkdir()
        mounts = ['--mount', 'type=bind,src=' + str(custom) + ',dst=' + CONFIG + ',readonly',
                  '--mount', 'type=bind,src=' + str(data) + ',dst=/etc/inframill/data']
        instance, mounted = persist(mounts, 8081, '/tmp')
        assert (data / 'image/namespace.json').is_file()
        assert (data / 'image/instances' / instance / 'commands.sqlite').is_file()
        assert any(m['Destination'] == CONFIG and not m['RW'] for m in mounted), mounted
        assert hashlib.sha256(custom.read_bytes()).hexdigest() == original
        print('Read-only custom YAML, bind data and config-relative paths passed on ' + arch, flush=True)

        # Starting the daemon on read-only storage must fail before it can serve.
        rejected = docker(prefix + ['--network', network,
                          '--mount', 'type=volume,src=' + volume + ',dst=' + DATA + ',readonly',
                          image], check=False, timeout=20)
        assert rejected.returncode != 0 and any(message in rejected.stderr.lower() for message in
               ('read-only', 'permission denied', 'cannot be locked')), rejected.stderr

        bad = root / 'invalid.yaml'
        bad.write_text('invalid: [')
        name = 'kit-invalid-' + uuid.uuid4().hex
        containers.add(name)
        rejected = docker(prefix + ['--name', name, '--mount', 'type=bind,src=' + str(bad) + ',dst=' + CONFIG + ',readonly', image], check=False, timeout=20)
        assert rejected.returncode != 0 and 'configuration' in rejected.stderr.lower(), rejected.stderr
        print('Invalid configuration and read-only data rejection passed on ' + arch, flush=True)
    finally:
        for name in containers:
            docker(['docker', 'rm', '-f', name], check=False)
        docker(['docker', 'volume', 'rm', volume])
