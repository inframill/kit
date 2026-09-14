# SPEC-0002: Native CLI and generated daemon commands

Status: Implemented
Owner: Inframill maintainers
Created: 2026-09-12
Updated: 2026-09-12

## Problem

cmd/main.go mixes command discovery, manual flag parsing, Connect request building,
file input, polling and process exit handling. Local exec/history implement another
parser, and remote presentation has a handwritten response-type switch. Adding an
RPC-backed command requires coordinated edits to several unrelated dispatch tables.
Help is mostly a global usage string; there is no shell completion or version command.

## Outcome

One coherent command tree combines explicit native commands with generated daemon
commands. Protobuf drives RPC command generation. A thin layer of Go structs defines
response presentation, and the existing .inframill.yaml supplies user-facing CLI
defaults. Ordinary daemon commands require protobuf changes and regeneration;
custom table layouts require only presentation definitions.

## Scope

### In scope

- Refactor every existing command into a Cobra command tree.
- Generate daemon command registration, typed request bindings, Connect calls,
  static completion metadata and descriptor-based default response rendering.
- Define curated table/detail presentation in hardcoded Go structs, separately
  from user defaults in the existing .inframill.yaml cli section.
- Add focused help/examples, shell completion, version reporting, endpoint/timeout
  environment defaults, selectable columns and controlled terminal presentation.
- Define terminal color and opt-in interaction now that the user has removed the
  baseline's blanket colors/prompts/pagers restriction.
- Preserve explicit Go workflows for waiting and deletion verification.
- Add deterministic generation and drift verification to existing root tasks.

### Out of scope

- RPC service changes, server reflection, streaming RPCs, authentication/profiles,
  runtime plugins, arbitrary query languages, a TUI, automatic retries, log follow
  and automatic pagination. These need separate behavioral requirements.
- Changes to domain execution, storage, Driver protocol, infrastructure or pkg/harness.

## Requirements

- **REQ-001:** cmd/main.go MUST remain the sole application entry point, containing
  process/signal setup, build-version variables and delegation to an internal CLI
  package. Native commands MUST cover exec, history, server, package build and
  driver debug info/validate/run. They MUST invoke existing typed application or
  protocol services without round-tripping through argv parsers. Daemon commands
  MUST use the generated EngineServiceClient and never open local Backend storage.
- **REQ-002:** Existing command paths, arguments, flags, defaults and successful
  output contracts MUST remain available. Cobra MUST supply per-command help,
  examples, grouped root help and normal interspersed flags/-- handling. No args
  MUST show help on stderr and exit 2; explicit help MUST write stdout and exit 0.
  RPC flags MUST be scoped to blueprint/operation/instance/command trees, usable
  after their group name or around leaf positional arguments. Native commands MUST
  reject unrelated RPC/output flags before effects. Driver protocol parsing and
  stdout MUST remain exact, without inherited CLI flags, banners or diagnostics.
- **REQ-003:** `version` and root `--version` MUST report the same core release
  version on stdout without config, network or storage access. Core and debug
  versions MUST remain independently injectable. `completion bash|zsh|fish|powershell`
  MUST emit scripts to stdout. Completion MUST offer commands, flags, enum values
  and appropriate file paths without contacting the daemon, loading application
  config, reading file contents, probing Drivers or acquiring storage.
- **REQ-004:** Remote settings resolution MUST use explicit flag, then supported
  environment variable, then the .inframill.yaml cli section, then built-in defaults.
  Supported environment overrides are INFRAMILL_ENDPOINT and INFRAMILL_TIMEOUT;
  color additionally follows REQ-010. A selected empty or
  invalid value MUST reject before requests. An explicit valid flag MUST override
  an invalid environment value. These variables MUST NOT affect native commands,
  help, version or completion. Endpoint credential/query/fragment and redirect
  restrictions MUST remain. Timeout remains one total invocation deadline.
- **REQ-005:** Protobuf descriptors and comments MUST drive generated command
  constructors, request fields/flags/types, typed Connect calls and help metadata.
  Generation MUST use documented conventions rather than a separate CLI command
  configuration schema. Every service method/request field MUST have generated
  coverage or an explicit supported adapter/exclusion. Unsupported constructs and
  ambiguous/colliding command or flag names MUST fail generation. Adding an ordinary
  supported unary RPC MUST NOT require handwritten command/response dispatch edits
  or presentation configuration; a complete descriptor-based fallback MUST work.
  Small Go adapters MAY supply exceptional naming, positional shortcuts, established
  bounds/defaults and workflows without duplicating the generated transport layer.
- **REQ-006:** Hardcoded, typed Go presentation structs MUST describe list rows,
  columns with stable keys, detail
  fields, nested JSON sections, enum/timestamp formatting, empty messages and
  pagination metadata. Presentation field paths MUST be validated against protobuf
  response descriptors in tests and before any request. Definitions MUST NOT
  construct requests, declare command bindings or control execution. The renderer
  MAY use protobuf reflection for field access; no runtime server reflection is needed.
  Existing default table content and complete protojson responses MUST remain; JSON MUST
  bypass table projection. Remote list commands MUST support `--columns KEY,...`
  in caller-specified order; empty, duplicate or unknown keys MUST exit 2 before
  requests. This flag MUST reject with JSON and on details/log/native commands.
  Full IDs, ordering, cursors, errors and nested sections MUST remain visible in
  default views, without truncation. Only presentation styling may depend on the
  terminal; data selection and machine output MUST NOT.
- **REQ-007:** Generation MUST support the existing command-to-RPC mapping,
  including constant-bound delete/log variants and catalog detail selection.
  Exceptional mappings and waiting MUST remain small typed Go adapters layered
  on generated bindings. They MUST preserve one-time admission, failed outcomes on stdout before
  exit 1, cancellation without cancelling accepted work, and deletion verification
  through Instance absence. A missing Command alone MUST NOT prove deletion.
- **REQ-008:** Exit codes MUST remain 0 success, 1 unsuccessful operation, 2 invalid
  CLI input, 3 runtime/output failure and 130 interruption. Existing Connect-error
  mapping MUST be characterized and preserved rather than silently reclassified.
  Diagnostics MUST be emitted once on stderr, without echoing input values or
  secrets. Local bounded output/cancellation/result-file behavior, known-secret
  redaction, remote 1 MiB input limits and remote-only stdin support MUST survive
  parser replacement. Raw logs and complete JSON log envelopes MUST remain intact.
- **REQ-009:** Existing connect:generate/connect:check tasks MUST include CLI
  generation/drift checks with deterministic output and no Node requirement.
  Dependent generation MUST be sequential, never clean handwritten directories,
  and never edit generated files manually. New generator code MUST be covered by
  owned formatting/lint/tests. No new Task names are required.
- **REQ-010:** Human help/tables/diagnostics MUST support `--color auto|always|never`,
  default auto. Auto MUST color only the destination terminal and MUST disable
  color when NO_COLOR is nonempty or TERM=dumb. An explicit always/never MUST take
  precedence. JSON, raw logs, Driver protocol and completion scripts MUST never
  receive presentation escapes, even with always. Color MUST NOT truncate data or
  become the only indication of status. Native execution results remain intact.
- **REQ-011:** Confirmation prompts are deferred. All commands MUST remain
  noninteractive; `--confirm` MUST reject as an unsupported flag before effects.
- **REQ-012:** Pagers are deferred. No command MUST launch or discover a pager;
  `--pager` and `--pager-arg` MUST reject as unsupported flags before effects.
- **REQ-013:** Basic user settings MUST live under `cli` in the existing
  .inframill.yaml: endpoint, timeout, output (table|json) and color (auto|always|never).
  Remote commands MUST work with defaults/environment/flags when the default file
  is absent. Explicit `--config FILE` MUST require a readable existing file; an
  empty path MUST reject. An existing malformed/unreadable file, duplicate or
  unknown cli setting, or invalid effective value MUST fail before requests.
  Missing cli fields/section MUST use lower-precedence defaults. A CLI-only config
  MUST suffice for remote commands; loading it MUST NOT require handler settings,
  initialize the server, probe Drivers or open Backend storage. Server and local
  exec MUST retain their required-config behavior and accept a valid cli section
  in the same file. Help/version/completion MUST bypass config loading. Output
  preferences apply to structured remote output and local history; raw log defaults
  MUST remain raw unless --output json is explicitly selected. Endpoint/timeout
  preferences MUST NOT change native execution. Parsing/composition changes MUST
  stay outside pkg/harness and preserve existing server configuration semantics.
- **NFR-001:** pkg/inframill MUST remain standard-library-only; Connect adapters
  MUST remain thin; CLI dependencies MUST stay outside domain packages. All work
  MUST preserve user runtime data and leave pkg/harness untouched.

## Acceptance criteria

- **AC-001** (REQ-001/002, NFR-001): Existing CLI integration scenarios pass through
  the new entry point; architecture tests prove CLI/daemon/local boundaries and
  one application entry point. No duplicate owned argv parser remains.
- **AC-002** (REQ-002/003): Help/version/completion succeed with missing config and
  an unreachable endpoint; spies observe no client/config/Driver/storage activity.
  Snapshots cover root and leaf help and all four completion generators.
- **AC-003** (REQ-004/008/013): Tests cover flags/env/config/default precedence, empty/invalid
  values, command-group flag placement, --, cancellation and output failures, with
  exact exit codes and no requests on local validation failure.
- **AC-004** (REQ-005/009): A disposable schema fixture adds a supported unary RPC;
  generation plus compilation produces its usable command and fallback rendering
  without handwritten dispatch edits or configuration. Negative fixtures reject
  unsupported types and naming collisions with actionable diagnostics.
- **AC-005** (REQ-006/008): Golden/semantic output tests cover every response shape,
  empty lists, full IDs/cursors, failure/nested sections, column order/rejection,
  unchanged JSON payloads and byte-exact raw logs including non-UTF-8 bytes.
  Descriptor validation rejects stale/invalid Go presentation paths before requests.
- **AC-006** (REQ-007/008): Fake-client and disposable-daemon tests prove one submit
  per invocation, idempotency passthrough, wait success/failure/interruption,
  surviving daemon work, verified deletion and missing-command nonconfirmation.
- **AC-007** (REQ-009, NFR-001): Two generations are byte-identical; drift checks
  detect changed or stale output. Spec, fast, relevant integration and final handoff
  checks pass. Diff review confirms no protected-code or runtime-data edits.
- **AC-008** (REQ-010/011/012): Terminal/nonterminal fixtures prove color precedence,
  escape-free machine output and no automatic interaction. Confirmation/pager flags
  reject before requests or effects; PAGER is ignored.
- **AC-009** (REQ-013): Fixtures cover absent default config, explicit missing config,
  malformed/unreadable files, unknown/duplicate cli keys, missing cli fields/section,
  CLI-only and combined server/CLI config. Higher-priority valid values override
  lower-priority invalid scalar values; structural YAML/type errors still reject.
  Server/local exec retain config validation and environment-resolution behavior;
  spies prove remote/help/version/completion never initialize server resources.

## Interfaces and examples

Added commands and flags; existing examples remain valid:

```sh
inframill instance list --help
inframill instance --endpoint http://127.0.0.1:8080 list --limit 20
inframill instance list --columns name,id,status
INFRAMILL_ENDPOINT=http://127.0.0.1:8080 inframill operation list
inframill completion zsh
inframill version
inframill --version
inframill instance list --color never
```

User defaults share the existing .inframill.yaml with server settings:

```yaml
cli:
  endpoint: http://127.0.0.1:8080
  timeout: 2m
  output: table
  color: auto
```

Table layouts remain Go source, not user YAML. There is no connect-rpc/cli.yaml.

## Failure behavior

Validate flags, columns, endpoint and bounded input before requests or effects.
Centralize CLI exit classification and safe diagnostics; suppress Cobra's automatic
duplicate error/usage printing. Preserve output-failure precedence over operation
failure and local result-file failure exit 3. Never automatically resubmit on an
ambiguous transport failure. Preserve protocol-specific Driver exit behavior.

## Security and safety

Help/version/static completion and generation are effect-free with respect to the
daemon and infrastructure; generation writes only generated source. History reads
existing local records. Server, exec, package build, Blueprint fetch, Instance init,
submit and delete retain their existing declared effects and verification. Wait
and catalog/log reads are observations; underlying operations keep Driver-declared
safety. Completion scripts are printed, never installed automatically. Invocations never prompt or start pagers.

## Compatibility and migration

The user explicitly removed SPEC-0000 REQ-015's blanket colors/prompts/pagers ban
and uncolored-table constraint during planning. REQ-010 records the requested
automatic terminal color with --color and NO_COLOR controls; REQ-011/012 defer prompts and pagers at the user’s request. The maintainer authorized continuation after supplying the harness section extension. Protocol,
domain APIs and storage versions do not change. Help layout and parser diagnostics intentionally
change; no-argument status and existing successful invocations remain stable.
Table projection is explicit and optional; prompts and pagers are deferred. Auto color changes
terminal presentation; piped default tables and machine output remain uncolored.
SPEC-0001 task names remain; their generation/check bodies gain CLI output coverage.
No compatibility shim, runtime migration or data deletion is proposed.

## Resolved decisions

The maintainer supplied `WithAllowedSections` and `Config.Sections` and requested
continuation on 2026-09-12. Native loading opts into `cli` through that extension;
remote settings use a small application-owned YAML reader inspecting only `cli`.
The harness continues to own server decoding and environment resolution. CLI
settings are literal strings; endpoint/timeout environment overrides follow REQ-004.
Native harness loading retains its exact environment-reference behavior for sections.

Ordinary RPC names split at the first CamelCase verb: ListWidgets becomes
`widget list`, GetWidget becomes `widget get`; remaining CamelCase words become
kebab case. List plurals ending in s are singularized. Request scalar fields become
kebab-case flags; messages use bounded `--FIELD-file` JSON inputs. Enums accept
case-insensitive labels with or without the common enum prefix. Repeated/map/oneof
request fields and streaming methods reject until explicitly supported. Exceptional
current paths and positional shortcuts are small Go adapters, documented in ADR-0001.
The user explicitly deferred prompts and pagers on 2026-09-12.
