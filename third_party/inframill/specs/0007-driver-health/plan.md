# SPEC-0007 implementation plan

Status: Implemented
Spec: `specs/0007-driver-health/spec.md`

## Approach

Add neutral DriverInfo/DriverStatus and required Backend.ListDrivers. Backend maps
Manager entries, Engine isolates/sorts results. Native composition constructs the
existing Backend and Blueprint source without calling Engine.Start. Add a native
CLI leaf and exclude only ListDrivers from RPC CLI emission. Record ADR-0005.

## Requirement mapping

| Requirement | Design element | Verification |
| --- | --- | --- |
| REQ-001 | Neutral query and Backend projection | Engine and Backend tests |
| REQ-002 | Protobuf method and thin adapter | Connect and daemon tests |
| REQ-003 | localexec composition without Start | CLI tests with unusable data root |
| REQ-004 | Native list presentation and flags | CLI output, failure and snapshot tests |
| NFR-001 | Existing process and package boundaries | Both gates and architecture tests |

## Components and boundaries

Keep current import boundaries. CLI localexec owns native composition and summary
serialization, output.View renders tables. Connect owns protobuf conversion.
Manager remains independent and Engine uses only neutral application contracts.

## Data and interface changes

Add DriverInfo summary, Backend/Engine query and protobuf request/response/enum.
Regenerate Go/Connect and TypeScript. No persistence or executable protocol change.

## Error and safety design

Offline health is data. Propagate context and Manager errors. Validate CLI options
before probes; preserve the existing environment policy and safe diagnostics.

## Test strategy

Use fakes and disposable local executable fixtures. Verify cached API snapshots,
local fresh construction, no HTTP client, no Instance state access and all formats.
Run task check-fast after meaningful slices and task check-full at handoff.

## Rollout and rollback

Deploy the additive service with regenerated bindings. Update custom Backend
implementations for the required method. No stored data migration.

## Alternatives considered

Importing pkg/driver into Engine violates its neutral contract. Starting Engine
workers for inspection risks changing Instance state. An automatically generated
remote driver list would contradict the requested local CLI behavior.
