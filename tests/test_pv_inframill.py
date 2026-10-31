import json
import io
import runpy
import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from scripts.kit.project import ROOT, project_version


def prepare_payload(root):
    """Materialize the payload with synthetic local TLS credentials for testing."""
    payload = pathlib.Path(root) / 'inframill'
    shutil.copytree(ROOT / 'blueprints/pv-cloud/latest/templates/dispatch/inframill', payload)
    compose = payload / 'compose.yaml.gotmpl'
    (payload / 'compose.yaml').write_text(compose.read_text().replace(
        '{{ toJSON .inframill.image }}', json.dumps('ghcr.io/inframill/kit:' + project_version())))
    compose.unlink()
    (payload / 'registry.json.gotmpl').unlink()
    (payload / 'registry.json').write_text('{}\n')
    (payload / 'tls/fullchain.pem.gotmpl').unlink()
    (payload / 'secrets').mkdir(mode=0o700)
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                    '-subj', '/CN=localhost', '-addext', 'subjectAltName=DNS:localhost,IP:127.0.0.1',
                    '-keyout', str(payload / 'secrets/tls_private_key'),
                    '-out', str(payload / 'tls/fullchain.pem')], check=True, capture_output=True)
    token = payload / 'secrets/root_token'
    token.write_text('synthetic-inframill-root-token')
    token.chmod(0o600)
    return payload


class InframillPayloadTests(unittest.TestCase):
    def test_start_requires_credentials_and_preserves_them(self):
        with tempfile.TemporaryDirectory(prefix='kit-inframill-') as td:
            root = pathlib.Path(td)
            payload = prepare_payload(root)
            commands = root / 'commands'
            docker = root / 'docker'
            docker.write_text('#!/bin/sh\nset -eu\nprintf "%s\\n" "$*" >> "$COMMANDS"\n')
            docker.chmod(0o700)
            env = {**os.environ, 'PATH': str(root) + os.pathsep + os.environ['PATH'],
                   'COMMANDS': str(commands)}

            def make(target):
                return subprocess.run(['make', target], cwd=payload, env=env,
                                      capture_output=True, text=True)

            key = payload / 'secrets/tls_private_key'
            token = payload / 'secrets/root_token'
            original_key, original_token = key.read_bytes(), token.read_bytes()
            key.unlink()
            missing = make('start')
            self.assertNotEqual(missing.returncode, 0)
            self.assertFalse(commands.exists())
            key.write_bytes(b'invalid-private-key')
            self.assertNotEqual(make('start').returncode, 0)
            self.assertFalse(commands.exists())
            key.write_bytes(original_key)
            token.write_text('')
            self.assertNotEqual(make('start').returncode, 0)
            token.write_bytes(original_token)
            installed = make('start')
            self.assertEqual(installed.returncode, 0, installed.stdout + installed.stderr)
            self.assertNotIn(original_token.decode(), installed.stdout + installed.stderr)
            self.assertEqual(commands.read_text().splitlines(), [
                'compose --project-name pv-cloud-inframill --file compose.yaml config --quiet',
                'compose --project-name pv-cloud-inframill --file compose.yaml up --detach --force-recreate --wait --wait-timeout 120',
            ])
            self.assertEqual(make('verify').returncode, 0)
            self.assertEqual(commands.read_text().splitlines()[-1],
                             'compose --project-name pv-cloud-inframill --file compose.yaml exec --no-TTY inframill python3 /etc/inframill/healthcheck.py')
            self.assertEqual(make('stop').returncode, 0)
            self.assertEqual(key.read_bytes(), original_key)
            self.assertEqual(token.read_bytes(), original_token)
            self.assertTrue((payload / 'blueprints').is_dir())
            self.assertEqual((payload / 'ssh').stat().st_mode & 0o777, 0o700)
            self.assertEqual(commands.read_text().splitlines()[-1],
                             'compose --project-name pv-cloud-inframill --file compose.yaml down')

    def test_https_healthcheck_uses_current_root_token_header(self):
        for source in (ROOT / 'blueprints/pv-cloud').glob('*/templates/dispatch/inframill/healthcheck.py'):
            with self.subTest(source=str(source)), patch('ssl.create_default_context'), \
                    patch('pathlib.Path.read_text', return_value='synthetic-token\n'), \
                    patch('urllib.request.urlopen') as request:
                request.return_value.__enter__.return_value = io.StringIO('{"version":"0.1.25-dev"}')
                runpy.run_path(str(source))
                headers = dict(request.call_args.args[0].header_items())
                self.assertEqual(headers['X-root-token'], 'synthetic-token')
                self.assertNotIn('X-root-password', headers)
