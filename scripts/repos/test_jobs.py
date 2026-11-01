"""Recipe contracts, selection, failures, and real Git status behavior."""
from contextlib import redirect_stderr
import io
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile
import time
import unittest

from scripts.core.context import Context
from scripts.repos.jobs import bind_command, run_recipe, status


class RecipeTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.context = Context(Path(temporary.name))
        self.repositories = {'one': 'one repo', 'two': 'two repo'}
        for path in self.repositories.values():
            self.context.path(path).mkdir()

    def run_job(self, recipe, **options):
        return run_recipe(self.context, 'test', repositories=self.repositories,
                          recipes={'test': recipe}, **options)

    def test_selection_passes_absolute_paths_in_order_without_duplicates(self):
        calls = []

        def inspect(path):
            calls.append(path)
            return [('name', path.name), ('exists', path.is_dir()), ('count', 2)]

        cwd = Path.cwd()
        report = self.run_job(inspect, selected=['two', 'one', 'two'])
        self.assertEqual(calls, [self.context.path('two repo'), self.context.path('one repo')])
        self.assertEqual(Path.cwd(), cwd)
        self.assertEqual([row['repo'] for row in report['results']], ['two', 'one'])
        self.assertEqual(report['results'][0]['values'],
                         {'name': 'two repo', 'exists': True, 'count': 2})
        self.assertTrue(all(row['error'] is None for row in report['results']))

    def test_invalid_selection_and_recipe_do_not_execute_anything(self):
        calls = []

        def inspect(path):
            calls.append(path)
            return []

        for options in ({'selected': ['one', 'missing']}, {'selected': []}):
            with self.subTest(options=options), self.assertRaises(ValueError):
                self.run_job(inspect, **options)
        with self.assertRaises(ValueError):
            run_recipe(self.context, 'missing', recipes={'test': inspect})
        self.assertEqual(calls, [])

    def test_failed_and_missing_repositories_do_not_stop_other_repos(self):
        def inspect(path):
            if path.name == 'one repo':
                raise RuntimeError('inspection failed')
            return [('ok', True)]

        self.repositories['missing'] = 'missing'
        report = self.run_job(inspect)
        self.assertIn('inspection failed', report['results'][0]['error'])
        self.assertEqual(report['results'][1]['values'], {'ok': True})
        self.assertIsNone(report['results'][1]['error'])
        self.assertIn('does not exist', report['results'][2]['error'])

    def test_invalid_return_values_are_reported_without_losing_other_results(self):
        invalid = [None, {'key': 'value'}, [('duplicate', 1), ('duplicate', 2)],
                   [('key',)], [('', 1)], [(3, 'value')], [('path', Path('/tmp'))],
                   [('number', float('nan'))]]
        for value in invalid:
            def inspect(path):
                return value if path.name == 'one repo' else [('ok', True)]

            with self.subTest(value=value):
                report = self.run_job(inspect)
                self.assertTrue(report['results'][0]['error'])
                self.assertEqual(report['results'][0]['values'], {})
                self.assertEqual(report['results'][1]['values'], {'ok': True})

    def test_empty_results_are_valid_and_prints_are_diagnostics(self):
        def inspect(path):
            print('inspecting ' + path.name)
            return []

        errors = io.StringIO()
        with redirect_stderr(errors):
            report = self.run_job(inspect, selected=['one'])
        self.assertEqual(errors.getvalue(), 'inspecting one repo\n')
        self.assertEqual(report['results'][0], {'repo': 'one', 'values': {}, 'error': None})

    def test_subprocess_errors_retain_partial_output_and_timeouts(self):
        failures = [subprocess.CalledProcessError(7, ['example'], output='stdout', stderr='stderr'),
                    subprocess.TimeoutExpired(['example'], 1, output=b'partial output')]
        for failure in failures:
            def inspect(path):
                raise failure

            with self.subTest(failure=failure):
                error = self.run_job(inspect, selected=['one'])['results'][0]['error']
                self.assertIn(type(failure).__name__, error)
                self.assertIn(failure.output.decode() if isinstance(failure.output, bytes)
                              else failure.output, error)

    def test_interruption_records_the_current_repo_and_stops(self):
        calls = []

        def inspect(path):
            calls.append(path)
            raise KeyboardInterrupt

        report = self.run_job(inspect)
        self.assertTrue(report['interrupted'])
        self.assertEqual(report['results'], [{'repo': 'one', 'values': {}, 'error': 'Interrupted'}])
        self.assertEqual(len(calls), 1)


class CommandRecipeTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()

    def test_shell_syntax_cwd_environment_and_both_output_streams(self):
        recipe = bind_command('printf "%s\\n" "$PWD"; printf "%s\\n" "$RECIPE_TEST"; '
                              'printf "stderr\\n" >&2; exit 7', env={'RECIPE_TEST': 'snapshot'})
        values = dict(recipe(self.root))
        self.assertEqual(values['exit_code'], 7)
        self.assertEqual(values['output'], f'{self.root}\nsnapshot\nstderr\n')
        self.assertNotIn('truncated', values)

    def test_output_cap_counts_characters_and_marks_truncation(self):
        import sys
        text = shlex.join([sys.executable, '-B', '-c', 'print("é" * 20, end="")'])
        values = dict(bind_command(text, output_limit=5)(self.root))
        self.assertEqual(values['output'], 'é' * 5 + '\n... [truncated]')
        self.assertTrue(values['truncated'])
        exact = dict(bind_command("printf 'abcde'", output_limit=5)(self.root))
        self.assertEqual(exact['output'], 'abcde')
        self.assertNotIn('truncated', exact)

    def test_silent_and_signalled_commands_still_return_their_exit_codes(self):
        self.assertEqual(dict(bind_command('exit 0')(self.root)), {'exit_code': 0})
        self.assertEqual(dict(bind_command('kill -TERM $$')(self.root)), {'exit_code': 143})

    def test_timeout_retains_output_and_stops_shell_descendants(self):
        import sys
        marker = self.root / 'child-finished'
        child = f'import time; from pathlib import Path; time.sleep(0.7); Path({str(marker)!r}).touch()'
        parent = ('import subprocess, sys, time; '
                  'print("started", flush=True); '
                  f'subprocess.Popen([sys.executable, "-B", "-c", {child!r}]); '
                  'time.sleep(10)')
        text = shlex.join([sys.executable, '-B', '-c', parent])
        values = dict(bind_command(text, timeout=0.3)(self.root))
        self.assertEqual(values['exit_code'], 124)
        self.assertTrue(values['timed_out'])
        self.assertIn('started', values['output'])
        time.sleep(0.8)
        self.assertFalse(marker.exists(), 'A timed-out command left a child running')


@unittest.skipUnless(shutil.which('git'), 'Git is required for status integration tests')
class GitStatusTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.env = dict(os.environ, GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM='1')
        self.git('init', '--quiet')

    def git(self, *args):
        return subprocess.run(['git', '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
                               '-c', 'commit.gpgsign=false', *args], cwd=self.root, env=self.env,
                              text=True, capture_output=True, check=True, timeout=10).stdout

    def test_clean_untracked_staged_modified_and_detached_states(self):
        branch = self.git('symbolic-ref', '--short', 'HEAD').strip()
        initial = dict(status(self.root))
        self.assertEqual(initial['branch'], branch)
        self.assertTrue(initial['clean'])
        self.assertIn('no commits', initial['attention'])
        self.assertIn('no upstream', initial['attention'])
        for key in ('staged', 'unstaged', 'untracked', 'conflicts', 'stashes'):
            self.assertEqual(initial[key], 0)
        for key in ('upstream', 'ahead', 'behind', 'nearest_tag', 'commits_since_tag', 'last_commit'):
            self.assertIsNone(initial[key])
        self.assertEqual(initial['tags'], [])
        tracked = self.root / 'file with spaces.txt'
        tracked.write_text('original\n')
        self.assertFalse(dict(status(self.root))['clean'])
        self.git('add', '--', tracked.name)
        self.assertFalse(dict(status(self.root))['clean'])
        self.git('commit', '--quiet', '-m', 'Fixture')
        committed = dict(status(self.root))
        self.assertTrue(committed['clean'])
        self.assertIn('Fixture', committed['last_commit'])
        self.assertIsNone(committed['nearest_tag'])
        tracked.write_text('changed\n')
        self.assertFalse(dict(status(self.root))['clean'])
        tracked.write_text('original\n')
        self.git('checkout', '--quiet', '--detach', 'HEAD')
        detached = dict(status(self.root))
        self.assertEqual(detached['branch'], '(detached)')
        self.assertTrue(detached['clean'])
        self.assertIn('detached HEAD', detached['attention'])

    def commit(self, message='Fixture'):
        self.git('add', '--all')
        self.git('commit', '--quiet', '--allow-empty', '-m', message)

    def test_counts_include_nested_files_and_staged_and_unstaged_changes(self):
        tracked = self.root / 'tracked.txt'
        tracked.write_text('original\n')
        self.commit()
        tracked.write_text('staged\n')
        self.git('add', '--', tracked.name)
        tracked.write_text('unstaged\n')
        nested = self.root / 'nested'
        nested.mkdir()
        (nested / 'one.txt').write_text('one')
        (nested / 'two\nwith newline.txt').write_text('two')
        values = dict(status(self.root))
        self.assertEqual([values[key] for key in ('staged', 'unstaged', 'untracked', 'conflicts')],
                         [1, 1, 2, 0])
        self.assertFalse(values['clean'])
        self.assertIn('uncommitted work', values['attention'])

    def test_renames_do_not_parse_the_source_filename_as_a_status_record(self):
        source = '# branch.ab +99 -99\nold name.txt'
        (self.root / source).write_text('original\n')
        self.commit()
        self.git('mv', '--', source, 'new name.txt')
        values = dict(status(self.root))
        self.assertEqual(values['staged'], 1)
        self.assertEqual(values['unstaged'], 0)
        self.assertEqual(values['untracked'], 0)
        self.assertIsNone(values['ahead'])
        self.assertIsNone(values['behind'])

    def test_tags_include_annotated_and_lightweight_and_distance_from_head(self):
        self.commit()
        self.git('tag', '-a', 'release-v1.0', '-m', 'Release')
        self.git('tag', 'lightweight')
        tagged = dict(status(self.root))
        self.assertEqual(tagged['tags'], ['lightweight', 'release-v1.0'])
        self.assertEqual(tagged['nearest_tag'], 'release-v1.0')
        self.assertEqual(tagged['commits_since_tag'], 0)
        self.commit('After release')
        later = dict(status(self.root))
        self.assertEqual(later['tags'], [])
        self.assertEqual(later['nearest_tag'], 'release-v1.0')
        self.assertEqual(later['commits_since_tag'], 1)
        self.assertIn('After release', later['last_commit'])

    def test_cached_upstream_counts_handle_ahead_behind_diverged_and_gone(self):
        self.commit()
        base = self.git('rev-parse', 'HEAD').strip()
        branch = self.git('symbolic-ref', '--short', 'HEAD').strip()
        self.git('config', 'remote.origin.url', '.')
        self.git('config', 'remote.origin.fetch', '+refs/heads/*:refs/remotes/origin/*')
        self.git('config', f'branch.{branch}.remote', 'origin')
        self.git('config', f'branch.{branch}.merge', 'refs/heads/main')
        self.git('update-ref', 'refs/remotes/origin/main', base)
        current = dict(status(self.root))
        self.assertEqual(current['upstream'], 'origin/main')
        self.assertEqual((current['ahead'], current['behind']), (0, 0))
        self.assertEqual(current['attention'], 'up to date')
        self.commit('Local commit')
        local = dict(status(self.root))
        self.assertEqual((local['ahead'], local['behind']), (1, 0))
        self.assertIn('unpushed commits', local['attention'])
        remote = self.git('commit-tree', f'{base}^{{tree}}', '-p', base, '-m', 'Remote commit').strip()
        self.git('update-ref', 'refs/remotes/origin/main', remote)
        diverged = dict(status(self.root))
        self.assertEqual((diverged['ahead'], diverged['behind']), (1, 1))
        self.assertIn('diverged', diverged['attention'])
        remote = self.git('commit-tree', 'HEAD^{tree}', '-p', 'HEAD', '-m', 'Ahead remote').strip()
        self.git('update-ref', 'refs/remotes/origin/main', remote)
        behind = dict(status(self.root))
        self.assertEqual((behind['ahead'], behind['behind']), (0, 1))
        self.assertIn('behind upstream', behind['attention'])
        self.git('update-ref', '-d', 'refs/remotes/origin/main')
        gone = dict(status(self.root))
        self.assertIsNone(gone['ahead'])
        self.assertIsNone(gone['behind'])
        self.assertIn('upstream missing', gone['attention'])
        self.git('update-ref', 'refs/remotes/origin/main', remote)
        self.git('checkout', '--quiet', '--orphan', 'unborn')
        self.git('config', 'branch.unborn.remote', 'origin')
        self.git('config', 'branch.unborn.merge', 'refs/heads/main')
        unborn = dict(status(self.root))
        self.assertIn('no commits', unborn['attention'])
        self.assertNotIn('upstream missing', unborn['attention'])
        self.assertIsNone(unborn['ahead'])

    def test_conflicts_and_stashes_are_visible_as_work_needing_attention(self):
        tracked = self.root / 'tracked.txt'
        tracked.write_text('base\n')
        self.commit()
        tracked.write_text('stashed\n')
        self.git('stash', 'push', '--quiet')
        stashed = dict(status(self.root))
        self.assertTrue(stashed['clean'])
        self.assertEqual(stashed['stashes'], 1)
        self.assertIn('stashed work', stashed['attention'])
        branch = self.git('symbolic-ref', '--short', 'HEAD').strip()
        self.git('checkout', '--quiet', '-b', 'side')
        tracked.write_text('side\n')
        self.commit('Side')
        self.git('checkout', '--quiet', branch)
        tracked.write_text('main\n')
        self.commit('Main')
        with self.assertRaises(subprocess.CalledProcessError):
            self.git('merge', '--no-edit', 'side')
        conflicted = dict(status(self.root))
        self.assertEqual(conflicted['conflicts'], 1)
        self.assertFalse(conflicted['clean'])
        self.assertTrue(conflicted['attention'].startswith('resolve conflicts'))

    def test_status_rejects_a_subdirectory_instead_of_inspecting_its_parent(self):
        nested = self.root / 'nested'
        nested.mkdir()
        with self.assertRaisesRegex(ValueError, 'repository root'):
            status(nested)
