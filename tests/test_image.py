"""Release admission, OCI integrity and Docker publication contracts."""
import json
import pathlib
import struct
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))
import build


class ReleaseTests(unittest.TestCase):
    def test_independent_patch_and_prerelease_versions(self):
        for kit, engine in [('0.6.7', '0.6.0-dev'), ('0.6.1-rc.1', '0.6.2'), ('2.9.3', '2.9.7-rc.1')]:
            lock = build.bundle_lock()
            lock['version'], lock['inframill']['version'] = kit, engine
            self.assertEqual(build.validate_release(lock, kit), lock)

    def test_invalid_release_selections(self):
        original = build.bundle_lock()
        mutations = [
            {'kit_version': original['version']},
            {'drivers': []},
            {'drivers': [original['drivers'][0]] * 2},
            {'drivers': [{'name': '../escape', 'version': '0.2.0'}]},
            {'drivers': [{'name': 'opentofu', 'version': 'latest'}]},
            {'drivers': [{**original['drivers'][0], 'implementation': 'old'}]},
            {'platforms': ['linux/amd64']},
            {'inframill': {**original['inframill'], 'version': '1.3.0'}},
            {'inframill': {**original['inframill'], 'version': '0.3'}},
            {'inframill': {**original['inframill'], 'revision': 'main'}},
        ]
        for mutation in mutations:
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                build.validate_release({**original, **mutation}, original['version'])

    def test_actions_reject_version_mismatch_before_effects(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            (root / 'VERSION').write_text('0.7.1\n')
            (root / 'releases').mkdir()
            (root / 'releases/0.7.1.lock.json').write_text(json.dumps({
                'version': '0.7.1', 'inframill': {'version': '0.6.2'}}))
            with patch.object(build, 'ROOT', root), patch.object(build, 'run') as run:
                for action in ('build', 'push', 'publish', 'validate', 'copy-engine'):
                    with self.subTest(action=action), self.assertRaisesRegex(ValueError, 'major/minor'):
                        build.main([action])
                run.assert_not_called()
            self.assertFalse((root / 'bin').exists())
            self.assertFalse((root / 'dist').exists())

    def test_locks_are_single_json_documents(self):
        lock = build.bundle_lock()
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            (root / 'releases').mkdir()
            (root / f"releases/{lock['version']}.lock.json").write_text(json.dumps(lock) + '{}')
            with patch.object(build, 'ROOT', root), self.assertRaises(json.JSONDecodeError):
                build.bundle_lock(lock['version'])

    def test_native_macho_architecture_and_checksum(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            for arch, machine in [('amd64', 0x1000007), ('arm64', 0x100000c)]:
                data = b'\xcf\xfa\xed\xfe' + struct.pack('<I', machine) + bytes(24)
                (root / 'engine').write_bytes(data)
                entry = {'path': 'engine', 'sha256': build.digest(data)}
                self.assertEqual(build.read_binary(root, entry, arch, 'darwin'), data)
                with self.assertRaisesRegex(ValueError, 'architecture mismatch'):
                    build.read_binary(root, entry, 'amd64' if arch == 'arm64' else 'arm64', 'darwin')
                (root / 'engine').write_bytes(data + b'tampered')
                with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
                    build.read_binary(root, entry, arch, 'darwin')


class ImageTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.output = pathlib.Path(self.tmp.name)
        self.layout = self.output / 'oci'
        (self.layout / 'blobs/sha256').mkdir(parents=True)
        self.lock = build.bundle_lock()
        self.layer = self.blob(b'layer content', 'application/vnd.oci.image.layer.v1.tar')
        config = self.blob(b'{}', 'application/vnd.oci.image.config.v1+json')
        manifest = self.blob(json.dumps({'config': config, 'layers': [self.layer]}).encode(),
                             'application/vnd.oci.image.manifest.v1+json')
        self.descriptor = self.blob(json.dumps({'manifests': [manifest]}).encode(),
                                   'application/vnd.oci.image.index.v1+json')
        (self.layout / 'index.json').write_text(json.dumps({'manifests': [self.descriptor]}))
        self.record = {'version': self.lock['version'], 'descriptor': self.descriptor,
                       'lock_sha256': build.digest((build.ROOT / f"releases/{self.lock['version']}.lock.json").read_bytes())}
        (self.output / 'build.json').write_text(json.dumps(self.record))

    def blob(self, data, media_type):
        checksum = build.digest(data)
        (self.layout / 'blobs/sha256' / checksum).write_bytes(data)
        return {'digest': 'sha256:' + checksum, 'size': len(data), 'mediaType': media_type}

    def test_verifies_entire_graph_and_lock(self):
        self.assertEqual(build.verify_image(self.output, self.lock), self.descriptor)
        self.record['lock_sha256'] = '0' * 64
        (self.output / 'build.json').write_text(json.dumps(self.record))
        with self.assertRaisesRegex(ValueError, 'lock differs'):
            build.verify_image(self.output, self.lock)

    def test_rejects_corrupt_missing_and_linked_transitive_blobs(self):
        path = self.layout / 'blobs/sha256' / self.layer['digest'].split(':')[1]
        for mutation in ('same size corruption', 'truncated', 'missing', 'symlink'):
            path.unlink(missing_ok=True)
            if mutation == 'same size corruption':
                path.write_bytes(b'x' * self.layer['size'])
            elif mutation == 'truncated':
                path.write_bytes(b'x')
            elif mutation == 'symlink':
                target = self.output / 'external'
                target.write_bytes(b'layer content')
                path.symlink_to(target)
            with self.subTest(mutation=mutation), self.assertRaises((ValueError, OSError)):
                build.verify_image(self.output, self.lock)

    def test_rejects_changed_root_descriptor(self):
        descriptor = {**self.descriptor, 'size': self.descriptor['size'] + 1}
        (self.layout / 'index.json').write_text(json.dumps({'manifests': [descriptor]}))
        with self.assertRaisesRegex(ValueError, 'root differs'):
            build.verify_image(self.output, self.lock)

    def test_publication_conflicts_idempotency_and_cleanup(self):
        checksum = self.descriptor['digest']
        scenarios = [([checksum], None, False), (['sha256:' + '0' * 64], 'already points', False),
                     ([None, 'sha256:' + '0' * 64], 'Concurrent', False),
                     ([None, None, checksum], None, True),
                     ([None, None, 'sha256:' + '0' * 64], 'Published digest mismatch', True)]
        for responses, error, pushed in scenarios:
            with self.subTest(responses=responses), \
                 patch.object(build, 'bundle_output', return_value=self.output), \
                 patch.object(build, 'remote_digest', side_effect=responses), \
                 patch.object(build, 'load_image') as load, patch.object(build, 'run') as run:
                if error:
                    with self.assertRaisesRegex(ValueError, error):
                        build.push_image()
                else:
                    build.push_image()
                self.assertEqual(load.called, responses[0] is None)
                if pushed:
                    run.assert_called_once_with(['docker', 'push', build.image_reference(self.lock['version'])])
                else:
                    run.assert_not_called()
                self.assertFalse((self.output / '.push-lock').exists())

    def test_remote_errors_fail_closed_and_release_guard(self):
        with patch.object(build, 'bundle_output', return_value=self.output), \
             patch.object(build, 'remote_digest', side_effect=RuntimeError('unauthorized')), \
             patch.object(build, 'load_image') as load:
            with self.assertRaisesRegex(RuntimeError, 'unauthorized'):
                build.push_image()
            load.assert_not_called()
            self.assertFalse((self.output / '.push-lock').exists())
        for message in ('unauthorized', 'connection refused', 'TLS certificate error'):
            result = subprocess.CompletedProcess([], 1, '', message)
            with patch.object(build.subprocess, 'run', return_value=result), self.assertRaises(RuntimeError):
                build.remote_digest('registry.invalid/test:1')
        result = subprocess.CompletedProcess([], 1, '', 'ERROR: registry.invalid/test:1: not found')
        with patch.object(build.subprocess, 'run', return_value=result):
            self.assertIsNone(build.remote_digest('registry.invalid/test:1'))


if __name__ == '__main__':
    unittest.main()
