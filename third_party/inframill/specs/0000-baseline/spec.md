# SPEC-0000: Inframill prerelease baseline

Status: Implemented
Owner: Inframill maintainers
Created: 2026-09-12
Updated: 2026-09-12

## Problem

Incremental specs and ADRs describe conflicting generations of configuration,
local execution and storage. The maintainer requested a reset of the records,
concise current documentation and removal of verified code/build leftovers.

## Outcome

One current baseline and architecture decision describe the complete application.
The root README is the sole user/developer guide. The application is tool-neutral and distributes one Go executable. Release versions
restart at 0.0.0-dev; protocol/storage versions and user runtime data remain intact.

## Scope

### In scope

Consolidate the current executable Driver, debug Driver and standalone binary
contracts. Replace superseded records, remap test traceability, shorten README,
reset development release identifiers and remove verified unused helpers and
stale build outputs. Preserve public APIs and data-protection checks.

### Out of scope

Protected pkg/harness, dependency/tool upgrades, generated API edits, live
infrastructure operations, registry publication, migrations, refresh, changes to
format versions and deletion of user runtime data.

## Requirements

- **REQ-001:** The standard-library-only public package pkg/inframill MUST expose
  Engine and neutral contracts. Engine MUST manage BlueprintSource and live
  Instances through methods, generate distinct UUID InstanceID/CommandID values,
  and route commands. Instance names MUST be immutable unique project/name strings.
  Engine MUST NOT import Drivers, rendering, SQLite or protocol implementations.
- **REQ-002:** BlueprintSource MUST expose metadata (directory, repository,
  reference, digest, tags, API version and description) without parsed schemas,
  files or Driver config, and validate Values against offline schemas. Backend
  MUST snapshot/render, validate Driver config/files/Values, initialize private
  storage and atomically publish <instances-root>/<project>/<name>. UUIDs remain
  in metadata and APIs. Initialization returns InstanceInfo without Command history.
  Concurrent initializations MUST serialize across Backend objects/processes and
  reject duplicate names/UUIDs before publication. Failed init removes only its own
  staging files, preserving existing data. Empty project directories are valid;
  project/Instance directories must be private and publication synced.
- **REQ-003:** Each Instance MUST own one bounded FIFO and worker, with 64 waiting
  commands by default and no global execution limit. Admission MUST validate
  operation/Args, commit SQLite, then publish to the queue before acknowledging.
  Matching idempotency key/operation/normalized Args MUST return the original ID
  before capacity checks; changed input MUST conflict. Keys have a 200-character
  limit. Full queues MUST reject immediately without executable records. Commands
  accepted by the daemon MUST outlive client cancellation and MUST NOT auto-retry.
- **REQ-004:** Instances MUST own private SQLite, logs, artifacts, workspace locks
  and atomic Info snapshots. Running MUST be durable before effects; command results
  and closed logs precede success. Ordinary command failures leave the worker
  usable; persistence/log failure marks that Instance failed and stops admission.
  Close drains until its deadline, then cancels/interrupts, and remains repeatable.
  Startup reconciles queued/running records as interrupted without replay. Discovery
  enumerates project/name directories, reads bounded regular instance.json identities
  and returns UUIDs in sorted order. Missing/unreadable identities, invalid names,
  name/path mismatches, duplicate UUIDs and unsafe path components MUST fail root
  discovery before workers start, preserving all data. Readable identities with
  corrupt execution metadata/storage surface as failed Instances while healthy
  siblings remain usable. Active Instance ownership MUST be exclusive.
- **REQ-005:** builtin.delete MUST queue behind earlier commands, block subsequent
  admission, close resources and remove only the selected named Instance leaf and
  its history. Preserve siblings and project parents. It MUST NOT destroy
  infrastructure or create a durable receipt. Absence confirms success; partial
  deletion leaves a failed Instance. Failed Instances reject deletion. Engine
  observes deletion through Instance.Deleted. Reusing a deleted name creates a new
  UUID; the old UUID cannot resolve to the new Instance.
- **REQ-006:** Queries MUST return independent last-known snapshots without provider
  calls. Instance lists sort by UUID; command history sorts by descending admission
  sequence. Pages default to 50, allow 1–100 and bind cursors to Instance/filter.
  Command/log reads MUST be Instance-scoped. Log readers snapshot bytes present at
  open, with EOF rather than following. RPC log reads default to 64 KiB, cap at
  1 MiB, validate offsets and return nextOffset/eof. Info contains UUID, name,
  Values, pinned Blueprint, idle/running/failed status and structured failure.
- **REQ-007:** OCI acquisition MUST be anonymous, bounded, digest-verified and
  atomically published. Daemon acquisition is explicit; local exec may fetch missing
  references as specified in REQ-017. List/Resolve stay local. Cached tags/pins MUST
  be reused without automatic refresh, and changed tags never modify existing
  Instances. Failed/corrupt cache reads must not silently replace content. Fetch
  uses a two-minute deadline and limits of 1 MiB manifest, 1 KiB config, 64 MiB
  compressed layer, 256 MiB extracted data and 10,000 entries. Package building MUST
  be deterministic. Source contains manifest.yaml, schema.json and template/.
  Root runtime/ and .inframill/ entries MUST be excluded without traversal from
  snapshots/packages; nested template entries remain ordinary content. Reject
  unsafe paths, links, special files, unknown layout, template collisions and
  missing template keys. Evaluate only .gotmpl with Go text/template and toJSON;
  preserve ordinary files, empty directories and executables. Schemas use offline
  JSON Schema Draft 2020-12 plus unique, IPv4 CIDR, disjoint-network and reference
  validators; external schema resources are forbidden. Acquisition validates
  generic metadata/layout/schema, not Driver config or template evaluation.
- **REQ-008:** Backend MUST receive a Drivers interface implemented by an independently
  composed registry. At startup, probe each configured executable with its literal
  prefix arguments followed by info, before opening storage or serving. Require a
  unique name, independent version and protocol 1 metadata with operation schemas,
  safety, effects and verification. Reject malformed metadata, unknown fields,
  unsupported protocols, duplicate names and probe failures; builtin is reserved.
  Names match [a-z0-9][a-z0-9-]*; versions are nonempty, at most 128 bytes, without
  whitespace/control characters. Operations are qualified as driver.operation.
  Each info, validate and run call MUST be a fresh process returning one bounded
  JSON response. Info takes no input; validate/run take one JSON request on stdin.
  Run receives the local operation name as an argument. Limits
  are 4 MiB request, 1 MiB metadata/validation, 16 MiB result, five-second probes
  and ten-second validation. Validation is effect-free and receives only relevant
  context in configuration, source, Instance and operation phases. Validate before
  admission and execution. Drivers own execution and verification before success;
  registry dispatch contains implementation panics without exposing panic values.
  There is no plugin SDK, resident process, implicit Driver or version resolver.
- **REQ-009:** Driver invocations MUST use argument arrays, controlled environments and
  no interactive input, editors, pagers or credential prompts. Only a zero exit
  status succeeds. Stdout contains one JSON protocol response; tool stdout and
  stderr are merged into Driver stderr. Capture redacted stdout/stderr in Command
  logs. Retain only HOME, PATH, TMPDIR, SSL_CERT_DIR, SSL_CERT_FILE and SSH_AUTH_SOCK
  from the ambient environment. Only run additionally receives the startup
  snapshot's INFRAMILL_SECRET_* values. Resolve secret references during execution;
  never put secrets in Args, Values, metadata, files or process arguments. Redact
  known-secret values from live/saved streams, structured results and errors,
  including split write boundaries. Empty buffers MUST NOT trigger writes or
  replay output after forwarding fails. On Linux/macOS cancellation MUST kill
  the Driver process group and bound pipe cleanup; children must not detach.
- **REQ-010:** The only bundled Driver MUST be debug, served by the same executable as
  inframill driver debug info|validate|run OPERATION, without application config
  or server startup. The host MUST load it through the same configured subprocess
  protocol as third-party Drivers. It reports its own version and protocol 1.
  Provide echo, write-artifact, read-artifact, record, logs, fail, wait and secret
  operations with schemas, declared safety/effects and verification. Artifact text
  reads/writes cap at 1 MiB and verify round trips. Record writes/read-verifies
  artifacts/record-<CommandID>.json containing protocol, commandID, binding, Values
  and arguments; reject duplicate records. Modes success/fail/block come from Args
  or Values.mode; fail occurs after recording, block waits for workspace/debug/release.
  Logs merges supplied stdout/stderr text into Driver stderr. Wait uses an integer
  durationMs from 0 to 60000 or the release-file barrier with a 60-second timeout,
  honoring cancellation. Secret resolves an Args reference or Values.secret_ref at
  execution; hosted output is redacted. Direct raw protocol calls bypass host
  redaction and are suitable only for synthetic secret fixtures. No tool-specific
  Driver implementations, schemas, examples or build workflows are bundled.
- **REQ-011:** Inframill-owned Blueprint, package-index, Instance, local-result and OCI artifact/layer identifiers MUST retain v1. SQLite
  user_version remains 1 and RPC remains inframill.v1. Named Instance directories
  contain private instance.json, commands.sqlite, logs/, artifacts/, saved source
  and workspace/<driver>/. Unsupported formats and unsupported directory layouts
  MUST reject before effects while preserving their bytes. No automatic migration
  or directory aliases. Third-party versions and protocols remain unchanged.
- **REQ-012:** This baseline and ADR-0000 MUST replace superseded numbered records;
  Git retains their history. Remap test comments to current requirement IDs and
  remove stale links, rollout histories and obsolete examples. README MUST be the
  sole current user/developer guide, with required config, complete InstanceConfig
  examples, named exec/history, daemon usage, credentials, output and development
  checks. New records start at 0001. Build binaries MUST remain outside tracked
  source; ignore rules MUST exclude build output and existing runtime/secret paths.
  Remove code only with reachability/reference evidence, preserving public contracts,
  interface implementations and data-protection checks.
- **REQ-013:** Backend MUST provide absolute workspace/artifact directory paths to
  executable Drivers and treat artifact bytes as opaque. Drivers own formats and
  must confine writes to these directories. The debug protocol adapter MUST use
  single artifact filenames, reject links/nonregular files, cap reads/writes at
  64 MiB and publish atomically with private permissions. Artifacts MUST survive
  separate executable invocations and Instance restarts. Drivers are trusted local
  programs with the host user's permissions; directory paths are not an OS sandbox.
  Backend MUST NOT maintain an unused duplicate artifact byte-access adapter.
- **REQ-014:** Server composition MUST load harness YAML, probe configured Drivers,
  compose BlueprintSource, Backend and Engine, and inject Engine into the thin
  Connect adapter. Engine starts before HTTP serving and shuts down through harness
  cleanup. Snapshot environment for registry settings and secret resolution. Harness
  owns serving/logging and stays application-neutral. Bind main.port (default 8080).
  Diagnostics go to stderr and omit request bodies/credentials. RPC remains the
  ten-method EngineService with structured results, existing error mappings, a
  1 MiB request limit and no-store.
- **REQ-015:** cmd/main.go MUST remain the only command entry point. Preserve
  server, package build, Blueprint fetch/list/show, operation list/show, Instance
  init/get/list/exec/delete and scoped command list/get/wait/stdout/stderr. RPC
  defaults are endpoint http://127.0.0.1:8080 and timeout 2m. Remote JSON input
  allows stdin and caps at 1 MiB; explicit waits do not cancel accepted server work.
  Structured management results and local history MUST default to
  tables, including when piped, with --output/-o table|json. Lists preserve ordering,
  full IDs/cursors, useful summary columns and explicit empty messages. Details
  show identifiers/status/timestamps/errors and labeled nested JSON; confirmations
  and failed waits retain returned outcomes. JSON is complete compact protobuf JSON
  remotely and indented local-result/v1 records/pages for history. Log commands
  default to raw bytes; only explicit JSON is supported for their full envelopes.
  Invalid formats/flags fail before effects; output write failures return exit 3.
  Exec/server/package build do not accept output flags. Rendering belongs only in
  CLI presentation code, without implicit truncation.
  CLI diagnostics use stderr. Exit codes remain 0 success, 1 failed operation,
  2 invalid input, 3 runtime/output failure and 130 interruption.
- **REQ-016:** Inframill MUST remain prerelease with no default compatibility or
  migration promise. User runtime data MUST never be deleted to enable a new format.
  pkg/harness MUST remain exclusively user-managed and unchanged. Generated
  artifacts MUST be regenerated only through their tools. Tests MUST use temporary
  fixtures and fake tools, never live apply/destroy as code validation.
- **REQ-017:** Local exec MUST use the shared Engine, Backend and Drivers without
  a daemon or TTY. Require --instance FILE containing exactly a nonempty blueprint
  string and values object; reject unknown/wrong/missing fields, malformed JSON,
  symlinks, nonregular files, stdin and files above 1 MiB. Optional --blueprint-dir
  snapshots local source without fetching or changing the config; empty overrides
  reject. Otherwise resolve from <dataDir>/blueprints and fetch absent references
  using the same BlueprintSource.Fetch as Engine. Registry settings use inherited
  INFRAMILL_HTTP_REGISTRIES. Fetch failures precede Instance creation/admission.
  Multiple Instances share one data root. --name selects an exact project/name,
  defaulting to local/default on every call. Initialize an absent target through
  Engine.InitInstance; never infer a sole sibling. Existing UUID/files/history
  remain, and normalized Values/selected Blueprint identity must match before
  command admission. The ignored config reference does not affect local overrides.
  A new name may use other inputs. Refresh is unsupported. Reject invalid/empty
  names before ownership. Local parent locking serializes local processes and busy
  ownership fails promptly. Exec has no --workspace flag; REQ-019 selects the root.
  Args are a separate optional regular JSON object file capped at 1 MiB.
- **REQ-018:** Local exec MUST wait, stream redacted Driver diagnostics live to stderr
  and save both Driver streams to command logs. Completed structured Driver results
  go to stdout as JSON. Stored failures include their code/cause on stderr and
  retain the history hint; missing structured errors use a generic fallback.
  Writes are bounded to 30 seconds and responsive to cancellation; broken output
  is a runtime failure. Optional --result-file MUST reserve a new private file
  outside data/source/cache/input paths before acquisition/admission. Atomically
  publish complete local-result/v1 identity, command, operation/Args/timestamps,
  status and result/error. Pre-admission failures have no Command ID; unconfirmed
  admitted outcomes are unknown. Output-file failure returns 3 even after effects.
  SIGINT/SIGTERM cancel local work and return 130, preserving interrupted history
  when possible; abrupt loss may leave an empty reservation. History requires
  --workspace DIR, accepts --name (default local/default), pagination and scoped
  command lookup, and requires no application config, source, Values, Drivers or
  daemon. Missing named history targets fail without initialization. Failed targets
  reject; readable failed siblings do not block healthy selection. Abandoned
  records are reconciled without replay, and sibling logs/history remain isolated.
- **REQ-019:** Server and exec MUST require ./.inframill.yaml in cwd by default,
  with --config FILE selecting another required file. No absent-file or legacy-name
  fallback. Empty overrides return exit 2; missing/invalid config returns exec 2
  or server 3 before storage/acquisition/result reservation or serving. Help needs
  no config. Shared typed appconfig decodes connect-engine settings.dataDir using
  unchanged harness YAML/environment support. Omitted/empty dataDir defaults to
  .inframill; absolute paths stay absolute and relative paths resolve from cwd,
  independently of config location. settings.drivers MUST be an explicit list of objects with path and optional literal
  args. Relative executable paths resolve from the configuration directory; there
  is no executable-name lookup through PATH. Reject unknown fields, blank paths and invalid argument
  types. An omitted/empty list exposes only builtin operations. Both entry points
  use blueprints/ and
  instances/ beneath it. Reject wrong-type, whitespace-only and NUL-containing
  paths with safe errors. Preserve existing data when selecting another root.
- **REQ-020:** Local exec MAY explicitly load --env-file PATH as ephemeral secret
  resolver input. No automatic discovery, process-environment mutation or arbitrary
  subprocess injection. Inherited variables win, including empty values. File
  assignments allow UTF-8/CRLF single-line KEY=VALUE, optional export, blank lines,
  comments, surrounding whitespace, empty values and single/double quotes. Keys
  match [A-Za-z_][A-Za-z0-9_]*; duplicate assignments use the last. Unquoted comments
  start at # at the value start or after whitespace; quoted values may have a
  trailing comment. Single quotes stay literal; double quotes decode newline,
  carriage-return, tab, backslash, quote and dollar escapes; other escapes retain
  backslashes. Variable/command substitutions and backticks are never evaluated.
  Reject missing =, bad keys, unclosed quotes, trailing quoted junk, NUL, invalid
  UTF-8 and multiline values. Require a regular non-symlink file at most 1 MiB,
  outside source/cache/data after resolving directory aliases. Fail with exit 2
  before storage/admission; safe diagnostics may include line numbers, not values.
  Result reservations retain precedence and must differ from env input. Parsed
  file values MUST not enter Values, Args, metadata, logs, history or process args;
  known-secret redaction and credential-free validation remain.

- **REQ-021:** Build, release and install MUST be direct Task commands producing
  only the Go executable, with no container or tool installation workflow. Build
  produces a native dist/inframill; install defaults to ~/.local/bin. Release
  requires an explicit filename-safe VERSION and builds darwin/linux amd64/arm64
  into a new dist/<version> directory without publishing or overwriting releases.
  Core and bundled debug development versions MUST start at 0.0.0-dev and retain
  independent linker overrides. Wire/storage v1 and third-party dependency/tool
  versions MUST NOT change as part of the release reset.

## Acceptance criteria

- **AC-001** (REQ-001/002/007/011): Metadata/schema/render/OCI and Backend tests prove
  named atomic publication, UUID identity, verified deterministic packages, root
  source exclusions, collision rejection and preservation of unsupported data.
- **AC-002** (REQ-003/004/005/006): Backend/Engine tests prove durable FIFO admission,
  idempotency, query isolation, ownership, drain/interruption/restart, failed-sibling
  isolation, safe discovery and named deletion/reuse with disposable storage.
- **AC-003** (REQ-008/009/010/013): Executable, debug and process tests prove
  metadata/config/Args validation, independent version reporting, verification,
  artifact persistence/private writes, secret redaction and process-group cleanup.
- **AC-004** (REQ-015/017/018/019/020): CLI/config and daemon tests prove required
  config, named Instance reuse/history, local fetch/override, tables/JSON/raw logs,
  input/env/result boundaries, failure reporting and interruption using temporary
  fixtures and the debug subprocess.
- **AC-005** (REQ-012/014/016/021): Audits prove one current baseline/ADR, one root
  README, valid links/record references, current examples, no tracked build output,
  reset development versions, no obsolete tool references in owned source, removed
  unreachable helpers and unchanged protected/generated/runtime data. Native build,
  isolated install and four-target release smoke checks pass.
- **AC-006** (all): Spec, fast, focused integration/race, daemon and one handoff gate
  pass with exact evidence recorded in tasks.md; skipped checks are explicit.

## Interfaces and examples

The Go module is github.com/sir-hassan/inframill. Neutral public contracts live in
pkg/inframill. Rendering, persistence, Driver registry and executable adapters
remain internal. Manifest systems maps Driver names to configuration objects.

    task build
    dist/inframill driver debug info
    dist/inframill exec debug.echo --instance examples/debug.json --blueprint-dir blueprints/debug --name examples/demo
    dist/inframill history --workspace .inframill --name examples/demo

Server/exec require .inframill.yaml. README documents executable protocol fields,
configuration, operations and development commands.

## Failure behavior

Preserve structured domain/Connect contracts and CLI exit classes. Unsupported
versions/layouts fail without migration or cleanup. Failed storage cannot claim
success. Cancellation and interruption do not imply infrastructure rollback;
operations never retry automatically after disconnection.

## Security and safety

This authorized cleanup is a reversible source/documentation change. It does not
change runtime format versions or authorize infrastructure mutations. Tests use
fake tools, temporary data and loopback fixtures. Explicit operations retain their
declared safety classes; state-changing Driver operations and local deletion are mutations.

## Compatibility and migration

Current public interfaces and stored formats are preserved. Retired records remain
in Git history. No aliases, automatic migration, cleanup of user runtime data or
compatibility promises for unsupported prerelease content are introduced. Rejected
layouts/version guards remain protective behavior, not migration machinery.

## Open questions

None.
