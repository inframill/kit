"""Human and agent interfaces run the same recipe and preserve typed results."""
from contextlib import redirect_stderr, redirect_stdout
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from scripts.cli import main
from scripts.core.context import Context


class ReposCLITests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.context = Context(Path(temporary.name), env={})
        self.repositories = {'one': 'one', 'two': 'two'}
        for path in self.repositories:
            self.context.path(path).mkdir()

    def invoke(self, args, recipe=None):
        from scripts.repos.jobs import RECIPES
        recipes = RECIPES if recipe is None else {'inspect': recipe, 'status': recipe}
        output, errors = io.StringIO(), io.StringIO()
        with patch('scripts.repos.jobs.REPOS', self.repositories), \
                patch('scripts.repos.jobs.RECIPES', recipes), \
                redirect_stdout(output), redirect_stderr(errors):
            code = main(['repos', *args], context=self.context)
        return code, output.getvalue(), errors.getvalue()

    def test_json_preserves_types_and_redirects_recipe_prints(self):
        def inspect(path):
            print('diagnostic')
            return [('clean', True), ('count', 3), ('details', 'first\nsecond'),
                    ('extra', {'items': [None, False, 2]})]

        code, output, errors = self.invoke(['run', 'inspect', '--repo', 'two', '--json'], inspect)
        self.assertEqual(code, 0)
        report = json.loads(output)
        self.assertEqual(report['recipe'], 'inspect')
        self.assertEqual(report['results'], [{'repo': 'two', 'error': None, 'values': {
            'clean': True, 'count': 3, 'details': 'first\nsecond', 'extra': {'items': [None, False, 2]}}}])
        self.assertEqual(errors, 'diagnostic\n')

    def test_table_uses_all_returned_keys_and_keeps_multiline_values(self):
        def inspect(path):
            return [('branch', 'main'), ('details', 'first\nsecond')] if path.name == 'one' else [('count', 3)]

        code, output, errors = self.invoke(['run', 'inspect'], inspect)
        self.assertEqual((code, errors), (0, ''))
        for value in ('Repository', 'branch', 'details', 'count', 'first', 'second', 'one', 'two'):
            self.assertIn(value, output)
        self.assertNotIn('Error', output)

    def test_per_repo_failures_return_nonzero_and_retain_successes_in_both_formats(self):
        def inspect(path):
            if path.name == 'one':
                raise RuntimeError('failed here')
            return [('ok', True)]

        code, output, errors = self.invoke(['run', 'inspect', '--json'], inspect)
        self.assertEqual((code, errors), (1, ''))
        results = json.loads(output)['results']
        self.assertIn('failed here', results[0]['error'])
        self.assertEqual(results[1], {'repo': 'two', 'values': {'ok': True}, 'error': None})
        code, output, _ = self.invoke(['run', 'inspect'], inspect)
        self.assertEqual(code, 1)
        self.assertIn('Error', output)
        self.assertIn('failed here', output)
        self.assertIn('two', output)

    def test_unknown_names_produce_json_errors_before_any_execution(self):
        def inspect(path):
            self.fail('Invalid selection must not invoke the recipe')

        for options in (['run', 'missing'], ['run', 'inspect', '--repo', 'one', '--repo', 'missing']):
            with self.subTest(options=options):
                code, output, errors = self.invoke([*options, '--json'], inspect)
                self.assertEqual((code, errors), (1, ''))
                report = json.loads(output)
                self.assertTrue(report['error'])
                self.assertEqual(report['results'], [])

    def test_discovery_has_json_and_table_output_without_running_a_recipe(self):
        def inspect(path):
            """Describe this recipe."""
            self.fail('Discovery must not execute recipes')

        code, output, errors = self.invoke(['list', '--json'], inspect)
        self.assertEqual((code, errors), (0, ''))
        self.assertEqual(json.loads(output)['repositories'],
                         [{'name': name, 'path': str(self.context.path(path))}
                          for name, path in self.repositories.items()])
        code, output, errors = self.invoke(['recipes', '--json'], inspect)
        self.assertEqual((code, errors), (0, ''))
        self.assertEqual(json.loads(output)['recipes'][0],
                         {'name': 'inspect', 'description': 'Describe this recipe.'})
        for command, expected in [('list', 'Repository'), ('recipes', 'Describe this recipe.')]:
            code, output, errors = self.invoke([command], inspect)
            self.assertEqual((code, errors), (0, ''))
            self.assertIn(expected, output)

    def test_status_alias_and_interruption_are_machine_readable(self):
        def inspect(path):
            raise KeyboardInterrupt

        code, output, errors = self.invoke(['status', '--json'], inspect)
        self.assertEqual((code, errors), (130, ''))
        report = json.loads(output)
        self.assertTrue(report['interrupted'])
        self.assertEqual(len(report['results']), 1)

    def test_command_recipe_reports_nonzero_exit_and_continues_in_table_and_json(self):
        text = 'printf "%s\\n" "$PWD"; case "$PWD" in */one) exit 7;; *) exit 0;; esac'
        code, output, errors = self.invoke(['run', 'command', '--command', text, '--json'])
        self.assertEqual((code, errors), (1, ''))
        results = json.loads(output)['results']
        self.assertEqual([row['values']['exit_code'] for row in results], [7, 0])
        self.assertTrue(all(row['error'] is None for row in results))
        for row in results:
            self.assertEqual(row['values']['output'], str(self.context.path(row['repo'])) + '\n')
        code, output, errors = self.invoke(['run', 'command', '--command', text])
        self.assertEqual((code, errors), (1, ''))
        for value in ('exit_code', 'output', 'one', 'two', '7'):
            self.assertIn(value, output)

    def test_command_recipe_caps_output_and_can_select_one_repo(self):
        code, output, errors = self.invoke(['run', 'command', '--command', "printf 'abcdefghijk'",
                                            '--repo', 'two', '--output-limit', '5', '--json'])
        self.assertEqual((code, errors), (0, ''))
        self.assertEqual(json.loads(output)['results'], [
            {'repo': 'two', 'values': {'exit_code': 0, 'output': 'abcde\n... [truncated]',
                                     'truncated': True}, 'error': None}])

    def test_invalid_command_options_are_rejected_before_any_repo_runs(self):
        options = [[], ['--command', '  '], ['--command', 'true', '--output-limit', '0'],
                   ['--command', 'true', '--timeout', '0'],
                   ['--command', 'true', '--timeout', 'nan'],
                   ['--command', 'true', '--timeout', 'inf']]
        with patch('scripts.repos.jobs.command') as command:
            for args in options:
                with self.subTest(args=args):
                    code, output, errors = self.invoke(['run', 'command', *args, '--json'])
                    self.assertEqual((code, errors), (1, ''))
                    report = json.loads(output)
                    self.assertEqual(report['results'], [])
                    self.assertTrue(report['error'])
            command.assert_not_called()

        def inspect(path):
            self.fail('An unrelated recipe must not run with command-only flags')

        code, output, _ = self.invoke(['run', 'inspect', '--command', 'true', '--json'], inspect)
        self.assertEqual(code, 1)
        self.assertEqual(json.loads(output)['results'], [])

    def test_command_timeout_returns_json_with_a_failure_exit_status(self):
        import shlex
        import sys
        text = shlex.join([sys.executable, '-B', '-c', 'import time; print("started", flush=True); time.sleep(10)'])
        code, output, errors = self.invoke(['run', 'command', '--command', text, '--repo', 'one',
                                            '--timeout', '0.3', '--json'])
        self.assertEqual((code, errors), (1, ''))
        values = json.loads(output)['results'][0]['values']
        self.assertEqual(values['exit_code'], 124)
        self.assertTrue(values['timed_out'])
        self.assertIn('started', values['output'])
