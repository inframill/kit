import os
import pathlib
import shutil
import stat
import subprocess
import tempfile
import unittest

from build import ROOT


def prepare_payload(root):
    payload = pathlib.Path(root) / 'postgres'
    shutil.copytree(ROOT / 'blueprints/pv-cloud/latest/templates/dispatch/postgres', payload)
    (payload / 'registry.json.gotmpl').unlink()
    (payload / 'registry.json').write_text('{}\n')
    return payload


class PostgresPayloadTests(unittest.TestCase):
    def test_install_preserves_password_and_propagates_compose_failure(self):
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
                return subprocess.run(['make', target], cwd=payload, env=env,
                                      capture_output=True, text=True)

            first = make('install')
            self.assertEqual(first.returncode, 0, first.stdout + first.stderr)
            password = payload / 'secrets/postgres_password'
            original = password.read_bytes()
            self.assertRegex(original.decode(), r'^[a-f0-9]{64}\n$')
            self.assertEqual(stat.S_IMODE(password.stat().st_mode), 0o600)
            self.assertEqual(stat.S_IMODE(password.parent.stat().st_mode), 0o700)
            self.assertNotIn(original.decode().strip(), first.stdout + first.stderr)
            self.assertEqual(make('install').returncode, 0)
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
            failed = make('install')
            self.assertNotEqual(failed.returncode, 0)
            self.assertIn('Error 17', failed.stderr)
            self.assertEqual(password.read_bytes(), original)
