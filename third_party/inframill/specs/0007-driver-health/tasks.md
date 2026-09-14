# SPEC-0007 tasks

Status: Implemented

## Preparation

- [x] `T-001` Read baseline, accepted changes, ADRs and nearby implementation.
- [x] `T-002` Discuss and accept the proposed contract and ADR-0005 before implementation (user approved cached API health and fresh local construction).

## Implementation

- [x] `T-010` Add Engine/Backend health query and focused tests (AC-001).
- [x] `T-011` Add RPC schema/adapter and tests; regenerate Go and TypeScript bindings (AC-002).
- [x] `T-012` Add native driver list and output/failure tests (AC-003).

## Verification

- [x] `T-020` Run task check-fast after meaningful slices.
- [x] `T-021` Run task check-full including daemon and generated drift (AC-004).

## Documentation and handoff

- [x] `T-030` Update README and help snapshots; record final verification evidence below.
- [x] `T-031` Mark Implemented only after all acceptance criteria pass.

## Verification evidence

| Criterion | Evidence | Result |
| --- | --- | --- |
| AC-001 | TestREQ001ListDriversSnapshots; TestREQ001DriverHealthProjection | Passed |
| AC-002 | TestREQ002ListDriversRPC; TestHarnessComposedServer; daemon test "SPEC-0007 Driver health is cached and exposed through the generated client" | Passed |
| AC-003 | TestREQ003NativeDriverList; TestREQ004DriverListFailuresAndUtilities; TestREQ004DriverListProbeBoundaries; TestREQ003LocalDriverHealth; TestREQ003DriverListIsNative; TestREQ003HelpAndCompletionSnapshots | Passed |
| AC-004 | task check-fast; task check-full: 8 specs, architecture, CLI integration, 19 daemon tests, Go/TypeScript generated drift | Passed |

Verification on 2026-09-13: `task check-fast` and `task check-full` passed.
The initial sandboxed fast attempt could not bind loopback fixtures or write the
Go module cache; checks were rerun with approved execution outside the sandbox.
An intermediate fast run caught the expected changed help snapshot; the snapshot
was regenerated from the temporary CLI binary and the subsequent fast/full gates
passed. The full gate ran once and included final probe cancellation/redaction
coverage, all CLI integration tests, 19 daemon tests and both generated drift checks.
No checks were skipped. Standalone gofmt formatted only changed owned handwritten
Go files. Help snapshots were produced from `/tmp/inframill-driver-health`; Go and
TypeScript bindings were generated through the existing Task commands. Existing
completion digests remained unchanged. No lint tools, go vet, protected harness
edits, live infrastructure mutations or runtime user data changes were performed.
