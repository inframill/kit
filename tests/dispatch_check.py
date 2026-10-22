"""Real Dispatch workflow through a disposable daemon and rendered Blueprint."""
import base64
import json
import os
import pathlib
import shutil
import sys
import tempfile

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))

from build import ROOT, configuration, local_tools, native_platform, providers_dist
from engine_client import Engine


def dispatch_check(tmp, env=None):
    tmp = pathlib.Path(tmp)
    source = tmp / 'blueprints/local-dispatch/latest'
    shutil.copytree(ROOT / 'tests/fixtures/blueprints/local-resource/latest', source)
    definition = json.loads((source / 'blueprint.yaml').read_text())
    del definition['provider_settings']['ansible']
    shutil.rmtree(source / 'templates/ansible')
    definition['provider_settings']['delivery'] = {
        'version': '0.1', 'settings': {
            'id': 'delivery', 'settings_file': 'kit-provider.json', 'producer_id': 'opentofu',
            'producer_version': '0.4.0', 'artifact': 'opentofu-nodes-v1.json', 'connection': 'local',
        },
    }
    (source / 'blueprint.yaml').write_text(json.dumps(definition))
    payload = source / 'templates/delivery/app'
    payload.mkdir(parents=True)
    destination = tmp / 'remote destination'
    settings = {
        'schema': 1, 'address_mode': 'public_ipv4', 'ssh_user': 'root',
        'directories': {'app': {
            'source': 'app', 'destination': str(destination), 'select': {'roles': ['local']},
            'exclude': ['**/*.env'],
        }},
    }
    (source / 'templates/delivery/kit-provider.json').write_text(json.dumps(settings))
    (payload / 'Makefile').write_text(
        '.PHONY: verify deploy fail\n'
        'verify:\n\t@test -x run.sh\n\t@printf "%s" "$(ARCH)" > architecture.txt\n'
        'result.txt: input.txt\n\t@cp input.txt result.txt\n'
        'deploy:\n\t@test -f architecture.txt\n\t@echo synthetic-dispatch-token\n'
        'fail:\n\t@echo expected-make-failure >&2\n\t@exit 7\n')
    (payload / 'run.sh').write_text('#!/bin/sh\nexit 0\n')
    (payload / 'run.sh').chmod(0o700)
    (payload / 'private.env').write_text('excluded\n')
    (payload / 'input.txt').write_text('first\n')
    tofu, ansible = local_tools()
    config = configuration(providers_dist(), tmp / 'data',
                           {'opentofu': tofu, 'ansible': ansible}, target=native_platform())
    config['engine']['providers'] = {
        'opentofu': config['engine']['providers']['opentofu'],
        'delivery': config['engine']['providers']['dispatch'],
    }
    config['engine']['localBlueprintPaths'] = [str(tmp / 'blueprints')]
    architecture = native_platform().split('/')[1]
    with Engine(tmp / 'daemon', config, ROOT / 'bin/inframill', env) as client:
        client.create_namespace('dispatch')
        client.fetch('built.in/local-dispatch', 'dispatch')
        iid = client.initialize('dispatch', 'sample', 'built.in/local-dispatch', {'architecture': architecture})
        workspace = tmp / 'data/dispatch/instances' / iid / 'workspace'
        assert (workspace / 'delivery/app/Makefile').is_file()
        assert not (workspace / 'dispatch').exists()
        client.rpc('UpsertNamespaceSecret', {'secret': {'name': 'token', 'value': 'synthetic-dispatch-token'}},
                   namespace='dispatch')
        client.execute('dispatch', iid, 'opentofu.init')
        plan = client.execute('dispatch', iid, 'opentofu.plan')
        client.execute('dispatch', iid, 'opentofu.apply', {'plan_id': plan['plan_id']})
        pushed = client.execute('dispatch', iid, 'delivery.push', {
            'directory': 'app', 'select': {'architecture': architecture},
            'targets': ['verify', 'result.txt', 'deploy'], 'variables': {'ARCH': architecture},
        })
        assert pushed['pushed'] and len(pushed['archive_sha256']) == 64, pushed
        assert [item['target'] for item in pushed['targets']['localhost']] == ['verify', 'result.txt', 'deploy'], pushed
        assert (destination / 'architecture.txt').read_text() == architecture
        assert not (destination / 'private.env').exists()
        assert (destination / 'result.txt').read_text() == 'first\n'
        (workspace / 'delivery/app/input.txt').write_text('second\n')
        client.execute('dispatch', iid, 'delivery.push', {'directory': 'app', 'targets': ['result.txt']})
        assert (destination / 'result.txt').read_text() == 'second\n', 'Make did not detect the changed input'
        executed = client.execute('dispatch', iid, 'delivery.make', {'directory': 'app', 'targets': ['deploy']})
        assert executed['pushed'] is False and executed['targets']['localhost'][0]['exit_code'] == 0, executed
        failed_id = client.rpc('SubmitCommand', {
            'instance_id': iid, 'command_name': 'delivery.make',
            'arguments': {'json': json.dumps({'directory': 'app', 'targets': ['fail', 'deploy']})},
        }, namespace='dispatch')['executionId']

        def completed():
            value = client.rpc('GetCommandExecution', {'instance_id': iid, 'execution_id': failed_id},
                               namespace='dispatch')['execution']
            return value if value['status'] in ('COMMAND_EXECUTION_STATUS_FAILED',
                                                'COMMAND_EXECUTION_STATUS_SUCCEEDED') else None
        failed = client.poll(completed)
        assert failed['status'] == 'COMMAND_EXECUTION_STATUS_FAILED', failed
        history = client.rpc('ListCommandExecutions', {'instance_id': iid}, namespace='dispatch')['executions']
        # ReadCommandLog returns base64 bytes through protobuf JSON.
        decoded = b''
        for execution in history:
            reply = client.rpc('ReadCommandLog', {
                'instance_id': iid, 'execution_id': execution['request']['executionId'],
                'stream': 'LOG_STREAM_STDERR', 'limit': 65536,
            }, namespace='dispatch')
            log = base64.b64decode(reply.get('data', ''))
            decoded += log
            if execution['request']['executionId'] == failed_id:
                assert b'"target": "fail"' in log and b'"target": "deploy"' not in log, log
        assert b'synthetic-dispatch-token' not in decoded
        assert b'[REDACTED]' in decoded and b'expected-make-failure' in decoded, decoded
        destroy = client.execute('dispatch', iid, 'opentofu.plan', {'destroy': True})
        client.execute('dispatch', iid, 'opentofu.destroy', {'plan_id': destroy['plan_id']})
    print('Dispatch Blueprint rendering, shared host artifacts, provider alias and real Ansible/Make workflow passed', flush=True)


if __name__ == '__main__':
    with tempfile.TemporaryDirectory(prefix='kit-dispatch-') as root:
        dispatch_check(pathlib.Path(root), os.environ.copy())
