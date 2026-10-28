import copy
import importlib.util
import json
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import tempfile
import unittest
import urllib.parse

from build import ROOT
from test_pv_postgres import prepare_payload as prepare_postgres, working_directory

SOURCE = ROOT / 'blueprints/pv-cloud/latest/templates/dispatch/millway'
SPEC = importlib.util.spec_from_file_location('pv_millway', SOURCE / 'manage.py')
millway = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(millway)


def prepare_payload(root, database_payload=None):
    root = Path(root)
    database_payload = database_payload or prepare_postgres(root)
    payload = root / 'millway'
    shutil.copytree(SOURCE, payload, ignore=shutil.ignore_patterns('__pycache__'))
    (payload / 'registry.json.gotmpl').unlink()
    (payload / 'registry.json').write_text('{}\n')
    (payload / 'settings.json.gotmpl').unlink()
    (payload / 'tls/fullchain.pem.gotmpl').unlink()
    (payload / 'secrets').mkdir(mode=0o700)
    for source, destination in [('secrets/tls_private_key', 'secrets/tls_private_key'),
                                ('secrets/app1_password', 'secrets/database_password'),
                                ('tls/fullchain.pem', 'tls/fullchain.pem')]:
        shutil.copy2(database_payload / source, payload / destination)
    for name in ['root_token', 'inframill_root_token']:
        target = payload / 'secrets' / name
        target.write_text(secrets.token_hex(32) + '\n')
        target.chmod(0o600)
    document = {
        'instance_id': '01ARZ3NDEKTSV4RRFFQ69G5FAV', 'zone_name': 'example.test',
        'nodes': {name: {'roles': ['common', 'docker', role]} for name, role in
                  [('data-1', 'data'), ('worker-1', 'worker'), ('web-1', 'web')]},
        'postgres': {},
        'millway': {'image': 'ghcr.io/inframill/millway:0.1.5-dev',
                    'google_client_ids': {'web': 'test-web.apps.googleusercontent.com'}},
    }
    (payload / 'settings.json').write_text(json.dumps(document))
    return payload


def run_start(payload, env):
    return subprocess.run([sys.executable, 'manage.py', 'start'], cwd=payload, env=env,
                          capture_output=True, text=True)


class MillwayPayloadTests(unittest.TestCase):
    def test_default_dns_credentials_and_non_root_runtime(self):
        with tempfile.TemporaryDirectory() as td:
            payload = prepare_payload(td)
            with working_directory(payload):
                config = millway.settings()
                self.assertEqual(config['database_host'], 'data-1-01arz3ndektsv4rrffq69g5fav.example.test')
                self.assertEqual(config['inframill_endpoint'], 'https://worker-1-01arz3ndektsv4rrffq69g5fav.example.test')
                millway.prepare(config)
                runtime = json.loads(Path('runtime/millway.json').read_text())
                connection = urllib.parse.urlsplit(runtime['main']['databaseUrl'])
                self.assertEqual(urllib.parse.unquote(connection.password), millway.secret('database_password'))
                self.assertEqual(connection.username, 'app1')
                self.assertEqual(urllib.parse.parse_qs(connection.query)['sslmode'], ['verify-full'])
                self.assertEqual(runtime['inframill']['hosts'][0]['rootToken'], millway.secret('inframill_root_token'))
                self.assertEqual(Path('runtime/millway.json').stat().st_mode & 0o777, 0o600)
                self.assertEqual(Path('runtime/server.key').stat().st_mode & 0o777, 0o600)
                before = Path('.env').read_bytes()
                millway.prepare(config)
                self.assertEqual(Path('.env').read_bytes(), before)
                for value in [millway.secret('database_password'), millway.secret('root_token'), millway.secret('inframill_root_token')]:
                    self.assertNotIn(value, before.decode())

    def test_invalid_roles_pool_and_urls_fail_before_effects(self):
        with tempfile.TemporaryDirectory() as td:
            payload = prepare_payload(td)
            with working_directory(payload):
                original = json.loads(Path('settings.json').read_text())
                cases = []
                for field, value in [('database_node', 'missing'), ('worker_node', 'data-1'),
                                      ('inframill_endpoint', 'http://worker.invalid'), ('hostname', 'custom.example.test'),
                                      ('max_connections', 21), ('image', 'image\nsecret')]:
                    changed = copy.deepcopy(original)
                    changed['millway'][field] = value
                    cases.append(changed)
                changed = copy.deepcopy(original)
                changed['nodes']['web-1']['roles'] = ['web']
                cases.append(changed)
                for document in cases:
                    Path('settings.json').write_text(json.dumps(document))
                    with self.assertRaises(millway.DeploymentError):
                        millway.settings()
