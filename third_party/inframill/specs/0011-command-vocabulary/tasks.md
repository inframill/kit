# SPEC-0011 tasks

Status: Implemented

## Preparation

- [x] `T-001` Record approved terminology, format boundaries and ADR-0009.
- [x] `T-002` Inspect domain, Driver, CLI generation, persistence and prior specs.

## Implementation

- [x] `T-010` Rename typed contracts, wire fields and owned fixtures (REQ-001/002).
- [x] `T-011` Add nested execution CLI, presentation and generated bindings (REQ-003).
- [x] `T-012` Version and reject incompatible stores/protocols without writes (REQ-004/005).

## Verification

- [x] `T-020` Run task check-fast and prove negative/preservation cases.
- [x] `T-021` Run task check-full and inspect owned/generated scope.

## Documentation and handoff

- [x] `T-030` Update root README, examples and CLI snapshots.
- [x] `T-031` Record evidence and mark Implemented after all criteria pass.

## Verification evidence

| Criterion | Evidence | Result |
| --- | --- | --- |
| AC-001 | task check-fast: domain, Backend, Driver, Connect and generator suites; task check-full: CLI integration and 19 daemon tests using renamed RPCs | Passed |
| AC-002 | TestREQ003RetiredCommandPaths; TestREQ003HelpAndCompletionSnapshots including command/execution groups; TestClientCommands including catalog show and execution list with inherited output/endpoint flags, status/limit binding; TestCLIOutputSelection; TestClientValidationAndFailures; remote wait/delete/log tests | Passed |
| AC-003 | TestREQ004CommandProtocolVocabulary; TestREQ004RejectLegacyCommandProtocol; TestREQ004CommandRecordVocabulary; TestREQ004CommandStoreVersionsPreserveInstance (versions 1 and 99, queued old records, byte-for-byte preservation through open/Spawn/Close); existing restart/history tests | Passed |
| AC-004 | task check-fast and task check-full; 12 valid specs, architecture checks, CLI integration, all 19 daemon tests, Go/TypeScript generation and both drift checks; source diff review | Passed |

2026-09-14: `task check-fast` passed after typed/wire/CLI changes and legacy
store/Driver rejection tests. Help and completion fixtures regenerated from the CLI.

2026-09-14 final verification:

- `task check-fast` passed with Go tests using `-vet=off`. Initial iterations found
  a queue field reference and stale CLI paths/snapshots; these were corrected before
  the successful gate. Sandbox cache restrictions required the approved unrestricted
  gate execution used by existing loopback fixtures.
- `go build -o /tmp/inframill-command-cli ./cmd` produced the temporary CLI used to
  regenerate root, command, command-execution, Driver and Instance help snapshots
  and four shell completion digests. The final fast gate verified exact matches.
- `task check-full` passed in one run after the successful fast slice: Go package
  checks, 12 valid specs, architecture, CLI integration, TypeScript compilation,
  19 daemon tests across two suites and both generated drift checks. Node emitted
  its existing non-failing experimental localStorage warning.
- Changed owned Go files were formatted with standalone gofmt. No linting or vet
  ran. Generated Go/CLI/TypeScript content comes from Buf generation; help and
  completion fixtures come from the executable. No protected harness or user
  runtime data was changed. All storage/protocol fixtures were disposable.
- Final review confirmed preserved instance.json schema, InstanceID/CommandID and
  file locations, explicit protocol/SQLite/local-result version 2, no compatibility
  aliases or automatic data migrations. All acceptance criteria are proven and
  no required checks were skipped.
