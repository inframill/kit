import subprocess
import unittest
from unittest.mock import patch

import sys
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from build import find_python


class PythonRuntimeTests(unittest.TestCase):
    def test_system_python_39_falls_back_to_supported_interpreter(self):
        with patch('build.sys.executable', '/usr/bin/python3'), \
             patch('build.shutil.which', side_effect=lambda name: {'/usr/bin/python3': name, 'python3.14': '/opt/homebrew/bin/python3.14'}.get(name)), \
             patch('build.subprocess.check_output', side_effect=['3.9.6\n', '3.14.7\n']):
            self.assertEqual(find_python(), ('/opt/homebrew/bin/python3.14', '3.14.7'))

    def test_supported_override_is_used(self):
        with patch('build.shutil.which', return_value='/custom/python'), \
             patch('build.subprocess.check_output', return_value='3.13.1\n'):
            self.assertEqual(find_python('/custom/python'), ('/custom/python', '3.13.1'))

    def test_invalid_override_does_not_silently_fall_back(self):
        for version in ['3.9.6\n', '3.15.0\n', 'invalid\n']:
            with self.subTest(version=version), \
                 patch('build.shutil.which', return_value='/custom/python') as lookup, \
                 patch('build.subprocess.check_output', return_value=version):
                with self.assertRaisesRegex(ValueError, 'PYTHON=/custom/python'):
                    find_python('/custom/python')
                lookup.assert_called_once_with('/custom/python')

    def test_missing_or_broken_interpreters_give_actionable_error(self):
        for executable in [None, '/broken/python']:
            with self.subTest(executable=executable), \
                 patch('build.shutil.which', return_value=executable), \
                 patch('build.subprocess.check_output', side_effect=subprocess.CalledProcessError(1, 'python')):
                with self.assertRaisesRegex(ValueError, 'Install one alongside your system Python'):
                    find_python()
