import json
import os
import pathlib
import subprocess
import tempfile
import unittest

from test_pv_inframill import prepare_payload as prepare_inframill
from test_pv_postgres import prepare_payload as prepare_postgres, run_start
from test_pv_millway import prepare_payload as prepare_millway, run_start as run_millway_start


class RegistryLoginTests(unittest.TestCase):
    def fixture(self, root, prepare):
        payload = prepare(root)
        commands = root / 'commands.jsonl'
        docker = root / 'docker'
        docker.write_text('''#!/usr/bin/env python3
import json, os, pathlib, sys
args = sys.argv[1:]
record = {'args': args}
if args[0] == 'login':
    record['stdin'] = sys.stdin.read()
with open(os.environ['COMMANDS'], 'a') as log:
    log.write(json.dumps(record) + '\\n')
if args[0] == 'login' and pathlib.Path('fail-login').exists():
    sys.exit(23)
''')
        docker.chmod(0o700)
        env = {**os.environ, 'PATH': str(root) + os.pathsep + os.environ['PATH'],
               'COMMANDS': str(commands)}
        (payload / 'registry.json').write_text(json.dumps({
            'host': 'registry.example.invalid:5000', 'username': 'robot$pv-cloud',
        }))
        (payload / 'secrets').mkdir(mode=0o700, exist_ok=True)
        token = payload / 'secrets/registry_token'
        token.write_text('synthetic-registry-token\n')
        token.chmod(0o600)
        return payload, commands, env

    def make(self, payload, env, target='start'):
        if payload.name == 'postgres':
            return run_start(payload, env)
        if payload.name == 'millway':
            return run_millway_start(payload, env)
        # Also prove login completes before Compose when Make is parallel.
        return subprocess.run(['make', '-j4', target], cwd=payload, env=env,
                              capture_output=True, text=True)

    def records(self, commands):
        return [json.loads(line) for line in commands.read_text().splitlines()] if commands.exists() else []

    def test_login_uses_stdin_before_compose_and_refreshes_credentials(self):
        for prepare in (prepare_inframill, prepare_postgres, prepare_millway):
            with self.subTest(payload=prepare.__module__), tempfile.TemporaryDirectory(prefix='kit-registry-') as td:
                payload, commands, env = self.fixture(pathlib.Path(td), prepare)
                for token in ('synthetic-registry-token\n', 'rotated-registry-token\n'):
                    (payload / 'secrets/registry_token').write_text(token)
                    installed = self.make(payload, env)
                    self.assertEqual(installed.returncode, 0, installed.stdout + installed.stderr)
                    self.assertNotIn(token.strip(), installed.stdout + installed.stderr)
                    records = self.records(commands)[-5 if prepare is prepare_millway else -3:]
                    self.assertEqual(records[0], {
                        'args': ['login', 'registry.example.invalid:5000', '--username',
                                 'robot$pv-cloud', '--password-stdin'],
                        'stdin': token,
                    })
                    self.assertEqual(records[1]['args'][0], 'compose')
                    self.assertEqual(records[1]['args'][5:], ['config', '--quiet'])
                    startup = records[-2] if prepare is prepare_millway else records[-1]
                    self.assertEqual(startup['args'][5], 'up')
                    if prepare is prepare_millway:
                        self.assertEqual(records[-1]['args'][5:], ['exec', '--no-TTY', 'millway',
                            '/millway', 'verify', '-config', '/etc/millway/millway.json'])

    def test_login_failure_stops_compose(self):
        for prepare in (prepare_inframill, prepare_postgres, prepare_millway):
            with self.subTest(payload=prepare.__module__), tempfile.TemporaryDirectory(prefix='kit-registry-') as td:
                payload, commands, env = self.fixture(pathlib.Path(td), prepare)
                (payload / 'fail-login').touch()
                installed = self.make(payload, env)
                self.assertNotEqual(installed.returncode, 0)
                self.assertIn('Docker registry login failed', installed.stderr)
                self.assertNotIn('synthetic-registry-token', installed.stdout + installed.stderr)
                self.assertEqual([record['args'][0] for record in self.records(commands)], ['login'])

    def test_missing_or_empty_token_stops_before_docker(self):
        for prepare in (prepare_inframill, prepare_postgres, prepare_millway):
            with self.subTest(payload=prepare.__module__), tempfile.TemporaryDirectory(prefix='kit-registry-') as td:
                payload, commands, env = self.fixture(pathlib.Path(td), prepare)
                token = payload / 'secrets/registry_token'
                token.unlink()
                for value in (None, '', ' \n'):
                    if value is not None:
                        token.write_text(value)
                    installed = self.make(payload, env)
                    self.assertNotEqual(installed.returncode, 0)
                    self.assertIn('registry', installed.stderr)
                    self.assertEqual(self.records(commands), [])

    def test_omitting_registry_skips_login_even_with_stale_token(self):
        for prepare in (prepare_inframill, prepare_postgres, prepare_millway):
            with self.subTest(payload=prepare.__module__), tempfile.TemporaryDirectory(prefix='kit-registry-') as td:
                payload, commands, env = self.fixture(pathlib.Path(td), prepare)
                (payload / 'registry.json').write_text('{}\n')
                installed = self.make(payload, env)
                self.assertEqual(installed.returncode, 0, installed.stdout + installed.stderr)
                self.assertEqual([record['args'][0] for record in self.records(commands)], ['compose'] * (4 if prepare is prepare_millway else 2))
