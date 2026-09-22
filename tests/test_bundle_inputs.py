import hashlib
import json
import pathlib
import struct
import tempfile
import unittest

import sys
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from build import read_binary, read_provider


class BinaryFixture:
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dist = pathlib.Path(self.tmp.name)

    def binary(self, machine=62, magic=b'\x7fELF\x02\x01'):
        data = bytearray(64)
        data[:6] = magic
        struct.pack_into('<H', data, 18, machine)
        path = self.dist / 'release/inframill'
        path.parent.mkdir(exist_ok=True)
        path.write_bytes(data)
        return {'path': 'release/inframill', 'sha256': hashlib.sha256(data).hexdigest()}, bytes(data)


class BinaryInputTests(BinaryFixture, unittest.TestCase):
    def test_accepts_exact_bytes_for_both_platforms(self):
        for arch, machine in [('amd64', 62), ('arm64', 183)]:
            entry, data = self.binary(machine)
            self.assertEqual(read_binary(self.dist, entry, arch), data)

    def test_rejects_changed_bytes(self):
        entry, _ = self.binary()
        (self.dist / entry['path']).write_bytes(b'changed')
        with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
            read_binary(self.dist, entry, 'amd64')

    def test_rejects_wrong_architecture(self):
        entry, _ = self.binary()
        with self.assertRaisesRegex(ValueError, 'architecture mismatch'):
            read_binary(self.dist, entry, 'arm64')

    def test_rejects_non_linux_binary(self):
        entry, _ = self.binary(magic=b'\xcf\xfa\xed\xfe\x00\x00')
        with self.assertRaisesRegex(ValueError, 'ELF binary'):
            read_binary(self.dist, entry, 'amd64')

    def test_missing_binary_is_an_error(self):
        with self.assertRaisesRegex(ValueError, 'Missing prebuilt'):
            read_binary(self.dist, {'path': 'missing', 'sha256': '0' * 64}, 'amd64')

    def test_paths_stay_relative_to_dist(self):
        for path in ['../inframill', '/tmp/inframill']:
            with self.assertRaisesRegex(ValueError, 'relative'):
                read_binary(self.dist, {'path': path, 'sha256': '0' * 64}, 'amd64')


class ProviderInputTests(BinaryFixture, unittest.TestCase):
    def provider(self, arch='amd64'):
        entry, data = self.binary(62 if arch == 'amd64' else 183)
        target = 'linux/' + arch
        folder = self.dist / 'opentofu/0.3.0' / target
        (folder / 'sources').mkdir(parents=True)
        (folder / 'provider').write_bytes(data)
        (folder / 'sources/main.go').write_text('package main\n')
        lock = {'inframill': {'version': '0.3.0-dev', 'revision': 'a' * 40},
                'platforms': ['linux/amd64', 'linux/arm64'],
                'opentofu': {'version': '1.11.5'}, 'ansible_core': '2.20.1'}
        selection = {'name': 'opentofu', 'version': '0.3.0'}
        record = {'schema': 1, 'release': {**selection, 'tool_version': '1.11.5', 'protocol': 1,
                                         'platforms': lock['platforms']},
                  'platform': target, 'inframill': lock['inframill'],
                  'files': {'provider': entry['sha256'], 'sources/main.go': hashlib.sha256(b'package main\n').hexdigest()},
                  'source_files': {'main.go': hashlib.sha256(b'package main\n').hexdigest()}}
        (folder / 'build.json').write_text(json.dumps(record))
        return folder, record, selection, target, lock

    def test_bundle_accepts_only_prebuilt_outputs_for_each_architecture(self):
        for arch in ['amd64', 'arm64']:
            folder, record, selection, target, lock = self.provider(arch)
            self.assertEqual(read_provider(self.dist, selection, target, lock)[1], record)

    def test_missing_platform_requires_an_explicit_provider_build(self):
        _, _, selection, _, lock = self.provider()
        with self.assertRaisesRegex(ValueError, 'Missing prebuilt provider'):
            read_provider(self.dist, selection, 'linux/arm64', lock)

    def test_bundle_rejects_provider_identity_tool_and_engine_mismatches(self):
        folder, original, selection, target, lock = self.provider()
        for field in ['identity', 'tool', 'engine', 'platform']:
            record = json.loads(json.dumps(original))
            if field == 'identity':
                record['release']['version'] = '9.9.9'
            elif field == 'tool':
                record['release']['tool_version'] = '9.9.9'
            elif field == 'engine':
                record['inframill']['revision'] = 'b' * 40
            else:
                record['platform'] = 'linux/arm64'
            (folder / 'build.json').write_text(json.dumps(record))
            with self.subTest(field=field), self.assertRaises(ValueError):
                read_provider(self.dist, selection, target, lock)

    def test_bundle_rejects_tampered_source_snapshots(self):
        folder, _, selection, target, lock = self.provider()
        (folder / 'sources/main.go').write_text('modified')
        with self.assertRaisesRegex(ValueError, 'checksum'):
            read_provider(self.dist, selection, target, lock)

    def test_bundle_rejects_unrecorded_files(self):
        folder, _, selection, target, lock = self.provider()
        (folder / 'unexpected').write_text('extra')
        with self.assertRaisesRegex(ValueError, 'unrecorded'):
            read_provider(self.dist, selection, target, lock)


if __name__ == '__main__':
    unittest.main()
