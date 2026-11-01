"""Invocation paths and environment never depend on another job's state."""
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from scripts.core.context import Context


class ContextTests(unittest.TestCase):
    def test_environment_is_an_immutable_snapshot(self):
        environment = {'JOB_SETTING': 'first'}
        context = Context(Path.cwd(), env=environment)
        environment['JOB_SETTING'] = 'changed'
        self.assertEqual(context.env['JOB_SETTING'], 'first')
        with self.assertRaises(TypeError):
            context.env['JOB_SETTING'] = 'changed'
        with patch.dict(os.environ, {'JOB_SETTING': 'before'}):
            inherited = Context(Path.cwd())
            os.environ['JOB_SETTING'] = 'after'
            self.assertEqual(inherited.env['JOB_SETTING'], 'before')

    def test_paths_and_process_overrides_are_scoped_to_each_context(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            first = Context(root / 'first', env={'SETTING': 'one', 'REMOVE': 'yes'})
            second = Context(root / 'second', env={'SETTING': 'two'})
            before = dict(os.environ)
            cwd = Path.cwd()
            with patch('scripts.core.process.run') as run:
                first.run(['tool'], cwd='work', env={'SETTING': 'override', 'REMOVE': None})
                self.assertEqual(run.call_args.kwargs['cwd'], first.root / 'work')
                self.assertEqual(run.call_args.kwargs['env'], {'SETTING': 'override'})
                second.run(['tool'])
                self.assertEqual(run.call_args.kwargs['cwd'], second.root)
                self.assertEqual(run.call_args.kwargs['env'], {'SETTING': 'two'})
            self.assertEqual(first.env['SETTING'], 'one')
            self.assertEqual(dict(os.environ), before)
            self.assertEqual(Path.cwd(), cwd)
            self.assertEqual(first.path('result.json'), first.root / 'result.json')
            self.assertEqual(first.path(root / 'external'), root / 'external')
