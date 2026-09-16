"""Verify the image's default command, mounted configuration and persistent storage."""
import hashlib
import json
import pathlib
import subprocess
import time
import urllib.error
import urllib.request
import uuid

from release_config import bundle_lock, configuration, driver_ids

CONFIG = '/etc/inframill/inframill.yaml'
DATA = '/var/lib/inframill'


def image_check(tmp, image, arch, network):
    root = pathlib.Path(tmp) / ('image-' + arch)
    root.mkdir()
    lock = bundle_lock()
    python = '/opt/inframill/tools/ansible/' + lock['ansible_core'] + '/bin/python3'
    prefix = ['docker', 'run', '--rm', '--pull=never', '--platform', 'linux/' + arch]
    containers = set()
    volume = 'kit-data-' + uuid.uuid4().hex
    reference = 'registry:5000/inframill/blueprints/local-resource:0.1.0'
    ids = driver_ids('local-resource', '0.1.0')

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
                return rpc(endpoint, 'ListDrivers')
            except (urllib.error.URLError, ConnectionError, TimeoutError):
                return None
        drivers = poll(ready)['drivers']
        assert {d['id'] for d in drivers} == set(ids.values()), drivers
        assert all(d['status'] == 'DRIVER_STATUS_AVAILABLE' for d in drivers), drivers
        return name, endpoint

    def stop(name):
        docker(['docker', 'stop', '--time', '10', name])
        containers.discard(name)

    def persist(mounts, port=8080, workdir=DATA):
        name, endpoint = start(mounts, port, workdir)
        rpc(endpoint, 'FetchBlueprint', {'reference': reference})
        instance = rpc(endpoint, 'InitInstance', {'name': 'image/persistence', 'instance': {
            'blueprint_reference': reference, 'values': {'architecture': arch},
        }})['instance']['id']
        command = rpc(endpoint, 'SubmitCommand', {'instance_id': instance,
                      'command': ids['ansible'] + '.syntax-check', 'args': {'playbook': 'site'}})['commandId']

        def completed():
            result = rpc(endpoint, 'GetCommandExecution', {'instance_id': instance, 'command_id': command})['execution']
            return result if result['status'] in ('COMMAND_STATUS_SUCCEEDED', 'COMMAND_STATUS_FAILED', 'COMMAND_STATUS_INTERRUPTED') else None
        result = poll(completed)
        assert result['status'] == 'COMMAND_STATUS_SUCCEEDED', result
        mounts_state = json.loads(docker(['docker', 'inspect', name]).stdout)[0]['Mounts']
        stop(name)
        name, endpoint = start(mounts, port, workdir)
        instances = rpc(endpoint, 'ListInstances')['instances']
        assert any(i['id'] == instance for i in instances), instances
        history = rpc(endpoint, 'ListCommandExecutions', {'instance_id': instance})['executions']
        assert len(history) == 1 and history[0]['status'] == 'COMMAND_STATUS_SUCCEEDED', history
        # 0.4.0 CLI roots: local inspection can read a running daemon's storage.
        cli = ['docker', 'exec', name, 'inframill']
        local = json.loads(docker(cli + ['local', 'history', '--config', CONFIG,
                           '--name', 'image/persistence', '-o', 'json']).stdout)['executions']
        assert len(local) == 1 and local[0]['command_id'] == command and local[0]['status'] == 'succeeded', local
        drivers = json.loads(docker(cli + ['local', 'driver', 'list', '--config', CONFIG, '-o', 'json']).stdout)['drivers']
        assert {d['id'] for d in drivers} == set(ids.values()), drivers
        remote = json.loads(docker(cli + ['server', 'instance', 'list', '--endpoint',
                            'http://127.0.0.1:' + str(port), '-o', 'json']).stdout)['instances']
        assert any(i['id'] == instance for i in remote), remote
        stop(name)
        return instance, mounts_state

    docker(['docker', 'volume', 'create', volume])
    try:
        # Parse the actual bundled YAML using the image's already-pinned PyYAML.
        raw = docker(prefix + ['--entrypoint', python, image, '-c',
                     'import json,yaml; print(json.dumps(yaml.safe_load(open("' + CONFIG + '"))))']).stdout
        config = json.loads(raw)
        assert config['engine']['dataDir'] == DATA and config['main']['port'] == 8080, config
        assert all(d['path'] == '/usr/local/bin/inframill-kit' for d in config['engine']['drivers'].values())
        persist(['--mount', 'type=volume,src=' + volume + ',dst=' + DATA])
        print('Default YAML, named volume and container replacement passed on ' + arch, flush=True)

        # A different working directory proves 0.3.0's config-relative data rule.
        custom = root / 'inframill.yaml'
        text = configuration('/usr/local/bin/inframill-kit', './data', drivers=lock['drivers'], format='yaml')
        assert 'port: 8080' in text
        custom.write_text(text.replace('port: 8080', 'port: 8081'))
        original = hashlib.sha256(custom.read_bytes()).hexdigest()
        data = root / 'data'
        data.mkdir()
        mounts = ['--mount', 'type=bind,src=' + str(custom) + ',dst=' + CONFIG + ',readonly',
                  '--mount', 'type=bind,src=' + str(data) + ',dst=/etc/inframill/data']
        instance, mounted = persist(mounts, 8081, '/tmp')
        assert (data / 'instances' / instance / 'commands.sqlite').is_file()
        assert any(m['Destination'] == CONFIG and not m['RW'] for m in mounted), mounted
        assert hashlib.sha256(custom.read_bytes()).hexdigest() == original
        print('Read-only custom YAML, bind data and config-relative paths passed on ' + arch, flush=True)

        values = root / 'values.json'
        values.write_text(json.dumps({'architecture': arch}))
        args = root / 'args.json'
        args.write_text('{"playbook":"site"}')
        rejected = docker(prefix + ['--network', network, '-e', 'INFRAMILL_HTTP_REGISTRIES=registry:5000',
                          '--mount', 'type=volume,src=' + volume + ',dst=' + DATA + ',readonly',
                          '--mount', 'type=bind,src=' + str(values) + ',dst=/values.json,readonly',
                          '--mount', 'type=bind,src=' + str(args) + ',dst=/args.json,readonly', image,
                          'local', 'exec', ids['ansible'] + '.syntax-check', '--config', CONFIG,
                          '--blueprint', reference, '--values-file', '/values.json', '--args-file', '/args.json', '--name', 'image/readonly'], check=False)
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
