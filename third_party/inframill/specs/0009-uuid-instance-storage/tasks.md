# SPEC-0009 tasks

Status: Implemented

## Preparation

- [x] `T-001` Read baseline/ADRs, storage/discovery/deletion, CLI/server and path-dependent fixtures.
- [x] `T-002` Draft requirements, acceptance criteria, design and ADR-0007; identify gaps.
- [x] `T-003` Resolve existing-data transition: user selected reject/preserve, no migration or compatibility.
- [x] `T-004` Review and mark spec/plan/ADR Accepted before implementation.

## Implementation

- [x] `T-010` Add required layout marker and UUID path/discovery rules (AC-001/002).
- [x] `T-011` Implement UUID staging/publication and metadata-based collision checks (AC-003).
- [x] `T-012` Preserve ownership and guard deletion/failure persistence against substitution (AC-004).
- [x] `T-013` Retain name-based CLI selection and supply UUID Driver paths (AC-005).
- [x] `T-014` Update Backend, Blueprint, CLI and daemon fixtures, including crash cleanup and negative assertions (AC-001 through AC-006).

## Verification

- [x] `T-020` Run task check-fast after meaningful slices; record failures and fixes.
- [x] `T-021` Run task check-full once before handoff; verify unchanged generated output.

## Documentation and handoff

- [x] `T-030` Update root README layout, transition and artifact/barrier examples.
- [x] `T-031` Record exact verification evidence and remaining limitations.
- [x] `T-032` Mark Implemented only after all acceptance criteria pass.

## Verification evidence

| Criterion | Evidence | Result |
| --- | --- | --- |
| AC-001 | `TestUUIDStorageLifecycle`, `TestLocalExecReuseAndResults`: UUID leaves, supported marker, names, private directories, source/log/artifact persistence | Passed fast/full |
| AC-002 | `TestUUIDStorageDiscoveryRejections`, `TestLegacyAndUnsupportedStoresArePreserved`, `TestLegacyLocalWorkspacePreserved`, `TestREQ007UUIDStorageTransition`: unsupported identities/layouts rejected before workers with bytes and symlink targets preserved | Passed fast/full |
| AC-003 | `TestUUIDStorageConcurrentCollisions` (six separate Backend objects, same name and same ID), `TestREQ004PublicationFailuresPreserveData`, `TestUUIDStorageReadableFailureIsolation`, daemon concurrent initialization | Passed fast/full |
| AC-004 | `TestREQ005SubstitutionPreservesReplacement`, `TestREQ005DeletionInitializationLock`, `TestREQ005PartialDeletionFails`, `TestQueuedDeletion`, existing ownership-conflict coverage | Passed fast/full |
| AC-005 | `TestLocalMultipleInstanceSelection`, local history/recovery and configured-daemon tests, `TestCustomExecutableDriver` (actual stdin UUID paths/binding), artifact roundtrip, daemon restart/deletion/name-reuse tests | Passed full |
| AC-006 | `TestStartupFailureClosesApplication`, `TestREQ007UUIDStorageTransition`, existing storage-free Driver health/help tests, README/ADR, both gates and unchanged generated Go/TypeScript | Passed full |

Implementation evidence (2026-09-13): the user authorized implementation of the
accepted spec/ADR, including rejection/preservation with no migration or compatibility.

- `task check-fast` passed after UUID layout, ownership and fixture changes.
  Earlier iterations exposed an obsolete private-parent assertion, substitution
  tests incorrectly requiring admission after moving SQLite, and a preservation
  assertion inserted in the wrong server test. All were corrected; the final
  fast gate passed all owned package tests with vet disabled.
- Added byte/directory/symlink snapshots for rejected roots; 15 discovery cases,
  10 substitution cases, cancellation/late publication collisions, namespace lock
  coordination and partial deletion under unprivileged permissions.
- The first `task check-full` passed fast/specs/architecture, then caught a fresh-root
  CLI fixture that changed its Go variable without updating its disposable config.
  Updated that config; the second full gate passed end to end:
  - clean Go generation, compile and all owned package tests with `-vet=off`;
  - 10 feature specs and architecture tests;
  - CLI integration and Engine fixture packages;
  - TypeScript generation/compilation and 19 real-daemon tests across two suites;
  - Go and TypeScript generated-drift checks.
  Node emitted its existing experimental localStorage warning; it did not fail a check.
- Owned handwritten Go changes were formatted with standalone gofmt. No linting
  or vet was run. The protected harness and pre-existing .inframill.yaml user edit
  remain untouched. All test data is disposable; no live infrastructure was used.
- No acceptance criteria remain unmet and no required check was skipped. Intentional
  prerelease format rejection is documented in README and accepted ADR-0007.

Remaining limitations are intentional: no old-data compatibility or migration,
no persistent name index or startup optimization, and no isolation from hostile
concurrent filesystem mutation by another process with the same OS permissions.
