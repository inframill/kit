"""Release preparation validates inputs before changing the three release records."""
import json
import os
from pathlib import Path
import struct
import sys
import tempfile
import unittest
from unittest.mock import patch
from types import SimpleNamespace

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import build


class PrepareReleaseTests(unittest.TestCase):
    def enterContext(self, context):
        value = context.__enter__()
        self.addCleanup(context.__exit__, None, None, None)
        return value

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.source = self.root / 'upstream'
        self.source.mkdir()
        (self.source / 'VERSION').write_text('0.22.0-dev\n')
        (self.root / 'VERSION').write_text('0.21.0-dev\n')
        (self.root / 'releases').mkdir()
        (self.root / 'providers').mkdir()
        self.config_path = self.root / 'providers/releases.json'
        self.config = json.loads((build.ROOT / 'providers/releases.json').read_text())
        self.config_path.write_text(json.dumps(self.config))
        self.dist = self.source / 'dist'
        self.paths = {}
        for system in ['linux', 'darwin']:
            for arch in ['amd64', 'arm64']:
                binary = bytearray(64)
                if system == 'linux':
                    binary[:6] = b'\x7fELF\x02\x01'
                    struct.pack_into('<H', binary, 18, {'amd64': 62, 'arm64': 183}[arch])
                else:
                    binary[:4] = b'\xcf\xfa\xed\xfe'
                    struct.pack_into('<I', binary, 4, {'amd64': 0x1000007, 'arm64': 0x100000c}[arch])
                path = self.dist / f'0.22.0-dev/inframill_0.22.0-dev_{system}_{arch}'
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(binary)
                self.paths[system + '/' + arch] = path
        self.enterContext(patch.object(build, 'ROOT', self.root))
        self.enterContext(patch.object(build, 'native_platform', return_value='darwin/arm64'))
        self.enterContext(patch.dict(os.environ, {'INFRAMILL_DIST_DIR': str(self.dist)}))
        self.dirty = ''
        self.binary_version = '0.22.0-dev\n'
        self.run = self.enterContext(patch.object(build, 'run', side_effect=self.command))

    def command(self, args, **kwargs):
        if args[:2] == ['git', 'rev-parse']:
            return SimpleNamespace(stdout='a' * 40 + '\n')
        if args[:2] == ['git', 'status']:
            return SimpleNamespace(stdout=self.dirty)
        self.assertEqual(args, [self.paths['darwin/arm64'], '--version'])
        return SimpleNamespace(stdout=self.binary_version)

    def metadata(self):
        paths = [self.root / 'VERSION', self.config_path, *sorted((self.root / 'releases').glob('*'))]
        return {str(p.relative_to(self.root)): p.read_bytes() for p in paths}

    def test_generates_only_release_records_and_repeats_without_rewrites(self):
        build.prepare_release(self.source)
        lock_path = self.root / 'releases/0.22.0-dev.lock.json'
        lock = json.loads(lock_path.read_text())
        self.assertEqual(lock['providers'], self.config['providers'])
        self.assertEqual(lock['inframill']['revision'], 'a' * 40)
        for target, entry in lock['inframill']['binaries'].items():
            self.assertEqual(entry['sha256'], build.digest(self.paths[target].read_bytes()))
        config = json.loads(self.config_path.read_text())
        self.assertEqual(config, {**self.config, 'inframill': {'version': '0.22.0-dev', 'revision': 'a' * 40}})
        before = self.metadata()
        with patch.object(Path, 'write_bytes', side_effect=AssertionError('unchanged file rewritten')):
            build.prepare_release(self.source)
        self.assertEqual(self.metadata(), before)

    def test_independent_kit_patch_uses_upstream_engine_version(self):
        build.prepare_release(self.source, '0.22.1-dev')
        lock = json.loads((self.root / 'releases/0.22.1-dev.lock.json').read_text())
        self.assertEqual(lock['inframill']['version'], '0.22.0-dev')
        self.assertEqual((self.root / 'VERSION').read_text(), '0.22.1-dev\n')

    def test_dirty_checkout_and_wrong_native_version_leave_metadata_unchanged(self):
        before = self.metadata()
        self.dirty = ' M cmd/main.go\n'
        with self.assertRaisesRegex(ValueError, 'Commit Inframill'):
            build.prepare_release(self.source)
        self.assertEqual(self.metadata(), before)
        self.dirty = ''
        self.binary_version = '0.21.0-dev\n'
        with self.assertRaisesRegex(ValueError, 'version differs'):
            build.prepare_release(self.source)
        self.assertEqual(self.metadata(), before)

    def test_missing_or_wrong_architecture_leaves_metadata_unchanged(self):
        before = self.metadata()
        path = self.paths['linux/arm64']
        path.write_bytes(self.paths['linux/amd64'].read_bytes())
        with self.assertRaisesRegex(ValueError, 'architecture mismatch'):
            build.prepare_release(self.source)
        self.assertEqual(self.metadata(), before)
        path.unlink()
        with self.assertRaisesRegex(ValueError, 'Missing'):
            build.prepare_release(self.source)
        self.assertEqual(self.metadata(), before)

    def test_existing_lock_conflict_and_wrong_release_line_reject(self):
        build.prepare_release(self.source)
        before = self.metadata()
        binary = self.paths['linux/amd64']
        binary.write_bytes(binary.read_bytes() + b'changed')
        with self.assertRaisesRegex(ValueError, 'already exists'):
            build.prepare_release(self.source)
        self.assertEqual(self.metadata(), before)
        with self.assertRaisesRegex(ValueError, 'major/minor'):
            build.prepare_release(self.source, '0.23.0-dev')
        self.assertEqual(self.metadata(), before)

    def test_write_failure_restores_previous_selections_and_removes_new_lock(self):
        before = self.metadata()
        write = Path.write_bytes
        failed = False
        def fail_once(path, data):
            nonlocal failed
            if path == self.root / 'VERSION' and not failed:
                failed = True
                raise OSError('simulated write failure')
            return write(path, data)
        with patch.object(Path, 'write_bytes', fail_once), self.assertRaisesRegex(OSError, 'simulated'):
            build.prepare_release(self.source)
        self.assertEqual(self.metadata(), before)
