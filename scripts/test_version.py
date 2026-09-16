"""The shared VERSION reader follows Inframill's file-only SemVer contract."""
import os
import pathlib
import subprocess
import tempfile
import unittest

from release_config import ROOT


class VersionTests(unittest.TestCase):
    def read(self, directory):
        return subprocess.run(['sh', str(ROOT / 'scripts/read-version.sh')], cwd=directory,
                              env={**os.environ, 'VERSION': '9.9.9', 'KIT_VERSION': '9.9.9',
                                   'BUNDLE_VERSION': '9.9.9'}, capture_output=True)

    def test_semver_and_optional_newline_ignore_environment(self):
        with tempfile.TemporaryDirectory() as td:
            for version in ['0.4.0', '0.0.0-dev', '1.2.3-rc.1', '1.2.3+build.4']:
                for ending in ['', '\n']:
                    with self.subTest(version=version, ending=ending):
                        data = (version + ending).encode()
                        path = pathlib.Path(td) / 'VERSION'
                        path.write_bytes(data)
                        result = self.read(td)
                        self.assertEqual(result.returncode, 0, result.stderr)
                        self.assertEqual(result.stdout, (version + '\n').encode())
                        self.assertEqual(path.read_bytes(), data)

    def test_missing_or_malformed_file_rejects_without_output(self):
        with tempfile.TemporaryDirectory() as td:
            for data in [None, b'', b'\n', b'0.4.0\n\n', b'0.4.0\n0.5.0', b' 0.4.0',
                         b'0.4.0 ', b'0.4.0\r\n', b'v0.4.0', b'0.4', b'00.4.0',
                         b'1.2.3-01', b'1.2.3-', b'1.2.3+', b'0.4.\x000']:
                with self.subTest(data=data):
                    if data is not None:
                        (pathlib.Path(td) / 'VERSION').write_bytes(data)
                    result = self.read(td)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertEqual(result.stdout, b'')
                    self.assertIn(b'VERSION', result.stderr)
