"""Prepare configuration and start the non-root Millway image."""
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import ssl
import subprocess
import sys
import urllib.parse

COMPOSE = ['docker', 'compose', '--project-name',
           os.environ.get('COMPOSE_PROJECT_NAME', 'pv-cloud-millway'), '--file', 'compose.yaml']
REFERENCE = re.compile(r'^\$\{[A-Za-z_][A-Za-z0-9_]*\}$')
IDENTIFIER = re.compile(r'^[a-z][a-z0-9_-]{0,63}$')
HOSTNAME = re.compile(r'^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$')


class DeploymentError(Exception):
    pass


def execute(argv, label):
    result = subprocess.run(argv, capture_output=True, text=True, timeout=240)
    if result.returncode:
        raise DeploymentError(label)


def secret(name, password=False):
    value = (Path('secrets') / name).read_text().rstrip('\r\n')
    minimum, maximum = (16, 1024) if password else (1, 65536)
    if not minimum <= len(value) <= maximum or any(not 33 <= ord(char) <= 126 for char in value):
        raise DeploymentError(f'Invalid credential file: {name}')
    return value


def integer(value, name, low, high):
    if type(value) is not int or not low <= value <= high:
        raise DeploymentError(f'{name} must be between {low} and {high}')
    return value


def settings():
    document = json.loads(Path('settings.json').read_text())
    if not isinstance(document, dict) or not isinstance(document.get('millway'), dict):
        raise DeploymentError('settings.json must contain a millway object')
    config = dict(document['millway'])
    fields = {'image', 'web_node', 'database_node', 'worker_node', 'database_host',
              'inframill_endpoint', 'google_client_ids', 'admin_emails', 'max_connections', 'port'}
    if config.keys() - fields:
        raise DeploymentError('Unknown Millway setting')
    image = config['image']
    if not isinstance(image, str) or not image or any(char.isspace() for char in image) or '$' in image:
        raise DeploymentError('Millway image must be an image reference without whitespace or interpolation')
    zone = document['zone_name']
    instance_id = document['instance_id']
    if not isinstance(zone, str) or not HOSTNAME.fullmatch(zone) or '.' not in zone:
        raise DeploymentError('Invalid Cloudflare zone name')
    if not isinstance(instance_id, str) or not re.fullmatch(r'[0-7][0-9A-HJKMNP-TV-Z]{25}', instance_id):
        raise DeploymentError('Invalid Instance ID')
    nodes = document['nodes']
    for field, default, role in [('web_node', 'web-1', 'web'), ('database_node', 'data-1', 'data'),
                                  ('worker_node', 'worker-1', 'worker')]:
        node = config.get(field, default)
        if not isinstance(node, str) or not IDENTIFIER.fullmatch(node) or node not in nodes:
            raise DeploymentError(f'{field} must identify a configured node')
        if not {role, 'docker'}.issubset(nodes[node]['roles']):
            raise DeploymentError(f'{field} requires {role} and docker roles')
        config[field] = node

    def node_host(field):
        return f'{config[field]}-{instance_id.lower()}.{zone}'

    config['database_host'] = config.get('database_host', node_host('database_node'))
    if not isinstance(config['database_host'], str) or not HOSTNAME.fullmatch(config['database_host']):
        raise DeploymentError('Invalid database_host')
    config['inframill_endpoint'] = config.get('inframill_endpoint', 'https://' + node_host('worker_node'))
    endpoint = urllib.parse.urlsplit(config['inframill_endpoint'])
    if (endpoint.scheme != 'https' or not endpoint.hostname or endpoint.username or endpoint.password
            or endpoint.path not in {'', '/'} or endpoint.query or endpoint.fragment):
        raise DeploymentError('Inframill endpoint must be an HTTPS origin')
    config['inframill_endpoint'] = config['inframill_endpoint'].rstrip('/')
    config['port'] = integer(config.get('port', 443), 'port', 443, 65535)
    postgres = document.get('postgres', {})
    databases = postgres.get('databases', [{'name': 'app1'}, {'name': 'app2'}])
    app = next((entry for entry in databases if entry['name'] == 'app1'), None)
    if app is None:
        raise DeploymentError('Millway requires the app1 PostgreSQL database')
    default_limit = min(20, (postgres.get('max_connections', 50) - 5) // len(databases))
    limit = app.get('connection_limit', default_limit)
    config['max_connections'] = integer(config.get('max_connections', min(10, limit)), 'max_connections', 1, limit)
    config['database_port'] = integer(postgres.get('port', 5432), 'database port', 1024, 65535)
    clients = config['google_client_ids']
    if (not isinstance(clients, dict) or clients.keys() - {'web', 'ios', 'android'}
            or not clients.get('web') or any(not isinstance(value, str) or not re.fullmatch(
                r'[A-Za-z0-9._-]+\.apps\.googleusercontent\.com', value) for value in clients.values())):
        raise DeploymentError('Configure valid Google client IDs, including web')
    emails = config.get('admin_emails', [])
    if not isinstance(emails, list) or any(not isinstance(value, str) or not re.fullmatch(r'[^\s@]+@[^\s@]+', value) for value in emails):
        raise DeploymentError('Invalid administrator email list')
    config['admin_emails'] = emails
    return config


def write(path, content, mode=0o600):
    path = Path(path)
    temporary = path.with_name(path.name + '.tmp')
    with temporary.open('w') as output:
        os.chmod(temporary, mode)
        output.write(content)
    temporary.replace(path)


def literal_secret(name):
    """Escape credentials that Millway would otherwise expand as environment references."""
    value = secret(name)
    return '$' + value if REFERENCE.fullmatch(value) else value


def application_config(config):
    password = urllib.parse.quote(secret('database_password', password=True), safe='')
    url = f"postgresql://app1:{password}@{config['database_host']}:{config['database_port']}/app1?sslmode=verify-full&sslrootcert=/etc/millway/ca.pem"
    token = literal_secret('root_token')
    return {
        'main': {'port': config['port'], 'databaseUrl': url,
                 'tls': {'certFile': '/etc/millway/fullchain.pem', 'keyFile': '/etc/millway/server.key'}},
        'logger': {'type': 'slog', 'settings': {'level': 'info', 'format': 'json'}},
        'handlers': [
            {'type': 'auth', 'settings': {'path': '/api/auth', 'rootToken': token,
                'issuer': 'https://accounts.google.com', 'jwksUrl': 'https://www.googleapis.com/oauth2/v3/certs',
                'audiences': list(config['google_client_ids'].values())}},
            {'type': 'connect-rpc-system', 'settings': {'path': '/api/millway.v1.SystemService/', 'rootToken': token}},
            {'type': 'connect-rpc-namespace', 'settings': {'path': '/api/millway.v1.NamespaceService/', 'rootToken': token}},
            {'type': 'static', 'settings': {'path': '/', 'directory': '/ui/dist'}},
        ],
        'access': {'adminEmails': config['admin_emails']}, 'workers': [],
        'inframill': {'hosts': [{'id': config['worker_node'], 'endpoint': config['inframill_endpoint'],
                               'rootToken': literal_secret('inframill_root_token')}],
                      'timeout': '5s', 'proxyTimeout': '2m', 'checkInterval': '10s'},
    }


def prepare(config):
    # Read and validate every credential before writing any generated files.
    application = application_config(config)
    try:
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain('tls/fullchain.pem', 'secrets/tls_private_key', password=lambda: '')
    except (OSError, ssl.SSLError):
        raise DeploymentError('Millway requires a matching, unencrypted shared TLS certificate/key') from None
    if sys.platform.startswith('linux'):
        threshold = int(Path('/proc/sys/net/ipv4/ip_unprivileged_port_start').read_text())
        if threshold > config['port']:
            raise DeploymentError('Apply the Ansible web role before starting Millway on this port')
    certificate = Path('tls/fullchain.pem').read_text()
    ca_path = ssl.get_default_verify_paths().cafile
    ca = (Path(ca_path).read_text() if ca_path else '') + '\n' + certificate
    files = {'millway.json': json.dumps(application, indent=2) + '\n', 'ca.pem': ca,
             'fullchain.pem': certificate, 'server.key': Path('secrets/tls_private_key').read_text()}
    runtime = Path('runtime')
    runtime.mkdir(mode=0o700, exist_ok=True)
    runtime.chmod(0o700)
    digest = hashlib.sha256()
    for name, content in files.items():
        write(runtime / name, content, mode=0o644 if name == 'ca.pem' else 0o600)
        digest.update(content.encode())
    write('.env', f"MILLWAY_IMAGE={config['image']}\nMILLWAY_DB_MAX_CONNS={config['max_connections']}\nMILLWAY_CONFIG_SHA={digest.hexdigest()}\n")


def main():
    os.chdir(Path(__file__).resolve().parent)
    command = sys.argv[1] if len(sys.argv) == 2 else ''
    if command != 'start':
        raise DeploymentError('Expected start')
    with Path('.start.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        config = settings()
        prepare(config)
        execute([sys.executable, 'registry_login.py'], 'Docker registry login failed')
        execute(COMPOSE + ['config', '--quiet'], 'Invalid Millway Compose configuration')
        execute(COMPOSE + ['--profile', 'tools', 'run', '--rm', '--no-deps', '--no-TTY', 'stage'], 'Millway credential staging failed')
        execute(COMPOSE + ['up', '--detach', '--no-deps', 'millway'], 'Millway startup failed')


if __name__ == '__main__':
    try:
        main()
    except DeploymentError as error:
        sys.exit(str(error))
    except (OSError, ValueError, TypeError, KeyError, subprocess.TimeoutExpired):
        sys.exit('Millway deployment failed; check settings, secret files and Docker availability')
