"""Bundle integrity and publication behavior, independent of source checkouts."""
import json
from pathlib import Path
import struct
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch

from scripts.kit import bundle
from scripts.kit.artifacts import verify_binary


class BundleTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.metadata = {'schema': 1, 'version': '9.2.1', 'platforms': ['linux/amd64', 'linux/arm64'],
                         'image': {'reference': 'registry.invalid/kit:9.2.1', 'digest': 'sha256:' + 'a' * 64}}
        (self.root / 'manifest.json').write_text(json.dumps(self.metadata))
        (self.root / 'image.oci.tar').write_bytes(b'opaque OCI archive verified as a whole')
        (self.root / 'sources.tar.gz').write_bytes(b'corresponding sources')
        bundle.write_checksums(self.root)

    def test_verify_is_self_contained_and_rejects_changed_archive(self):
        with patch.object(bundle, 'ROOT', self.root / 'missing-source-checkout'):
            self.assertEqual(bundle.verify(self.root), self.metadata)
        (self.root / 'image.oci.tar').write_bytes(b'corrupt')
        with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
            bundle.verify(self.root)

    def test_missing_extra_and_symlinked_files_are_rejected(self):
        path = self.root / 'sources.tar.gz'
        for action in ('missing', 'extra', 'symlink'):
            with self.subTest(action=action):
                path.unlink(missing_ok=True)
                (self.root / 'unexpected').unlink(missing_ok=True)
                if action == 'extra':
                    path.write_bytes(b'corresponding sources')
                    (self.root / 'unexpected').write_text('extra')
                elif action == 'symlink':
                    path.symlink_to(self.root / 'image.oci.tar')
                with self.assertRaises(ValueError):
                    bundle.verify(self.root)

    def test_checksum_paths_cannot_escape_bundle_or_repeat(self):
        for entry in ['../escape', '/absolute', 'manifest.json\n' + 'b' * 64 + '  manifest.json']:
            with self.subTest(entry=entry):
                (self.root / 'SHA256SUMS').write_text('b' * 64 + '  ' + entry + '\n')
                with self.assertRaises(ValueError):
                    bundle.verify(self.root)

    def test_wrong_architecture_is_rejected(self):
        data = bytearray(64)
        data[:6] = b'\x7fELF\x02\x01'
        struct.pack_into('<H', data, 18, 183)
        verify_binary(data, 'linux/arm64')
        with self.assertRaisesRegex(ValueError, 'architecture mismatch'):
            verify_binary(data, 'linux/amd64')
        with self.assertRaises(ValueError):
            verify_binary(b'not a binary', 'linux/arm64')

    def test_loaded_digest_must_match_archive_record(self):
        with patch.object(bundle, 'run') as run, patch.object(bundle, 'loaded_digest', return_value='sha256:' + 'b' * 64):
            with self.assertRaisesRegex(ValueError, 'Loaded image digest'):
                bundle.load_image(self.root)
            self.assertEqual(run.call_count, 1)

    def test_conflicting_publication_never_loads_or_pushes(self):
        with patch.object(bundle, 'remote_digest', return_value='sha256:' + 'b' * 64), \
             patch.object(bundle, 'load_image') as load, patch.object(bundle, 'run') as run:
            with self.assertRaisesRegex(ValueError, 'already points'):
                bundle.push(self.root)
            load.assert_not_called()
            run.assert_not_called()

    def test_same_digest_is_idempotent_and_registry_errors_fail_closed(self):
        with patch.object(bundle, 'remote_digest', return_value=self.metadata['image']['digest']), \
             patch.object(bundle, 'load_image') as load:
            bundle.push(self.root)
            load.assert_not_called()
        with patch.object(bundle, 'remote_digest', side_effect=RuntimeError('unauthorized')), \
             patch.object(bundle, 'load_image') as load:
            with self.assertRaises(RuntimeError):
                bundle.push(self.root)
            load.assert_not_called()

    def test_publication_rechecks_conflicts_and_final_digest(self):
        digest = self.metadata['image']['digest']
        for responses, error, pushes in [([None, None, digest], None, 1),
                                          ([None, 'other'], 'Concurrent', 0),
                                          ([None, None, 'other'], 'Published digest', 1)]:
            with self.subTest(responses=responses), \
                 patch.object(bundle, 'remote_digest', side_effect=responses), \
                 patch.object(bundle, 'load_image'), patch.object(bundle, 'run') as run:
                if error:
                    with self.assertRaisesRegex(ValueError, error):
                        bundle.push(self.root)
                else:
                    bundle.push(self.root)
                self.assertEqual(run.call_count, pushes)

    def test_failed_remote_inspection_is_not_a_missing_tag(self):
        for detail in ('unauthorized', 'connection refused', 'TLS certificate error'):
            result = subprocess.CompletedProcess([], 1, '', detail)
            with patch.object(bundle.subprocess, 'run', return_value=result), self.assertRaises(RuntimeError):
                bundle.remote_digest('registry.invalid/kit:1')

    def test_input_changes_are_detected_before_packaging(self):
        with patch.object(bundle, 'ROOT', self.root), patch.object(bundle, 'source_files', return_value={'main.go': 'original'}):
            bundle.capture()
            bundle.check_inputs()
        with patch.object(bundle, 'ROOT', self.root), patch.object(bundle, 'source_files', return_value={'main.go': 'edited'}):
            with self.assertRaisesRegex(ValueError, 'Sources changed'):
                bundle.check_inputs()

    def test_pkg_sources_are_fingerprinted_and_archived(self):
        subprocess.run(['git', 'init', '--quiet', str(self.root)], check=True)
        source = self.root / 'pkg/runmill/runmill.go'
        source.parent.mkdir(parents=True)
        source.write_text('package runmill\n')
        engine = self.root / '.build/engine/source'
        engine.mkdir(parents=True)
        (engine / 'private-engine.go').write_text('full engine implementation must be excluded')
        with patch.object(bundle, 'ROOT', self.root), \
             patch.object(bundle, 'runtime_settings', return_value={'SOURCE_DATE_EPOCH': '1'}):
            bundle.capture()
            archive = self.root / 'sources.tar.gz'
            bundle.source_archive(archive)
            with tarfile.open(archive) as snapshot:
                self.assertEqual(snapshot.extractfile('kit/pkg/runmill/runmill.go').read(), source.read_bytes())
                self.assertEqual(snapshot.getnames(), ['kit/pkg/runmill/runmill.go'])
            source.write_text('package runmill\n// changed\n')
            with self.assertRaisesRegex(ValueError, 'Sources changed'):
                bundle.check_inputs()

    def test_legacy_provider_binaries_with_upstream_runmill_are_rejected(self):
        info = '\tpath\tgithub.com/inframill/kit/cmd\n\tmod\tgithub.com/inframill/kit\t(devel)\n'
        bundle.verify_provider_build_info(info)
        for version in ('v0.0.0-pinned', '(devel)'):
            with self.subTest(version=version), self.assertRaisesRegex(ValueError, 'still imports upstream'):
                bundle.verify_provider_build_info(info + '\tdep\tgithub.com/inframill/inframill\t' + version + '\n')

    def test_nested_checksum_filename_cannot_hide_an_extra_file(self):
        nested = self.root / 'notices/SHA256SUMS'
        nested.parent.mkdir()
        nested.write_text('unrecorded content')
        with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
            bundle.verify(self.root)
        bundle.write_checksums(self.root)
        self.assertEqual(bundle.verify(self.root), self.metadata)
