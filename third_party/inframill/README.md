# Inframill

Inframill is a tool-neutral Go control plane. A **Blueprint** is a reusable
implementation. An **Instance** is a named deployment with an immutable
`project/name` and a separate UUID **InstanceID**. External Drivers supply
commands; Inframill owns validation, queues, logs, artifacts and history.
The [debug Blueprint](blueprints/debug/manifest.yaml) exercises these features
locally without infrastructure tools.

Inframill is prerelease. The [baseline](specs/0000-baseline/spec.md) and
[architecture decision](docs/decisions/0000-architecture.md), as amended by accepted
change specs and ADRs, define the current contract. Interfaces and stored formats
may change without backward compatibility or migration tooling. Only the current
schema is supported; unsupported data is rejected and preserved. See
[SPEC-0012](specs/0012-prerelease-version-reset/spec.md) and
[ADR-0010](docs/decisions/0010-prerelease-version-reset.md).

## Build and configure

Build, install and distribute one Go executable using Go, Task and basic shell utilities:

```sh
task build                         # dist/inframill
task install                       # ~/.local/bin/inframill
task install INSTALL_DIR=/custom/bin
task release VERSION=0.0.0-dev
./dist/inframill driver debug info
```

The [root Taskfile](Taskfile.yml) invokes Go and install directly.
`OUTPUT` overrides the native build destination; install builds before replacing
the installed executable. Add the installation directory to PATH yourself.
Release requires a filename-safe `VERSION`, producing
`dist/<version>/inframill_<version>_<os>_<arch>` for darwin/linux on amd64/arm64.
`DIST_DIR` overrides the parent. Existing release directories reject; failed
builds may leave partial output. Publishing is separate. Builds disable CGO;
Go may download dependencies or its toolchain.

Core and debug Driver versions start at `0.0.0-dev`. Release embeds `VERSION` in
the core; the Driver version is independent and can be set in custom builds with
`-ldflags="-X main.debugVersion=0.0.0-test"`. Driver protocol is `1`; command SQLite stores use
`user_version=1`, and local result records use `local-result/v1`. Instance metadata
uses `inframill.dev/instance/v1` and `storage_layout: "uuid-v1"`.
Distribution has no container or tool installation tasks.

`server`, local `exec` and `driver list` require `./.inframill.yaml`, or `--config FILE`.
The bundled [.inframill.yaml](.inframill.yaml) explicitly selects the debug subcommand:

```yaml
engine:
  dataDir: .inframill
  drivers:
    debug:
      path: ./dist/inframill
      args: [driver, debug]

handlers:
  - type: connect-engine
    settings:
      path: /inframill.v1.EngineService/
```

`engine.dataDir` defaults to `.inframill`, with relative paths resolved from the working
directory. It contains `blueprints/` and `instances/<InstanceID>/`. Driver paths
are absolute or relative to the config file's directory; there is no PATH lookup.
Prefix `args` are literal strings placed before `info`, `validate` or `run COMMAND`.
An external Driver can use `{path: /opt/drivers/example}`. Empty or omitted
`engine.drivers` exposes only `builtin.delete`. An omitted or empty `engine`
section uses both defaults. Native commands still require exactly one
`connect-engine` handler; its only setting is `path`.

At startup Inframill probes every configured executable with `info`. The YAML
object key is the configured Driver ID; it selects the Blueprint configuration,
workspace directory and command prefix. It is independent of the executable's
reported name/version. Multiple IDs can select the same executable or different
versions. IDs match `[a-z0-9][a-z0-9-]*`; `builtin` is reserved.

Missing executables, failed probes and invalid metadata remain **offline** entries
with a reason. Healthy entries remain usable and the daemon can start. An Instance
requiring an unknown or offline Driver cannot initialize; a command requiring
one cannot prepare. Structural configuration errors still reject startup.
Local exec probes each invocation and rejects an unavailable selected Driver before
creating its workspace. The debug subcommand uses this same subprocess protocol.

The public [driver.Manager](pkg/driver/manager.go) owns this behavior:

```go
type Manager interface {
    List(context.Context) ([]Entry, error)
    Validate(context.Context, ValidationRequest) error
    Prepare(context.Context, CommandRequest) (PreparedCommand, error)
}
```

Construct it with `driver.New(ctx, configurations, environ)`. `List` returns sorted,
independent snapshots with ID, executable configuration, status, reason, metadata
and check time. Connected means the latest `info` succeeded; it does not represent
a persistent connection or infrastructure health. `List` reads cached health.
`Validate` checks the Blueprint's `drivers` section, rendered source and Values.
`Prepare` selects `DriverID` and the local `Command`, validates the request, and
returns a `PreparedCommand` whose `Execute(ctx)` performs the command. Both
methods refresh selected entries, allowing repaired executables to recover.
The shared Engine exposes a health summary through
`Engine.ListDrivers(ctx) ([]inframill.DriverInfo, error)` and the Connect API's
`EngineService.ListDrivers`. Summaries contain configured ID, reported name/version,
status (`connected` or `offline`), offline reason and last check time. Empty
configuration returns no Drivers; `builtin.delete` is a command, not a Driver.
API listing reads the daemon's cached health without triggering probes.

Inspect Drivers locally without starting or contacting an API:

```sh
./dist/inframill driver list
./dist/inframill driver list --config alternate.yaml -o json
./dist/inframill driver list --columns id,status,reason
```

`driver list` constructs the same Manager, Backend and Engine used by local exec
and calls the Engine directly. Each invocation probes configured executables with
`info`; it does not open Instance storage, acquire workspace locks or fetch
Blueprints. It can therefore run while the daemon owns the same data root, but its
health results come from its own probes and config, not the daemon's snapshot.
Offline Drivers remain visible and do not cause a nonzero exit code.

Tables show all summary fields by default, with the cell width limit below. JSON
returns `{"drivers": [...]}` with `id`, `name`, `version`, lowercase `status`, `reason` and `checkedAt` timestamps;
name/version are empty when offline. CLI output/color defaults apply, and help
works without config or probes. Endpoint/timeout flags are unsupported and their
environment defaults are ignored. See [SPEC-0007](specs/0007-driver-health/spec.md)
and [ADR-0005](docs/decisions/0005-driver-health-interfaces.md).

For example, configuring `local-debug: {path: ./dist/inframill, args: [driver, debug]}`
selects commands such as `local-debug.echo`, while executable metadata still
reports `debug`. The Blueprint must use `drivers: {local-debug: {}}` and place
Driver workspace files under `template/local-debug/`. Driver-specific
`env` configuration is deferred; the existing environment policy below applies.

An optional `cli` section in the same file supplies user defaults:

```yaml
cli:
  endpoint: http://127.0.0.1:8080
  timeout: 2m
  output: table
  color: auto
```

Daemon management and history tolerate an absent default file. Explicit `--config`
requires a readable file; an existing malformed file rejects. Remote settings are
read without validating engine or server settings, resolving their environment references,
probing Drivers or opening storage. CLI-only YAML suffices for remote commands.
CLI keys must be unique known strings. Native `server`, `exec` and `driver list`
require complete application configuration and retain harness environment resolution.

The YAML also configures the listener and logger. Numeric `main.port` defaults to
8080; the server binds all interfaces and has no authentication. The Connect path
must match the compiled service shown above. Supported harness settings resolve
exact `${NAME}` references from the startup environment. Missing or invalid config
fails before storage, fetching, result reservation or serving.

## Local execution

Run the [debug Instance config](examples/debug.json) without a daemon:

```sh
./dist/inframill exec debug.echo \
  --instance examples/debug.json --blueprint-dir blueprints/debug \
  --name examples/debug
./dist/inframill history --workspace .inframill --name examples/debug
./dist/inframill history --workspace .inframill --name examples/debug --command COMMAND_UUID -o json
```

Instance JSON contains exactly a nonempty `blueprint` string and a `values` object.
Only Values reach templates and Drivers. Supply command Args separately with
`--args-file FILE`. Local JSON inputs must be regular, non-symlink files at most
1 MiB; stdin is unsupported.

Without `--blueprint-dir`, exec resolves the config reference from the shared cache
and fetches it if absent. Cached references work offline without automatic refresh;
corrupt content rejects. A local override snapshots the source and ignores the
config reference for selection. Acquisition failure creates no Instance or Command.

`--name project/name` defaults to `local/default` on every invocation. Exec creates
an absent name or reuses its UUID, files and history; changed Blueprint identity or
normalized Values reject before admission. Choose a different name for different
inputs. Local processes lock the data root and fail promptly if busy. Use separate
roots for concurrent daemon and local execution.

History requires `--workspace DIR` and an existing name, but no config, source,
Values, Drivers or daemon. It defaults to tables; `--output/-o json` returns complete
records. Pagination uses `--limit` (1–100, default 50) and `--cursor`.

### Credentials and output

Drivers resolve references from `INFRAMILL_SECRET_<UPPERCASE_REFERENCE>`. Keep
values out of Args, Values, templates, metadata and artifacts. Local exec can load
an explicit `--env-file PATH`; inherited variables win, including empty values.
The file must be regular, non-symlink, UTF-8, at most 1 MiB and outside data/source/cache.
It is never discovered automatically or loaded into the process environment.

Dotenv supports single-line `KEY=VALUE`, optional `export`, comments, whitespace,
empty values, single/double quotes and CRLF. Duplicate keys use the last value.
Single quotes stay literal; double quotes decode common escaped characters.
Substitutions and backticks are never evaluated. Invalid syntax fails with safe
line-number diagnostics. See [REQ-020](specs/0000-baseline/spec.md) for exact syntax.

Tool stdout/stderr merge into Driver stderr, streamed live and saved in `stderr.log`.
The verified JSON result goes to stdout and `stdout.log`; failed or malformed
responses publish no result. Known secrets are redacted before logs/results.
CLI diagnostics go to stderr.

`--result-file NEW_FILE` exports a complete `inframill.dev/local-result/v1` record.
Its parent must exist and its path must be outside data/source/cache/input paths.
The new file is reserved privately before effects and published atomically.
Pre-admission rejection has no Command ID; an unconfirmed admitted outcome is
`unknown`. Output-file failure exits 3 even if command effects occurred.

Exit codes: 0 success, 1 command/verification failure, 2 invalid input,
3 runtime/ownership/output failure, 130 interruption. SIGINT/SIGTERM cancels local
work and its Unix process group. Output writes have a 30-second bound. Restart
marks unfinished records interrupted without replay; cancellation does not imply
rollback. Failed commands retain history and print their stored cause.

## Daemon and management CLI

Start the daemon from the directory containing the required application config:

```sh
task run
# Or use the compiled CLI:
./dist/inframill server
./dist/inframill server --config alternate.yaml
```

Fetch Blueprints explicitly before daemon initialization:

```sh
./dist/inframill blueprint fetch ghcr.io/your-org/example-cloud:1.2
./dist/inframill blueprint list
./dist/inframill blueprint show ghcr.io/your-org/example-cloud@sha256:PINNED_DIGEST
./dist/inframill command list
./dist/inframill command show debug.record
./dist/inframill instance init example/dev --file instance.json
./dist/inframill instance list --project example --limit 20
./dist/inframill instance get INSTANCE_UUID
./dist/inframill instance exec INSTANCE_UUID debug.record --args-file args.json --wait
./dist/inframill command execution list --instance-id INSTANCE_UUID --status running
./dist/inframill command execution get INSTANCE_UUID COMMAND_UUID
./dist/inframill command execution wait INSTANCE_UUID COMMAND_UUID --timeout 10m
./dist/inframill command execution stdout INSTANCE_UUID COMMAND_UUID
./dist/inframill command execution stderr INSTANCE_UUID COMMAND_UUID --offset 0 --limit 65536
./dist/inframill instance delete INSTANCE_UUID --wait
```

Management uses UUIDs returned by initialization. The Instance and Args file flags
accept `-` for stdin and cap input at 1 MiB. RPC settings resolve explicit flags, then `INFRAMILL_ENDPOINT` /
`INFRAMILL_TIMEOUT`, then the YAML `cli` section, then built-in defaults. Invalid
lower-priority scalar settings can be overridden; YAML structure/type errors always
reject. Endpoint/timeout defaults do not affect native commands. CLI settings are
literal strings in the remote reader.

RPC flags are `--endpoint URL`
(default `http://127.0.0.1:8080`), `--timeout DURATION` (default `2m`) and
`--output/-o table|json`. Flags may follow the remote group name or surround leaf positional arguments;
`--` ends flag parsing. RPC flags are scoped to daemon command groups.

Structured responses default to ASCII tables. Human help, tables and diagnostics
use color on terminals, honoring nonempty `NO_COLOR` and `TERM=dumb`.
`--color auto|always|never` overrides that policy; JSON, raw logs, Driver protocol
and completion scripts remain free of presentation escapes. Prompts and pagers
are deferred: no command discovers `PAGER` or prompts for confirmation.

List commands accept `--columns KEY,...` in display order (tables only). Keys:

| List | Column keys |
| --- | --- |
| driver (local) | id, name, version, status, reason, checked_at |
| blueprint | reference, description |
| command | name, safety, description |
| instance | name, id, status, blueprint |
| command | id, command, status, created_at, started_at, ended_at |

Table headers and cells are limited to 20 terminal display columns per line,
including a trailing `...` when shortened. This applies to identifiers, timestamps,
error messages and other strings in both lists and details. Wide Unicode characters
count by display width. Use `-o json` to retrieve full identifiers and values.
Column selection is rejected for JSON and non-list commands. Pagination footers
and labeled nested JSON sections remain complete; raw logs are unchanged.
Explicit JSON preserves complete protobuf responses, and native Driver/history
JSON preserves complete records. See [SPEC-0008](specs/0008-table-cell-width/spec.md).
Log commands default to raw bytes; explicit JSON returns the envelope with base64
data, nextOffset and eof. Table mode is not supported for logs. Exec and server do not accept output flags.

Exec/delete acknowledge durable admission with a Command UUID; `--wait` polls its
outcome. Client cancellation does not cancel accepted daemon work. Optional
`--idempotency-key` (at most 200 characters) deduplicates matching command/Args
within an Instance; changed input conflicts, and a retry with matching input
returns the original ID before capacity checks. Without a key, each admission
creates a new Command. Deletion removes local state/history only; it never invokes external infrastructure deletion. Confirm it through Instance absence.

### Help, version and completion

```sh
./dist/inframill --help
./dist/inframill instance list --help
./dist/inframill instance --endpoint http://127.0.0.1:8080 list --columns name,id,status
./dist/inframill version          # same core version as --version
./dist/inframill completion zsh   # also bash, fish, powershell
```

Help, version and static completion bypass config and daemon access. Completion
scripts are printed for you to install. No arguments prints help to stderr and
exits 2; explicit help prints to stdout and exits 0.

## Blueprints and commands

A **command definition** describes an available action such as `debug.echo`.
Submitting a **command request** creates one **command execution**, identified by
its CommandID, with status, timestamps, logs and a result or error. The Backend
owns queuing and history and routes each command to its configured Driver.

The catalog API is `ListCommandDefinitions`; execution queries are
`GetCommandExecution` and `ListCommandExecutions`. Catalog responses contain
`commands`; execution responses contain `execution` or `executions`. Requests
use `command` to select the action. Local history JSON pages contain `executions`.
Command failures use `COMMAND_FAILED` and `COMMAND_CANCELLED`. See
[SPEC-0011](specs/0011-command-vocabulary/spec.md) and
[ADR-0009](docs/decisions/0009-command-vocabulary.md).

A package contains `manifest.yaml`, `schema.json` and `template/`. Manifest
`api_version` is `inframill.dev/blueprint/v1`; Driver config is under `drivers`.
The Blueprint name is not a manifest field.

```yaml
api_version: inframill.dev/blueprint/v1
description: Local feature testing
drivers:
  debug: {}
```

Driver configuration is opaque to the core. Only `.gotmpl` files are evaluated
with Go text/template and lose their suffix; `toJSON` emits typed JSON. Templates
receive Values. Missing keys, collisions, links and special files reject. Ordinary
files, empty directories and executable modes are preserved. Root `.inframill/`
and `runtime/` are excluded from snapshots and packages without traversal.
Schemas use offline JSON Schema Draft 2020-12 with unique, IPv4 CIDR,
disjoint-network and reference validators; external schema resources are rejected.
Every command declares its safety class, effects and verification behavior.

The CLI consumes published Blueprint packages. The former `package build` command
has been removed; package authoring and publishing are separate from the application.
For local development, `exec --blueprint-dir` uses a source directory directly.
Pulls are anonymous and use HTTPS unless a development registry is explicitly
listed in `INFRAMILL_HTTP_REGISTRIES`. Private registry login is unsupported.

Acquisition verifies archives and extracted source within two minutes. Limits are
1 MiB manifest, 1 KiB config, 64 MiB compressed layer, 256 MiB expanded data and
10,000 entries. A failed fetch preserves prior mappings. No package-deletion API
or automatic cache refresh is provided.

## Debug commands

The debug Driver uses empty manifest configuration (`debug: {}`). It runs locally,
without external tools or network access. `debug.echo` returns the supplied message,
Instance Values and binding. All command Args reject unknown fields and invalid
types. Inspect schemas and safety declarations with `inframill driver debug info`.

| Command | Args | Behavior |
| --- | --- | --- |
| `debug.echo` | Optional `message` string | Returns message, Values and binding |
| `debug.write-artifact` | Required `name`, `content` strings | Writes privately and verifies exact content by reading back |
| `debug.read-artifact` | Required `name` string | Returns artifact name and text content |
| `debug.record` | Optional `message`, `mode` strings | Saves a CommandID record before success, deliberate failure or blocking |
| `debug.logs` | Optional `stdout`, `stderr` strings | Emits both messages verbatim, in order, on the merged stderr log |
| `debug.fail` | Optional `message` string | Fails deliberately without artifact writes |
| `debug.wait` | Optional `durationMs` integer, 0–60000 | Waits for duration, or for a workspace `release` file when omitted |
| `debug.secret` | Optional `reference` string | Exercises known-secret redaction in both stderr and the JSON result |

Artifact write and record commands are `mutating`; the others are `read_only`
with their diagnostic effects declared. Artifacts use single filenames, private
atomic writes and regular-file checks. Debug text read/write content is limited
to 1 MiB. The shared adapter additionally enforces its 64 MiB artifact ceiling.
Known runtime secrets are rejected from artifact writes.

```sh
printf '%s\n' '{"name":"sample.txt","content":"hello"}' > /tmp/debug-args.json
./dist/inframill exec debug.write-artifact \
  --instance examples/debug.json --blueprint-dir blueprints/debug \
  --name examples/debug --args-file /tmp/debug-args.json

printf '%s\n' '{"name":"sample.txt"}' > /tmp/debug-args.json
./dist/inframill exec debug.read-artifact \
  --instance examples/debug.json --blueprint-dir blueprints/debug \
  --name examples/debug --args-file /tmp/debug-args.json
```

`debug.record` creates `artifacts/record-<CommandID>.json` containing `protocol: 1`,
`commandID`, `binding`, `values` and `arguments`. Its result contains `recorded`,
`artifact` and `commandID`. A duplicate CommandID rejects rather than overwriting
the existing record. Count these files to check execution counts across failures
and restarts. Admission validation creates no record.

Record mode comes from Args `mode`, then optional Instance Values `mode`, then
`success`. Use `{"mode":"fail"}` to fail **after** the record is published, or
`{"mode":"block"}` to wait after recording. This tests partial effects and
interruption without replay. Blocking record and wait commands emit
`waiting for release` and wait up to 60 seconds for the regular file
`<dataDir>/instances/<InstanceID>/workspace/debug/release`. The file is not
consumed; remove it before another barrier test. Local Ctrl-C interrupts the Driver.
For an intentional daemon crash, release any surviving debug barriers during test
cleanup; process loss does not imply rollback.

`debug.secret` uses Args `reference` or Values `secret_ref`, resolving
`INFRAMILL_SECRET_<UPPERCASE_REFERENCE>` only during execution. A missing or empty
secret fails. With a synthetic secret available, the result is
`{"secret":"[REDACTED]"}` and stderr contains `[REDACTED]`. Use this command
through `exec` or the daemon: the external host provides the redaction boundary.
Invoking the raw `driver debug run secret` protocol command directly bypasses that
boundary and emits the raw synthetic value. It never writes secret artifacts.

## Implementing a Driver

A Driver is an executable implementing three commands. Each invocation starts a
fresh process and exits when complete. Any language can implement the contract;
there is no required SDK, network listener or persistent session. The debug implementation
is [debugdriver](internal/debugdriver/driver.go), exposed through
[CLI dispatch](cmd/main.go). The wire types are in
[protocol.go](pkg/driver/protocol.go).

| Invocation | Input | Success output on stdout |
| --- | --- | --- |
| `DRIVER info` | None | Identity and command metadata |
| `DRIVER validate` | One JSON request on stdin | `{"valid":true}` |
| `DRIVER run COMMAND` | One JSON request on stdin | One JSON value containing the verified result |

Exit zero only on success. Write safe error diagnostics to stderr and exit nonzero
on failure. Stdout must contain exactly one JSON document, with no banners or
progress messages. Merge both output streams of any invoked tool into Driver
stderr. In Go, assign the tool's `Stdout` and `Stderr` to `os.Stderr`; capture a copy
privately if the Driver needs to parse tool JSON. Tool streams lose their original
stdout/stderr distinction, and cross-stream ordering is not guaranteed.

`info` is a cheap, effect-free startup ping. It must work without external tools, credentials or infrastructure access. For example, `example-driver info`
could return:

```json
{
  "name": "example",
  "version": "1.2.3",
  "protocol": 1,
  "commands": {
    "echo": {
      "description": "Return the supplied message",
      "argumentsSchema": {
        "type": "object",
        "properties": {"message": {"type": "string"}},
        "required": ["message"],
        "additionalProperties": false
      },
      "safety": "read_only",
      "effects": ["Reads the supplied arguments"],
      "verification": "Returned message equals the supplied message"
    }
  }
}
```

With configured ID `example`, the public command is `example.echo`; `run` receives
the local name `echo`. The reported name does not choose the command prefix.
Names match `[a-z0-9][a-z0-9-]*`; `builtin` is reserved.
Version is a nonempty string of at most 128 bytes with no whitespace or control
characters. It is reported by the executable, without version selection rules.
Every command requires a nonempty description, an argument schema object, a
safety class (`read_only`, `reversible`, `mutating`, `destructive`), a nonempty
effects list and a verification description. Unknown protocol versions and unknown
metadata fields reject. Command semantics and verification belong to the Driver.

Both `validate` and `run` accept the following request fields. Fields marked
optional are supplied only when relevant; empty objects may be omitted except
`config`. Do not require command context during configuration validation.

| Field | Meaning |
| --- | --- |
| `protocol` | Required integer `1` |
| `config` | This Driver's Blueprint manifest configuration object |
| `blueprintDirectory` | Optional absolute Blueprint source directory for source validation |
| `binding` | Optional Instance identity object: `instanceID`, `name`, `blueprint`, `blueprintReference`, `digest` |
| `values` | Optional Instance Values object |
| `command` | Optional local command name; required for `run` and must match argv |
| `arguments` | Optional command Args object; absent means empty |
| `workspaceDirectory` | Absolute rendered workspace for this Driver; supplied for command validation/run |
| `artifactDirectory` | Absolute shared Instance artifact directory; supplied for command validation/run |
| `commandID` | Command UUID during execution; absent during admission validation |

In `binding`, `instanceID` is the immutable UUID and `name` is `project/name`.
`blueprint` identifies the repository, `blueprintReference` is the selected pinned
reference, and `digest` identifies the normalized Instance configuration. Preserve
these fields when associating artifacts with an Instance.

For example, the minimal configuration validation exchange is:

```sh
printf '%s\n' '{"protocol":1,"config":{}}' | ./example-driver validate
# stdout: {"valid":true}
```

Validation happens in phases: configuration alone, configuration plus Blueprint
source, configuration plus binding/Values, and finally the full command request.
Check the fields provided, reject invalid inputs and return `{"valid":true}` only
after validation. It must not contact infrastructure, modify files, invoke tools
with effects, prompt or resolve secrets. Command validation runs before admission
and again before execution. A validation rejection prevents `run`.

For `example.echo`, validate the message type; in `run echo`, read the same request,
return `{"message":"..."}` using a JSON encoder and verify it matches the argument.
Real Drivers perform their command and verification before emitting the result.
The [custom executable integration fixture](tests/cli/executable_drivers_test.go)
demonstrates replacing a Driver through config and reading history after removing
that executable. Driver mode needs no application config and never starts the server.

Requests are capped at 4 MiB, `info` and validation responses at 1 MiB, and `run`
results at 16 MiB. Probes time out after five seconds and validation after ten.
Execution follows Command cancellation. Malformed, excessive or failed responses
fail the invocation; the Backend never retries or replays commands. On Linux and
macOS, cancellation kills the Driver's process group. Keep tool/provider children
in that group so they terminate too. Do not detach or daemonize.

The host supplies `HOME`, `PATH`, `TMPDIR`, `SSL_CERT_DIR`, `SSL_CERT_FILE` and
`SSH_AUTH_SOCK` when available. Only `run` additionally receives the startup
snapshot's `INFRAMILL_SECRET_*` environment variables, including local exec's
explicit dotenv settings. `info` and `validate` receive none of those secret
variables. Resolve secret references only for commands that need them, and never
put secret values in arguments, metadata or files. The host redacts known secrets
from logs, results and errors.

Drivers are trusted local programs with the host user's permissions. Directory
paths are access instructions, not an OS sandbox. Keep writes in the supplied
workspace and artifact directories. Use single filenames, reject symlinks and
nonregular files, bound artifacts to 64 MiB, and publish artifacts atomically with
mode 0600; the debug adapter enforces these rules. Drivers own artifact formats. The Backend retains Command IDs, queues, history and success only after
execution and verification finish. Driver crashes do not imply rollback.

## Architecture and development

Public standard-library-only contracts live in [pkg/inframill](pkg/inframill).
Engine routes commands through neutral interfaces; the internal Backend owns
rendering, private storage and one FIFO worker per Instance (64 waiting slots by
default). Admission commits SQLite before acknowledgement; running is durable
before effects, and results/logs close before success. Ordinary command failures
leave workers usable; storage failures isolate the Instance. Shutdown drains until
its deadline, then interrupts. Restart never replays unfinished commands.

```text
<dataDir>/instances/<InstanceID>/
  instance.json
  commands.sqlite
  .owner.lock
  manifest.yaml
  schema.json
  template/
  workspace/<driver-id>/
  artifacts/
  logs/<command-uuid>/stdout.log
  logs/<command-uuid>/stderr.log
```

Directories are private; metadata, logs and artifacts use mode 0600. Discovery
validates identities and paths before workers start. Ambiguous/unreadable identities
fail discovery while preserving data; readable identities with broken storage
surface as failed Instances. The full lowercase UUID is the directory name;
`project/name` stays immutable metadata used by CLI selectors. Use JSON output to
retrieve complete IDs (table cells are limited to 20 display columns). Recreating a
name after deletion allocates a new UUID and directory.

Instance metadata requires `storage_layout: "uuid-v1"` alongside
`api_version: "inframill.dev/instance/v1"`. Invalid or unsupported layouts are
rejected without modifying data.
See [SPEC-0009](specs/0009-uuid-instance-storage/spec.md) and
[ADR-0007](docs/decisions/0007-uuid-instance-storage.md).

Initialization and deletion share a filesystem namespace lock. Deletion drains
prior work, closes the database and removes only the selected UUID leaf. Directory
replacement, symlinks or changed identity cause failure without metadata writes
into the suspect target. Root-relative filesystem operations constrain publication/removal;
Drivers remain trusted host executables, without isolation from hostile concurrent
filesystem changes by another process with the same OS permissions.

The thin [Connect EngineService](connect-rpc/proto/inframill/v1/inframill.proto)
exposes Blueprint fetch/list, Driver health list, command definitions, Instance
init/get/list, command submission, execution get/list and log reads. Requests cap at 1 MiB;
responses use no-store. Instance and execution lists default to 50 items (maximum
100); catalog lists return all entries. Log reads snapshot current bytes without
following, default to 64 KiB (maximum 1 MiB) and return nextOffset/eof.

Follow [AGENTS.md](AGENTS.md), the [constitution](specs/constitution.md) and
[baseline](specs/0000-baseline/spec.md). This is the only user/developer README.
New behavior starts with an accepted spec/plan/tasks directory using the
[templates](specs/templates/spec.md); new records start at 0001.

```sh
task check-fast
task check-full
```

These are the only check/test task entry points. `check-fast` sequentially cleans
and regenerates Go bindings, compiles the application to `/dev/null`, and runs
package tests under `cmd`, `internal`, `pkg/inframill` and `pkg/driver`. It produces
no application binary and needs neither Node nor lint tools. Generation can update
tracked source.

`check-full` runs fast, then validates specs, runs architecture and CLI integration
tests, regenerates TypeScript bindings, runs all real-daemon tests, and checks
both Go and TypeScript generated drift. Full builds disposable test executables
under the cache directory. It requires Node.js 22+ and
`npm --prefix tests/engine ci`; missing dependencies fail instead of skipping tests.
Tests use temporary data, loopback fixtures and debug subprocesses.

All linting is separate manual maintainer work:

```sh
task lint
```

That task checks Go formatting and lint, protobuf lint, shell syntax and diff
whitespace. Agents must not run it, Go lint tools, `go vet`, or lint autofixes.
All test commands disable implicit vet with `-vet=off`. Standalone `gofmt` may be
used on changed owned handwritten Go files. Linting is not part of either test
gate or generation, and is not required for agent handoff.

All tasks live in [Taskfile.yml](Taskfile.yml). Use `task --list` for the catalog.
The former `check:fast`, `check:handoff`, `check:spec`, `go:test`, `test:engine`,
`connect:check`, `connect:check-ts` and `go:fix` tasks have been removed without
aliases. See [SPEC-0005](specs/0005-check-workflows/spec.md) for the current workflow.

`pkg/harness` is an upstream copy managed exclusively by the maintainer. Its tests
and source are excluded from owned test and formatting targets. Raw `go test ./...`
is not a supported gate. Task caches default to `/tmp/inframill-check-cache`;
override with CACHE_ROOT or GOCACHE. Manual lint cache overrides use
GOLANGCI_LINT_CACHE. `task build` still writes the application to ignored `dist/`.

To regenerate explicitly, run `task connect:generate` then
`task connect:generate-ts` sequentially. They clean only their generated output
trees and never invoke lint tools. Never edit generated files manually.

### CLI generation

`task connect:generate` regenerates Go protobuf, Connect and Cobra bindings together
under `internal/gen/` (RPC transport) and `internal/cli/gen` (CLI glue);
`task check-full` detects changed or stale output in both generated-only trees.
Go generation and `task check-fast` work without Node. The local build-time plugin is `internal/cli/generator/cmd`; it compiles
without any generated package. Never hand-edit generated output.

Generated Go bindings are application-internal. External Go clients can generate
their own bindings from `connect-rpc/proto`; the RPC wire contract is unchanged.
Test-only TypeScript bindings remain in `tests/engine/gen`.

Unary EngineService methods follow `VerbResource` conventions: `ListWidgets` becomes
`widget list`, `GetWidget` becomes `widget get`. Remaining CamelCase resource words
become kebab case. Scalar fields become kebab-case flags; message fields become
bounded `--FIELD-file` JSON inputs. Enums accept labels with or without the common
prefix, case-insensitively. Repeated/map/oneof request fields, streaming methods,
unsupported scalar kinds and naming collisions reject during generation.

Small typed Go adapters preserve established positional arguments, list/log bounds,
`instance exec/delete`, catalog `show`, log stream variants and verified waits.
Ordinary new unary methods need regeneration without a handwritten dispatch change.
`ListDrivers` is an explicit exception: its `driver list` CLI command uses native
Engine composition rather than the generated remote command path.
Response tables/details are Go definitions in `internal/cli/output/remote`, validated
against response descriptors. Unconfigured responses retain complete JSON sections
in their fallback human view. User YAML configures defaults, never command schemas
or table layouts. See [SPEC-0002](specs/0002-cli-refactor/spec.md) and
[ADR-0001](docs/decisions/0001-cli-generation.md) for boundaries and acceptance criteria.

CLI ownership is consolidated under `internal/cli`: root command composition;
`config` for Cobra-free YAML preferences; `runtime` for remote invocation policy;
`output` and `output/remote` for views; `localexec` for local execution/history and
Driver health; `generator` for the build-time plugin; and `gen` for emitted command glue.
Shared Blueprint, Driver, Backend and server services remain outside this tree.
See [SPEC-0003](specs/0003-cli-layout/spec.md) and
[ADR-0002](docs/decisions/0002-cli-package-boundaries.md) for these boundaries.
Generated output locations follow [SPEC-0004](specs/0004-internal-generated-code/spec.md)
and [ADR-0003](docs/decisions/0003-internal-generated-code.md).

Driver ownership is consolidated separately from the CLI: `pkg/driver` owns the
public Manager, configuration, health and executable protocol; its `internal/process`
subpackage owns process launch, cancellation and redaction. `internal/debugdriver`
owns the bundled debug implementation, protocol server and artifact codec. Backend
maps Driver metadata to application types; `pkg/driver` depends only on the standard
library and its private process package.
