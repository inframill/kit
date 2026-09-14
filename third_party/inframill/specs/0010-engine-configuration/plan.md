# SPEC-0010 implementation plan

Status: Implemented
Spec: `specs/0010-engine-configuration/spec.md`

## Approach

Allow engine alongside cli through the existing harness section hook. Decode it
strictly into typed EngineSettings using harness.Settings. Split the path-only
ConnectSettings decoder from engine decoding. Load validates both before returning
the same composition Config consumed by server and local commands.

## Requirement mapping

| Requirement | Design element | Verification |
| --- | --- | --- |
| REQ-001 | Top-level typed engine and path-only handler decoder | Application, local CLI and daemon tests |
| REQ-002 | Existing defaults/path normalization and harness resolution | Application and CLI path tests |
| REQ-003 | Strict native decoders before composition | Negative unit and CLI side-effect/exit tests |
| REQ-004 | Existing isolated CLI preference reader | CLI fake-client and config tests |
| NFR-001 | Existing task gates and protected scope | Both gates and diff inspection |

## Components and boundaries

internal/appconfig owns the application section and strict Connect settings shape;
connectadapter validates the compiled mount path. Server/localexec continue to
consume appconfig.Config. pkg/harness remains unchanged.

## Data and interface changes

Only the application YAML contract and internal decoding types change. There are
no public Go domain, RPC, generated schema, Blueprint or persisted-format changes.

## Error and safety design

Wrap decode failures with safe errors. Reject legacy settings before composition,
including when new settings are also supplied, avoiding accidental default roots.
Retain existing exit mapping and environment redaction. No infrastructure checks.

## Test strategy

Adapt existing fixtures across appconfig, CLI, server and TypeScript daemon suites.
Add section/default/legacy rejection and remote-isolation coverage, plus native
failure tests with disposable probe and storage sentinels. Run task check-fast
after the implementation slice and task check-full once before handoff. No linting.

## Rollout and rollback

Update the bundled YAML and root README together, preserving user edits. Users
move fields explicitly; storage directories and their contents are never migrated.

## Alternatives considered

Separate root dataDir/drivers keys obscure shared ownership. Leaving them under
Connect couples application settings to HTTP configuration. Renaming main or
removing the required handler would expand scope. See ADR-0008.
