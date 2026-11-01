"""Engine artifact verification does not stage or archive engine implementation source."""
import hashlib
import json
from pathlib import Path
import shutil
import struct
import tempfile
import unittest
from unittest.mock import patch

from scripts.kit import engine


class EngineArtifactTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        (self.root / 'bin').mkdir()
        (self.root / 'pkg/runmill').mkdir(parents=True)
        (self.root / 'pkg/runmill/runmill.go').write_text('package runmill\n')
        (self.root / '.build/engine/notices').mkdir(parents=True)
        self.pin = {'version': '1.2.3', 'revision': 'a' * 40,
                    'runmill_sha256': engine.runmill_digest(self.root / 'pkg/runmill'), 'binaries': {}}
        for platform in engine.PLATFORMS:
            system, arch = platform.split('/')
            data = bytearray(64)
            if system == 'linux':
                data[:6] = b'\x7fELF\x02\x01'
                struct.pack_into('<H', data, 18, 62 if arch == 'amd64' else 183)
            else:
                data[:4] = b'\xcf\xfa\xed\xfe'
                struct.pack_into('<I', data, 4, 0x1000007 if arch == 'amd64' else 0x100000c)
            path = self.root / 'bin' / engine.binary_name(platform)
            path.write_bytes(data)
            self.pin['binaries'][platform] = hashlib.sha256(data).hexdigest()
        shutil.copy2(self.root / 'bin/inframill-linux-amd64', self.root / 'bin/inframill')
        (self.root / '.build/engine/engine.json').write_text(json.dumps(self.pin))
        self.patch(engine, 'ROOT', self.root)
        self.patch(engine, 'native_platform', return_value='linux/amd64')
        self.patch(engine, 'runtime_settings', return_value={'INFRAMILL': self.pin})
        self.patch(engine.subprocess, 'check_output', side_effect=self.build_info)

    def patch(self, target, name, *args, **kwargs):
        patched = patch.object(target, name, *args, **kwargs)
        value = patched.start()
        self.addCleanup(patched.stop)
        return value

    def build_info(self, args, **kwargs):
        path = Path(args[-1])
        platform = path.name.removeprefix('inframill-').rsplit('-', 1)
        return ('\tpath\t' + engine.MODULE + '/cmd\n\tbuild\tvcs.revision=' + self.pin['revision'] +
                '\n\tbuild\tvcs.modified=false\n\tbuild\tGOOS=' + platform[0] + '\n\tbuild\tGOARCH=' + platform[1] + '\n')

    def test_prepared_artifacts_verify_without_engine_sources_or_sibling(self):
        engine.verify_prepared()
        self.assertFalse((self.root / '.build/engine/source').exists())
        self.assertFalse((self.root / '.build/engine/source.tar').exists())

    def test_stale_metadata_changed_binary_and_changed_runmill_are_rejected(self):
        for change in ('metadata', 'binary', 'runmill'):
            with self.subTest(change=change):
                if change == 'metadata':
                    path = self.root / '.build/engine/engine.json'
                    value = json.dumps({**self.pin, 'revision': 'b' * 40}).encode()
                elif change == 'binary':
                    path = self.root / 'bin/inframill-linux-arm64'
                    value = path.read_bytes() + b'changed'
                else:
                    path = self.root / 'pkg/runmill/runmill.go'
                    value = b'package runmill\n// changed\n'
                original = path.read_bytes()
                path.write_bytes(value)
                with self.assertRaises(ValueError):
                    engine.verify_prepared()
                path.write_bytes(original)

    def test_binary_identity_requires_expected_revision_platform_and_clean_build(self):
        path = self.root / 'bin/inframill-linux-amd64'
        valid = self.build_info(['go', 'version', '-m', path])
        for field, replacement in [(self.pin['revision'], 'b' * 40), ('modified=false', 'modified=true'),
                                   ('GOOS=linux', 'GOOS=darwin'), (engine.MODULE + '/cmd', 'other/cmd')]:
            with self.subTest(field=field), patch.object(engine.subprocess, 'check_output', return_value=valid.replace(field, replacement)), \
                 self.assertRaisesRegex(ValueError, 'build identity differs'):
                engine.verify_engine_binary(path, 'linux/amd64', self.pin)
        with self.assertRaisesRegex(ValueError, 'architecture mismatch'):
            engine.verify_engine_binary(path, 'linux/arm64', self.pin)

    def test_runmill_import_replacement_keeps_same_source_hash(self):
        package = self.root / 'pkg/runmill'
        example = package / 'example_test.go'
        example.write_text('import "' + engine.MODULE + '/pkg/runmill"\n')
        before = engine.runmill_digest(package)
        example.write_text('import "' + engine.LOCAL_RUNMILL + '"\n')
        self.assertEqual(engine.runmill_digest(package), before)
