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

    def rpc(method, body=None, *, namespace=None):
        headers = {'Content-Type': 'application/json', 'Connect-Protocol-Version': '1'}
        if namespace is not None:
            headers['X-Namespace-ID'] = namespace
        request = urllib.request.Request(endpoint + method, json.dumps(body or {}).encode(), headers)
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

    scopes = {}

    def initialize(namespace, name, reference, values):
        rpc('FetchBlueprint', {'reference': reference}, namespace=namespace)
        iid = rpc('InitInstance', {'name': name, 'blueprint_reference': reference,
                  'values': {'json': json.dumps(values)}}, namespace=namespace)['instance']['id']
        scopes[iid] = namespace
        return iid

    def submit(instance, command, args=None):
        return rpc('SubmitCommand', {'instance_id': instance, 'command_name': command, 'arguments': {'json': json.dumps(args or {})}}, namespace=scopes[instance])['executionId']

    def execution(instance, command):
        return rpc('GetCommandExecution', {'instance_id': instance, 'execution_id': command}, namespace=scopes[instance])['execution']

    def done(instance, command):
        value = poll(lambda: (r if r['status'] in ('COMMAND_EXECUTION_STATUS_SUCCEEDED', 'COMMAND_EXECUTION_STATUS_FAILED', 'COMMAND_EXECUTION_STATUS_INTERRUPTED') else None) if (r := execution(instance, command)) else None)
        assert value['status'] == 'COMMAND_EXECUTION_STATUS_SUCCEEDED', value
        return json.loads(value['result']['json']) if 'result' in value else None

    def run(instance, command, args=None):
        return done(instance, submit(instance, command, args))

    def replace(namespace, secrets):
        rpc('ReplaceNamespaceSecrets', {'secrets': secrets}, namespace=namespace)

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

    def rejected(method, body, code, namespace=None):
        try:
            rpc(method, body, namespace=namespace)
        except urllib.error.HTTPError as error:
            failure = json.load(error)
            assert failure['code'] == code, failure
        else:
            raise AssertionError(method + ' unexpectedly succeeded')

    try:
        start()
        assert rpc('ListNamespaces').get('namespaces', []) == []
        reference = registry + '/inframill/blueprints/local-resource:0.1.0'
        rejected('InitInstance', {'name': 'local', 'blueprint_reference': reference,
                                 'values': {'json': '{}'}}, 'not_found', namespace='missing')
        rejected('FetchBlueprint', {'reference': reference}, 'not_found', namespace='missing')
        rejected('ReplaceNamespaceSecrets', {'secrets': {}}, 'not_found', namespace='missing')
        assert not (data_root / 'missing').exists()
        namespaces = ['acceptance', 'alpha', 'beta', 'empty']
        for namespace in namespaces:
            rpc('CreateNamespace', {'id': namespace})
        rejected('CreateNamespace', {'id': 'alpha'}, 'already_exists')
        statuses = {d['id']: d['availability'] for d in rpc('ListProviders')['providers']}
        assert statuses['opentofu'] == 'PROVIDER_AVAILABILITY_AVAILABLE', statuses
        assert statuses['ansible'] == 'PROVIDER_AVAILABILITY_AVAILABLE', statuses
        assert statuses['unavailable'] == 'PROVIDER_AVAILABILITY_UNAVAILABLE', statuses
        iid = initialize('acceptance', 'local', registry + '/inframill/blueprints/local-resource:0.1.0', {'architecture': 'arm64' if platform.machine() in ('arm64', 'aarch64') else 'amd64'})
        run(iid, 'opentofu.init')
        plan = run(iid, 'opentofu.plan')
        run(iid, 'opentofu.apply', {'plan_id': plan['plan_id']})
        run(iid, 'ansible.apply', {'playbook': 'site'})
        plan = run(iid, 'opentofu.plan', {'destroy': True})
        run(iid, 'opentofu.destroy', {'plan_id': plan['plan_id']})
        assert len(rpc('ListCommandExecutions', {'instance_id': iid}, namespace='acceptance')['executions']) == 6
        ref = registry + '/inframill/blueprints/protocol-fixture:0.1.0'
        first = initialize('alpha', 'one', ref, {})
        other = initialize('beta', 'one', ref, {})
        # Discovery includes every available Provider, regardless of saved bindings.
        commands = cli('provider', 'commands')['commands']
        names = {c['name'] for c in commands}
        assert {'fixture.probe', 'opentofu.plan', 'ansible.apply'} <= names, commands
        assert not any(name.startswith(('builtin.', 'unavailable.')) for name in names), commands
        wire_commands = rpc('ListCommandDefinitions')['commands']
        assert commands == [{**c, 'argumentsSchema': json.loads(c['argumentsSchema']['json'])}
                            for c in wire_commands], commands
        assert cli('provider', 'commands', 'fixture.probe') == next(
            c for c in commands if c['name'] == 'fixture.probe')
        assert [i['id'] for i in rpc('ListInstances', namespace='alpha')['instances']] == [first]
        replace('alpha', {'token': 'synthetic-first'})
        assert rpc('ListNamespaceSecretNames', namespace='alpha')['names'] == [{'name': 'token'}]
        assert run(other, 'fixture.probe', {'text': 'isolation'})['secret_count'] == 0
        waiting = submit(first, 'fixture.probe', {'text': 'snapshot', 'wait': True})
        artifacts = data_root / 'alpha/instances' / first / 'artifacts'
        poll(lambda: (artifacts / 'ready').exists())
        queued = submit(first, 'fixture.probe', {'text': 'next snapshot'})
        assert execution(first, queued)['status'] == 'COMMAND_EXECUTION_STATUS_QUEUED'
        assert artifacts.is_dir()
        # A timed-out CLI wait must not cancel accepted work.
        result = subprocess.run([str(ROOT / 'bin/inframill'), 'execution', 'wait', waiting, '--instance', first, '--namespace', 'alpha', '--endpoint', endpoint.rsplit('/inframill', 1)[0], '--timeout', '50ms', '-o', 'json'], capture_output=True, text=True)
        assert result.returncode == 3, result
        assert 'execution wait' in result.stderr and waiting in result.stderr, result.stderr
        assert execution(first, waiting)['status'] == 'COMMAND_EXECUTION_STATUS_RUNNING'
        replace('alpha', {'token': 'synthetic-second'})
        (artifacts / 'release').write_text('continue')
        assert done(first, waiting)['secret_hash'] == expected('synthetic-first')
        assert done(first, queued)['secret_hash'] == expected('synthetic-second')
        for verb in ['get', 'wait']:
            result = cli('execution', verb, waiting, '--instance', 'one', '--namespace', 'alpha')['execution']
            assert result['status'] == 'COMMAND_EXECUTION_STATUS_SUCCEEDED', result
        history = cli('execution', 'list', '--instance', 'one', '--namespace', 'alpha', '--all', '--limit', '1')['executions']
        assert {e['request']['executionId'] for e in history} == {waiting, queued}, history
        log_reply = rpc('ReadCommandLog', {'instance_id': first, 'execution_id': waiting, 'stream': 'LOG_STREAM_STDOUT', 'limit': 65536}, namespace='alpha')
        output = base64.b64decode(log_reply['data']).decode()
        assert [json.loads(line)['type'] for line in output.splitlines()] == ['message', 'success']
        assert 'synthetic-first' not in output
        assert cli('execution', 'logs', waiting, '--instance', first, '--namespace', 'alpha', '--follow', '--limit', '16', raw=True) == output
        replace('alpha', {})
        assert run(first, 'fixture.probe', {'text': 'cleared'})['secret_count'] == 0
        replace('alpha', {'token': 'synthetic-restart'})
        stop()
        start()
        assert rpc('ListNamespaces')['namespaces'] == [{'id': namespace_id, 'status': 'NAMESPACE_STATUS_ACTIVE'} for namespace_id in namespaces]
        assert rpc('ListNamespaceSecretNames', namespace='alpha').get('names', []) == []
        assert run(first, 'fixture.probe', {'text': 'restart'})['secret_count'] == 0
        replace('beta', {'token': 'synthetic-deleted'})
        rpc('ReplaceNamespaceRegistries', {'registries': {
            'example.invalid': {'username': 'example', 'password': 'synthetic-registry'}}}, namespace='beta')
        busy = submit(other, 'fixture.probe', {'text': 'delete while busy', 'wait': True})
        beta_artifacts = data_root / 'beta/instances' / other / 'artifacts'
        poll(lambda: (beta_artifacts / 'ready').exists())
        rpc('DeleteNamespace', {'id': 'beta'})
        assert rpc('GetNamespace', {'id': 'beta'})['namespace']['status'] == 'NAMESPACE_STATUS_DELETING'
        rejected('SubmitCommand', {'instance_id': other, 'command_name': 'fixture.probe',
                                  'arguments': {'json': '{}'}}, 'failed_precondition', namespace='beta')
        assert beta_artifacts.is_dir()
        (beta_artifacts / 'release').write_text('continue')
        done(other, busy)
        poll(lambda: not (data_root / 'beta').exists())
        assert run(first, 'fixture.probe', {'text': 'sibling preserved'})['secret_count'] == 0
        rpc('CreateNamespace', {'id': 'beta'})
        assert rpc('ListNamespaceSecretNames', namespace='beta').get('names', []) == []
        assert rpc('ListNamespaceRegistryHosts', namespace='beta').get('hosts', []) == []
        assert rpc('ListBlueprints', namespace='beta').get('blueprints', []) == []
        for path in data_root.rglob('*'):
            if path.is_file():
                content = path.read_bytes()
                assert all(token.encode() not in content for token in ('synthetic-first', 'synthetic-second', 'synthetic-restart', 'synthetic-deleted', 'synthetic-registry')), path
        print('Daemon workflows, namespace lifecycle, busy deletion, credential isolation and restart passed', flush=True)
    finally:
        stop()
        log.close()
