# Repository automation

Run the standard-library-only `scripts` package from the checkout root with
Python 3.9 or newer. No installation or virtual environment is needed:

```sh
python3 -m scripts --help
python3 -m scripts repos --help
python3 -m scripts repos list
python3 -m scripts repos recipes
```

Commands use `python3 -m scripts <group> <command>`. For example, `repos` is the
group and `status` is the command in `python3 -m scripts repos status`.
Help and argument errors do not import job implementations.

## Kit command group

Kit's existing development, engine, runtime, and bundle commands remain available:

```sh
python3 -m scripts kit --help
python3 -m scripts kit runtime config
python3 -m scripts kit bundle verify --bundle /path/to/release-bundle
```

Read [the Kit automation guide](kit/README.md) for the full command tree, prerequisites,
artifact verification, rollback, and operational effects. Kit-specific rollback helpers
live in `scripts/kit/workspace.py`; the shared `core` and `repos` files match Toolai.
`task check:python` runs both shared and Kit job tests, plus the existing blueprint/runtime
unit suites. The configured Toolai default checks already include this Task job.

## Local repository recipes

Run one named recipe across the hardcoded repositories. `status` highlights work
needing attention, working tree changes, upstream sync, tags, and the last commit.
It does not fetch from remotes or modify the repositories.

```sh
python3 -m scripts repos list
python3 -m scripts repos recipes
python3 -m scripts repos run status
python3 -m scripts repos run status --repo hummus --repo kit
python3 -m scripts repos status                 # Shorthand for run status
```

The status recipe returns these fields:

| Field | Meaning |
| --- | --- |
| `attention` | Flags conflicts, diverged history, uncommitted work, behind/unpushed commits, missing upstreams, detached HEAD, no commits, or stashed work. Multiple flags appear on separate lines. |
| `branch`, `clean` | Current branch (or `(detached)`) and whether the working tree and index are clean. A clean repo can still have unpushed commits or stashes. |
| `staged`, `unstaged`, `untracked`, `conflicts` | File counts; conflicted files are counted separately. A file with staged and unstaged changes appears in both counts. Untracked files include files inside new directories. |
| `stashes` | Number of stash entries. |
| `upstream`, `ahead`, `behind` | Tracked upstream and commit counts relative to its locally cached ref. Counts are `null` if comparison is unavailable. These are not live remote checks. |
| `tags` | All local tags pointing at HEAD, including annotated and lightweight tags. |
| `nearest_tag`, `commits_since_tag` | Reachable tag selected by `git describe --tags` and the number of additional commits; both are `null` if no tag is reachable. This is not necessarily the highest version tag or a tag from another branch. |
| `last_commit` | Short commit hash, ISO commit timestamp, and subject; `null` before the first commit. |

Wide tables split into column groups with the repository name repeated in each
group. JSON retains all fields in a single result object for each repository.

The `command` recipe runs a quoted shell command in each selected repository:

```sh
python3 -m scripts repos run command --command 'git log -1 --oneline'
python3 -m scripts repos run command --command 'git diff --stat' --repo hummus --json
python3 -m scripts repos run command --command 'git tag | head -5' --output-limit 500 --timeout 30
```

The string runs through `/bin/sh -c`, so quoting, pipes, and command chains work.
Input is noninteractive. Each row contains `exit_code` and, when present, combined
stdout/stderr in `output`. Output is capped at the first 1,000 characters per repo
by default; `--output-limit` changes that cap. Truncated output gets a marker and
`truncated: true` in the result. These options apply equally to table and JSON
output. A silent command returns just its exit code.

The default timeout is 120 seconds per repo, configurable with `--timeout`.
Timeout stops the command's process group and reports `exit_code: 124` and
`timed_out: true`, retaining its capped output. Interruption also stops the process
group. Command output is staged in a temporary file, with only the capped prefix
read into memory; the file is removed afterward. Other repos still run after a
nonzero command exit. The overall CLI exits `1` if any command fails or times out.
The per-repo `error` remains `null` when execution completes with a nonzero exit;
inspect `values.exit_code`. Launch or recipe exceptions use `error` as usual.

Edit `REPOS` and `RECIPES` in [`repos/jobs.py`](repos/jobs.py). The configured
repos are `../hummus`, `../millway`, `../kit`, `../inframill`, and `../toolai`.
Relative paths resolve from this checkout; absolute paths and `~` paths
also work.
Each recipe is a single ordinary Python function receiving an absolute `Path`
and returning a list of `(key, value)` pairs. For the `command` recipe, the CLI
binds its string and options before calling the function with the repo path:

```python
def files(repo_path):
    """Check for common project files."""
    return [
        ('readme', (repo_path / 'README.md').is_file()),
        ('go_module', (repo_path / 'go.mod').is_file()),
    ]


RECIPES = {
    'status': status,
    'files': files,
}
```

Keys must be unique, nonempty strings. Values must be JSON serializable, with
finite numbers. An empty result list is valid. Each repository becomes a table
row; returned keys become columns in first-seen order. Missing keys appear as
blank cells. Multiline and long values wrap inside cells without truncation.

Recipes return data rather than printing it. Accidental Python prints are sent
to stderr. For subprocesses, capture output explicitly, for example using
`Context(repo_path).output([...], stderr=subprocess.STDOUT)` with the shared
helper's default 120-second timeout, or an explicit `timeout=...`. Recipes take
only the repo path; this helper creates its own environment snapshot. Do not
change process cwd, `os.environ`, or configuration globals inside recipes.

Codex and other callers can use the same CLI without interactive prompts:

```sh
python3 -m scripts repos list --json
python3 -m scripts repos recipes --json
python3 -m scripts repos run status --repo hummus --json
```

Discovery returns `{"repositories": [{"name": "...", "path": "..."}]}` or
`{"recipes": [{"name": "...", "description": "..."}]}` without executing
recipes. A recipe run returns one JSON document on stdout, preserving value
types:

```json
{
  "recipe": "status",
  "results": [
    {
      "repo": "hummus",
      "values": {"branch": "main", "clean": true},
      "error": null
    }
  ]
}
```

Per-repo failures have empty `values` and an `error` string. Other repos still
run; table output adds an Error column. Unknown recipes or repository names
are rejected before execution. In JSON mode these validation errors return
`{"recipe": "...", "results": [], "error": "..."}`. Exit status is `0` when
all repos succeed and `1` for recipe or selection failures. Interruption stops
execution, retains completed results, adds `"interrupted": true`, and exits
`130`. Argument parsing errors exit `2` with normal usage text on stderr.

## Layout

```text
scripts/
  __init__.py            package marker
  __main__.py            python3 -m scripts entry point
  cli.py                 explicit group registration and exit handling
  test_cli.py            command routing, help, argument and exit-status tests
  core/
    context.py           checkout paths and immutable environment snapshots
    process.py           argument-list subprocesses, timeouts and diagnostics
    workspace.py         unique temporary staging and cleanup
    test_*.py            shared infrastructure tests
  kit/
    cli.py               Kit command groups and argument adapters
    workspace.py         Kit output installation and rollback
    test_*.py            Kit job and command interface tests
  repos/
    cli.py               recipe selection and table/JSON output
    jobs.py              hardcoded paths, named recipe functions and execution
    table.py             complete multiline tables without dependencies
    test_*.py            recipe, CLI, table and Git integration tests
```

`core` stays independent of groups and third-party packages. A group
uses its own modules and shared primitives. Keep CLI adapters separate from job
functions, and import implementations inside the handlers.

## Invocation context and staging

CLI handlers receive a `scripts.core.context.Context` explicitly and pass it to
jobs that need it. Repository recipe functions receive only their repository
path, as described above. The launcher derives the checkout root from its own
location and snapshots the environment once per invocation. Python callers can
pass another root and environment for fixtures:

```python
from scripts.core.context import Context
from scripts.core.workspace import workspace


def execute(context: Context):
    with workspace(context.root / '.cache/scripts', prefix='example-') as stage:
        context.run(['some-tool', '--output', stage / 'result.json'],
                    env={'TOOL_MODE': 'local', 'UNWANTED_OVERRIDE': None})
        # Validate staged files before installing deliberate outputs.
```

`context.path(...)` resolves relative paths from the checkout root. Subprocesses
default to that working directory and the context's environment snapshot.
Per-command overrides affect only the child; `None` removes a variable.
`context.output(...)` captures stdout. `context.run(...)` also accepts options
such as `capture_output`, `check` and `timeout`, with a 120-second default timeout.
Commands execute argument lists without a shell. Do not change process cwd,
`os.environ` or module globals to configure jobs.

Workspaces are unique and cleaned on success, failure and interruption. They do
not provide an OS sandbox or locking for stable output paths. Validate staged
outputs before installing them and preserve applicable rollback and checksum
contracts. Keep builds, checks, setup and publication explicit.

The launcher, shared code and repo jobs use only the standard library.
The repository status recipe additionally needs Git on PATH.
Future jobs that need third-party packages should explicitly provision a dedicated
virtual environment using repository-owned pins and invoke its interpreter in a
subprocess. Help and unrelated groups must not install or import those packages.

## Add a group or command

1. Create `scripts/<group>/__init__.py` and `cli.py` with
   `register(groups)`, then register it explicitly in `scripts/cli.py`.
2. Declare commands and flags in the group CLI. Set a handler accepting
   `(args, context)` and defer job imports until that handler runs. Add subgroups
   when useful.
3. Put ordinary job functions in their own modules and pass invocation context
   explicitly. Reuse `core` for subprocesses and temporary workspaces.
4. Keep `test_*.py` beside the modules they cover and include `__init__.py` in
   packages for unittest discovery. Use temporary fixture roots and mock external
   installation, services and publication boundaries.

## Validation and command results

```sh
python3 -B -m unittest discover -s scripts -t . -p 'test_*.py'
python3 -B -m unittest scripts.repos.test_cli
toolai check
```

The default Toolai check workflow includes Python unit tests through the
configured `check:python` Task job. Task is optional and is not needed here. Tests use temporary fixtures
and the running Python interpreter, with no network access. Git status integration
tests use temporary Git repositories and are skipped when Git is unavailable.

Successful commands return zero; argument errors return two. Expected job errors
write diagnostics to stderr and return one. Child-process failures preserve the
child exit status (signal exits use `128 + signal`); interruption returns 130.
The `repos` group aggregates recipe failures into exit status one as described
above. Results go to stdout, while command traces and diagnostics go to stderr.
