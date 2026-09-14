# SPEC-0010 tasks

Status: Implemented

## Preparation

- [x] `T-001` Record user-approved scope, accepted spec and ADR-0008.
- [x] `T-002` Inspect shared loading, harness section hook and native/remote fixtures.

## Implementation

- [x] `T-010` Split engine and Connect settings; update composition fixtures (REQ-001/002).
- [x] `T-011` Add strict legacy/malformed rejection and isolation tests (REQ-003/004).

## Verification

- [x] `T-020` Run task check-fast after the implementation slice.
- [x] `T-021` Run task check-full and inspect owned changes.

## Documentation and handoff

- [x] `T-030` Update bundled YAML and root README preserving unrelated user edits.
- [x] `T-031` Record evidence and mark Implemented only after all criteria pass.

## Verification evidence

| Criterion | Evidence | Result |
| --- | --- | --- |
| AC-001 | TestREQ001EngineSectionDefaults, TestDataDirectorySettings, TestDriverConfiguration, TestREQ002EngineDriverEnvironment, TestREQ003InvalidPathsRemainConfigured, TestExecConfiguredDataDirectory, TestConfiguredDataSharedWithDaemon, TestREQ003LocalDriverHealth, TestHarnessComposedServer; 19 daemon tests | Passed |
| AC-002 | TestREQ003RejectEngineLayouts, TestInvalidDataConfiguration, TestREQ003EngineConfigRejectionBeforeEffects (12 native command/layout combinations), TestExecDataConfigRejections | Passed |
| AC-003 | TestREQ013SectionIsolation (CLI-only, invalid engine shape and unresolved references), TestREQ004PrecedenceAndColumns (remote fake-client invocation with invalid/unresolved engine) | Passed |
| AC-004 | task check-fast; task check-full: 11 valid specs, architecture, CLI integration, 19 daemon tests, Go/TypeScript drift checks; source diff review | Passed |

2026-09-14: `task check-fast` passed (Go regeneration, build and owned package
tests with `-vet=off`). Standalone gofmt applied to changed owned Go files.

2026-09-14: `task check-full` passed outside the sandbox after the initial sandboxed
attempt was blocked by a loopback listener permission failure (and reported a Go
module-cache write restriction). The successful gate includes Go tests with
`-vet=off`, spec/architecture checks, CLI integration, TypeScript compilation,
all 19 daemon tests and both generated drift checks. Node emitted a non-failing
localStorage experimental warning during TypeScript generation.

Final diff review confirms that the bundled YAML preserves the user's existing
main-section whitespace edit, and all other changes are owned source, fixtures,
documentation and this accepted contract. No generated content, protected harness
files or runtime data changed. No linters, go vet or live infrastructure commands
were run. No acceptance criteria remain unmet and no required checks were skipped.
