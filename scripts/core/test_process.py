"""Exercise the subprocess boundary using only the running Python interpreter."""
from contextlib import redirect_stderr
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from scripts.core.context import Context


class ProcessTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.context = Context(self.root, env={'JOB_SETTING': 'expected'})

    def test_cwd_environment_and_literal_arguments_reach_the_child(self):
        literal = 'space ; $(touch should-not-exist) `touch neither`'
        source = ('import os, sys, json; print(json.dumps([os.getcwd(), '
                  'os.environ.get("JOB_SETTING"), os.environ.get("REMOVED"), sys.argv[1:]]))')
        output = self.context.output([sys.executable, '-c', source, literal], env={'REMOVED': None})
        self.assertEqual(json.loads(output), [str(self.root), 'expected', None, [literal]])
        self.assertEqual(list(self.root.iterdir()), [])

    def test_failures_propagate_with_exit_status_and_captured_diagnostics(self):
        args = [sys.executable, '-c', 'import sys; print("failure", file=sys.stderr); sys.exit(7)']
        with self.assertRaises(subprocess.CalledProcessError) as raised:
            self.context.run(args, capture_output=True, log=False)
        self.assertEqual(raised.exception.returncode, 7)
        self.assertIn('failure', raised.exception.stderr)
        result = self.context.run(args, check=False, capture_output=True, log=False)
        self.assertEqual(result.returncode, 7)
        self.assertIn('failure', result.stderr)

    def test_logging_uses_stderr_and_does_not_dump_the_environment(self):
        errors = io.StringIO()
        with redirect_stderr(errors):
            result = self.context.run([sys.executable, '-c', 'print("result")'], capture_output=True)
        self.assertEqual(result.stdout, 'result\n')
        self.assertIn(sys.executable, errors.getvalue())
        self.assertNotIn('JOB_SETTING', errors.getvalue())
        self.assertNotIn('expected', errors.getvalue())

    def test_shell_commands_are_rejected(self):
        for args, options in [('echo hello', {}), (['echo', 'hello'], {'shell': True})]:
            with self.subTest(args=args), self.assertRaises(ValueError):
                self.context.run(args, **options)

    def test_timeout_is_enforced(self):
        with self.assertRaises(subprocess.TimeoutExpired):
            self.context.run([sys.executable, '-c', 'import time; time.sleep(30)'], timeout=0.1, log=False)

    def test_commands_have_a_default_timeout(self):
        with patch('scripts.core.process.subprocess.run') as run:
            self.context.run(['tool'], log=False)
        self.assertGreater(run.call_args.kwargs['timeout'], 0)
