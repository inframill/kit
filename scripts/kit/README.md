# Kit automation

`scripts` is the repository's standard-library-only Python automation package.
Run it from the checkout root with Python 3.9 or newer:

```sh
python3 -m scripts --help
python3 -m scripts kit --help
python3 -m scripts kit runtime config
python3 -m scripts kit bundle verify --bundle /path/to/release-bundle
```

Task exposes convenient focused jobs backed by these commands. Toolai owns Go
tooling and aggregate workflows. Build, check, setup and publication remain
explicit operations. For example, `kit bundle verify` only verifies the existing
bundle; `kit bundle push` verifies and publishes it without building.

## Layout and ownership

```text
scripts/
  __main__.py             module entry point
  cli.py                  group registration and exit handling
  test_cli.py             shared repository command interface tests
  core/
    context.py            checkout paths and immutable environment snapshots
    process.py            subprocess execution and diagnostics
    workspace.py          unique temporary staging and cleanup
    test_*.py             shared infrastructure tests
  kit/
    cli.py                Kit's command tree and argument adapters
    workspace.py          Kit output installation and rollback
    project.py            Kit paths, versions and configuration
    engine.py             sibling engine verification and import
    dev.py                Runmill and runtime pin synchronization
    runtime.py            local tool setup and configuration generation
    bundle.py             image and release packaging, verification, publication
    artifacts.py          executable format and architecture verification
    test_*.py             Kit job tests
    _test_fixtures.py      metadata-only fixture checkouts for tests
  repos/                  sibling repository recipes and their tests
```

Groups depend on `core` and their own modules. `core` has no group
dependencies. Register groups explicitly in `scripts/cli.py`; there is no
filesystem scanning or plugin loading. CLI modules declare arguments and defer
implementation imports until dispatch, so help and invalid arguments need no
external tools or prepared artifacts.

`runtime/tools.py` remains a standalone helper shared by local tool setup and the
image build, which copies it into the container without the automation package.
Blueprint and acceptance checks remain under the repository's `tests/` directory.

## Commands

| Command after `python3 -m scripts kit` | Effect |
| --- | --- |
| `dev sync` | Import Runmill, verified sibling binaries, engine pins and notices |
| `engine copy` | Import existing sibling binaries matching the current runtime pin |
| `engine build` | Build binaries in the clean sibling checkout |
| `runtime setup` | Install pinned OpenTofu and an isolated Ansible environment |
| `runtime config [--output PATH]` | Print or write native development configuration |
| `bundle capture` | Record source inputs before building |
| `bundle image` | Package prepared binaries and build/load the local image |
| `bundle release` | Assemble a release bundle from the prepared image |
| `bundle verify [--bundle PATH]` | Verify an existing bundle and print its manifest |
| `bundle load [--bundle PATH]` | Verify and load an existing bundle |
| `bundle push [--bundle PATH]` | Verify and publish an existing bundle |

Relative paths, including environment path overrides, resolve from the checkout
root. Absolute paths select external locations explicitly. The checkout root is
derived from this package's location. Python callers can pass a `Context` with
another root and environment, including a temporary fixture checkout.

Existing `python3 -m scripts.kit.{dev,engine,runtime,bundle}` entry points forward
to the same command tree. Engine still defaults to `copy`, and dev to `sync`.
Existing read helpers in `scripts.kit.project` also remain available to callers.
Unsupported options now fail at their specific command instead of being ignored.

Successful commands return zero; argument errors return two. Expected job failures
print a diagnostic to stderr and return one, subprocess failures preserve the child
exit status, and interruption returns 130. JSON results stay on stdout; command
traces and failure diagnostics go to stderr.

## Execution and isolation

Pass a `Context` through job functions instead of changing `os.environ`, changing
the process working directory, or modifying module globals:

```python
from scripts.core.context import Context
from scripts.core.workspace import workspace


def execute(context: Context):
    with workspace(context.root / '.build', prefix='example-') as stage:
        context.run(['some-tool', '--output', stage / 'result.json'],
                    env={'TOOL_MODE': 'local', 'UNWANTED_OVERRIDE': None})
        # Validate and install deliberate outputs before leaving the workspace.
```

Each context snapshots its environment. Per-command overrides affect only the
child process; a `None` value removes a variable. Subprocesses use the checkout
root as their default working directory and execute argument lists directly.
`context.output(...)` captures stdout; `context.run(...)` supports subprocess
options such as `capture_output`, `check` and `timeout`. Commands time out after
120 seconds by default. Jobs explicitly allow longer for builds, dependency
installation, image loading and publication.

Workspaces are unique per operation and cleaned on success, failure or interruption.
Deliberate outputs such as `bin/`, installed tool caches and release bundles have
their existing stable locations. Operations targeting those same outputs should
run sequentially; temporary staging does not provide locking or an OS sandbox.
Use `kit.workspace.install` to replace validated staged outputs together, with
an empty backup directory inside the workspace. Engine/source synchronization and
image/release installation restore previous outputs on failure or interruption.
Image assembly also verifies Docker's loaded digest before replacing the prepared
bundle. Runtime setup stages OpenTofu and backs up both installed tools while
Ansible is installed and checked at its final path, preserving virtualenv launchers
and restoring the previous tools if setup fails. `kit.workspace.replace_outputs`
provides this rollback scope for jobs that must write at their final paths.

Keep the launcher and shared code free of third-party Python dependencies. A job
that needs extra packages should explicitly provision its own versioned virtual
environment and invoke its interpreter in a subprocess. Ansible setup already
follows this pattern. Help, checks and unrelated groups must not provision
those environments or import their packages.

## Add a group or command

1. Add `scripts/<group>/__init__.py` and `cli.py` with
   `register(groups)`. Add a group parser, subcommands and a handler via
   `set_defaults(handler=...)`; handlers accept `(args, context)`.
2. Call its registration function in `scripts/cli.py`. Import job implementations
   inside handlers. Keep argument parsing separate from the job's ordinary Python
   functions, which accept a context explicitly.
3. Add `test_*.py` files beside the modules they cover in `scripts/<group>/`.
   Keep `__init__.py` in each package so unittest discovers nested suites. Use
   temporary fixture roots and mock external tool boundaries. Share primitives
   in `core` when more than one group needs them.
4. Add Task shortcuts when useful and document any deliberate side effects.
   Keep aggregate Go/build/check ownership in Toolai.

## Unit tests

```sh
python3 -B -m unittest discover -s scripts -t . -p 'test_*.py'
python3 -B -m unittest scripts.core.test_process
task check:python
toolai check
```

`task check:python` runs automation tests and the repository's remaining Python
unit tests; the default `toolai check` includes that task. Automation tests use
temporary metadata/files, disposable local Git fixtures, and the running Python
interpreter. External installation, build and publication commands are mocked;
unit tests require no Docker, network downloads, installed infrastructure tools,
credentials or sibling checkout. Acceptance suites remain separately selected.
