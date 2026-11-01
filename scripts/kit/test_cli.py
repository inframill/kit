"""The public command tree is discoverable, precise and safe to inspect."""
from contextlib import redirect_stdout, redirect_stderr
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from scripts.cli import ROOT, main
from scripts.kit._test_fixtures import project


class CLITests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.context = project(temporary.name)

    def invoke(self, args):
        output, errors = io.StringIO(), io.StringIO()
        with redirect_stdout(output), redirect_stderr(errors):
            result = main(args, context=self.context)
        return result, output.getvalue(), errors.getvalue()

    def test_each_job_routes_through_its_group_with_the_invocation_context(self):
        cases = [('dev sync', 'dev.sync'), ('engine copy', 'engine.copy'),
                 ('engine build', 'engine.build_sibling'), ('runtime setup', 'runtime.setup'),
                 ('bundle capture', 'bundle.capture'), ('bundle image', 'bundle.build_image'),
                 ('bundle release', 'bundle.release'), ('bundle load', 'bundle.load_image'),
                 ('bundle push', 'bundle.push')]
        for command, target in cases:
            with self.subTest(command=command), patch('scripts.kit.' + target) as job:
                self.assertEqual(self.invoke(['kit', *command.split()])[0], 0)
                job.assert_called_once()
                self.assertIs(job.call_args.kwargs['context'], self.context)
                if command == 'dev sync':
                    self.assertEqual(job.call_args.args, (self.context.root.parent / 'inframill',))
                elif command in ('bundle load', 'bundle push'):
                    self.assertEqual(job.call_args.args, (self.context.root / 'dist/kit/1.2.3',))

    def test_config_can_print_or_write_without_installed_tools(self):
        result, output, errors = self.invoke(['kit', 'runtime', 'config'])
        self.assertEqual((result, errors), (0, ''))
        config = json.loads(output)
        self.assertEqual(config['engine']['dataDir'], str(self.context.root / '.inframill'))
        self.assertEqual(config['engine']['providers']['dispatch']['args'], ['dispatch-0.1.0'])
        result, _, errors = self.invoke(['kit', 'runtime', 'config', '--output', 'generated/config.json'])
        self.assertEqual((result, errors), (0, ''))
        self.assertEqual(json.loads((self.context.root / 'generated/config.json').read_text()), config)

    def test_bundle_verify_accepts_an_explicit_path_and_prints_json(self):
        with patch('scripts.kit.bundle.verify', return_value={'schema': 1}) as verify:
            result, output, errors = self.invoke(['kit', 'bundle', 'verify', '--bundle', 'artifacts/release'])
        self.assertEqual((result, errors), (0, ''))
        verify.assert_called_once_with(self.context.root / 'artifacts/release')
        self.assertEqual(json.loads(output), {'schema': 1})

    def test_missing_unknown_and_misplaced_arguments_fail_before_running_jobs(self):
        cases = [[], ['unknown'], ['kit'], ['kit', 'unknown'], ['kit', 'bundle'],
                 ['kit', 'runtime', 'setup', '--output', 'wrong'],
                 ['kit', 'bundle', 'capture', '--bundle', 'wrong']]
        for args in cases:
            with self.subTest(args=args), patch('scripts.kit.cli.bundle_command') as job:
                with self.assertRaises(SystemExit) as raised:
                    self.invoke(args)
                self.assertEqual(raised.exception.code, 2)
                job.assert_not_called()

    def test_command_failures_have_actionable_errors_and_exit_codes(self):
        failures = [(ValueError('invalid artifacts'), 1), (OSError('missing tool'), 1),
                    (RuntimeError('registry unavailable'), 1),
                    (subprocess.CalledProcessError(7, ['tool']), 7),
                    (subprocess.CalledProcessError(-15, ['tool']), 143),
                    (subprocess.TimeoutExpired(['tool'], 120), 1),
                    (KeyboardInterrupt(), 130)]
        for failure, code in failures:
            with self.subTest(failure=failure), patch('scripts.kit.engine.copy', side_effect=failure):
                result, output, errors = self.invoke(['kit', 'engine', 'copy'])
                self.assertEqual(result, code)
                self.assertEqual(output, '')
                self.assertTrue(errors.strip())
                self.assertNotIn('Traceback', errors)

    def test_help_requires_no_tools_or_job_imports(self):
        levels = [[], ['kit'], ['kit', 'dev'], ['kit', 'engine'], ['kit', 'runtime'],
                  ['kit', 'bundle'], ['kit', 'bundle', 'push']]
        source = ('import sys; from scripts.cli import main\n'
                  'try: main(sys.argv[1:])\n'
                  'except SystemExit as error:\n'
                  ' assert error.code == 0\n'
                  ' assert not any("scripts.kit." + name in sys.modules '
                  'for name in ("bundle", "engine", "runtime", "dev"))\n')
        before = sorted(self.context.root.rglob('*'))
        for args in levels:
            with self.subTest(args=args):
                result = subprocess.run([sys.executable, '-B', '-c', source, *args, '--help'],
                                        cwd=self.context.root, env={'PYTHONPATH': str(ROOT), 'PATH': ''},
                                        capture_output=True, text=True, check=True)
                self.assertIn('usage:', result.stdout)
                self.assertEqual(result.stderr, '')
        self.assertEqual(sorted(self.context.root.rglob('*')), before)

    def test_legacy_module_entrypoints_forward_to_the_command_tree(self):
        for module in ('dev', 'engine', 'runtime', 'bundle'):
            with self.subTest(module=module):
                result = subprocess.run([sys.executable, '-B', '-m', 'scripts.kit.' + module, '--help'],
                                        cwd=self.context.root, env={'PYTHONPATH': str(ROOT), 'PATH': ''},
                                        capture_output=True, text=True, check=True)
                self.assertIn('usage: python3 -m scripts kit', result.stdout)
                self.assertEqual(result.stderr, '')
