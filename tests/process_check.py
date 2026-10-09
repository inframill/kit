"""Exercise released provider access modes and process control with a local stub."""
import json
import pathlib
import shlex
import urllib.error

from blueprint_workflows import LOCAL_REFERENCE
from engine_client import Engine
from events_check import provider_events, stream_check, retention_check


def process_check(tmp, config, binary, arch, env=None):
    tmp = pathlib.Path(tmp)
    tmp.mkdir(parents=True)
    control = tmp / 'control'
    control.mkdir()
    tool = tmp / 'tofu'
    # Control files are outside the Instance: validate remains workspace read-only.
    tool.write_text('''#!/bin/sh
set -eu
if [ "$1" = version ]; then
    printf '{"terraform_version":"1.11.5"}'
    exit 0
fi
control=''' + shlex.quote(str(control)) + '''
touch "$control/$1-$$"
while [ ! -f "$control/$1-release" ]; do sleep 0.05; done
''')
    tool.chmod(0o700)
    config = json.loads(json.dumps(config))
    config['engine']['dataDir'] = str(tmp / 'data')
    config['engine']['eventJournalMaxBytes'] = 131072
    config['engine']['providers']['opentofu']['args'] = ['--tool', str(tool)]
    terminal = {'COMMAND_EXECUTION_STATUS_SUCCEEDED', 'COMMAND_EXECUTION_STATUS_FAILED',
                'COMMAND_EXECUTION_STATUS_INTERRUPTED', 'COMMAND_EXECUTION_STATUS_KILLED',
                'COMMAND_EXECUTION_STATUS_CANCELLED'}
    with Engine(tmp / 'daemon', config, binary, env) as client:
        client.create_namespace('process')
        client.create_namespace('foreign')
        client.fetch(LOCAL_REFERENCE, 'process')
        iid = client.initialize('process', 'local', LOCAL_REFERENCE, {'architecture': arch})

        def submit(command):
            return client.rpc('SubmitCommand', {
                'instance_id': iid, 'command_name': 'opentofu.' + command,
                'arguments': {'json': '{}'},
            }, namespace='process')['executionId']

        def execution(eid):
            return client.rpc('GetCommandExecution', {
                'instance_id': iid, 'execution_id': eid,
            }, namespace='process')['execution']

        def running(eid):
            result = execution(eid)
            return result if result['status'] == 'COMMAND_EXECUTION_STATUS_RUNNING' and 'pid' in result else None

        def done(eid):
            return client.poll(lambda: (r if r['status'] in terminal else None)
                               if (r := execution(eid)) else None, 30)

        def signal(eid, name, namespace='process'):
            return client.rpc('SignalCommandExecution', {
                'instance_id': iid, 'execution_id': eid, 'signal': name,
            }, namespace=namespace)

        def rejected(eid, namespace, code):
            try:
                signal(eid, 'SIGKILL', namespace)
            except urllib.error.HTTPError as error:
                failure = json.load(error)
                assert failure['code'] == code, failure
            else:
                raise AssertionError('Unexpected signal delivery')

        modes = {c['name']: c['accessMode'] for c in
                 client.rpc('ListCommandDefinitions')['commands']}
        assert modes['opentofu.validate'] == 'COMMAND_ACCESS_MODE_READ_ONLY', modes
        for name, mode in modes.items():
            if name.startswith(('opentofu.', 'ansible.')) and name != 'opentofu.validate':
                assert mode == 'COMMAND_ACCESS_MODE_READ_WRITE', (name, mode)

        first, second = submit('validate'), submit('validate')
        readers = [client.poll(lambda: running(eid), 30) for eid in (first, second)]
        client.poll(lambda: len(list(control.glob('validate-[0-9]*'))) == 2, 30)
        assert readers[0]['pid'] != readers[1]['pid'], readers
        assert all(int(r['pid']) > 0 and 'exitCode' not in r and int(r['revision']) > 0
                   for r in readers), readers
        writer, later = submit('init'), submit('validate')
        assert execution(writer)['status'] == 'COMMAND_EXECUTION_STATUS_QUEUED'
        assert execution(later)['status'] == 'COMMAND_EXECUTION_STATUS_QUEUED'
        rejected(first, 'foreign', 'not_found')
        rejected(writer, 'process', 'already_exists')
        signal(first, 'SIGKILL')
        killed = done(first)
        assert killed['status'] == 'COMMAND_EXECUTION_STATUS_INTERRUPTED', killed
        assert killed['exitCode'] == -1 and int(killed['revision']) > int(readers[0]['revision']), killed
        assert running(second) and execution(writer)['status'] == 'COMMAND_EXECUTION_STATUS_QUEUED'
        signal(second, 'SIGKILL')
        assert done(second)['exitCode'] == -1
        client.poll(lambda: running(writer), 30)
        client.poll(lambda: list(control.glob('init-[0-9]*')), 30)
        assert execution(later)['status'] == 'COMMAND_EXECUTION_STATUS_QUEUED'
        assert len(list(control.glob('validate-[0-9]*'))) == 2
        (control / 'init-release').touch()
        completed_writer = done(writer)
        assert completed_writer['status'] == 'COMMAND_EXECUTION_STATUS_SUCCEEDED', completed_writer
        assert completed_writer['exitCode'] == 0
        client.poll(lambda: running(later), 30)
        (control / 'validate-release').touch()
        completed_reader = done(later)
        assert completed_reader['status'] == 'COMMAND_EXECUTION_STATUS_SUCCEEDED', completed_reader
        assert completed_reader['exitCode'] == 0
        rejected(first, 'process', 'already_exists')
        (control / 'validate-release').unlink()
        active = submit('validate')
        client.poll(lambda: running(active), 30)
        queued = submit('init')
        assert execution(queued)['status'] == 'COMMAND_EXECUTION_STATUS_QUEUED'
        receipt = client.rpc('SubmitCommand', {
            'instance_id': iid, 'command_name': 'builtin.cancel',
            'arguments': {'json': '{}'},
        }, namespace='process')['executionId']
        assert execution(active)['status'] == 'COMMAND_EXECUTION_STATUS_KILLED'
        assert execution(queued)['status'] == 'COMMAND_EXECUTION_STATUS_CANCELLED'
        assert execution(receipt)['status'] == 'COMMAND_EXECUTION_STATUS_SUCCEEDED'
        assert client.rpc('GetInstance', {'instance_id': iid}, namespace='process')['instance']['status'] == 'INSTANCE_STATUS_READY'
        saved = {eid: execution(eid) for eid in (first, second, writer, later, active, queued, receipt)}
        page = client.rpc('ListCommandExecutions', {'instance_id': iid}, namespace='process')
        assert {r['request']['executionId']: r for r in page['executions']} == saved
        provider_events(client, 'process', iid)
        stream_check(client, 'process')
        retention_check(client)
        event_head = client.rpc('ReadNamespaceEvents', namespace='process')['headSequence']

    # Restart must preserve outcomes/revisions without adopting diagnostic PIDs.
    with Engine(tmp / 'restarted', config, binary, env) as client:
        page = client.rpc('ReadNamespaceEvents', {'after_sequence': event_head}, namespace='process')
        assert page['resyncRequired'] and any(e['type'] == 'stream.reset' for e in page['events']), page
        for eid, previous in saved.items():
            result = client.rpc('GetCommandExecution', {
                'instance_id': iid, 'execution_id': eid,
            }, namespace='process')['execution']
            assert result == previous, (result, previous)
    print('Provider reader overlap, writer fairness, scoped signals, PID/exit codes and durable revisions passed', flush=True)
    print('Namespace event summaries, CLI live/replay, secret isolation, retention and restart refresh passed', flush=True)
