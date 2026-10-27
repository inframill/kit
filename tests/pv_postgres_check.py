"""Real PostgreSQL acceptance checks in disposable Docker containers and storage."""
import json
import os
import re
from pathlib import Path
import secrets
import subprocess
import sys
import tempfile
import uuid

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from test_pv_postgres import postgres, prepare_payload, working_directory


def check():
    with tempfile.TemporaryDirectory(prefix='kit-pv-postgres-') as td:
        payload = prepare_payload(td)
        project = 'kit-pg-test-' + uuid.uuid4().hex[:10]
        env = {**os.environ, 'COMPOSE_PROJECT_NAME': project}
        compose = ['docker', 'compose', '--project-name', project, '--file', 'compose.yaml']
        config = {'port': 40000 + secrets.randbelow(20000), 'memory_mb': 1024, 'max_connections': 20}

        def configure():
            (payload / 'settings.json').write_text(json.dumps(config))

        def run(argv, data=None, success=True):
            result = subprocess.run(argv, cwd=payload, env=env, input=data,
                                    capture_output=True, text=True, timeout=240)
            if success:
                assert result.returncode == 0, result.stdout + result.stderr
            return result

        def make(target, success=True):
            result = run(['make', target], success=success)
            for path in (payload / 'secrets').glob('*password'):
                assert path.read_text().strip() not in result.stdout + result.stderr, 'Credential leaked to Make output'
            return result

        def external_client(password, database='app1', sslmode='verify-full'):
            # A bridge-network client reaches the host-network server through the
            # Docker gateway. This exercises a non-loopback source and TLS identity.
            gateway = run(['docker', 'network', 'inspect', 'bridge', '--format',
                           '{{(index .IPAM.Config 0).Gateway}}']).stdout.strip()
            escape = lambda value: value.replace('\\', '\\\\').replace(':', '\\:')
            credential = ':'.join(escape(value) for value in
                                  ['postgres.test', str(config['port']), database, 'app1', password])
            script = """set -eu
umask 077
passfile=$(mktemp)
trap 'rm -f "$passfile"' EXIT
IFS= read -r credential
printf '%s\\n' "$credential" > "$passfile"
PGPASSFILE="$passfile" PGHOST=postgres.test PGHOSTADDR="$1" PGPORT="$2" PGUSER=app1 PGDATABASE="$3" \
 PGSSLMODE="$4" PGSSLROOTCERT=/root.crt PGCONNECT_TIMEOUT=5 psql -X -At --set ON_ERROR_STOP=1
"""
            return run(['docker', 'run', '--rm', '-i', '--network', 'bridge',
                        '--mount', f'type=bind,source={payload / "tls/fullchain.pem"},target=/root.crt,readonly',
                        '--entrypoint', 'sh', 'postgres:16', '-c', script, 'client',
                        gateway, str(config['port']), database, sslmode],
                       credential + '\nSELECT current_user;\n', success=False)

        configure()
        postgres.COMPOSE = compose
        try:
            password_file = payload / 'secrets/app1_password'
            supplied_password = password_file.read_bytes()
            password_file.unlink()
            failed = make('start', success=False)
            assert failed.returncode != 0 and 'Missing credential file for app1' in failed.stderr
            assert not password_file.exists(), 'Start generated an application password'
            password_file.write_bytes(supplied_password)
            make('start')
            print('Fresh start: two databases, TLS, migrations and isolation passed', flush=True)
            original = {path.name: path.read_bytes() for path in (payload / 'secrets').glob('*password')}
            container = run(compose + ['ps', '--quiet', 'postgres']).stdout.strip()
            host = json.loads(run(['docker', 'inspect', '--format', '{{json .HostConfig}}', container]).stdout)
            assert host['NetworkMode'] == 'host' and not host['PortBindings']
            assert host['CpuQuota'] == 0 and host['Memory'] == 1024 * 1024 * 1024
            assert host['LogConfig']['Type'] == 'local'
            make('start')
            assert run(compose + ['ps', '--quiet', 'postgres']).stdout.strip() == container, 'Unchanged start recreated PostgreSQL'
            assert original == {path.name: path.read_bytes() for path in (payload / 'secrets').glob('*password')}
            with working_directory(payload):
                result = postgres.client(config, 'app1', 'app1', postgres.password('app1', required=True),
                                         'CREATE TABLE public.persisted (id integer); INSERT INTO public.persisted VALUES (7);')
                assert result.returncode == 0
            app1 = (payload / 'secrets/app1_password').read_text().strip()
            assert external_client(app1).returncode == 0, 'External TLS hostname verification failed'
            assert external_client(app1, sslmode='disable').returncode != 0
            assert external_client(app1, database='app2').returncode != 0
            assert external_client('incorrect-password-value').returncode != 0
            print('External bridge client: verified TLS identity, authentication and access restrictions', flush=True)

            config['databases'] = [{'name': 'app1', 'password_secret': 'app1-password'},
                                   {'name': 'app2', 'password_secret': 'app2-password'},
                                   {'name': 'app3', 'password_secret': 'test-app3-password'}]
            config['max_connections'] = 30
            (payload / 'secrets/app3_password').write_text("namespace:password\\with'punctuation$\n")
            configure()
            make('start')
            print('Existing volume: added a third database with an injected credential', flush=True)

            changed = 'new-app2-password-' + secrets.token_hex(16)
            previous = (payload / 'secrets/app2_password').read_text().strip()
            (payload / 'secrets/app2_password').write_text(changed + '\n')
            failed = make('verify', success=False)
            assert failed.returncode != 0 and 'verification failed for app2' in failed.stderr
            make('start')
            with working_directory(payload):
                assert postgres.client(config, 'app2', 'app2', previous, 'SELECT 1;').returncode != 0
            assert (payload / 'secrets/postgres_password').read_bytes() == original['postgres_password']
            print('Start applied the supplied password change; verification detected stale credentials', flush=True)

            config['source_ips'] = ['192.0.2.0/24']
            configure()
            make('start')
            assert external_client(app1).returncode != 0, 'External source allowlist was not enforced'
            print('External source CIDR restriction passed', flush=True)
            make('stop')
            make('start')
            with working_directory(payload):
                result = postgres.client(config, 'app1', 'app1', app1, 'SELECT id FROM public.persisted;')
                assert result.returncode == 0 and '7' in result.stdout.splitlines()
            make('verify')
            print('Stop/restart preserved data and credentials; effective settings verified', flush=True)
        except Exception:
            logs = run(compose + ['logs', '--no-color', '--tail', '80'], success=False)
            for line in logs.stdout.splitlines():
                if 'ERROR:' in line or 'FATAL:' in line:
                    for path in (payload / 'secrets').glob('*password'):
                        line = line.replace(path.read_text().strip(), '[REDACTED]')
                    print(re.sub(r'SCRAM-SHA-256\$\S+', '[REDACTED]', line), flush=True)
            raise
        finally:
            run(compose + ['down', '--volumes', '--remove-orphans'])


if __name__ == '__main__':
    check()
