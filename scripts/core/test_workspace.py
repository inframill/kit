"""Staging is unique and cleaned up on every exit path."""
from pathlib import Path
import tempfile
import unittest

from scripts.core.workspace import workspace


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
