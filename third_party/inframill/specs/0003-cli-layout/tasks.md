# SPEC-0003 tasks

Status: Implemented

## Preparation

- [x] `T-001` Accept the maintainer's command removal and package boundary requirements.
- [x] `T-002` Record existing CLI and generation behavior from SPEC-0002.

## Implementation

- [x] `T-010` Remove package registration and use the shared builder in tests.
- [x] `T-011` Relocate packages, update generation, and add architecture checks.
- [x] `T-012` Update README, help snapshot and generator documentation.
- [x] `T-013` Restore missing destination files reported by the maintainer's build.

## Verification

- [x] `T-020` Prove AC-001 through AC-004 and record current verification below.
- [x] `T-021` Review protected-code/runtime-data diff and set Implemented status.

## Verification evidence

| Criterion | Evidence | Result |
| --- | --- | --- |
| AC-001 | `task check:fast`: removed-command integration tests and root help/completion tests | Passed 2026-09-13 |
| AC-002 | `go build cmd/main.go`; `task check:fast` including architecture and all owned Go packages | Passed 2026-09-13 |
| AC-003 | `task connect:generate`; `TestREQ005GeneratedExtensionCompilesAndRuns`; `task check:handoff` generation drift check | Passed 2026-09-13 |
| AC-003 | Temporary `go tool buf generate --template connect-rpc/buf.gen.yaml --output TEMP`; compare both trees, inject stale files into each temporary tree and require diff exit 1; hash CLI source before/after | Deterministic output, stale detection and source preservation passed 2026-09-13 |
| AC-004 | `task go:fix`; `task check:spec`; `task check:fast`; `task test:engine`; `task check:handoff`; `git diff --check` | Passed; 0 lint issues, 2 engine suites / 18 tests passed 2026-09-13 |
| NFR-001 | `git diff --name-only -- pkg/harness` and working-tree review | No protected-code or runtime-data changes |

Restoration: the maintainer's exact build initially failed because relocated CLI
destination files were missing while tracked imports/deletions remained. Restored
handwritten sources from their prior tracked versions with the specified paths,
regenerated bindings, and restored the fixture helper and change records. No Go
module dependency changes were needed. All listed checks ran after restoration.
