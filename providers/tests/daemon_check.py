"""Exercise Kit through the released Engine HTTP interface in a disposable daemon."""
import base64
import hashlib
import json
import pathlib
import platform
import socket
import subprocess
import time
import urllib.error
import urllib.request

from blueprints.tests.integration import publish_protocol_fixture
from build import ROOT, configuration, local_tools, bundle_lock, providers_dist, native_platform


def daemon_check(tmp, registry, env):
    tmp = pathlib.Path(tmp) / 'daemon'
    tmp.mkdir()
    binary = tmp / 'kit-fixtures'
    subprocess.run(['go', 'build', '-o', str(binary), './tests/harness'], cwd=ROOT / 'providers', check=True)
    publish_protocol_fixture(tmp, env, bundle_lock()['inframill']['version'])
    tofu, ansible = local_tools()
    data_root = tmp / 'data'
    config = configuration(providers_dist(), data_root, {'opentofu': tofu, 'ansible': ansible}, target=native_platform())
    config['engine']['providers']['fixture'] = {'path': str(binary), 'args': ['provider', 'fixture', '--provider-version', '0.1.0']}
    config['engine']['providers']['unavailable'] = {'path': str(tmp / 'missing')}
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
                return rpc('ListProviders')
            except (urllib.error.URLError, ConnectionError):
                return None
        poll(ready)
        assert rpc('GetServerVersion') == {'version': bundle_lock()['inframill']['version']}

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
        rpc('FetchBlueprint', {'reference': reference, 'namespace': name.split('/', 1)[0]})
        return rpc('InitInstance', {'name': name, 'blueprint_reference': reference, 'values': {'json': json.dumps(values)}})['instance']['id']

    def submit(instance, command, args=None):
        return rpc('SubmitCommand', {'instance_id': instance, 'command_name': command, 'arguments': {'json': json.dumps(args or {})}})['executionId']

    def execution(instance, command):
        return rpc('GetCommandExecution', {'instance_id': instance, 'execution_id': command})['execution']

    def done(instance, command):
        value = poll(lambda: (r if r['status'] in ('COMMAND_EXECUTION_STATUS_SUCCEEDED', 'COMMAND_EXECUTION_STATUS_FAILED', 'COMMAND_EXECUTION_STATUS_INTERRUPTED') else None) if (r := execution(instance, command)) else None)
        assert value['status'] == 'COMMAND_EXECUTION_STATUS_SUCCEEDED', value
        return json.loads(value['result']['json']) if 'result' in value else None

    def run(instance, command, args=None):
        return done(instance, submit(instance, command, args))

    def replace(namespace, secrets):
        rpc('ReplaceNamespaceSecrets', {'namespace': namespace, 'secrets': secrets})

    def expected(secret):
        return hashlib.sha256(secret.encode()).hexdigest()

    try:
        start()
        statuses = {d['id']: d['availability'] for d in rpc('ListProviders')['providers']}
        assert statuses['opentofu'] == 'PROVIDER_AVAILABILITY_AVAILABLE', statuses
        assert statuses['ansible'] == 'PROVIDER_AVAILABILITY_AVAILABLE', statuses
        assert statuses['unavailable'] == 'PROVIDER_AVAILABILITY_UNAVAILABLE', statuses
        iid = initialize('acceptance/local', registry + '/inframill/blueprints/local-resource:0.1.0', {'architecture': 'arm64' if platform.machine() in ('arm64', 'aarch64') else 'amd64'})
        run(iid, 'opentofu.init')
        plan = run(iid, 'opentofu.plan')
        run(iid, 'opentofu.apply', {'plan_id': plan['plan_id']})
        run(iid, 'ansible.apply', {'playbook': 'site'})
        plan = run(iid, 'opentofu.plan', {'destroy': True})
        run(iid, 'opentofu.destroy', {'plan_id': plan['plan_id']})
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
        assert execution(first, queued)['status'] == 'COMMAND_EXECUTION_STATUS_QUEUED'
        # A timed-out CLI wait must not cancel accepted work.
        result = subprocess.run([str(ROOT / 'bin/inframill'), 'server', 'command', 'execution', 'wait', first, waiting, '--endpoint', endpoint.rsplit('/inframill', 1)[0], '--timeout', '50ms', '-o', 'json'], capture_output=True, text=True)
        assert result.returncode != 0
        assert execution(first, waiting)['status'] == 'COMMAND_EXECUTION_STATUS_RUNNING'
        replace('alpha', {'token': 'synthetic-second'})
        (artifacts / 'release').write_text('continue')
        assert done(first, waiting)['secret_hash'] == expected('synthetic-first')
        assert done(first, queued)['secret_hash'] == expected('synthetic-second')
        log_reply = rpc('ReadCommandLog', {'instance_id': first, 'execution_id': waiting, 'stream': 'LOG_STREAM_STDOUT', 'limit': 65536})
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
