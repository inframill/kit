import copy
import json
import pathlib
import os
import tempfile
import unittest
from unittest.mock import patch

import sys
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import build as release_config


class ReleaseConfigTests(unittest.TestCase):
    def test_real_lock_has_both_runtime_platforms_and_one_production_version(self):
        lock = release_config.bundle_lock()
        self.assertEqual(set(lock), {'version', 'inframill', 'providers', 'platforms'})
        self.assertEqual(set(lock['platforms']), {'linux/arm64', 'linux/amd64'})
        self.assertEqual({d['version'] for d in lock['providers']}, {'0.4.0'})
        self.assertEqual(set(lock['inframill']['binaries']),
                         {'linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64'})

    def test_rejects_mismatched_release_identity(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            (root/'releases').mkdir()
            (root/'releases/0.5.0.lock.json').write_text('{"version":"0.5.1"}')
            with patch.object(release_config, 'ROOT', root):
                with self.assertRaisesRegex(ValueError, 'identity mismatch'):
                    release_config.bundle_lock('0.5.0')

    def test_release_is_self_contained(self):
        version = release_config.project_version()
        original = release_config.bundle_lock(version)
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            (root/'releases').mkdir()
            (root/f'releases/{version}.lock.json').write_text(json.dumps(original))
            with patch.object(release_config, 'ROOT', root):
                self.assertEqual(release_config.bundle_lock(version), original)
    def test_rejects_obsolete_runtime_fields(self):
        original = release_config.bundle_lock()
        for field in ['opentofu', 'ansible_core', 'python', 'python_dependencies',
                      'base_image', 'base_platform_digests', 'debian_snapshot', 'source_date_epoch']:
            lock = copy.deepcopy(original)
            lock[field] = 'obsolete'
            with self.subTest(field=field), self.assertRaisesRegex(ValueError, 'Unknown image release fields'):
                release_config.validate_release(lock, lock['version'])

    def test_version_file_controls_lock_and_output_despite_overrides(self):
        with patch.dict(os.environ, {'VERSION': '9.9.9', 'KIT_VERSION': '9.9.9',
                                    'BUNDLE_VERSION': '9.9.9'}):
            self.assertEqual(release_config.bundle_lock()['version'], release_config.project_version())
            self.assertEqual(release_config.bundle_output().name, release_config.project_version())
