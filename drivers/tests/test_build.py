import copy
import json
from pathlib import Path
import shutil
import struct
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import build


class DriverBuildTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='driver build ')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.config, self.selected = build.releases()

    def output(self, target='linux/amd64'):
        folder = self.root / target
        folder.mkdir(parents=True)
        binary = bytearray(64)
        binary[:6] = b'\x7fELF\x02\x01'
        struct.pack_into('<H', binary, 18, {'amd64': 62, 'arm64': 183}[target.split('/')[1]])
        (folder / 'driver').write_bytes(binary)
        record = {'schema': 1, 'release': self.selected[0], 'platform': target,
                  'inframill': self.config['inframill'], 'go_version': self.config['go_version'],
                  'source_files': {'input.go': 'a' * 64}, 'files': {'driver': build.sha256(binary)}}
        (folder / 'build.json').write_text(json.dumps(record))
        return folder, record

    def test_each_selected_version_has_its_own_entrypoint_and_metadata(self):
        for release in self.selected:
            folder = build.ROOT / 'roots' / release['name'] / release['version']
            self.assertTrue((folder / 'cmd/main.go').is_file())
            self.assertTrue((folder / 'driver_test.go').is_file())
            self.assertEqual(set(release['platforms']), set(build.LINUX))

    def test_rejects_wrong_directory_identity_and_duplicate_selection(self):
        shutil.copy(build.ROOT / 'releases.json', self.root)
        for release in self.selected:
            folder = self.root / 'roots' / release['name'] / release['version']
            folder.mkdir(parents=True)
            (folder / 'release.json').write_text(json.dumps(release))
        release = self.selected[0]
        path = self.root / 'roots' / release['name'] / release['version'] / 'release.json'
        changed = {**release, 'version': '9.9.9'}
        path.write_text(json.dumps(changed))
        with self.assertRaisesRegex(ValueError, 'identity'):
            build.releases(self.root)
        path.write_text(json.dumps(release))
        config = copy.deepcopy(self.config)
        config['drivers'].append(config['drivers'][0])
        (self.root / 'releases.json').write_text(json.dumps(config))
        with self.assertRaisesRegex(ValueError, 'duplicate'):
            build.releases(self.root)

    def test_verifies_both_architectures_and_rejects_swapped_binaries(self):
        for target in build.LINUX:
            folder, record = self.output(target)
            build.verify_output(folder, record['release'], target, self.config)
            other = 'linux/arm64' if target.endswith('amd64') else 'linux/amd64'
            with self.assertRaisesRegex(ValueError, 'architecture'):
                build.verify_binary((folder / 'driver').read_bytes(), other)

    def test_corrupt_output_and_changed_sources_are_not_reused(self):
        folder, record = self.output()
        with self.assertRaisesRegex(ValueError, 'sources changed'):
            build.verify_output(folder, record['release'], record['platform'], self.config, {'new.go': 'b' * 64})
        (folder / 'driver').write_bytes(b'corrupt')
        with self.assertRaisesRegex(ValueError, 'checksum'):
            build.verify_output(folder, record['release'], record['platform'])

    def test_changed_dependency_pin_is_not_reused(self):
        folder, record = self.output()
        config = copy.deepcopy(self.config)
        config['inframill']['revision'] = 'a' * 40
        with self.assertRaisesRegex(ValueError, 'pins changed'):
            build.verify_output(folder, record['release'], record['platform'], config)

    def test_source_snapshot_is_owned_by_the_subproject(self):
        for release in self.selected:
            sources = build.source_files(build.ROOT, release)
            self.assertIn('roots/' + release['name'] + '/' + release['version'] + '/cmd/main.go', sources)
            self.assertIn('LICENSE', sources)
            self.assertFalse(any('testcatalog' in p or p.startswith('../') for p in sources))

    def test_no_implicit_version_aliases_or_unmatched_selection(self):
        for version in ['latest', '0.2', 'v0.2.0', '../0.2.0', '0.2.0-01']:
            self.assertFalse(build.valid_version(version))
        with self.assertRaisesRegex(ValueError, 'No matching'):
            build.releases(name='missing')
