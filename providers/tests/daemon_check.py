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

    def cli(*args, raw=False):
        command = [str(ROOT / 'bin/inframill'), *args,
                   '--endpoint', endpoint.rsplit('/inframill', 1)[0]]
        if not raw:
            command += ['-o', 'json']
        result = subprocess.run(command, capture_output=True, text=True, timeout=30)
        assert result.returncode == 0, result.stderr
        return result.stdout if raw else json.loads(result.stdout)

    def rejected(method, body, code):
        try:
            rpc(method, body)
        except urllib.error.HTTPError as error:
            failure = json.load(error)
            assert failure['code'] == code, failure
        else:
            raise AssertionError(method + ' unexpectedly succeeded')

    try:
        start()
        assert rpc('ListNamespaces').get('namespaces', []) == []
        reference = registry + '/inframill/blueprints/local-resource:0.1.0'
        rejected('InitInstance', {'name': 'missing/local', 'blueprint_reference': reference,
                                 'values': {'json': '{}'}}, 'not_found')
        rejected('FetchBlueprint', {'reference': reference, 'namespace': 'missing'}, 'not_found')
        rejected('ReplaceNamespaceSecrets', {'namespace': 'missing', 'secrets': {}}, 'not_found')
        assert not (data_root / 'missing').exists()
        namespaces = ['acceptance', 'alpha', 'beta', 'empty']
        for namespace in namespaces:
            rpc('CreateNamespace', {'name': namespace})
        rejected('CreateNamespace', {'name': 'alpha'}, 'already_exists')
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
        # Saved bindings exclude Kit commands from this fixture-only Instance.
        commands = cli('command', 'list', '--instance', 'alpha/one')['commands']
        assert {c['name'] for c in commands} == {'builtin.delete', 'fixture.probe'}, commands
        wire_commands = rpc('ListCommandDefinitions', {'instance_id': first})['commands']
        assert commands == [{**c, 'argumentsSchema': json.loads(c['argumentsSchema']['json'])}
                            for c in wire_commands], commands
        assert [i['id'] for i in rpc('ListInstances', {'namespace': 'alpha'})['instances']] == [first]
        replace('alpha', {'token': 'synthetic-first'})
        assert rpc('ListNamespaceSecretNames', {'namespace': 'alpha'})['names'] == [{'namespace': 'alpha', 'name': 'token'}]
        assert run(other, 'fixture.probe', {'text': 'isolation'})['secret_count'] == 0
        waiting = submit(first, 'fixture.probe', {'text': 'snapshot', 'wait': True})
        artifacts = data_root / 'alpha/instances' / first / 'artifacts'
        poll(lambda: (artifacts / 'ready').exists())
        queued = submit(first, 'fixture.probe', {'text': 'next snapshot'})
        assert execution(first, queued)['status'] == 'COMMAND_EXECUTION_STATUS_QUEUED'
        rejected('DeleteNamespace', {'name': 'alpha'}, 'failed_precondition')
        assert artifacts.is_dir()
        # A timed-out CLI wait must not cancel accepted work.
        result = subprocess.run([str(ROOT / 'bin/inframill'), 'execution', 'wait', waiting, '--instance', first, '--endpoint', endpoint.rsplit('/inframill', 1)[0], '--timeout', '50ms', '-o', 'json'], capture_output=True, text=True)
        assert result.returncode == 3, result
        assert 'execution wait' in result.stderr and waiting in result.stderr, result.stderr
        assert execution(first, waiting)['status'] == 'COMMAND_EXECUTION_STATUS_RUNNING'
        replace('alpha', {'token': 'synthetic-second'})
        (artifacts / 'release').write_text('continue')
        assert done(first, waiting)['secret_hash'] == expected('synthetic-first')
        assert done(first, queued)['secret_hash'] == expected('synthetic-second')
        for verb in ['get', 'wait']:
            result = cli('execution', verb, waiting, '--instance', 'alpha/one')['execution']
            assert result['status'] == 'COMMAND_EXECUTION_STATUS_SUCCEEDED', result
        history = cli('execution', 'list', '--instance', 'alpha/one', '--all', '--limit', '1')['executions']
        assert {e['request']['executionId'] for e in history} == {waiting, queued}, history
        log_reply = rpc('ReadCommandLog', {'instance_id': first, 'execution_id': waiting, 'stream': 'LOG_STREAM_STDOUT', 'limit': 65536})
        output = base64.b64decode(log_reply['data']).decode()
        assert [json.loads(line)['type'] for line in output.splitlines()] == ['message', 'success']
        assert 'synthetic-first' not in output
        assert cli('execution', 'logs', waiting, '--instance', first, '--follow', '--limit', '16', raw=True) == output
        replace('alpha', {})
        assert run(first, 'fixture.probe', {'text': 'cleared'})['secret_count'] == 0
        replace('alpha', {'token': 'synthetic-restart'})
        stop()
        start()
        assert rpc('ListNamespaces')['namespaces'] == [{'name': name} for name in namespaces]
        assert rpc('ListNamespaceSecretNames', {'namespace': 'alpha'}).get('names', []) == []
        assert run(first, 'fixture.probe', {'text': 'restart'})['secret_count'] == 0
        replace('beta', {'token': 'synthetic-deleted'})
        rpc('ReplaceNamespaceRegistries', {'namespace': 'beta', 'registries': {
            'example.invalid': {'username': 'example', 'password': 'synthetic-registry'}}})
        rpc('DeleteNamespace', {'name': 'beta'})
        assert not (data_root / 'beta').exists()
        assert other not in [i['id'] for i in rpc('ListInstances')['instances']]
        assert run(first, 'fixture.probe', {'text': 'sibling preserved'})['secret_count'] == 0
        rpc('CreateNamespace', {'name': 'beta'})
        assert rpc('ListNamespaceSecretNames', {'namespace': 'beta'}).get('names', []) == []
        assert rpc('ListNamespaceRegistryHosts', {'namespace': 'beta'}).get('hosts', []) == []
        assert rpc('ListBlueprints', {'namespace': 'beta'}).get('blueprints', []) == []
        for path in data_root.rglob('*'):
            if path.is_file():
                content = path.read_bytes()
                assert all(token.encode() not in content for token in ('synthetic-first', 'synthetic-second', 'synthetic-restart', 'synthetic-deleted', 'synthetic-registry')), path
        print('Daemon workflows, namespace lifecycle, busy deletion, credential isolation and restart passed', flush=True)
    finally:
        stop()
        log.close()
