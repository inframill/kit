"""Reconcile application databases without exposing credentials in argv or logs."""
import base64
import fcntl
import hashlib
import hmac
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import ssl
import subprocess
import sys


COMPOSE = ['docker', 'compose', '--project-name',
           os.environ.get('COMPOSE_PROJECT_NAME', 'pv-cloud-postgres'), '--file', 'compose.yaml']
IDENTIFIER = re.compile(r'^[a-z][a-z0-9_]{0,47}$')
SECRET_NAME = re.compile(r'^[a-z][a-z0-9_-]{0,127}$')
PUBLIC_SOURCES = ['0.0.0.0/0', '::/0']


class SetupError(Exception):
    pass


def integer(value, name, low, high):
    if type(value) is not int or not low <= value <= high:
        raise SetupError(f'{name} must be an integer between {low} and {high}')
    return value


def settings():
    config = json.loads(Path('settings.json').read_text())
    allowed = {'databases', 'source_ips', 'port', 'memory_mb', 'max_connections', 'work_mem_mb'}
    if not isinstance(config, dict) or config.keys() - allowed:
        raise SetupError('Invalid PostgreSQL settings')
    config = dict(config)
    config['port'] = integer(config.get('port', 5432), 'port', 1024, 65535)
    config['max_connections'] = integer(config.get('max_connections', 50), 'max_connections', 10, 500)
    config['work_mem_mb'] = integer(config.get('work_mem_mb', 4), 'work_mem_mb', 1, 64)
    physical_mb = os.sysconf('SC_PHYS_PAGES') * os.sysconf('SC_PAGE_SIZE') // (1024 * 1024)
    if physical_mb < 1024:
        raise SetupError('The data node needs at least 1024 MiB of host memory')
    reserve_mb = max(512, physical_mb // 4)
    budget = physical_mb - reserve_mb
    config['memory_mb'] = integer(config.get('memory_mb', budget), 'memory_mb', 512, max(512, budget))
    # Leave headroom for backends, concurrent work areas, autovacuum and the OS.
    if config['memory_mb'] < config['max_connections'] * (8 + 2 * config['work_mem_mb']) + 256:
        raise SetupError('Connection and work_mem budgets exceed memory_mb; reduce them or increase memory_mb')
    sources = config.get('source_ips', PUBLIC_SOURCES)
    if (not isinstance(sources, list) or not sources or len(sources) > 100
            or any(not isinstance(source, str) for source in sources) or len(sources) != len(set(sources))):
        raise SetupError('source_ips must contain unique IPv4 or IPv6 CIDRs')
    try:
        config['source_ips'] = [str(ipaddress.ip_network(source, strict=True)) for source in sources]
    except (ValueError, TypeError):
        raise SetupError('source_ips must contain canonical IPv4 or IPv6 CIDRs') from None
    databases = config.get('databases', [{'name': 'app1', 'password_secret': 'app1-password'},
                                       {'name': 'app2', 'password_secret': 'app2-password'}])
    if not isinstance(databases, list) or not 1 <= len(databases) <= 20:
        raise SetupError('Configure between one and twenty databases')
    names, normalized = set(), []
    for database in databases:
        fields = {'name', 'password_secret', 'connection_limit', 'statement_timeout_ms',
                  'lock_timeout_ms', 'idle_transaction_timeout_ms'}
        if not isinstance(database, dict) or database.keys() - fields:
            raise SetupError('Invalid database settings')
        name = database.get('name', '')
        if not isinstance(name, str) or not IDENTIFIER.fullmatch(name) or name in {
                'postgres', 'template0', 'template1', 'public'} or name.startswith('pg_') or name in names:
            raise SetupError('Database names must be unique, non-reserved lowercase identifiers of at most 48 characters')
        names.add(name)
        database = dict(database)
        reference = database.get('password_secret')
        if not isinstance(reference, str) or not SECRET_NAME.fullmatch(reference):
            raise SetupError(f'Database {name} requires a valid password_secret reference')
        default_limit = min(20, (config['max_connections'] - 5) // len(databases))
        database['connection_limit'] = integer(database.get('connection_limit', default_limit), 'connection_limit', 1, 495)
        for field, default in [('statement_timeout_ms', 30000), ('lock_timeout_ms', 5000),
                               ('idle_transaction_timeout_ms', 60000)]:
            database[field] = integer(database.get(field, default), field, 0, 86400000)
        normalized.append(database)
    if sum(database['connection_limit'] for database in normalized) > config['max_connections'] - 5:
        raise SetupError('Database connection limits must leave five connections for administration')
    config['databases'] = normalized
    return config


def write(path, value, mode=0o644):
    path = Path(path)
    if path.is_symlink():
        raise SetupError('Managed files cannot be symbolic links')
    temporary = path.with_name(path.name + '.tmp')
    with temporary.open('w') as output:
        os.chmod(temporary, mode)
        output.write(value)
    temporary.replace(path)


def password(name, required=False):
    directory = Path('secrets')
    directory.mkdir(mode=0o700, exist_ok=True)
    directory.chmod(0o700)
    path = directory / (name + '_password')
    if path.is_symlink():
        raise SetupError('Credential files cannot be symbolic links')
    if not path.exists():
        if required or name != 'postgres':
            raise SetupError(f'Missing credential file for {name}; push namespace secrets or restore its backup')
        # Exclusive creation preserves the first credential, including on retries.
        with path.open('x') as output:
            os.chmod(path, 0o600)
            output.write(secrets.token_hex(32) + '\n')
    path.chmod(0o600)
    value = path.read_text().rstrip('\r\n')
    if not 16 <= len(value) <= 1024 or any(not 33 <= ord(char) <= 126 for char in value):
        raise SetupError(f'Credential for {name} must contain 16–1024 printable ASCII characters without whitespace')
    return value


def execute(argv, label, data=None, check=True):
    result = subprocess.run(argv, input=data, capture_output=True, text=True, timeout=240)
    if check and result.returncode:
        # psql errors can contain SQL or credential verifiers. Do not forward them.
        raise SetupError(label)
    return result


def admin(sql):
    return execute(COMPOSE + ['exec', '--no-TTY', '--user', 'postgres', 'postgres',
                             'psql', '-X', '-qAt', '--set', 'ON_ERROR_STOP=1', '-U', 'postgres', '-d', 'postgres'],
                   'PostgreSQL administration failed; inspect the service locally',
                   "SET log_statement = 'none'; SET log_min_error_statement = 'panic';\n" + sql).stdout.strip()


def quote(value):
    return "'" + value.replace("'", "''") + "'"


def scram(password_value, salt=None):
    salt = salt if salt is not None else secrets.token_bytes(16)
    salted = hashlib.pbkdf2_hmac('sha256', password_value.encode(), salt, 4096)
    stored = hashlib.sha256(hmac.digest(salted, b'Client Key', 'sha256')).digest()
    server = hmac.digest(salted, b'Server Key', 'sha256')
    encode = lambda value: base64.b64encode(value).decode()
    return f'SCRAM-SHA-256$4096:{encode(salt)}${encode(stored)}:{encode(server)}'


def matches(password_value, verifier):
    try:
        algorithm, parameters, keys = verifier.split('$')
        iterations, salt = parameters.split(':')
        if algorithm != 'SCRAM-SHA-256' or not 1 <= int(iterations) <= 1000000:
            return False
        salted = hashlib.pbkdf2_hmac('sha256', password_value.encode(), base64.b64decode(salt, validate=True), int(iterations))
        stored = hashlib.sha256(hmac.digest(salted, b'Client Key', 'sha256')).digest()
        return hmac.compare_digest(base64.b64encode(stored).decode(), keys.split(':')[0])
    except (ValueError, TypeError, AttributeError):
        return False


def prepare(config):
    certificate = Path('tls/fullchain.pem')
    key = Path('secrets/tls_private_key')
    try:
        ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER).load_cert_chain(certificate, key, password=lambda: '')
    except (OSError, ssl.SSLError):
        raise SetupError('Shared TLS certificate/key is missing, invalid, encrypted or mismatched; push the payload first') from None
    key.chmod(0o600)
    runtime = Path('runtime')
    runtime.mkdir(exist_ok=True)
    runtime.chmod(0o755)
    memory = config['memory_mb']
    cpus = len(os.sched_getaffinity(0)) if hasattr(os, 'sched_getaffinity') else (os.cpu_count() or 1)
    workers = min(cpus, 8)
    conf = f"""listen_addresses = '*'
port = {config['port']}
hba_file = '/etc/pv-postgres/runtime/pg_hba.conf'
ssl = on
ssl_min_protocol_version = 'TLSv1.2'
ssl_cert_file = '/run/pv-postgres-tls/server.crt'
ssl_key_file = '/run/pv-postgres-tls/server.key'
password_encryption = 'scram-sha-256'
shared_buffers = '{memory // 4}MB'
effective_cache_size = '{memory * 3 // 4}MB'
work_mem = '{config['work_mem_mb']}MB'
maintenance_work_mem = '{min(256, memory // 16)}MB'
autovacuum_work_mem = '{min(64, memory // 32)}MB'
max_connections = {config['max_connections']}
max_worker_processes = {max(2, workers)}
max_parallel_workers = {workers}
max_parallel_workers_per_gather = {min(2, max(0, workers - 1))}
max_wal_size = '1GB'
checkpoint_completion_target = 0.9
shared_preload_libraries = 'pg_stat_statements'
logging_collector = off
log_destination = 'stderr'
fsync = on
full_page_writes = on
synchronous_commit = on
"""
    # Exact database/user pairs prevent cross-database and remote admin access.
    hba = ['local all postgres peer', 'local all all reject']
    sources = list(dict.fromkeys(['127.0.0.1/32', '::1/128'] + config['source_ips']))
    for database in config['databases']:
        for source in sources:
            hba.append(f'hostssl "{database["name"]}" "{database["name"]}" {source} scram-sha-256')
    hba += ['host all all 0.0.0.0/0 reject', 'host all all ::/0 reject']
    files = {'postgresql.conf': conf, 'pg_hba.conf': '\n'.join(hba) + '\n',
             'fullchain.pem': certificate.read_text()}
    digest = hashlib.sha256()
    for name, content in files.items():
        write(runtime / name, content)
        digest.update(content.encode())
    digest.update(key.read_bytes())
    write('.env', f"POSTGRES_MEMORY_MB={memory}\nPOSTGRES_SHM_MB={min(1024, max(128, memory // 8))}\n"
          f"POSTGRES_PORT={config['port']}\nPOSTGRES_CONFIG_SHA={digest.hexdigest()}\n", 0o600)


def reconcile(config, passwords):
    names = ','.join(quote(database['name']) for database in config['databases'])
    catalog = json.loads(admin(f"""SELECT json_build_object(
      'roles', (SELECT coalesce(json_agg(json_build_object('name', rolname, 'password', rolpassword,
        'elevated', rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls,
        'member', EXISTS(SELECT 1 FROM pg_auth_members m WHERE m.member = r.oid OR m.roleid = r.oid))), '[]')
        FROM pg_authid r WHERE rolname IN ('postgres', {names})),
      'databases', (SELECT coalesce(json_object_agg(datname, pg_get_userbyid(datdba)), '{{}}')
        FROM pg_database WHERE datname IN ({names})));"""))
    roles = {role['name']: role for role in catalog['roles']}
    if not matches(passwords['postgres'], roles['postgres']['password']):
        raise SetupError('Admin password file does not match the existing database; restore the original credential')
    # Validate every existing role/database before altering any of them.
    for database in config['databases']:
        name = database['name']
        role = roles.get(name)
        if role and (role['elevated'] or role['member']):
            raise SetupError(f'Existing role {name} has elevated privileges or memberships; resolve them explicitly')
        if name in catalog['databases'] and catalog['databases'][name] != name:
            raise SetupError(f'Existing database {name} has another owner; ownership must be migrated explicitly')
    for database in config['databases']:
        name = database['name']
        identifier = '"' + name + '"'
        if name not in roles:
            admin(f'CREATE ROLE {identifier} LOGIN PASSWORD {quote(scram(passwords[name]))};')
        elif not matches(passwords[name], roles[name]['password']):
            admin(f'ALTER ROLE {identifier} PASSWORD {quote(scram(passwords[name]))};')
        if name not in catalog['databases']:
            admin(f'CREATE DATABASE {identifier} OWNER {identifier};')
        admin(f"""ALTER ROLE {identifier} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS
          CONNECTION LIMIT {database['connection_limit']};
          ALTER DATABASE {identifier} CONNECTION LIMIT {database['connection_limit']};
          REVOKE ALL ON DATABASE {identifier} FROM PUBLIC;
          ALTER ROLE {identifier} IN DATABASE {identifier} SET statement_timeout = '{database['statement_timeout_ms']}ms';
          ALTER ROLE {identifier} IN DATABASE {identifier} SET lock_timeout = '{database['lock_timeout_ms']}ms';
          ALTER ROLE {identifier} IN DATABASE {identifier} SET idle_in_transaction_session_timeout = '{database['idle_transaction_timeout_ms']}ms';""")
        # The database owner is pg_database_owner in newly created PostgreSQL 16 databases.
        execute(COMPOSE + ['exec', '--no-TTY', '--user', 'postgres', 'postgres', 'psql', '-X', '-q',
                          '--set', 'ON_ERROR_STOP=1', '-U', 'postgres', '-d', name],
                f'Schema setup failed for {name}',
                f'REVOKE ALL ON SCHEMA public FROM PUBLIC; GRANT ALL ON SCHEMA public TO {identifier};\n'
                'CREATE EXTENSION IF NOT EXISTS pg_stat_statements;\n')


def client(config, user, database, password_value, sql, sslmode='require'):
    # Passfile and SQL travel only through stdin. Passwords never enter argv/env.
    escape = lambda value: value.replace('\\', '\\\\').replace(':', '\\:')
    passfile = ':'.join(escape(value) for value in ['127.0.0.1', str(config['port']), database, user, password_value])
    script = """set -eu
umask 077
passfile=$(mktemp)
trap 'rm -f "$passfile"' EXIT HUP INT TERM
IFS= read -r credential
printf '%s\\n' "$credential" > "$passfile"
PGPASSFILE="$passfile" PGHOST=127.0.0.1 PGPORT="$1" PGUSER="$2" PGDATABASE="$3" PGSSLMODE="$4" \
  psql -X -At --set ON_ERROR_STOP=1
"""
    return execute(COMPOSE + ['exec', '--no-TTY', '--user', 'postgres', 'postgres',
                             'sh', '-c', script, 'client', str(config['port']), user, database, sslmode],
                   'Database client verification failed', passfile + '\n' + sql, check=False)


def verify(config, passwords):
    for database in config['databases']:
        name = database['name']
        probe = '__pv_cloud_verify_' + secrets.token_hex(8)
        checked = client(config, name, name, passwords[name],
                         "SELECT current_database(), current_user, ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid();\n"
                         f'BEGIN; CREATE TABLE public.{probe} (value integer); '
                         f'INSERT INTO public.{probe} VALUES (1); ROLLBACK;\n')
        if checked.returncode or f'{name}|{name}|t' not in checked.stdout.splitlines():
            raise SetupError(f'TLS login or schema ownership verification failed for {name}')
        for other in config['databases']:
            if other['name'] != name and client(config, name, other['name'], passwords[name], 'SELECT 1;').returncode == 0:
                raise SetupError(f'Cross-database access verification failed for {name}')
        if client(config, name, name, passwords[name], 'SELECT 1;', sslmode='disable').returncode == 0:
            raise SetupError(f'Unencrypted connection was accepted for {name}')
        if client(config, name, name, secrets.token_hex(32), 'SELECT 1;').returncode == 0:
            raise SetupError(f'Password verification failed for {name}')
        print(f'{name}: TLS login, migrations and database isolation verified')
    if client(config, 'postgres', 'postgres', passwords['postgres'], 'SELECT 1;').returncode == 0:
        raise SetupError('Administrator TCP access must be disabled')
    active = json.loads(admin("SELECT json_object_agg(name, setting) FROM pg_settings WHERE name IN "
                              "('shared_buffers','max_connections','ssl','fsync','full_page_writes','synchronous_commit','port');"))
    if active != {'shared_buffers': str(config['memory_mb'] // 4 * 128),
                  'max_connections': str(config['max_connections']), 'ssl': 'on', 'fsync': 'on',
                  'full_page_writes': 'on', 'synchronous_commit': 'on', 'port': str(config['port'])}:
        raise SetupError('Effective PostgreSQL settings differ from the configured resource/durability settings')


def main():
    os.chdir(Path(__file__).resolve().parent)
    command = sys.argv[1] if len(sys.argv) == 2 else ''
    if command not in {'start', 'verify'}:
        raise SetupError('Expected start or verify')
    with Path('.setup.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        config = settings()
        passwords = {'postgres': password('postgres', required=command == 'verify')}
        for database in config['databases']:
            passwords[database['name']] = password(database['name'], required=True)
        if command == 'start':
            prepare(config)
            execute([sys.executable, 'registry_login.py'], 'Docker registry login failed; check registry.json and the nonempty secrets/registry_token file')
            execute(COMPOSE + ['config', '--quiet'], 'Invalid PostgreSQL Compose configuration')
            execute(COMPOSE + ['up', '--detach', '--wait', '--wait-timeout', '120'], 'PostgreSQL Compose startup failed')
            reconcile(config, passwords)
        verify(config, passwords)


if __name__ == '__main__':
    try:
        main()
    except SetupError as error:
        sys.exit(str(error))
    except (OSError, ValueError, TypeError, KeyError, subprocess.TimeoutExpired):
        sys.exit('PostgreSQL operation failed; check settings, credential files and Docker availability')
