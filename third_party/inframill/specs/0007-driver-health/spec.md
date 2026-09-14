# SPEC-0007: Driver health through Engine, API and local CLI

Status: Implemented
Owner: Inframill maintainers
Created: 2026-09-13
Updated: 2026-09-13

## Problem

Configured Driver health is available only through driver.Manager. Engine consumers,
API clients and CLI users cannot inspect it through their application interfaces.

## Outcome

Expose a typed Engine query, a thin Connect method and a local driver list command.
The user explicitly requested all three surfaces and direct Engine use by the CLI.

## Scope

Include configured identity, reported name/version, status, reason and check time.
Full executable configuration and operation metadata remain on existing Go Manager
and operation catalog interfaces. Refresh commands and remote CLI mode are deferred.

## Requirements

- **REQ-001:** Engine.ListDrivers(ctx) MUST return all configured Drivers as sorted,
  independent DriverInfo snapshots with ID, Name, Version, typed Status, Reason and
  CheckedAt. Backend MUST project cached Manager health without probing, storage
  access or Instance workers. Listing MUST work before Engine.Start and propagate
  cancellation and backend errors. Empty catalogs MUST return an empty slice.
  builtin.delete is an operation and MUST NOT appear as a configured Driver.
- **REQ-002:** EngineService.ListDrivers MUST expose an empty request and repeated
  typed DriverInfo response matching the Engine query, including connected/offline
  enum status and timestamp. Offline entries MUST remain successful query results.
  The adapter MUST only translate application results and errors.
- **REQ-003:** inframill driver list MUST load required native application config
  (./.inframill.yaml or --config FILE), construct Manager/Backend/Engine locally,
  and call Engine.ListDrivers without starting HTTP, contacting an API, acquiring
  workspace ownership, reading/writing Instance state or fetching Blueprints.
  Each invocation MUST perform Manager construction's bounded effect-free info probes.
  Existing driver debug protocol commands MUST retain their behavior.
- **REQ-004:** CLI MUST default to a table containing id, name, version, status,
  reason and checked_at, with --columns selection and --output/-o json returning
  {"drivers": [...]} containing the complete summary. JSON uses lowercase status
  and checkedAt timestamps. CLI output/color defaults apply; RPC endpoint/timeout
  flags MUST reject and their environment defaults MUST be ignored. Help/completion
  MUST bypass configuration and probes. Offline/empty results exit 0; invalid
  config/flags exit 2, runtime/output failure exits 3, cancellation exits 130.
- **NFR-001:** pkg/inframill MUST remain standard-library-only; Manager and process
  ownership, secret redaction and info-health semantics MUST remain intact. No
  infrastructure mutation, user data change, harness edits or linting are allowed.

## Acceptance criteria

- **AC-001** (REQ-001): Engine/Backend tests prove mapping, sort order, isolation,
  cached query behavior, empty catalogs, errors and cancellation before Start.
- **AC-002** (REQ-002): Connect tests prove connected/offline/empty entries, typed
  fields, timestamp and error translation; a real daemon query succeeds.
- **AC-003** (REQ-003/004): CLI tests prove local connected/offline listing with no
  client creation or data access, required config, fresh probes, JSON/table/columns,
  invalid options, offline help, empty results, cancellation and output failure.
- **AC-004** (NFR-001): Both task check-fast and task check-full pass, including
  architecture tests, deterministic Go/TypeScript regeneration and CLI snapshots.

## Interfaces and examples

Engine.ListDrivers(context.Context) ([]DriverInfo, error)
Backend.ListDrivers(context.Context) ([]DriverInfo, error)
EngineService.ListDrivers(ListDriversRequest) returns (ListDriversResponse)

```sh
inframill driver list
inframill driver list --config alternate.yaml -o json
inframill driver list --columns id,status,reason
```

## Failure behavior

Connected means the latest info probe succeeded, not infrastructure health or a
persistent connection. Offline entries contain a reason and empty name/version.
API queries read daemon cached health; local invocations probe their own config.
No retries, persistent health records or operation admission are introduced.

## Security and safety

Listing is read_only. Its effects are reading cached metadata; local construction
also reads config and invokes effect-free info without credentials. Verification
is the returned status/reason/check timestamp. Known-secret redaction is retained.

## Compatibility and migration

This explicitly supersedes SPEC-0006 and ADR-0004's RPC/CLI health deferral under
the user's new request. It adds a required Backend method (prerelease Go interface
change) and an additive RPC. ADR-0001's generated CLI rule gains a narrow native
ListDrivers exception. Stored formats and user data remain untouched.

## Open questions

None.
