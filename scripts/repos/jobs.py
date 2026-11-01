"""Edit REPOS, recipe functions, and RECIPES here to configure local automation."""
from contextlib import redirect_stdout
from functools import partial
import json
import math
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile

from scripts.core.context import Context


# Paths are relative to the Toolai checkout, independent of the caller's cwd.
REPOS = {
    'hummus': '../hummus',
    'millway': '../millway',
    'kit': '../kit',
    'inframill': '../inframill',
    'toolai': '../toolai',
}


def _worktree_status(output):
    # Porcelain v2 uses NUL-separated records, including an extra source-path
    # record for renames/copies: https://git-scm.com/docs/git-status
    headers = {}
    counts = dict(staged=0, unstaged=0, untracked=0, conflicts=0)
    records = iter(output.split('\0'))
    for record in records:
        if record.startswith('# '):
            key, _, value = record[2:].partition(' ')
            headers[key] = value
        elif record.startswith(('1 ', '2 ')):
            xy = record.split(' ', 2)[1]
            counts['staged'] += xy[0] != '.'
            counts['unstaged'] += xy[1] != '.'
            if record.startswith('2 '):
                next(records)  # Original filename, which may resemble a status record.
        elif record.startswith('u '):
            counts['conflicts'] += 1
        elif record.startswith('? '):
            counts['untracked'] += 1
    return headers, counts


def _attention(headers, counts, ahead, behind):
    flags = []
    if counts['conflicts']:
        flags.append('resolve conflicts')
    if ahead and behind:
        flags.append('diverged')
    if any(counts[key] for key in ('staged', 'unstaged', 'untracked')):
        flags.append('uncommitted work')
    if behind:
        flags.append('behind upstream')
    if ahead:
        flags.append('unpushed commits')
    if headers['branch.oid'] == '(initial)':
        flags.append('no commits')
    if headers['branch.head'] == '(detached)':
        flags.append('detached HEAD')
    elif 'branch.upstream' not in headers:
        flags.append('no upstream')
    if 'branch.upstream' in headers and ahead is None and headers['branch.oid'] != '(initial)':
        flags.append('upstream missing')
    if int(headers.get('stash', 0)):
        flags.append('stashed work')
    return '\n'.join(flags) or 'up to date'


def status(repo_path):
    """Show work needing attention, branch sync, tags, and the last commit."""
    context = Context(repo_path)
    root = context.output(['git', 'rev-parse', '--show-toplevel'],
                          stderr=subprocess.STDOUT).rstrip('\n')
    if Path(root).resolve() != context.root:
        raise ValueError('Configure the repository root, not a directory inside another repo')
    output = context.output(['git', '--no-optional-locks', 'status', '--porcelain=v2', '-z',
                             '--branch', '--ahead-behind', '--show-stash', '--renames',
                             '--untracked-files=all', '--ignore-submodules=none'],
                            stderr=subprocess.STDOUT)
    headers, counts = _worktree_status(output)
    ahead, behind = None, None
    if 'branch.ab' in headers:
        ahead, behind = (int(value[1:]) for value in headers['branch.ab'].split())
    tags, nearest_tag, commits_since_tag, last_commit = [], None, None, None
    if headers['branch.oid'] != '(initial)':
        tags = context.output(['git', 'tag', '--points-at', 'HEAD', '--sort=refname'],
                              stderr=subprocess.STDOUT).splitlines()
        description = context.output(['git', 'describe', '--tags', '--long', '--always', 'HEAD'],
                                     stderr=subprocess.STDOUT).strip()
        parts = description.rsplit('-', 2)
        if len(parts) == 3 and parts[1].isdigit() and parts[2].startswith('g'):
            nearest_tag, commits_since_tag = parts[0], int(parts[1])
        last_commit = context.output(['git', 'log', '-1', '--no-show-signature',
                                      '--format=%h %cI %s'], stderr=subprocess.STDOUT).rstrip('\n')
    return [
        ('attention', _attention(headers, counts, ahead, behind)),
        ('branch', headers['branch.head']),
        ('clean', not any(counts.values())),
        *counts.items(),
        ('stashes', int(headers.get('stash', 0))),
        ('upstream', headers.get('branch.upstream')),
        ('ahead', ahead),
        ('behind', behind),
        ('tags', tags),
        ('nearest_tag', nearest_tag),
        ('commits_since_tag', commits_since_tag),
        ('last_commit', last_commit),
    ]


def _stop_command(process):
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    process.wait()


def command(repo_path, *, text, output_limit=1000, timeout=120, env=None):
    """Run a shell command, returning its exit code and a capped output prefix."""
    context = Context(repo_path) if env is None else Context(repo_path, env=env)
    timed_out = False
    # A file keeps noisy children from filling memory or leaking into JSON stdout.
    # Only the capped prefix is read; the temporary file is always removed.
    with tempfile.TemporaryFile(mode='w+', encoding='utf-8', errors='replace') as output:
        with subprocess.Popen(['/bin/sh', '-c', text], cwd=context.root, env=dict(context.env),
                              stdin=subprocess.DEVNULL, stdout=output, stderr=subprocess.STDOUT,
                              start_new_session=True) as process:
            try:
                process.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                _stop_command(process)
                timed_out = True
            except BaseException:
                _stop_command(process)
                raise
            exit_code = 124 if timed_out else process.returncode
            if exit_code < 0:
                exit_code = 128 - exit_code
        output.seek(0)
        captured = output.read(output_limit + 1)
    truncated = len(captured) > output_limit
    values = [('exit_code', exit_code)]
    if captured:
        values.append(('output', captured[:output_limit] + ('\n... [truncated]' if truncated else '')))
    if truncated:
        values.append(('truncated', True))
    if timed_out:
        values.append(('timed_out', True))
    return values


def bind_command(text, *, output_limit=1000, timeout=120, env=None):
    """Validate CLI options before any repo runs, then bind a one-path recipe."""
    if not isinstance(text, str) or not text.strip():
        raise ValueError('The command recipe requires a nonempty --command string')
    if output_limit < 1:
        raise ValueError('--output-limit must be at least 1 character')
    if not math.isfinite(timeout) or timeout <= 0:
        raise ValueError('--timeout must be a positive, finite number of seconds')
    return partial(command, text=text, output_limit=output_limit, timeout=timeout, env=env)


# Each recipe is ONE function: recipe(absolute_repo_path) -> [(key, value), ...].
# The CLI binds the command recipe's options before passing its single path.
RECIPES = {
    'status': status,
    'command': command,
}


def repository_paths(context, repositories=None):
    repositories = REPOS if repositories is None else repositories
    return {name: context.path(Path(path).expanduser()) for name, path in repositories.items()}


def _values(pairs):
    if not isinstance(pairs, list):
        raise ValueError('A recipe must return a list of (key, value) pairs')
    values = {}
    for pair in pairs:
        if not isinstance(pair, (tuple, list)) or len(pair) != 2:
            raise ValueError('Each recipe result must be a (key, value) pair')
        key, value = pair
        if not isinstance(key, str) or not key.strip():
            raise ValueError('Result keys must be nonempty strings')
        if key in values:
            raise ValueError(f'Duplicate result key: {key}')
        values[key] = value
    try:
        json.dumps(values, allow_nan=False)
    except (TypeError, ValueError) as error:
        raise ValueError('Result values must be JSON serializable, with finite numbers') from error
    return values


def _error_text(error):
    parts = [f'{type(error).__name__}: {error}']
    if isinstance(error, (subprocess.CalledProcessError, subprocess.TimeoutExpired)):
        for value in (error.output, error.stderr):
            if value:
                parts.append(value.decode(errors='replace') if isinstance(value, bytes) else value)
    return '\n'.join(parts).rstrip()


def run_recipe(context, name, *, selected=None, repositories=None, recipes=None):
    """Call one recipe per repo, collecting values and errors without prompts."""
    repositories = REPOS if repositories is None else repositories
    recipes = RECIPES if recipes is None else recipes
    if name not in recipes:
        raise ValueError(f'Unknown recipe: {name}. Available: {", ".join(recipes)}')
    recipe = recipes[name]
    if not callable(recipe):
        raise ValueError(f'Recipe {name} must be a function')
    names = list(repositories) if selected is None else list(dict.fromkeys(selected))
    unknown = [repository for repository in names if repository not in repositories]
    if unknown:
        raise ValueError(f'Unknown repositories: {", ".join(unknown)}')
    if not names:
        raise ValueError('No repositories selected')

    report = {'recipe': name, 'results': []}
    for repository in names:
        result = {'repo': repository, 'values': {}, 'error': None}
        try:
            path = context.path(Path(repositories[repository]).expanduser())
            if not path.is_dir():
                raise ValueError(f'Repository directory does not exist: {path}')
            # A recipe returns data. Accidental Python prints are diagnostics,
            # so they cannot corrupt the table or the JSON document on stdout.
            with redirect_stdout(sys.stderr):
                result['values'] = _values(recipe(path))
        except KeyboardInterrupt:
            result['error'] = 'Interrupted'
            report['interrupted'] = True
        except Exception as error:
            result['error'] = _error_text(error)
        report['results'].append(result)
        if report.get('interrupted'):
            break
    return report
