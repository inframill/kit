import contextlib
import copy
import importlib.util
import io
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
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
        'millway': {'image': 'ghcr.io/inframill/millway:0.1.4-dev',
                    'google_client_ids': {'web': 'test-web.apps.googleusercontent.com'}},
    }
    (payload / 'settings.json').write_text(json.dumps(document))
    return payload


def run_start(payload, env):
    with working_directory(payload), patch.dict(os.environ, env), \
            patch.object(millway, '__file__', str(payload / 'manage.py')), \
            patch.object(sys, 'argv', ['manage.py', 'start']), \
            patch.object(millway, 'verify'), patch.object(millway, 'request', return_value=b'{}'):
        try:
            millway.main()
        except millway.DeploymentError as error:
            return subprocess.CompletedProcess(['start'], 1, '', str(error))
    return subprocess.CompletedProcess(['start'], 0, '', '')


class MillwayPayloadTests(unittest.TestCase):
    def test_default_dns_credentials_and_non_root_runtime(self):
        with tempfile.TemporaryDirectory() as td:
            payload = prepare_payload(td)
            with working_directory(payload):
                config = millway.settings()
                self.assertEqual(config['database_host'], 'data-1-01arz3ndektsv4rrffq69g5fav.example.test')
                self.assertEqual(config['inframill_endpoint'], 'https://worker-1-01arz3ndektsv4rrffq69g5fav.example.test')
                self.assertEqual(config['hostname'], 'web-1-01arz3ndektsv4rrffq69g5fav.example.test')
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

    def test_verification_checks_exact_migration_set_and_fresh_versions(self):
        with tempfile.TemporaryDirectory() as td:
            payload = prepare_payload(td)
            with working_directory(payload):
                config = millway.settings()
                contract = json.loads(Path('migrations.json').read_text())

                def request(config, endpoint, path, *args):
                    if path == '/':
                        return b'<html>Millway</html>'
                    version = contract['version'] if path.startswith('/api/') else contract['inframill_version']
                    return json.dumps({'version': version}).encode()

                for migrations, valid in [(contract['migrations'], True), ({}, False),
                                           ({**contract['migrations'], 'unexpected.sql': 'changed'}, False),
                                           ({**contract['migrations'], next(iter(contract['migrations'])): 'changed'}, False)]:
                    with self.subTest(valid=valid), patch.object(millway, 'request', side_effect=request), \
                            patch.object(millway, 'database', side_effect=['app1|app1|1', json.dumps(migrations)]) as database, \
                            contextlib.redirect_stdout(io.StringIO()) as output:
                        if valid:
                            millway.verify(config)
                        else:
                            with self.assertRaisesRegex(millway.DeploymentError, 'migrations'):
                                millway.verify(config)
                        self.assertIn('BEGIN READ ONLY', database.call_args_list[1].args[1])
                        self.assertIn('inframill: worker-1', output.getvalue())

    def test_non_object_version_responses_fail_cleanly(self):
        with tempfile.TemporaryDirectory() as td:
            payload = prepare_payload(td)
            with working_directory(payload):
                contract = json.loads(Path('migrations.json').read_text())
                with patch.object(millway, 'database', side_effect=['app1|app1|1', json.dumps(contract['migrations'])]), \
                        patch.object(millway, 'request', return_value=b'[]'), contextlib.redirect_stdout(io.StringIO()):
                    with self.assertRaisesRegex(millway.DeploymentError, 'inframill, millway'):
                        millway.verify(millway.settings())

    def test_failed_database_still_pings_inframill_and_hides_credentials(self):
        with tempfile.TemporaryDirectory() as td:
            payload = prepare_payload(td)
            with working_directory(payload), patch.object(millway, 'database', side_effect=millway.DeploymentError('secret database URL')), \
                    patch.object(millway, 'request', side_effect=OSError('secret token')), \
                    contextlib.redirect_stdout(io.StringIO()) as output:
                with self.assertRaisesRegex(millway.DeploymentError, 'database, inframill, millway'):
                    millway.verify(millway.settings())
                self.assertNotIn('secret database URL', output.getvalue())
                self.assertNotIn('secret token', output.getvalue())
                self.assertIn('migrations: skipped', output.getvalue())
