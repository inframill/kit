"""Validate namespace event integration through released RPC and CLI surfaces."""
import contextlib
import json
import os
import pathlib
import select
import sqlite3
import subprocess
import tempfile
import time
import urllib.error


def read_events(client, namespace, after=0, limit=17):
    events = []
    while True:
        page = client.rpc('ReadNamespaceEvents', {'after_sequence': str(after), 'limit': limit},
                          namespace=namespace)
        events.extend(page.get('events', []))
        after = int(page['nextSequence'])
        if after == int(page['headSequence']):
            break
    sequences = [int(event['sequence']) for event in events]
    assert sequences == sorted(set(sequences)), sequences
    return events, page


def provider_events(client, namespace, instance):
    history = client.rpc('ListCommandExecutions', {'instance_id': instance},
                         namespace=namespace)['executions']

    def current():
        events, page = read_events(client, namespace)
        latest = {}
        for event in events:
            if 'execution' in event and event['execution']['instanceId'] == instance:
                summary = event['execution']
                key = summary['executionId']
                if key in latest:
                    assert int(summary['revision']) > int(latest[key]['revision']), summary
                latest[key] = summary
                assert set(summary) <= {'instanceId', 'executionId', 'revision', 'status', 'pid', 'exitCode', 'reason', 'cancelRequestedAt'}
        if any(latest.get(row['request']['executionId'], {}).get('revision') != row['revision']
               for row in history):
            return None
        for row in history:
            summary = latest[row['request']['executionId']]
            assert summary['status'] == row['status'], (summary, row)
            for field in ('reason', 'cancelRequestedAt'):
                assert summary.get(field) == row.get(field), (summary, row)
            for field in ('pid', 'exitCode'):
                assert (field in summary) == (field in row), (summary, row)
                if field in row:
                    assert int(summary[field]) == int(row[field]), (summary, row)
        return page

    return client.poll(current, 30)


@contextlib.contextmanager
def watch(client, namespace, after=None):
    args = [client.binary, 'namespace', 'events', 'watch', '--namespace', namespace,
            '--endpoint', client.endpoint, '--timeout', '45s']
    if after is not None:
        args += ['--after-sequence', str(after)]
    with tempfile.TemporaryFile() as errors:
        process = subprocess.Popen(args, env=client.env, stdout=subprocess.PIPE, stderr=errors)
        buffered = b''

        def receive():
            nonlocal buffered
            deadline = time.monotonic() + 30
            while b'\n' not in buffered:
                remaining = deadline - time.monotonic()
                assert remaining > 0 and select.select([process.stdout], [], [], remaining)[0], 'Event watch stalled'
                data = os.read(process.stdout.fileno(), 65536)
                if not data:
                    errors.seek(0)
                    raise AssertionError('Event watch closed: ' + errors.read().decode())
                buffered += data
            line, buffered = buffered.split(b'\n', 1)
            return json.loads(line)

        try:
            yield receive
        finally:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
            process.stdout.close()


def stream_check(client, namespace):
    for scope in (None, 'missing-events'):
        try:
            client.rpc('ReadNamespaceEvents', namespace=scope)
        except urllib.error.HTTPError as error:
            assert json.load(error)['code'] == ('invalid_argument' if scope is None else 'not_found')
        else:
            raise AssertionError('Unscoped or missing namespace event read succeeded')
    client.create_namespace('events-sibling')
    sibling = client.rpc('ReadNamespaceEvents', namespace='events-sibling')
    with watch(client, namespace) as receive:
        ready = receive()['ready']
        head = ready['sequence']
        client.rpc('UpsertNamespaceSecret', {'secret': {'name': 'private-event-name', 'value': 'synthetic-event-secret'}},
                   namespace=namespace)
        event = receive()['event']
        assert event['type'] == 'secret.upserted' and event['credentials'] == {'name': 'private-event-name'}, event
        assert int(event['sequence']) == int(head) + 1, event
    assert client.rpc('ReadNamespaceEvents', namespace='events-sibling') == sibling
    with watch(client, namespace, head) as receive:
        assert receive()['ready']['sequence'] == head
        assert receive()['event'] == event
    result = subprocess.run([client.binary, 'namespace', 'events', 'read', '--namespace', namespace,
                             '--endpoint', client.endpoint, '--after-sequence', head, '--limit', '1'],
                            env=client.env, capture_output=True, text=True, check=True, timeout=30)
    page = json.loads(result.stdout)
    assert page['events'] == [event] and page['nextSequence'] == event['sequence'], page
    assert {'headSequence', 'firstSequence', 'resyncRequired'} <= set(page)
    journal = pathlib.Path(client.config['engine']['dataDir']) / namespace / 'data.sqlite'
    with sqlite3.connect(journal.as_uri() + '?mode=ro', uri=True) as db:
        content = str(db.execute('SELECT record FROM events').fetchall())
    assert 'private-event-name' in content and 'synthetic-event-secret' not in content
    client.rpc('DeleteNamespaceSecret', {'name': 'private-event-name'}, namespace=namespace)
    client.rpc('DeleteNamespace', {'id': 'events-sibling'})


def retention_check(client):
    namespace = 'event-retention'
    client.create_namespace(namespace)
    for count in range(1500):
        client.rpc('UpsertNamespaceSecret', {'secret': {'name': 'retention', 'value': str(count)}}, namespace=namespace)
        if count % 50 == 49:
            page = client.rpc('ReadNamespaceEvents', namespace=namespace)
            if int(page['firstSequence']) > 1:
                break
    else:
        raise AssertionError('Configured event retention did not compact')
    assert page['resyncRequired'], page
    path = pathlib.Path(client.config['engine']['dataDir']) / namespace / 'data.sqlite'
    with sqlite3.connect(path.as_uri() + '?mode=ro', uri=True) as db:
        size = db.execute('SELECT bytes FROM event_state WHERE id=1').fetchone()[0]
    assert size <= client.config['engine']['eventJournalMaxBytes'], size
    with watch(client, namespace, 0) as receive:
        ready = receive()['ready']
        assert ready['resyncRequired'] and int(ready['sequence']) == int(page['firstSequence']) - 1, ready
        assert receive()['event']['sequence'] == page['firstSequence']
    client.rpc('DeleteNamespace', {'id': namespace})
