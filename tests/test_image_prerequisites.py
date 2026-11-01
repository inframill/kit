"""Missing or invalid release inputs fail before any disposable Docker resources."""
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

import check as acceptance


class ImagePrerequisiteTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.folder = Path(temporary.name) / 'dist/kit/1.2.3'
        self.folder.mkdir(parents=True)

    def test_missing_bundle_reports_path_before_creating_registry(self):
        with patch.object(sys, 'argv', ['check.py', 'image']), \
             patch.object(acceptance, 'bundle_output', return_value=self.folder), \
             patch.object(acceptance, 'verify') as verify, \
             patch.object(acceptance, 'registry') as registry, \
             self.assertRaisesRegex(RuntimeError, 'Missing release bundle: ' + str(self.folder)):
            acceptance.main()
        verify.assert_not_called()
        registry.assert_not_called()

    def test_corrupt_or_wrong_version_bundle_never_creates_registry(self):
        (self.folder / 'manifest.json').write_text('{}')
        for failure in ('checksum', 'version'):
            with self.subTest(failure=failure), patch.object(sys, 'argv', ['check.py', 'image']), \
                 patch.object(acceptance, 'bundle_output', return_value=self.folder), \
                 patch.object(acceptance, 'project_version', return_value='1.2.3'), \
                 patch.object(acceptance, 'verify', side_effect=ValueError('checksum mismatch') if failure == 'checksum' else None,
                              return_value={'version': 'older'}), \
                 patch.object(acceptance, 'registry') as registry, \
                 self.assertRaises((ValueError, RuntimeError)):
                acceptance.main()
            registry.assert_not_called()

    def test_verified_bundle_is_used_by_container_checks(self):
        (self.folder / 'manifest.json').write_text('{}')
        with patch.object(sys, 'argv', ['check.py', 'image']), \
             patch.object(acceptance, 'bundle_output', return_value=self.folder), \
             patch.object(acceptance, 'project_version', return_value='1.2.3'), \
             patch.object(acceptance, 'verify', return_value={'version': '1.2.3'}), \
             patch.object(acceptance, 'registry') as registry, \
             patch.object(acceptance, 'container_checks') as checks:
            registry.return_value.__enter__.return_value = ('address', 'test-network', 'push-address')
            acceptance.main()
        self.assertEqual(checks.call_args.args[1], 'test-network')
        self.assertEqual(checks.call_args.args[3], self.folder)
