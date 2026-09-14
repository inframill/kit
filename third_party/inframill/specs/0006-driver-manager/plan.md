# SPEC-0006 implementation plan

Status: Implemented
Spec: `specs/0006-driver-manager/spec.md`

## Approach

Move executable protocol/host into pkg/driver, private process runner below it,
and the bundled debug implementation/protocol server into internal/debugdriver.
Replace the internal registry with the three-method Manager. Copy inputs and
snapshots through JSON normalization; synchronize per-entry probes and snapshots.

## Requirement mapping

| Requirement | Design element | Verification |
| --- | --- | --- |
| REQ-001 | Manager and package boundaries | Architecture tests |
| REQ-002 | ID-keyed typed configuration | Config and manager tests |
| REQ-003 | Per-entry probes and cached health | Health and cancellation tests |
| REQ-004 | ValidateRequest and manifest rename | Backend/config tests |
| REQ-005 | OperationRequest and Backend mapping | Executable, CLI, daemon tests |
| NFR-001 | Existing process/protocol constraints, task scope | Both gates and source review |

## Components and boundaries

pkg/driver owns the public contract and executable mechanics; its internal/process
owns launch/cancellation/redaction. internal/debugdriver owns the bundled executable
and artifact codecs. Backend maps public Driver metadata to application definitions.
The public application pkg/inframill remains independent and unchanged.

## Data and interface changes

Map configuration IDs select drivers and operation namespaces. Rename Blueprint
systems to drivers without compatibility parsing or data migration. Protocol stays 1.

## Error and safety design

Invalid executables remain offline. Selected unknown/offline IDs fail validation
or preparation. No setup effects occur in Prepare. Preserve bounded I/O, controlled
environment, cancellation and secret handling. List clones cached status.

## Test strategy

Retain subprocess/secret/artifact tests and adapt Backend test doubles to Manager.
Add health, recovery, aliases, isolation and old-manifest rejection tests. Run
check-fast during meaningful slices and check-full at handoff. Only disposable
local fixtures are used; no linters or live infrastructure commands.

## Rollout and rollback

Update checked-in config and examples together. Existing runtime data is untouched.
Users explicitly update their own prerelease config; no compatibility shims.

## Alternatives considered

Keeping a backend registry would split ownership. Exporting the application domain
would couple the reusable manager to storage-facing types. See ADR-0004.
