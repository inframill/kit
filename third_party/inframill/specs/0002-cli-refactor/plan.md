# SPEC-0002 implementation plan

Status: Implemented
Spec: `specs/0002-cli-refactor/spec.md`

## Approach

Use Cobra/pflag for the entire application command tree. Cobra is already an
indirect dependency (v1.10.1); make the selected pinned version direct without an
unrelated upgrade. Keep the existing generated Connect transport. Add a small,
repository-specific protoc plugin using Go protogen, driven by protobuf descriptors
and comments. Generate command glue using documented conventions. Hardcoded Go
structs customize response presentation; the existing .inframill.yaml cli section
supplies user defaults. Retain small Go adapters for exceptional command UX and
workflows. There is no separate command-definition YAML or configuration DSL.

Current evidence:

- `cmd/main.go`: command/flag switches, RPC construction, catalog filtering,
  polling, JSON file input and exit policy are coupled.
- `internal/localexec/cli.go`: a second flag parser is mixed with execution setup.
- `internal/clioutput/remote/response.go`: handwritten response switch/getters.
- `connect-rpc/proto/inframill/v1/inframill.proto`: ten unary methods, already
  backed by generated Go clients; no reflection or new RPC is needed.
- `tests/architecture/dependencies_test.go`: explicit package allowlists and a
  single-file cmd rule must be updated for the new package boundaries.
- `connect-rpc/buf.gen.yaml`: clean output currently targets gen; generated CLI
  files must join the same complete output set or use isolated output ownership.

## Requirement mapping

| Requirement | Design element | Verification |
| --- | --- | --- |
| REQ-001/002 | Cobra composition, typed native adapters | Existing CLI tests, architecture checks, parser matrix |
| REQ-003 | Offline help/version/completion | Snapshots and dependency spies |
| REQ-004 | Lazy RPC options resolver/client factory | Precedence and zero-request tests |
| REQ-005 | Proto-driven generator and naming/binding conventions | Synthetic extension and negative fixtures |
| REQ-006 | Go presentation structs and descriptor-aware renderer | Descriptor validation, output goldens and JSON/log equivalence |
| REQ-007 | Typed workflow hooks | Wait/delete/admission tests |
| REQ-008 | Shared safe CLI errors and injected I/O | Exit, cancellation, secret and broken-output tests |
| REQ-009 | Existing generation/drift tasks extended | Reproducibility, drift and handoff checks |
| REQ-010/011/012 | Terminal color; reject deferred interaction flags | TTY matrix and no-request tests |
| REQ-013 | Isolated CLI settings loader and shared YAML integration | Config presence/precedence and native regression fixtures |
| NFR-001 | Updated package dependency assertions | Architecture tests and final diff review |

## Components and boundaries

Implemented ownership (file names may vary within these boundaries):

```text
cmd/main.go                        signals, versions, os.Exit only
internal/cli/                      root tree and handwritten native constructors
internal/cliruntime/               RPC options, inputs, errors, typed wait workflows
internal/clioutput/                shared table/detail primitives; no command parsing
internal/clioutput/remote/         Go presentation definitions and protobuf field access
internal/cliconfig/                typed user defaults from .inframill.yaml
internal/localexec/                typed Exec/History options and existing lifecycle
internal/cligen/                   protobuf conventions/validation and Go emission
internal/cligen/cmd/main.go        build-time protoc plugin entry point
.inframill.yaml                    existing app config plus optional cli settings
gen/cli/                          generated Cobra constructors and request bindings
gen/inframill/v1/                  existing generated protobuf and Connect transport
```

`internal/cli` composes `gen/cli`, `internal/cliruntime`, localexec, server and the
Driver adapter. Generated CLI imports runtime/output helpers and generated RPC
packages. Runtime imports the typed RPC client and presentation primitives, never
the root command package or generated CLI package. Domain/Backend/server adapter
packages do not import Cobra or CLI packages. Avoid a dependency cycle by keeping
workflow hook contracts below the generated command layer.

The build-time plugin is not another shipped application command; place it outside
cmd so the existing single application entry rule and release target stay intact.
Keep tests/format/lint coverage through the existing internal source root. Record
the generation boundary, Go presentation ownership, YAML integration and workflow
exception in a new ADR
before implementation; ADR-0000's domain/server/Driver decisions remain intact.

Native migration extracts public-internal typed `ExecOptions`/`HistoryOptions` and
entry functions from localexec. Preserve explicit flag presence separately from
values for --env-file and --blueprint-dir. Cobra adapters only bind and validate;
localexec retains acquisition, locking, result reservation, execution and history.
Do not convert typed options back to argument slices. `driver debug` needs a raw
protocol passthrough adapter: document its info/validate/run syntax in help while
delegating raw arguments/streams to driverexec.Serve without Cobra interference.

Inject context, streams, environment snapshot, build info and a lazy client factory.
Construct a fresh command tree per invocation/test; avoid package-global flag state.
Bind RPC flags at each remote group, not the root, to prevent native flag leakage.
Resolve config/environment options in execution, after parsing and help/completion
exits. Preserve whether --config was explicitly supplied so absence of the default
file differs from an explicit missing file.
Add an injected terminal detector for presentation tests. Color is shared by
human-facing paths, with machine/protocol writers bypassing it. Auto colors the
destination terminal, honoring NO_COLOR/TERM, with always/never overrides. Prompts
and pagers are deferred at the user's explicit request; their flags reject.

### Generated versus handwritten daemon behavior

| CLI path | RPC(s) | Generated mapping / explicit behavior |
| --- | --- | --- |
| blueprint fetch/list | FetchBlueprint/ListBlueprints | Direct bindings and views |
| blueprint show | ListBlueprints | Small Go exact-reference selector; not-found error |
| operation list/show | ListOperations | Generated list; small Go exact-name selector |
| instance init/get/list | InitInstance/GetInstance/ListInstances | Positions, filters, bounded InstanceConfig file |
| instance exec | SubmitCommand | Arguments file/key; optional typed wait hook |
| instance delete | SubmitCommand | Constant builtin.delete; optional verified-delete hook |
| command list/get | ListCommands/GetCommand | Positions/flags, enum and pagination bindings |
| command wait | GetCommand/GetInstance | Small Go command adapter and typed polling workflow |
| command stdout/stderr | ReadCommandLog | Constant stream, offset/limit, raw or JSON output |

Use protobuf methods/fields/comments for ordinary command generation. Document
conventions for method naming, kebab-case flags, enums and message file input.
Do not create a command metadata catalog that has to repeat each RPC definition.
Proto3 does not encode the current positional shortcuts or all requiredness/range
rules; keep those exceptional policies in narrow Go adapters. Do not make the
presentation structs a place to hide request defaults, command aliases or workflows.
The accepted spec records the naming/binding conventions; ADR-0001 records exceptions.
Emit direct typed Connect calls; avoid a redundant wrapper client.

### Configuration and rendering

Three distinct sources of truth:

| Source | Responsibility |
| --- | --- |
| .proto files | RPC methods, messages, field types and comments for generated commands |
| Go presentation structs | Response tables, details, formatting and pagination labels |
| .inframill.yaml cli section | User endpoint, timeout, output and color defaults |

Illustrative Go presentation definition (names to be finalized):

```go
Table{
    Rows: "instances",
    Columns: []Column{
        {Field: "name", Header: "Name"},
        {Field: "id", Header: "Instance UUID"},
        {Field: "status", Header: "Status", Format: EnumLabel},
    },
    Empty:      "No instances found.",
    NextCursor: "next_cursor",
}
```

Register presentation structs against generated response descriptors. Validate
field paths, kinds, nested sections and formats in tests and before requests, so
proto changes cannot silently break layouts. Protobuf reflection is sufficient
for this thin rendering layer; generated Go accessor code is not required.
Unconfigured responses use a complete descriptor-based fallback. Use protojson
directly for JSON regardless of table configuration. Reuse go-pretty and
clioutput.View; replace the response-type switch with definitions and a shared
renderer. Local history retains its versioned presentation and shares primitives.
Keep deletion confirmation typed in Go with its existing JSON fields.

User configuration uses the existing file:

```yaml
cli:
  endpoint: http://127.0.0.1:8080
  timeout: 2m
  output: table
  color: auto
```

Resolve explicit flags, then supported environment overrides, then cli settings,
then defaults. Support INFRAMILL_ENDPOINT/INFRAMILL_TIMEOUT and the separate
NO_COLOR rule; no additional environment variables are needed. NO_COLOR disables
automatic color; explicit --color always/never wins. Config output applies to
structured commands/history, never to protocol/result streams or raw-log defaults.
Help/version/completion bypass file loading and use flags/environment for styling.

Remote commands tolerate only absence of the implicit default file. An explicitly
selected file must exist, even when its settings are overridden by flags. Existing
unreadable/malformed files reject. Decode a typed cli section, distinguishing missing
from explicitly empty values. Reject unknown/duplicate cli keys and structural/type
errors; validate scalar values after precedence resolution. Missing section/fields
fall through to defaults. A CLI-only file requires no handlers. Ignore unrelated
server sections semantically when loading remote settings; never resolve their
environment references, probe Drivers, validate server settings or open storage.
Native server/exec retain their required file and server validation semantics.

The maintainer's harness extension now exposes WithAllowedSections and
Config.Sections. Native appconfig opts into cli and validates the typed section
outside harness. Remote settings use an application-owned YAML node reader that
validates the document and cli shape without decoding server settings or resolving
their environment references. No stripped file, copied harness decoder or protected
edit is needed. CLI scalar settings remain literal in the remote reader; native
loading preserves harness environment-reference semantics.

### Generation workflow

Add the local plugin to the existing Go Buf generation template using an argument
array (for example `local: [go, run, ./internal/cligen/cmd]`). It must compile without
depending on the generated CLI output, including from a clean checkout. Keep all
generated output under gen and run the complete generation set together so Buf's
clean behavior cannot remove another generator's output without recreating it.

Validate descriptors and naming/binding conventions before producing a successful
plugin response. Generation must not read user configuration or presentation Go
structs, or depend on generated CLI output to bootstrap. Emit no
timestamps or absolute workspace paths; use stable ordering and formatted source.
The existing temporary-output drift script compares the complete gen tree, including
CLI additions and removals. Test this from a clean fixture with generated output
absent. Keep TypeScript generation separate and sequential after Go when proto changes;
no TypeScript schema edits are expected for this refactor.

## Data and interface changes

No RPC methods/fields, persisted records, Instance JSON or Driver messages change.
New CLI features are those in the spec; current names are retained because the
requested cleanup does not require renaming them. Keep `main.version` and
`main.debugVersion` linker targets. The existing YAML gains an optional cli section;
there is no separate source-development config format. Go presentation definitions
are internal types, not serialized user configuration. Update only the root README
as the user guide.

## Error and safety design

Capture present behavior before changing it. In particular, remote Connect errors
currently generally become exit 3; do not introduce a new mapping as incidental
cleanup. Silence Cobra automatic errors/usage and supply a safe formatter that does
not echo rejected values. Test flag parser failures using synthetic secret markers.
Preserve local SIGPIPE handling, bounded writes and cancellation behavior, including
result-file failure taking precedence over operation status.

Use one cancellable deadline across submit plus wait. Never retry a submission.
During delete waits, only Instance not-found confirms deletion; surface other RPC
errors, retain failed outcomes and handle disappearing Command records until the
deadline. Characterize current behavior before any correction; update spec scope
if tests uncover an unrelated behavioral defect.

## Test strategy

First retain the existing executable tests as the contract baseline. Add a focused
in-memory command harness for flag/help/input/client-factory assertions and fake
typed clients for wait sequencing. Keep real executable tests for process exits,
signals, Driver passthrough, raw streams, local file/ownership behavior and daemon
survival after client cancellation. Use synthetic secrets, temp data and loopback
daemons only; no configured infrastructure or user data roots.

Test the generator independently with descriptor fixtures, then compile and
execute generated fixture code against a fake service. This proves extension and
request/view correctness independently of generated golden text. Negative fixtures
cover coverage drift and unsupported schemas. Test Go presentation definitions
against descriptors separately. Add YAML precedence/absence/native-integration
fixtures for the accepted section boundary. Golden output tests retain empty,
nested, failed and paginated response semantics, plus --columns validation.

Run task check:spec for these planning records. During implementation run narrow
tests and task check:fast after meaningful slices; use task go:fix when needed.
Run the relevant tests/cli daemon scenarios and task test:engine for final transport
regression coverage, then task check:handoff once. Record exact commands and any
unmet acceptance criteria in tasks.md.

## Rollout and rollback

Implement in vertical slices on a branch: contract baseline, native tree, one
generated list command, complete generated catalog, then remove the old dispatcher.
Temporary adapters may exist between commits but are removed before handoff. Ship
one binary after the complete command matrix passes. Rollback replaces the binary;
there is no data migration or runtime configuration rollback. Do not ship two CLI
implementations or an indefinite legacy mode.

## Alternatives considered

- Handwritten Cobra commands: straightforward, but request/table boilerplate still
  grows with the API and does not meet the requested generation outcome.
- Pure runtime reflection/generic RPC CLI: easy API exposure, but positional UX,
  bounded file inputs, readable details and verified waits still need policy.
- Protobuf custom options: feasible, but CLI layout metadata would enter the shared
  service schema and other language tooling. Descriptor-validated Go structs
  keep presentation separate without permitting silent drift.
- A sidecar YAML defining commands, bindings and tables: rejected during discussion.
  Protobuf should drive generation; Go structs should configure rendering only.
- A universal CLI generator/framework: unnecessary for this repository's ten unary
  methods. Keep the plugin small and require acceptance tests before expanding it.

Official references supporting the selected building blocks:

- [Cobra commands and groups](https://cobra.dev/docs/how-to-guides/working-with-commands/)
  and [shell completion](https://cobra.dev/docs/how-to-guides/shell-completion/).
- [Connect generated Go client](https://connectrpc.com/docs/go/getting-started/).
- [Go protogen](https://pkg.go.dev/google.golang.org/protobuf/compiler/protogen)
  and [Buf generation configuration](https://buf.build/docs/configuration/v2/buf-gen-yaml/).
