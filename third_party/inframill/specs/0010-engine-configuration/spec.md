# SPEC-0010: Top-level engine configuration

Status: Implemented
Owner: Inframill maintainers
Created: 2026-09-14
Updated: 2026-09-14

## Problem

Shared storage and Driver settings are nested under the Connect handler even
though local execution and Driver inspection consume them without HTTP serving.

## Outcome

The application owns a top-level engine object containing dataDir and drivers.
Connect handler settings describe only the RPC mount path. The user approved
this change while retaining the harness main section.

## Scope

### In scope

- Native application decoding, Connect settings, shipped YAML, documentation and fixtures.

### Out of scope

- Renaming main, harness changes, removing the native handler requirement,
  RPC/protocol changes, storage migrations or compatibility aliases.

## Requirements

- **REQ-001:** Native server, exec and driver list MUST consume dataDir and drivers
  exclusively from the top-level engine object. Connect handler settings MUST
  accept only path. Native loading MUST continue to require exactly one
  connect-engine handler; server mount-path validation MUST remain unchanged.
- **REQ-002:** An omitted or empty engine object MUST select .inframill and no
  Drivers. Omitted/empty dataDir MUST default to .inframill; relative dataDir
  MUST remain cwd-relative and executable paths MUST remain config-relative.
  Driver ID maps, literal args, offline executable handling and native harness
  environment resolution MUST retain their existing semantics.
- **REQ-003:** Native loading MUST reject non-object engine sections, unknown or
  duplicate fields, invalid field types and legacy handler dataDir/drivers,
  including mixed old/new configuration, before probing or storage effects.
  Errors MUST NOT disclose supplied values. Existing exit codes MUST remain:
  invalid config is exec/driver list 2 and server 3.
- **REQ-004:** Remote CLI preference loading MUST continue to inspect only cli,
  without validating or resolving engine settings, probing Drivers or opening
  storage. CLI-only files MUST remain valid for remote commands.
- **NFR-001:** No protected harness edits, user data rewriting/deletion, live
  infrastructure effects or automated linting are permitted. Both existing
  verification gates MUST pass with Go vet disabled.

## Acceptance criteria

- **AC-001** (REQ-001/002): Application tests prove the new section, omitted/empty
  defaults, path rules, native environment references and unchanged Driver maps;
  local exec, Driver list and daemon fixtures use the new layout successfully.
- **AC-002** (REQ-003): Negative tests reject malformed and legacy/mixed layouts,
  preserve stored sentinel data and create no probe marker or new storage;
  CLI tests prove the existing native config error exit codes.
- **AC-003** (REQ-004): CLI tests succeed with invalid/unresolved engine fields
  without constructing native dependencies, and retain CLI-only loading.
- **AC-004** (NFR-001): task check-fast and task check-full pass; source review
  confirms no harness/runtime-data changes and current documentation/examples.

## Interfaces and examples

```yaml
engine:
  dataDir: .inframill
  drivers:
    debug:
      path: ./dist/inframill
      args: [driver, debug]
handlers:
  - type: connect-engine
    settings:
      path: /inframill.v1.EngineService/
```

main, logger, workers and cli retain their names and contracts.

## Failure behavior

No old/new precedence or fallback is defined: old handler engine fields reject.
Missing/invalid selected files retain existing safe errors and exit codes.
Offline executable health remains distinct from structural configuration failure.

## Security and safety

Configuration parsing is read_only: it reads the selected file and supplied
startup environment without probes or storage access. Server/exec retain their
existing mutation classes, effects and verification. Driver listing remains
read_only with effect-free info probes only after valid configuration. Tests
use disposable directories, fake clients and local fixture executables.

## Compatibility and migration

This user-approved prerelease breaking change supersedes SPEC-0000 REQ-019's
handler location and SPEC-0006's application-handler-settings example. Driver
map and path semantics otherwise remain unchanged. Users explicitly move the
fields into engine; no compatibility parser or automatic migration is added.
Stored formats and existing Instance data are untouched. See ADR-0008.

## Open questions

None.
