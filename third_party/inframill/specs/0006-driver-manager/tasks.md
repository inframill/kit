# SPEC-0006 tasks

Status: Implemented

## Preparation

- [x] `T-001` Record user-approved contract and ADR (REQ-001 through REQ-005).
- [x] `T-002` Inspect current registry, executable host, config and tests.

## Implementation

- [x] `T-010` Introduce public Manager, health, protocol and private process (AC-001/002).
- [x] `T-011` Move debug Driver and adapt Backend/config/composition (AC-003/004).
- [x] `T-012` Update focused tests, architecture rules and gate scope.

## Verification

- [x] `T-020` Run check-fast and check-full without linting.

## Documentation and handoff

- [x] `T-030` Update root README, config and Blueprint examples.
- [x] `T-031` Record evidence and mark implemented only after all criteria pass.

## Verification evidence

| Criterion | Evidence | Result |
| --- | --- | --- |
| AC-001 | `task check-full`: TestPackageBoundaries, TestDriverManagerRemainsIndependent, obsolete-directory assertions; `task check-fast` includes pkg/driver | Passed |
| AC-002 | TestREQ003ConfiguredHealth, TestREQ005RecoveryRoutingAndIsolation, TestREQ003ConcurrentHealthAndCancellation, TestExecutableLimits | Passed |
| AC-003 | TestDriverConfiguration, TestREQ003InvalidPathsRemainConfigured, TestREQ004BlueprintValidation, TestREQ004UnavailableDriverRejectsInitialization, TestREQ004LegacySystemsManifestIsPreserved | Passed |
| AC-004 | `task check-full`: executable protocol/redaction/cancellation tests; TestCustomExecutableDriver with alias and unrelated offline entry; TestHarnessComposedServer with offline entry; all 18 daemon tests and both generated drift checks | Passed |

Verification on 2026-09-13: `go build -o /dev/null ./cmd`, `task check-fast`,
and `task check-full` passed. Gates required execution outside the sandbox for
local loopback fixtures and the Go cache. Standalone gofmt was applied to changed
owned handwritten Go files. No linters, go vet, lint autofixes, live infrastructure
operations, runtime data changes or protected harness edits were performed.
The full gate validated all seven specs. Generated source has no content changes.
