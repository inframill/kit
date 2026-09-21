"""Exercise Kit through the released Engine HTTP interface in a disposable daemon."""
import base64
import hashlib
import json
import os
import pathlib
import platform
import socket
import subprocess
import time
import urllib.error
import urllib.request

from release_config import ROOT, configuration, local_tools, bundle_lock


def daemon_check(tmp, registry, env):
    tmp = pathlib.Path(tmp) / 'daemon'
    tmp.mkdir()
    binary = tmp / 'kit-fixtures'
    subprocess.run(['go', 'build', '-tags=kit_testdrivers', '-o', str(binary), './cmd/inframill-kit'], cwd=ROOT, check=True)
    # This blueprint and driver exist only in test builds, never the release set.
    source = tmp / 'blueprints/protocol-fixture/versions/0.1.0'
    (source / 'template/fixture').mkdir(parents=True)
    (source / 'template/fixture/readme.txt').write_text('secret snapshot test\n')
    (source / 'blueprint.yaml').write_text(json.dumps({'api_version': 'inframill.dev/blueprint/v1', 'description': 'protocol fixture', 'values_schema': {'type': 'object', 'additionalProperties': False}, 'driver_settings': {'fixture': {'id': 'fixture', 'required_driver_version': '0.1.0', 'contract': '0.1.0'}}}))
    selection = {**env, 'BLUEPRINT_ROOT': str(tmp), 'BLUEPRINT': 'protocol-fixture', 'BLUEPRINT_VERSION': '0.1.0', 'KIT_DIST_DIR': str(tmp / 'dist')}
    prefix = ['go', 'run', '-tags=kit_testdrivers', './cmd/kit-build']
    digest = subprocess.check_output(prefix + ['blueprint:source-digest'], cwd=ROOT, env=selection, text=True).strip()
    release = tmp / 'releases/blueprints/protocol-fixture/0.1.0.yaml'
    release.parent.mkdir(parents=True)
    release.write_text(json.dumps({'name': 'protocol-fixture', 'version': '0.1.0', 'inframill_version': bundle_lock()['inframill']['version'], 'protocol': 1, 'platforms': ['linux/amd64', 'linux/arm64'], 'architectures': ['amd64', 'arm64'], 'drivers': [{'id': 'fixture', 'name': 'fixture', 'version': '0.1.0'}], 'source_sha256': digest, 'status': 'test'}))
    for action in ['blueprint:package', 'blueprint:push']:
        subprocess.run(prefix + [action], cwd=ROOT, env=selection, check=True)
    tofu, ansible = local_tools()
    data_root = tmp / 'data'
    config = configuration(ROOT / 'bin/inframill-kit', data_root, {'opentofu': tofu, 'ansible': ansible})
    config['engine']['drivers']['fixture'] = {'path': str(binary), 'args': ['driver', 'fixture', '--driver-version', '0.1.0']}
    config['engine']['drivers']['unavailable'] = {'path': str(tmp / 'missing')}
    with socket.socket() as reservation:
        reservation.bind(('127.0.0.1', 0))
        port = reservation.getsockname()[1]
    config['main'] = {'port': port, 'shutdownTimeout': '5s'}
    config_path = tmp / 'config.json'
    config_path.write_text(json.dumps(config))
    endpoint = f'http://127.0.0.1:{port}/inframill.v1.EngineService/'
    process = None
    log = (tmp / 'daemon.log').open('w+')

    def rpc(method, body=None):
        request = urllib.request.Request(endpoint + method, json.dumps(body or {}).encode(), {'Content-Type': 'application/json', 'Connect-Protocol-Version': '1'})
        with urllib.request.urlopen(request, timeout=15) as response:
            return json.load(response)

    def poll(fn, timeout=30):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            value = fn()
            if value:
                return value
            time.sleep(0.03)
        raise RuntimeError('Daemon acceptance timed out')

    def start():
        nonlocal process
        process = subprocess.Popen([str(ROOT / 'bin/inframill'), 'server', 'start', '--config', str(config_path)], env=env, stdout=log, stderr=log)
        def ready():
            if process.poll() is not None:
                log.flush()
                raise RuntimeError('Daemon exited: ' + (tmp / 'daemon.log').read_text())
            try:
                return rpc('ListDrivers')
            except (urllib.error.URLError, ConnectionError):
                return None
        poll(ready)

    def stop():
        nonlocal process
        if process is not None:
            process.terminate()
            try:
                process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
            process = None

    def initialize(name, reference, values):
        rpc('FetchBlueprint', {'reference': reference})
        return rpc('InitInstance', {'name': name, 'blueprint_reference': reference, 'values': values})['instance']['id']

    def submit(instance, command, args=None):
        return rpc('SubmitCommand', {'instance_id': instance, 'command': command, 'args': args or {}})['commandId']

    def execution(instance, command):
        return rpc('GetCommandExecution', {'instance_id': instance, 'command_id': command})['execution']

    def done(instance, command):
        value = poll(lambda: (r if r['status'] in ('COMMAND_STATUS_SUCCEEDED', 'COMMAND_STATUS_FAILED', 'COMMAND_STATUS_INTERRUPTED') else None) if (r := execution(instance, command)) else None)
        assert value['status'] == 'COMMAND_STATUS_SUCCEEDED', value
        return value.get('result')

    def run(instance, command, args=None):
        return done(instance, submit(instance, command, args))

    def replace(namespace, secrets):
        rpc('ReplaceNamespaceSecrets', {'namespace': namespace, 'secrets': secrets})

    def expected(secret):
        return hashlib.sha256(secret.encode()).hexdigest()

    try:
        start()
        statuses = {d['id']: d['status'] for d in rpc('ListDrivers')['drivers']}
        assert statuses['opentofu-0-2-0'] == 'DRIVER_STATUS_AVAILABLE', statuses
        assert statuses['ansible-0-2-0'] == 'DRIVER_STATUS_AVAILABLE', statuses
        assert statuses['unavailable'] == 'DRIVER_STATUS_UNAVAILABLE', statuses
        iid = initialize('acceptance/local', registry + '/inframill/blueprints/local-resource:0.1.0', {'architecture': 'arm64' if platform.machine() in ('arm64', 'aarch64') else 'amd64'})
        run(iid, 'opentofu-0-2-0.init')
        plan = run(iid, 'opentofu-0-2-0.plan')
        run(iid, 'opentofu-0-2-0.apply', {'plan_id': plan['plan_id']})
        run(iid, 'ansible-0-2-0.apply', {'playbook': 'site'})
        plan = run(iid, 'opentofu-0-2-0.plan', {'destroy': True})
        run(iid, 'opentofu-0-2-0.destroy', {'plan_id': plan['plan_id']})
        assert len(rpc('ListCommandExecutions', {'instance_id': iid})['executions']) == 6
        ref = registry + '/inframill/blueprints/protocol-fixture:0.1.0'
        first = initialize('alpha/one', ref, {})
        other = initialize('beta/one', ref, {})
        assert [i['id'] for i in rpc('ListInstances', {'namespace': 'alpha'})['instances']] == [first]
        replace('alpha', {'token': 'synthetic-first'})
        assert rpc('ListNamespaceSecretNames', {'namespace': 'alpha'})['names'] == ['token']
        assert run(other, 'fixture.probe', {'text': 'isolation'})['secret_count'] == 0
        waiting = submit(first, 'fixture.probe', {'text': 'snapshot', 'wait': True})
        artifacts = data_root / 'instances' / first / 'artifacts'
        poll(lambda: (artifacts / 'ready').exists())
        queued = submit(first, 'fixture.probe', {'text': 'next snapshot'})
        assert execution(first, queued)['status'] == 'COMMAND_STATUS_QUEUED'
        # A timed-out CLI wait must not cancel accepted work.
        result = subprocess.run([str(ROOT / 'bin/inframill'), 'server', 'command', 'execution', 'wait', first, waiting, '--endpoint', endpoint.rsplit('/inframill', 1)[0], '--timeout', '50ms', '-o', 'json'], capture_output=True, text=True)
        assert result.returncode != 0
        assert execution(first, waiting)['status'] == 'COMMAND_STATUS_RUNNING'
        replace('alpha', {'token': 'synthetic-second'})
        (artifacts / 'release').write_text('continue')
        assert done(first, waiting)['secret_hash'] == expected('synthetic-first')
        assert done(first, queued)['secret_hash'] == expected('synthetic-second')
        log_reply = rpc('ReadCommandLog', {'instance_id': first, 'command_id': waiting, 'stream': 'LOG_STREAM_STDOUT', 'limit': 65536})
        output = base64.b64decode(log_reply['data']).decode()
        assert [json.loads(line)['type'] for line in output.splitlines()] == ['message', 'success']
        assert 'synthetic-first' not in output
        replace('alpha', {})
        assert run(first, 'fixture.probe', {'text': 'cleared'})['secret_count'] == 0
        replace('alpha', {'token': 'synthetic-restart'})
        stop()
        start()
        assert rpc('ListNamespaceSecretNames', {'namespace': 'alpha'}).get('names', []) == []
        assert run(first, 'fixture.probe', {'text': 'restart'})['secret_count'] == 0
        for path in data_root.rglob('*'):
            if path.is_file():
                content = path.read_bytes()
                assert all(token.encode() not in content for token in ('synthetic-first', 'synthetic-second', 'synthetic-restart')), path
        print('Daemon Kit workflow, history, replies, availability, secret snapshots and restart passed', flush=True)
    finally:
        stop()
        log.close()
