import hashlib
import json
import pathlib
import os
import tempfile
import unittest
from unittest.mock import patch

import release_config


class ReleaseConfigTests(unittest.TestCase):
    def test_real_lock_has_both_runtime_platforms_and_one_production_version(self):
        lock = release_config.bundle_lock('0.4.0')
        self.assertIn('linux/arm64', lock['opentofu']['assets'])
        self.assertIn('linux/amd64', lock['opentofu']['assets'])
        self.assertEqual({d['version'] for d in lock['drivers']}, {'0.1.0'})

    def test_toolchain_changes_require_a_new_pin(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            (root/'releases/bundles').mkdir(parents=True)
            data = b'{}'
            (root/'tools.json').write_bytes(data+b' ')
            lock = {'toolchain': {'path': 'tools.json', 'sha256': hashlib.sha256(data).hexdigest()}}
            (root/'releases/bundles/0.4.0.lock.yaml').write_text(json.dumps(lock))
            with patch.object(release_config, 'ROOT', root):
                with self.assertRaisesRegex(ValueError, 'Toolchain lock checksum'):
                    release_config.bundle_lock('0.4.0')

    def test_fixture_blueprint_versions_select_real_driver_requirements(self):
        for case in release_config.fixture_cases():
            requirements = release_config.requirements(case['name'], case['version'])
            self.assertEqual({d['version'] for d in requirements}, {'0.1.0'})
            self.assertEqual(set(release_config.driver_ids(case['name'],case['version'])), {'opentofu','ansible'})

    def test_version_file_controls_lock_and_output_despite_overrides(self):
        with patch.dict(os.environ, {'VERSION': '9.9.9', 'KIT_VERSION': '9.9.9',
                                    'BUNDLE_VERSION': '9.9.9', 'BLUEPRINT_VERSION': '9.9.9'}):
            self.assertEqual(release_config.bundle_lock()['version'], '0.4.0')
            self.assertEqual(release_config.bundle_output().name, '0.4.0')
