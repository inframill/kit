"""Exercise the unchanged Millway image against real PostgreSQL and Inframill."""
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import tempfile
import uuid

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from build import ROOT
from test_pv_postgres import prepare_payload as prepare_postgres
from test_pv_inframill import prepare_payload as prepare_inframill
from test_pv_millway import prepare_payload as prepare_millway


def check():
    with tempfile.TemporaryDirectory(prefix='kit-millway-') as td:
        root = Path(td)
        database = prepare_postgres(root)
        web = prepare_millway(root, database)
        worker = prepare_inframill(root)
        project = 'kit-web-test-' + uuid.uuid4().hex[:10]
        port = 40000 + secrets.randbelow(15000)
        database_port, worker_port, web_port = port, port + 1, port + 2
        projects = {database: project + '-db', worker: project + '-worker', web: project + '-web'}

        def run(payload, argv, data=None, success=True):
            result = subprocess.run(argv, cwd=payload, env={**os.environ, 'COMPOSE_PROJECT_NAME': projects[payload]},
                                    input=data, capture_output=True, text=True, timeout=240)
            if success:
                assert result.returncode == 0, result.stdout + result.stderr
            return result

        def make(payload, target, success=True):
            result = run(payload, ['make', target], success=success)
            for name in ['root_token', 'inframill_root_token', 'database_password', 'app1_password', 'app2_password', 'postgres_password']:
                path = payload / 'secrets' / name
                if path.exists():
                    assert path.read_text().strip() not in result.stdout + result.stderr, 'Credential appeared in output'
            return result

        def compose(payload):
            return ['docker', 'compose', '--project-name', projects[payload], '--file', 'compose.yaml']

        (database / 'settings.json').write_text(json.dumps({'port': database_port, 'memory_mb': 1024, 'max_connections': 30}))
        for source, destination in [('secrets/tls_private_key', 'secrets/tls_private_key'), ('tls/fullchain.pem', 'tls/fullchain.pem')]:
            shutil.copy2(database / source, worker / destination)
        shutil.copy2(web / 'secrets/inframill_root_token', worker / 'secrets/root_token')
        configuration = worker / 'inframill.yaml'
        configuration.write_text(configuration.read_text().replace('port: 443', f'port: {worker_port}', 1))
        health = worker / 'healthcheck.py'
        health.write_text(health.read_text().replace('127.0.0.1:443', f'127.0.0.1:{worker_port}'))
        document = json.loads((web / 'settings.json').read_text())
        document['postgres'] = {'port': database_port, 'max_connections': 30}
        document['millway'].update({'database_host': '127.0.0.1',
                                   'inframill_endpoint': f'https://127.0.0.1:{worker_port}', 'port': web_port})
        (web / 'settings.json').write_text(json.dumps(document))
        try:
            make(database, 'start')
            make(worker, 'start')
            make(web, 'start')
            print('Three-service start: Millway 0.1.5-dev native verification passed', flush=True)
            container = run(web, compose(web) + ['ps', '--quiet', 'millway']).stdout.strip()
            inspected = json.loads(run(web, ['docker', 'inspect', '--format', '{{json .}}', container]).stdout)
            assert inspected['Config']['User'] == '65532:65532'
            assert inspected['HostConfig']['NetworkMode'] == 'host'
            assert inspected['HostConfig']['ReadonlyRootfs']
            assert inspected['HostConfig']['CapDrop'] == ['ALL']
            # Exercise the web-role kernel policy in a disposable network
            # namespace, without changing the workstation/VM host's sysctl.
            run(web, ['docker', 'run', '--rm', '--user', '65532:65532', '--cap-drop', 'ALL',
                      '--security-opt', 'no-new-privileges:true', '--sysctl', 'net.ipv4.ip_unprivileged_port_start=443',
                      '--entrypoint', 'python3', 'ghcr.io/inframill/kit:' + (ROOT / 'VERSION').read_text().strip(),
                      '-c', "import os,socket;assert os.getuid()==65532;s=socket.socket();s.bind(('0.0.0.0',443));print('Non-root port 443 binding passed')"])
            make(web, 'verify')
            make(database, 'stop')
            failed = make(web, 'verify', success=False)
            assert failed.returncode != 0 and 'database FAILED' in failed.stdout
            assert 'inframill "worker-1" READY' in failed.stdout
            make(database, 'start')
            make(web, 'verify')
            print('Native verification rejects an unavailable database and still probes Inframill', flush=True)
            token = worker / 'secrets/root_token'
            original = token.read_bytes()
            token.write_text('incorrect-upstream-root-token')
            make(worker, 'start')
            failed = make(web, 'verify', success=False)
            assert failed.returncode != 0 and 'inframill "worker-1"' in failed.stdout
            token.write_bytes(original)
            make(worker, 'start')
            make(web, 'verify')
            print('Incorrect Inframill credentials rejected', flush=True)
            make(web, 'stop')
            make(web, 'start')
            make(web, 'verify')
            print('Non-root restart passed native Millway verification', flush=True)
        except Exception:
            logs = run(web, compose(web) + ['logs', '--no-color', '--tail', '30', 'millway'], success=False)
            for line in logs.stdout.splitlines():
                if 'millway:' in line or 'error' in line.lower():
                    for name in ['root_token', 'inframill_root_token', 'database_password']:
                        value = (web / 'secrets' / name).read_text().strip()
                        line = line.replace(value, '[REDACTED]')
                    print(line, flush=True)
            raise
        finally:
            for payload in [web, worker, database]:
                run(payload, compose(payload) + ['down', '--volumes', '--remove-orphans'], success=False)


if __name__ == '__main__':
    check()
