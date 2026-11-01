"""The public CLI is discoverable, lazy and independent of external tools."""
from contextlib import redirect_stderr, redirect_stdout
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from scripts.cli import ROOT, main
from scripts.core.context import Context


class CLITests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.context = Context(Path(temporary.name), env={})

    def invoke(self, args):
        output, errors = io.StringIO(), io.StringIO()
        with redirect_stdout(output), redirect_stderr(errors):
            result = main(args, context=self.context)
        return result, output.getvalue(), errors.getvalue()

    def test_group_dispatch_passes_the_invocation_context(self):
        with patch('scripts.repos.jobs.repository_paths', return_value={}) as job:
            result, output, errors = self.invoke(['repos', 'list', '--json'])
        self.assertEqual((result, errors), (0, ''))
        self.assertEqual(json.loads(output), {'repositories': []})
        job.assert_called_once_with(self.context)

    def test_failures_and_interruption_have_consistent_exit_codes(self):
        failures = [(OSError('missing tool'), 1), (ValueError('invalid input'), 1),
                    (RuntimeError('job failed'), 1),
                    (subprocess.TimeoutExpired(['tool'], 120), 1),
                    (subprocess.CalledProcessError(7, ['tool']), 7),
                    (subprocess.CalledProcessError(-15, ['tool']), 143),
                    (KeyboardInterrupt(), 130)]
        for failure, code in failures:
            with self.subTest(failure=failure), patch('scripts.repos.jobs.repository_paths', side_effect=failure):
                result, output, errors = self.invoke(['repos', 'list'])
            self.assertEqual((result, output), (code, ''))
            self.assertTrue(errors.strip())
            self.assertNotIn('Traceback', errors)

    def test_help_and_argument_errors_do_not_import_jobs_or_change_the_checkout(self):
        source = ('import sys; from scripts.cli import main\n'
                  'try: main(sys.argv[1:])\n'
                  'except SystemExit as error:\n'
                  ' assert "scripts.repos.jobs" not in sys.modules\n'
                  ' raise\n')
        cases = [(['--help'], 0), ([], 2), (['missing'], 2),
                 (['repos', '--help'], 0), (['repos', 'run', '--help'], 0),
                 (['repos', 'list', '--help'], 0), (['repos', 'recipes', '--help'], 0),
                 (['repos'], 2), (['repos', 'missing'], 2), (['repos', 'run'], 2),
                 (['repos', 'list', '--unknown'], 2),
                 (['repos', 'status', '--repo'], 2),
                 (['repos', 'run', 'status', 'another'], 2)]
        before = sorted(self.context.root.rglob('*'))
        for args, status in cases:
            with self.subTest(args=args):
                result = subprocess.run([sys.executable, '-B', '-c', source, *args],
                                        cwd=self.context.root, env={'PYTHONPATH': str(ROOT), 'PATH': ''},
                                        capture_output=True, text=True, timeout=10)
                self.assertEqual(result.returncode, status, result.stderr)
                self.assertIn('usage:', result.stdout if status == 0 else result.stderr)
                self.assertNotIn('Traceback', result.stderr)
        self.assertEqual(sorted(self.context.root.rglob('*')), before)

    def test_module_entry_point_runs_without_external_tools(self):
        result = subprocess.run([sys.executable, '-B', '-m', 'scripts', 'repos', 'recipes', '--json'],
                                cwd=self.context.root, env={'PYTHONPATH': str(ROOT), 'PATH': ''},
                                capture_output=True, text=True, timeout=10)
        self.assertEqual((result.returncode, result.stderr), (0, ''))
        self.assertIn('status', [recipe['name'] for recipe in json.loads(result.stdout)['recipes']])
