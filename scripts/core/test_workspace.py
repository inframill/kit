"""Staging is unique and cleaned up on every exit path."""
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from scripts.core.workspace import install, workspace


class WorkspaceTests(unittest.TestCase):
    def test_nested_invocations_are_separate_and_preserve_outputs(self):
        with tempfile.TemporaryDirectory() as temporary:
            parent = Path(temporary) / 'staging'
            with workspace(parent, prefix='job-') as first:
                (first / 'input').write_text('first')
                with workspace(parent, prefix='job-') as second:
                    self.assertNotEqual(first, second)
                    self.assertFalse((second / 'input').exists())
                    (parent / 'result').write_text('deliberate output')
                self.assertFalse(second.exists())
                self.assertTrue(first.exists())
            self.assertFalse(first.exists())
            self.assertEqual((parent / 'result').read_text(), 'deliberate output')

    def test_failure_and_interrupt_clean_up_staging(self):
        with tempfile.TemporaryDirectory() as temporary:
            for error in (RuntimeError, KeyboardInterrupt):
                with self.subTest(error=error), self.assertRaises(error):
                    with workspace(temporary, prefix='job-') as stage:
                        (stage / 'partial').write_text('incomplete')
                        raise error()
                self.assertFalse(stage.exists())

    def test_install_replaces_files_and_directories_together(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            target = root / 'output'
            target.mkdir()
            (target / 'obsolete').write_text('old')
            with workspace(root, prefix='install-') as stage:
                prepared = stage / 'prepared'
                prepared.mkdir()
                (prepared / 'new').write_text('new')
                pin = stage / 'pin'
                pin.write_text('pin')
                backup = stage / 'backup'
                backup.mkdir()
                install([(prepared, target), (pin, root / 'pin')], backup)
            self.assertEqual([path.name for path in target.iterdir()], ['new'])
            self.assertEqual((target / 'new').read_text(), 'new')
            self.assertEqual((root / 'pin').read_text(), 'pin')
            self.assertEqual({path.name for path in root.iterdir()}, {'output', 'pin'})

    def test_failed_or_interrupted_install_restores_existing_and_removes_new_outputs(self):
        for failure in (OSError, KeyboardInterrupt):
            for existing in (False, True):
                with self.subTest(failure=failure, existing=existing), tempfile.TemporaryDirectory() as temporary:
                    root = Path(temporary)
                    target = root / 'output'
                    if existing:
                        target.mkdir()
                        (target / 'previous').write_text('previous')
                    pin = root / 'pin'
                    pin.write_text('old pin')
                    with workspace(root, prefix='install-') as stage:
                        prepared = stage / 'prepared'
                        prepared.mkdir()
                        (prepared / 'new').write_text('new')
                        new_pin = stage / 'pin'
                        new_pin.write_text('new pin')
                        backup = stage / 'backup'
                        backup.mkdir()
                        rename = Path.rename

                        def fail_pin(path, destination):
                            if path == new_pin:
                                raise failure()
                            return rename(path, destination)

                        with patch.object(Path, 'rename', fail_pin), self.assertRaises(failure):
                            install([(prepared, target), (new_pin, pin)], backup)
                    self.assertEqual(pin.read_text(), 'old pin')
                    self.assertEqual(target.exists(), existing)
                    if existing:
                        self.assertEqual([path.name for path in target.iterdir()], ['previous'])
                        self.assertEqual((target / 'previous').read_text(), 'previous')

    def test_symlink_target_rejection_restores_earlier_outputs(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            target = root / 'output'
            target.write_text('previous')
            link = root / 'link'
            link.symlink_to(target)
            with workspace(root, prefix='install-') as stage:
                first, second = stage / 'first', stage / 'second'
                first.write_text('first')
                second.write_text('second')
                backup = stage / 'backup'
                backup.mkdir()
                with self.assertRaisesRegex(ValueError, 'symlink'):
                    install([(first, target), (second, link)], backup)
            self.assertEqual(target.read_text(), 'previous')
            self.assertTrue(link.is_symlink())
