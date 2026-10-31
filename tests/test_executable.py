import copy
import json
from pathlib import Path
import shutil
import struct
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from scripts import executable as build


class ProviderBuildTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='provider build ')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.config, self.selected = build.releases()

    def output(self, target='linux/amd64'):
        folder = self.root / target
        folder.mkdir(parents=True)
        binary = bytearray(64)
        binary[:6] = b'\x7fELF\x02\x01'
        struct.pack_into('<H', binary, 18, {'amd64': 62, 'arm64': 183}[target.split('/')[1]])
        (folder / 'exec').write_bytes(binary)
        (folder / 'sources').mkdir()
        (folder / 'sources/input.go').write_text('source')
        record = {'schema': 2, 'version': '0.1.24-dev', 'providers': self.selected, 'platform': target,
                  'inframill': self.config['inframill'], 'go_version': self.config['go_version'],
                  'source_files': {'input.go': build.digest(b'source')}, 'files': {'exec': build.digest(binary), 'sources/input.go': build.digest(b'source')}}
        (folder / 'build.json').write_text(json.dumps(record))
        return folder, record

    def test_selected_versions_have_metadata_and_share_one_entrypoint(self):
        self.assertEqual(list((build.ROOT / 'cmd').rglob('main.go')), [build.ROOT / 'cmd/main.go'])
        for release in self.selected:
            folder = build.ROOT / 'pkg/providers' / release['name'] / ('v' + release['version'].replace('.', '_'))
            self.assertTrue((folder / 'provider_test.go').is_file())
            self.assertEqual(set(release['platforms']), set(build.LINUX))

    def test_rejects_wrong_directory_identity_and_duplicate_selection(self):
        shutil.copy(build.ROOT / 'releases.json', self.root)
        for release in self.selected:
            folder = self.root / 'pkg/providers' / release['name'] / ('v' + release['version'].replace('.', '_'))
            folder.mkdir(parents=True)
            (folder / 'release.json').write_text(json.dumps(release))
        release = self.selected[0]
        path = self.root / 'pkg/providers' / release['name'] / ('v' + release['version'].replace('.', '_')) / 'release.json'
        changed = {**release, 'version': '9.9.9'}
        path.write_text(json.dumps(changed))
        with self.assertRaisesRegex(ValueError, 'identity'):
            build.releases(self.root)
        path.write_text(json.dumps(release))
        config = copy.deepcopy(self.config)
        config['providers'].append(config['providers'][0])
        (self.root / 'releases.json').write_text(json.dumps(config))
        with self.assertRaisesRegex(ValueError, 'duplicate'):
            build.releases(self.root)

    def test_verifies_both_architectures_and_rejects_swapped_binaries(self):
        for target in build.LINUX:
            folder, record = self.output(target)
            build.verify_output(folder, record['version'], target, self.config, self.selected)
            other = 'linux/arm64' if target.endswith('amd64') else 'linux/amd64'
            with self.assertRaisesRegex(ValueError, 'architecture'):
                build.verify_binary((folder / 'exec').read_bytes(), other)

    def test_corrupt_output_and_changed_sources_are_not_reused(self):
        folder, record = self.output()
        with self.assertRaisesRegex(ValueError, 'sources changed'):
            build.verify_output(folder, record['version'], record['platform'], self.config, self.selected, {'new.go': 'b' * 64})
        (folder / 'exec').write_bytes(b'corrupt')
        with self.assertRaisesRegex(ValueError, 'checksum'):
            build.verify_output(folder, record['version'], record['platform'])

    def test_changed_dependency_pin_is_not_reused(self):
        folder, record = self.output()
        config = copy.deepcopy(self.config)
        config['inframill']['revision'] = 'a' * 40
        with self.assertRaisesRegex(ValueError, 'pins changed'):
            build.verify_output(folder, record['version'], record['platform'], config)

    def test_one_snapshot_contains_all_implementations_and_the_dispatcher(self):
        sources = build.source_files(build.ROOT)
        self.assertIn('cmd/main.go', sources)
        for release in self.selected:
            directory = 'v' + release['version'].replace('.', '_')
            self.assertIn('pkg/providers/' + release['name'] + '/' + directory + '/provider.go', sources)
        self.assertIn('LICENSE', sources)
        self.assertFalse(any(p.startswith('providers/') or p.startswith('../') for p in sources))

    def test_no_implicit_version_aliases(self):
        for version in ['latest', '0.2', 'v0.2.0', '../0.2.0', '0.2.0-01']:
            self.assertFalse(build.valid_version(version))


class RefreshBuildTests(unittest.TestCase):
    def test_refresh_preserves_previous_output_on_failure_and_backs_up_on_success(self):
        from unittest.mock import patch
        import subprocess
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            selected = build.releases()[1]
            version = '0.1.24-dev'
            config = build.releases()[0]
            dist = root / 'dist'
            output = dist / version / 'linux/amd64'
            output.mkdir(parents=True)
            (output / 'previous').write_text('keep me')
            (root / 'input.go').write_text('source')
            sources = {'input.go': build.digest(b'source')}
            with patch.object(build, 'source_files', return_value=sources), \
                 patch.object(build, 'run', side_effect=subprocess.CalledProcessError(1, 'go build')):
                with self.assertRaises(subprocess.CalledProcessError):
                    build.build(root, dist, version, 'linux/amd64', config, selected, root, {}, refresh=True)
            self.assertEqual((output / 'previous').read_text(), 'keep me')
            self.assertEqual(list(output.parent.glob('.previous-*')), [])

            def compile_binary(args, **kwargs):
                binary = bytearray(64)
                binary[:6] = b'\x7fELF\x02\x01'
                struct.pack_into('<H', binary, 18, 62)
                Path(args[args.index('-o') + 1]).write_bytes(binary)
            with patch.object(build, 'source_files', return_value=sources), \
                 patch.object(build, 'run', side_effect=compile_binary) as compile, \
                 patch.object(build.subprocess, 'check_output', return_value=''), \
                 patch.object(build, 'verify_source'):
                build.build(root, dist, version, 'linux/amd64', config, selected, root, {}, refresh=True)
                self.assertEqual(compile.call_count, 1)
                build.build(root, dist, version, 'linux/amd64', config, selected, root, {}, refresh=True)
                self.assertEqual(compile.call_count, 1)
            backups = list(output.parent.glob('.previous-*'))
            self.assertEqual(len(backups), 1)
            self.assertEqual((backups[0] / 'previous').read_text(), 'keep me')
            build.verify_output(output, version, 'linux/amd64', config, selected, sources)
