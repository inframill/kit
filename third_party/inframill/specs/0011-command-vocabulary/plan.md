# SPEC-0011 implementation plan

Status: Implemented
Spec: `specs/0011-command-vocabulary/spec.md`

## Approach

Rename owned typed contracts and serialization fields as one coherent slice.
Separate action metadata from execution records, adapt protobuf-driven CLI naming
with explicit catalog/execution paths, regenerate both language bindings. Bump only
the changed Driver, command-store and local-result formats.

## Requirement mapping

| Requirement | Design element | Verification |
| --- | --- | --- |
| REQ-001 | Domain/Driver/Backend typed rename | Package and integration tests |
| REQ-002 | Protobuf, Driver requests, error codes and serializers | Wire and adapter tests |
| REQ-003 | Generator exceptions, nested runtime groups, output definitions | CLI paths, snapshots, lifecycle |
| REQ-004 | Protocol 2, SQLite 2 read-only preflight, strict records | Legacy rejection/preservation tests |
| REQ-005 | Existing instance schema/layout and execution behavior | Lifecycle and daemon regression suites |
| NFR-001 | Existing generation and task gates | Fast/full and diff review |

## Components and boundaries

Engine remains neutral. Backend owns execution persistence and routing. Driver
Manager owns executable dispatch. Connect remains thin. The CLI generator emits
paths and the runtime assembles nested groups with flags on the resource parent.
No new package boundary or protected source change is required.

## Data and interface changes

CommandDefinition, PreparedCommand, CommandExecution and execution query/page types
replace prior names without aliases. Store request.command, local result command,
Driver commands/command and RPC execution/executions are the new wire fields.
Driver protocol and SQLite user_version become 2; local-result becomes v2.
instance.json, files, Blueprint schemas and protobuf package v1 remain unchanged.

## Error and safety design

Read the existing database version in mode=ro before acquiring Instance ownership
or writing failure metadata. Incompatible Instances can be exposed as failed in
memory. Retain a second version check in normal store opening. Decode stored
records strictly to reject old/mixed keys rather than ignoring them. Preserve
all current secret and subprocess protections.

## Test strategy

Adapt owned unit, fake executable, generator synthetic-extension, snapshot, CLI
integration and daemon fixtures. Add negative old-path/wire coverage and old-store
byte preservation tests. Run task check-fast after meaningful slices; run task
check-full before handoff. No linting, vet or real infrastructure operations.

## Rollout and rollback

Update Driver binaries, application and generated clients together. Preserve old
stores without automatic migration; a prior binary can access its prior formats.
Reverting source must never rewrite or delete newly created or existing user data.

## Alternatives considered

Using Command for both metadata and records obscures the distinction. Retaining
Operation as an alias perpetuates ambiguity. Renaming files offers no semantic
benefit and adds migration work. A broad Driver redesign is separately deferred.
