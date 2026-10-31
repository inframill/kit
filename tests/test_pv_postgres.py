import contextlib
import importlib.util
import json
import os
import pathlib
import secrets
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from scripts.kit.project import ROOT

SOURCE = ROOT / 'blueprints/pv-cloud/latest/templates/dispatch/postgres'
SPEC = importlib.util.spec_from_file_location('pv_postgres', SOURCE / 'manage.py')
postgres = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(postgres)


def prepare_payload(root):
    payload = pathlib.Path(root) / 'postgres'
    shutil.copytree(SOURCE, payload, ignore=shutil.ignore_patterns('__pycache__'))
    (payload / 'registry.json.gotmpl').unlink()
    (payload / 'registry.json').write_text('{}\n')
    (payload / 'settings.json.gotmpl').unlink()
    (payload / 'settings.json').write_text(json.dumps({'memory_mb': 1024, 'max_connections': 20}))
    (payload / 'tls/fullchain.pem.gotmpl').unlink()
    (payload / 'secrets').mkdir(mode=0o700)
    for name in ['app1', 'app2']:
        password = payload / f'secrets/{name}_password'
        password.write_text(secrets.token_hex(32) + '\n')
        password.chmod(0o600)
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '2',
                    '-subj', '/CN=postgres.test', '-addext', 'subjectAltName=DNS:postgres.test,DNS:*.example.test,IP:127.0.0.1',
                    '-keyout', str(payload / 'secrets/tls_private_key'),
                    '-out', str(payload / 'tls/fullchain.pem')], check=True, capture_output=True)
    return payload


@contextlib.contextmanager
def working_directory(path):
    previous = pathlib.Path.cwd()
    os.chdir(path)
    try:
        yield
    finally:
        os.chdir(previous)


def run_start(payload, env):
    """Exercise startup with the real registry helper and mocked database checks.

    The Docker acceptance check exercises reconciliation and verification against
    PostgreSQL; these unit fixtures replace Docker with a command recorder.
    """
    with working_directory(payload), patch.dict(os.environ, env), \
            patch.object(postgres, '__file__', str(payload / 'manage.py')), \
            patch.object(sys, 'argv', ['manage.py', 'start']), \
            patch.object(postgres, 'reconcile'), patch.object(postgres, 'verify'):
        try:
            postgres.main()
        except postgres.SetupError as error:
            return subprocess.CompletedProcess(['start'], 1, '', str(error))
    return subprocess.CompletedProcess(['start'], 0, '', '')


class PostgresPayloadTests(unittest.TestCase):
    def test_start_preserves_password_and_propagates_compose_failure(self):
        with tempfile.TemporaryDirectory(prefix='kit-postgres-') as td:
            root = pathlib.Path(td)
            payload = prepare_payload(root)
            commands = root / 'commands'
            docker = root / 'docker'
            docker.write_text('#!/bin/sh\nset -eu\n'
                              'test -s secrets/postgres_password\n'
                              'printf "%s\\n" "$*" >> "$COMMANDS"\n'
                              'if [ -f fail ] && [ "$6" = up ]; then exit 17; fi\n')
            docker.chmod(0o700)
            env = {**os.environ, 'PATH': str(root) + os.pathsep + os.environ['PATH'],
                   'COMMANDS': str(commands)}

            def make(target):
                if target == 'start':
                    return run_start(payload, env)
                return subprocess.run(['make', target], cwd=payload, env=env,
                                      capture_output=True, text=True)

            first = make('start')
            self.assertEqual(first.returncode, 0, first.stdout + first.stderr)
            password = payload / 'secrets/postgres_password'
            original = password.read_bytes()
            self.assertRegex(original.decode(), r'^[a-f0-9]{64}\n$')
            self.assertEqual(stat.S_IMODE(password.stat().st_mode), 0o600)
            self.assertEqual(stat.S_IMODE(password.parent.stat().st_mode), 0o700)
            self.assertNotIn(original.decode().strip(), first.stdout + first.stderr)
            self.assertEqual(make('start').returncode, 0)
            self.assertEqual(password.read_bytes(), original)
            self.assertEqual(commands.read_text().splitlines(), [
                'compose --project-name pv-cloud-postgres --file compose.yaml config --quiet',
                'compose --project-name pv-cloud-postgres --file compose.yaml up --detach --wait --wait-timeout 120',
            ] * 2)
            self.assertEqual(make('stop').returncode, 0)
            self.assertEqual(password.read_bytes(), original)
            self.assertEqual(commands.read_text().splitlines()[-1],
                             'compose --project-name pv-cloud-postgres --file compose.yaml down')
            (payload / 'fail').touch()
            failed = make('start')
            self.assertNotEqual(failed.returncode, 0)
            self.assertIn('PostgreSQL Compose startup failed', failed.stderr)
            self.assertEqual(password.read_bytes(), original)

    def test_admin_generation_and_required_application_credentials_survive_retries(self):
        with tempfile.TemporaryDirectory() as td, working_directory(td):
            original = postgres.password('postgres')
            self.assertEqual(postgres.password('postgres'), original)
            for name in ['app1', 'app2']:
                with self.assertRaisesRegex(postgres.SetupError, 'Missing credential file'):
                    postgres.password(name)
                self.assertFalse(pathlib.Path(f'secrets/{name}_password').exists())
            pathlib.Path('secrets/app2_password').write_text('aB:cD\\eF\'gH$iJkLmN\n')
            self.assertEqual(postgres.password('app2', required=True), "aB:cD\\eF'gH$iJkLmN")
            pathlib.Path('secrets/app2_password').write_text('\n')
            with self.assertRaisesRegex(postgres.SetupError, '16–1024'):
                postgres.password('app2')
            self.assertEqual(pathlib.Path('secrets/app2_password').read_text(), '\n')

    def test_start_rejects_missing_or_invalid_application_passwords_before_docker(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            payload = prepare_payload(root)
            marker = root / 'docker-called'
            docker = root / 'docker'
            docker.write_text('#!/bin/sh\ntouch "$DOCKER_CALLED"\n')
            docker.chmod(0o700)
            env = {**os.environ, 'PATH': str(root) + os.pathsep + os.environ['PATH'],
                   'DOCKER_CALLED': str(marker)}
            for name in ['app1', 'app2']:
                password = payload / f'secrets/{name}_password'
                original = password.read_bytes()
                for value in [None, '', ' \n', 'too-short']:
                    if value is None:
                        password.unlink()
                    else:
                        password.write_text(value)
                    result = subprocess.run(['make', 'start'], cwd=payload, env=env,
                                            capture_output=True, text=True)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn(name, result.stderr)
                    self.assertFalse(marker.exists(), 'Invalid credentials must fail before Docker runs')
                    if value is None:
                        self.assertFalse(password.exists(), 'Start must not generate an application password')
                password.write_bytes(original)

    def test_settings_reject_invalid_names_and_excessive_resource_budgets(self):
        with tempfile.TemporaryDirectory() as td, working_directory(td):
            cases = [
                {'databases': [{'name': name}]} for name in
                ['postgres', 'template0', 'public', 'pg_admin', 'a;DROP DATABASE postgres', 'x' * 49]
            ] + [
                {'databases': [{'name': 'app1'}, {'name': 'app1'}]},
                {'memory_mb': 512, 'max_connections': 100},
                {'source_ips': ['0.0.0.0/0; DROP']},
                {'source_ips': ['192.0.2.1/24']},
                {'max_connections': True},
                {'databases': [{'name': 'app1', 'connection_limit': 50}]},
                {'databases': [{'name': 'app1'}]},
                {'databases': [{'name': 'app1', 'password_secret': ''}]},
            ]
            for config in cases:
                with self.subTest(config=config):
                    pathlib.Path('settings.json').write_text(json.dumps(config))
                    with self.assertRaises(postgres.SetupError):
                        postgres.settings()

    def test_tls_key_validation_and_runtime_configuration(self):
        with tempfile.TemporaryDirectory() as td:
            payload = prepare_payload(td)
            with working_directory(payload):
                config = postgres.settings()
                postgres.prepare(config)
                configuration = pathlib.Path('runtime/postgresql.conf').read_text()
                self.assertIn("shared_buffers = '256MB'", configuration)
                self.assertIn('max_connections = 20', configuration)
                hba = pathlib.Path('runtime/pg_hba.conf').read_text()
                self.assertIn('hostssl "app1" "app1" 0.0.0.0/0 scram-sha-256', hba)
                self.assertNotIn('hostssl "app1" "app2"', hba)
                previous = pathlib.Path('.env').read_bytes()
                postgres.prepare(config)
                self.assertEqual(pathlib.Path('.env').read_bytes(), previous)
                postgres.prepare({**config, 'port': 55432})
                self.assertNotEqual(pathlib.Path('.env').read_bytes(), previous)
                pathlib.Path('secrets/tls_private_key').write_text('invalid private key')
                with self.assertRaisesRegex(postgres.SetupError, 'TLS certificate/key'):
                    postgres.prepare(config)

    def catalog(self, passwords, role_names=(), owners=None, elevated=False):
        return json.dumps({'roles': [
            {'name': name, 'password': postgres.scram(passwords[name]),
             'elevated': elevated and name != 'postgres', 'member': False}
            for name in ['postgres', *role_names]
        ], 'databases': owners or {}})

    def configuration(self):
        return {'databases': [dict(name=name, connection_limit=10, statement_timeout_ms=30000,
                                  lock_timeout_ms=5000, idle_transaction_timeout_ms=60000)
                              for name in ['app1', 'app2']]}

    def test_reconciliation_creates_databases_without_exposing_passwords(self):
        passwords = {name: name + '-strong-password' for name in ['postgres', 'app1', 'app2']}
        catalog = self.catalog(passwords)
        with patch.object(postgres, 'execute') as execute, patch.object(postgres, 'admin', return_value=catalog) as admin:
            postgres.reconcile(self.configuration(), passwords)
            statements = '\n'.join(call.args[0] for call in admin.call_args_list)
        self.assertIn('CREATE DATABASE "app1" OWNER "app1"', statements)
        self.assertIn('CREATE DATABASE "app2" OWNER "app2"', statements)
        self.assertIn('NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS', statements)
        self.assertNotIn('DROP ', statements)
        self.assertTrue(execute.called)
        for password in passwords.values():
            self.assertNotIn(password, statements)

    def test_start_preserves_unchanged_credentials_and_applies_changed_passwords(self):
        passwords = {name: name + '-strong-password' for name in ['postgres', 'app1', 'app2']}
        owners = {'app1': 'app1', 'app2': 'app2'}
        catalog = self.catalog(passwords, ['app1', 'app2'], owners)
        with patch.object(postgres, 'execute'), patch.object(postgres, 'admin', return_value=catalog) as admin:
            postgres.reconcile(self.configuration(), passwords)
            self.assertFalse(any('PASSWORD ' in call.args[0] for call in admin.call_args_list))
            changed = {**passwords, 'app2': 'changed-strong-password'}
            admin.reset_mock()
            postgres.reconcile(self.configuration(), changed)
            rotations = [call.args[0] for call in admin.call_args_list if 'PASSWORD ' in call.args[0]]
            self.assertEqual(len(rotations), 1)
            self.assertIn('ALTER ROLE "app2" PASSWORD', rotations[0])
            self.assertNotIn(changed['app2'], rotations[0])

    def test_existing_ownership_or_privileged_roles_require_explicit_migration(self):
        passwords = {name: name + '-strong-password' for name in ['postgres', 'app1', 'app2']}
        for catalog in [self.catalog(passwords, ['app1'], {'app1': 'postgres'}),
                        self.catalog(passwords, ['app1'], elevated=True)]:
            with patch.object(postgres, 'admin', return_value=catalog) as admin:
                with self.assertRaises(postgres.SetupError):
                    postgres.reconcile(self.configuration(), passwords)
                self.assertEqual(admin.call_count, 1)

    def test_database_error_details_do_not_leak_into_output(self):
        result = subprocess.CompletedProcess([], 1, stdout='', stderr='PASSWORD secret-credential')
        with patch.object(postgres.subprocess, 'run', return_value=result):
            with self.assertRaisesRegex(postgres.SetupError, '^Database operation failed$'):
                postgres.execute(['psql'], 'Database operation failed', 'secret-credential')
