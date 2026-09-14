# SPEC-0006: Public Driver Manager

Status: Implemented
Owner: Inframill maintainers
Created: 2026-09-13
Updated: 2026-09-13

## Problem

Driver configuration, executable health and dispatch span internal packages and
are keyed by executable names. One unavailable executable prevents startup.

## Outcome

A self-contained public Manager owns configured Drivers, health, Blueprint
validation and operation preparation. Configuration IDs select implementations.
The user explicitly authorized this prerelease contract change.

## Scope

Public pkg/driver and private process handling; internal debug Driver; Backend,
composition, examples and tests. Environment configuration, resident processes,
version resolution, new RPC/CLI health commands and migration tooling are deferred.

## Requirements

- **REQ-001:** pkg/driver MUST expose Manager with List, Validate and Prepare,
  typed requests/results and PreparedOperation.Execute. It MUST depend only on
  the standard library and its own internal packages. Backend MUST consume Manager.
- **REQ-002:** Application drivers MUST be an object keyed by configured ID,
  containing path and optional literal prefix args. IDs MUST match the existing
  lowercase name syntax and exclude builtin. Executable name/version metadata
  MUST remain separate; multiple IDs MAY select identical executable identities.
- **REQ-003:** Construction MUST probe every configured entry using info. Invalid
  paths, permissions, arguments, failed probes and invalid metadata MUST produce
  offline entries with reasons, not fail the whole catalog. List MUST return
  sorted independent snapshots with ID, metadata, status and check time.
  List reports cached probe health; Validate and Prepare re-probe selected entries
  so an offline executable can recover. Cancellation MUST propagate. Concurrent
  calls MUST be safe. Validation/run failure alone MUST NOT change info health.
- **REQ-004:** Manifest drivers MUST replace systems, keyed by configured ID.
  Manager.Validate MUST validate all referenced configurations, rendered source
  and instance Values using the existing executable validate protocol. Unknown or
  offline IDs MUST reject before publishing an Instance. Errors identify the ID.
- **REQ-005:** Manager.Prepare MUST select by configured ID and local operation,
  validate a private snapshot of the request without running effects, and return
  an executable prepared operation. Catalog names MUST use ID.operation. Offline
  unrelated entries MUST NOT block healthy operations. Admission and execution
  retain their existing preparation, queue, cancellation and log semantics.
- **NFR-001:** Protocol 1, bounded JSON, argument arrays, environment allowlisting,
  secret redaction, process cancellation and private atomic debug artifacts MUST
  remain intact. No env configuration, infrastructure effects during verification,
  protected harness edits, or automated linting are permitted. Both test gates
  MUST include pkg/driver package tests; no additional task entrypoints are added.

## Acceptance criteria

- **AC-001** (REQ-001, NFR-001): Architecture tests enforce the public package's
  independence and removal of the obsolete Driver/process packages; both gates pass.
- **AC-002** (REQ-002/003): Tests prove sorted health including missing, malformed,
  nonexecutable and repeated executable identities, snapshot isolation, cancellation
  and recovery on preparation.
- **AC-003** (REQ-004): Tests prove map configuration, Blueprint drivers validation,
  unknown/offline rejection and rejection of systems without deleting existing data.
- **AC-004** (REQ-005, NFR-001): Executable and application tests prove ID routing,
  operation preparation, validation errors, results, logs, secrets and cancellation;
  the CLI and daemon suites pass with the new configuration.

## Interfaces and examples

Application handler settings:

```yaml
drivers:
  local-debug:
    path: ./dist/inframill
    args: [driver, debug]
```

Blueprint manifest uses drivers: {local-debug: {}}. The operation is
local-debug.echo; info still reports name debug and its executable version.
List is an in-process API, not an added RPC or CLI command. Connected means the
last info probe succeeded, not that a resident process or remote service is alive.

## Failure behavior

Structural application config errors fail loading. Individual executable errors
are health results. Validation/preparation errors reject the selected operation;
execution errors retain existing command history. Construction cancellation fails
rather than publishing a partial catalog. Cached health is refreshed on use.

## Security and safety

Drivers remain trusted local executables. info and validate must be effect-free;
Prepare only invokes these commands. Execute invokes run. Known-secret redaction
and runtime-only secret injection are preserved. Paths/args must not carry secrets.

## Compatibility and migration

This deliberately supersedes SPEC-0000 REQ-008 and ADR-0000's internal registry,
name-based selection and fail-fast discovery, and their manifest systems contract.
SPEC-0005 test scope expands to pkg/driver. Existing application list configuration
and systems manifests reject without aliases. No stored user files are rewritten
or deleted; maintainers explicitly update their own configuration and Blueprints.
RPC and Driver wire versions remain unchanged; there is no generated API change.

## Open questions

None.
