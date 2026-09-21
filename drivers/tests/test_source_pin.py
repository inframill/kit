import unittest
from unittest.mock import patch
import sys
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from build import verify_source

class SourcePinTests(unittest.TestCase):
    def test_mismatched_revision_and_dirty_source_reject(self):
        for outputs in [('different\n', ''), ('expected\n', ' M pkg/driver/driver.go\n')]:
            with patch('build.subprocess.check_output', side_effect=outputs):
                with self.assertRaises(ValueError):
                    verify_source('/tmp/kit', 'expected')

    def test_clean_dependency_is_recorded(self):
        with patch('build.subprocess.check_output', side_effect=['expected\n', '']):
            self.assertEqual(verify_source('/tmp/kit', 'expected'), 'expected')
