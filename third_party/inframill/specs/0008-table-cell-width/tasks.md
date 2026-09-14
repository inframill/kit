# SPEC-0008 tasks

Status: Implemented

## Preparation

- [x] `T-001` Accept the user-requested table limit and document superseded rules.
- [x] `T-002` Read renderer, presentation contracts and pinned width helper code.

## Implementation

- [x] `T-010` Bound shared table headers/cells with visible truncation (AC-001).
- [x] `T-011` Update format tests and prove full JSON/native values (AC-002).

## Verification

- [x] `T-020` Run task check-fast and task check-full (AC-003).

## Documentation and handoff

- [x] `T-030` Update README and record verification evidence.
- [x] `T-031` Mark Implemented only after every acceptance criterion passes.

## Verification evidence

| Criterion | Evidence | Result |
| --- | --- | --- |
| AC-001 | TestREQ001TableCellWidth; TestREQ002TablePresentationOnly | Passed |
| AC-002 | TestResponseFormats (all remote JSON round trips); TestREQ002DriverTableKeepsFullJSON; TestLocalHistoryOutput; TestLogOutputFormats; TestWaitTables; TestInstanceListTable | Passed |
| AC-003 | task check-fast; task check-full (9 specs, architecture, CLI integration, 19 daemon tests, generated Go/TypeScript drift) | Passed |

Verification on 2026-09-13: `task check-fast` passed after the shared renderer
change. `task check-full` ran once and passed with the final native JSON test and
updated table/identity assertions, including all 19 daemon tests and both generated
drift checks. Both gates used approved execution outside the sandbox for Go cache
access and disposable loopback fixtures. No checks were skipped. Standalone gofmt
was applied only to changed owned handwritten Go files. Generated bindings and
pkg/harness have no changes. No lint tools, go vet, live infrastructure mutations,
credentials or user runtime data were used.
