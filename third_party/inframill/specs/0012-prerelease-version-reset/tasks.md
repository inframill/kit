# SPEC-0012 tasks

Status: Implemented

## Preparation

- [x] `T-001` Record user authorization, accepted scope and ADR-0010.
- [x] `T-002` Inspect formats, prior spec, README and compatibility-related code.

## Implementation

- [x] `T-010` Reset current format versions and fixtures (REQ-001).
- [x] `T-011` Validate current records read-only and test rejection/preservation (REQ-002).

## Verification

- [x] `T-020` Run task check-fast (passed; Go generation, compile and package tests with vet disabled).
- [x] `T-021` Run task check-full and inspect for compatibility branches (passed; no owned compatibility readers, aliases or migrations).

## Documentation and handoff

- [x] `T-030` Clean guide, clarify prerelease agent instructions and record historical version supersession.
- [x] `T-031` Record evidence and mark Implemented after every criterion passes.

## Verification evidence

| Criterion | Evidence | Result |
| --- | --- | --- |
| AC-001 | TestDebugSubcommandProtocol, TestLocalExecReuseAndResults, TestLocalHistoryOutput, store version assertion, restart/lifecycle suites; task check-fast and task check-full | Passed |
| AC-002 | TestREQ002InvalidCommandStoresPreserveInstance (2/99; v1 unknown queued/terminal and missing command); Driver protocol/schema rejection tests; owned source review | Passed |
| AC-003 | README/AGENTS.md review; task check-fast; task check-full (13 specs, architecture, CLI, 19 daemon tests, Go/TS generated drift) | Passed |

Checks completed on 2026-09-14 with vet disabled. No lint was run and no protected
harness files changed. No runtime user data was opened or modified. The source
audit found no existing compatibility implementation to remove; strict rejection
checks remain. Dependencies, OCI/IP versions and release tooling are unchanged.
