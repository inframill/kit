"""Real Dispatch workflow through a disposable daemon and rendered Blueprint."""
import base64
import json
import os
import pathlib
import shlex
import shutil
import sys
import tempfile

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))

from build import ROOT, configuration, local_tools, native_platform, providers_dist
from engine_client import Engine
from blueprint_workflows import command_contract_check


def dispatch_check(tmp, env=None):
    tmp = pathlib.Path(tmp)
    source = tmp / 'blueprints/local-dispatch/latest'
    shutil.copytree(ROOT / 'tests/fixtures/blueprints/local-resource/latest', source)
    definition = json.loads((source / 'blueprint.yaml').read_text())
    del definition['provider_settings']['ansible']
    definition['command_arguments'] = {
        name: schema for name, schema in definition['command_arguments'].items()
        if not name.startswith('ansible.')
    }
    shutil.rmtree(source / 'templates/ansible')
    definition['provider_settings']['delivery'] = {
        'version': '0.1', 'settings': {
            'id': 'delivery', 'settings_file': 'kit-provider.json', 'producer_id': 'opentofu',
            'producer_version': '0.4.0', 'artifact': 'opentofu-nodes-v1.json', 'connection': 'local',
        },
    }
    for command in ['push', 'make']:
        schema = {
            'type': 'object', 'additionalProperties': False, 'required': ['directory'],
            'properties': {'directory': {'type': 'array', 'minItems': 1, 'maxItems': 2,
                                        'uniqueItems': True, 'items': {'type': 'string', 'enum': ['app', 'worker']}}},
        }
        if command == 'make':
            schema['required'].append('target')
            schema['properties']['target'] = {
                'type': 'string', 'pattern': '^[a-zA-Z0-9_][a-zA-Z0-9_./-]{0,127}$',
            }
        definition['command_arguments']['delivery.' + command] = schema
    (source / 'blueprint.yaml').write_text(json.dumps(definition))
    payload = source / 'templates/delivery/app'
    payload.mkdir(parents=True)
    destination = tmp / 'remote destination'
    worker_destination = tmp / 'remote worker'
    architecture = native_platform().split('/')[1]
    settings = {
        'schema': 1, 'address_mode': 'public_ipv4', 'ssh_user': 'root',
        'directories': {'app': {
            'source': 'app', 'destination': str(destination),
            'select': {'roles': ['local'], 'architecture': architecture},
            'secret_files': {'secrets/root_token': 'token'},
        }, 'worker': {
            'source': 'worker', 'destination': str(worker_destination),
            'select': {'roles': ['local'], 'architecture': architecture},
        }},
    }
    (source / 'templates/delivery/kit-provider.json').write_text(json.dumps(settings))
    (payload / 'Makefile').write_text(
        '.PHONY: verify deploy fail\n'
        f'verify:\n\t@test -x run.sh\n\t@printf "%s" "{architecture}" > architecture.txt\n'
        'result.txt: input.txt\n\t@cp input.txt result.txt\n'
        'deploy:\n\t@test -f architecture.txt\n\t@echo synthetic-dispatch-token\n'
        'fail:\n\t@echo expected-make-failure >&2\n\t@exit 7\n')
    (payload / 'run.sh').write_text('#!/bin/sh\nexit 0\n')
    (payload / 'run.sh').chmod(0o700)
    (payload / 'private.env').write_text('included\n')
    (payload / 'input.txt').write_text('first\n')
    worker = source / 'templates/delivery/worker'
    worker.mkdir()
    (worker / 'Makefile').write_text(
        'verify:\n\t@test -f ' + shlex.quote(str(destination / 'architecture.txt')) + '\n'
        '\t@touch verified\nfail:\n\t@touch should-not-run\n')
    tofu, ansible = local_tools()
    config = configuration(providers_dist(), tmp / 'data',
                           {'opentofu': tofu, 'ansible': ansible}, target=native_platform())
    config['engine']['providers'] = {
        'opentofu': config['engine']['providers']['opentofu'],
        'delivery': config['engine']['providers']['dispatch'],
    }
    config['engine']['localBlueprintPaths'] = [str(tmp / 'blueprints')]
    with Engine(tmp / 'daemon', config, ROOT / 'bin/inframill', env) as client:
        client.create_namespace('dispatch')
        client.fetch('built.in/local-dispatch', 'dispatch')
        iid = client.initialize('dispatch', 'sample', 'built.in/local-dispatch', {'architecture': architecture})
        command_contract_check(client, 'dispatch', iid,
            ['opentofu.' + c for c in ['init', 'validate', 'plan', 'apply', 'destroy']] +
            ['delivery.push', 'delivery.make'], [
                ('dispatch.push', {'directory': ['app']}),
                ('delivery.push', {'directory': ['app'], 'target': 'deploy'}),
                ('delivery.make', {'directory': ['app']}),
                ('delivery.make', {'directory': ['app'], 'target': ['deploy']}),
                ('delivery.push', {'directory': []}),
                ('delivery.push', {'directory': ['app', 'app']}),
                ('delivery.make', {'directory': ['app', 'app'], 'target': 'verify'}),
                ('delivery.make', {'directory': ['missing'], 'target': 'verify'}),
            ])
        workspace = tmp / 'data/dispatch/instances' / iid / 'workspace'
        assert (workspace / 'delivery/app/Makefile').is_file()
        assert not (workspace / 'dispatch').exists()
        client.rpc('UpsertNamespaceSecret', {'secret': {'name': 'token', 'value': 'synthetic-dispatch-token'}},
                   namespace='dispatch')
        client.execute('dispatch', iid, 'opentofu.init')
        plan = client.execute('dispatch', iid, 'opentofu.plan')
        client.execute('dispatch', iid, 'opentofu.apply', {'plan_id': plan['plan_id']})
        pushed = client.execute('dispatch', iid, 'delivery.push', {'directory': ['app', 'worker']})
        assert pushed['pushed'] and set(pushed['directories']) == {'app', 'worker'}, pushed
        assert all(result['hosts']['localhost']['exit_code'] == 0 and not result['targets']
                   for result in pushed['directories'].values()), pushed
        verified = client.execute('dispatch', iid, 'delivery.make', {'directory': ['app', 'worker'], 'target': 'verify'})
        assert set(verified['directories']) == {'app', 'worker'}, verified
        assert (worker_destination / 'verified').exists()
        for target in ['result.txt', 'deploy']:
            made = client.execute('dispatch', iid, 'delivery.make', {'directory': ['app'], 'target': target})
            assert [item['target'] for item in made['targets']['localhost']] == [target], made
        assert (destination / 'architecture.txt').read_text() == architecture
        assert (destination / 'private.env').read_text() == 'included\n'
        assert (destination / 'result.txt').read_text() == 'first\n'
        secret_file = destination / 'secrets/root_token'
        assert secret_file.read_text() == 'synthetic-dispatch-token'
        assert secret_file.stat().st_mode & 0o777 == 0o600
        client.rpc('UpsertNamespaceSecret', {'secret': {'name': 'token', 'value': 'rotated-dispatch-token'}},
                   namespace='dispatch')
        (workspace / 'delivery/app/input.txt').write_text('second\n')
        # SCP preserves whole seconds; make the changed input newer than the
        # remote Make output even when both writes occur within one second.
        changed_at = (destination / 'result.txt').stat().st_mtime + 2
        os.utime(workspace / 'delivery/app/input.txt', (changed_at, changed_at))
        client.execute('dispatch', iid, 'delivery.push', {'directory': ['app']})
        client.execute('dispatch', iid, 'delivery.make', {'directory': ['app'], 'target': 'result.txt'})
        assert (destination / 'result.txt').read_text() == 'second\n', 'Make did not detect the changed input'
        assert secret_file.read_text() == 'rotated-dispatch-token'
        # Keep both credential versions available for the log-redaction assertion.
        client.rpc('UpsertNamespaceSecret', {'secret': {'name': 'previous-token', 'value': 'synthetic-dispatch-token'}},
                   namespace='dispatch')
        executed = client.execute('dispatch', iid, 'delivery.make', {'directory': ['app'], 'target': 'deploy'})
        assert executed['pushed'] is False and executed['targets']['localhost'][0]['exit_code'] == 0, executed
        failed_id = client.rpc('SubmitCommand', {
            'instance_id': iid, 'command_name': 'delivery.make',
            'arguments': {'json': json.dumps({'directory': ['app', 'worker'], 'target': 'fail'})},
        }, namespace='dispatch')['executionId']

        def completed():
            value = client.rpc('GetCommandExecution', {'instance_id': iid, 'execution_id': failed_id},
                               namespace='dispatch')['execution']
            return value if value['status'] in ('COMMAND_EXECUTION_STATUS_FAILED',
                                                'COMMAND_EXECUTION_STATUS_SUCCEEDED') else None
        failed = client.poll(completed)
        assert failed['status'] == 'COMMAND_EXECUTION_STATUS_FAILED', failed
        assert not (worker_destination / 'should-not-run').exists(), 'Make continued after a failed directory'
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
                events = [json.loads(line) for line in log.splitlines() if line.strip()]
                assert any(event.get('dispatch') == 'make' and event.get('target') == 'fail'
                           and event.get('exit_code') != 0 for event in events), events
                assert all(event.get('target') != 'deploy' for event in events), events
        assert b'synthetic-dispatch-token' not in decoded
        assert b'[REDACTED]' in decoded and b'expected-make-failure' in decoded, decoded
        destroy = client.execute('dispatch', iid, 'opentofu.plan', {'destroy': True})
        client.execute('dispatch', iid, 'opentofu.destroy', {'plan_id': destroy['plan_id']})
    print('Dispatch Blueprint rendering, shared host artifacts, provider alias and direct copy/Make workflow passed', flush=True)


if __name__ == '__main__':
    with tempfile.TemporaryDirectory(prefix='kit-dispatch-') as root:
        dispatch_check(pathlib.Path(root), os.environ.copy())
