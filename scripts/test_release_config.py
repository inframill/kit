import copy
import io
import json
import pathlib
import os
import runpy
import sys
import tempfile
import unittest
from unittest.mock import patch

import release_config


class ReleaseConfigTests(unittest.TestCase):
    def test_real_lock_has_both_runtime_platforms_and_one_production_version(self):
        lock = release_config.bundle_lock()
        self.assertIn('linux/arm64', lock['opentofu']['assets'])
        self.assertIn('linux/amd64', lock['opentofu']['assets'])
        self.assertEqual({d['version'] for d in lock['drivers']}, {'0.2.0'})
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
        rendered = release_config.python_requirements(original)
        for package in original['python_dependencies']:
            self.assertIn(package['name'] + '==' + package['version'], rendered)
            for digest in package['hashes']:
                self.assertIn('--hash=sha256:' + digest, rendered)

    def test_python_dependencies_reject_missing_hashes_and_conflicting_versions(self):
        original = release_config.bundle_lock()
        for mutation in ['hashes', 'source', 'version', 'duplicate']:
            lock = copy.deepcopy(original)
            package = next(p for p in lock['python_dependencies'] if p['name'] == 'ansible-core')
            if mutation == 'hashes':
                package['hashes'] = []
            elif mutation == 'source':
                package['source']['sha256'] = '0' * 64
            elif mutation == 'version':
                package['version'] = '9.9.9'
            else:
                lock['python_dependencies'].append(package)
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                release_config.python_requirements(lock)

    def test_python_pin_update_accepts_a_new_release_target(self):
        lock = release_config.bundle_lock()
        lock['ansible_core'] = '2.21.0'
        script = release_config.ROOT / 'scripts/lock-python.py'
        metadata = {'urls': [{'packagetype': 'sdist', 'url': 'https://example.invalid/source.tar.gz',
                              'digests': {'sha256': 'a' * 64}}]}
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            (root/'releases').mkdir()
            path = root/f"releases/{lock['version']}.lock.json"
            path.write_text(json.dumps(lock))
            report = root/'report.json'
            report.write_text(json.dumps({'install': [{'metadata': {'name': 'ansible-core', 'version': '2.21.0'}}]}))
            with patch.object(release_config, 'ROOT', root), \
                 patch.object(release_config, 'project_version', return_value=lock['version']), \
                 patch.object(sys, 'argv', [str(script), str(report)]), \
                 patch('urllib.request.urlopen', return_value=io.BytesIO(json.dumps(metadata).encode())):
                runpy.run_path(str(script), run_name='__main__')
            updated = json.loads(path.read_text())
            self.assertEqual(updated['python_dependencies'][0]['version'], '2.21.0')
            self.assertNotIn('blueprints', updated)
            self.assertEqual(list(root.glob('runtime/*')), [])

    def test_version_file_controls_lock_and_output_despite_overrides(self):
        with patch.dict(os.environ, {'VERSION': '9.9.9', 'KIT_VERSION': '9.9.9',
                                    'BUNDLE_VERSION': '9.9.9', 'BLUEPRINT_VERSION': '9.9.9'}):
            self.assertEqual(release_config.bundle_lock()['version'], release_config.project_version())
            self.assertEqual(release_config.bundle_output().name, release_config.project_version())
