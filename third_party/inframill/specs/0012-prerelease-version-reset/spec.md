# SPEC-0012: Prerelease version reset

Status: Implemented
Owner: Inframill maintainers
Created: 2026-09-14
Updated: 2026-09-14

## Problem

The command vocabulary change introduced version 2 and upgrade-oriented guide
text despite the application still being prerelease with no compatibility promise.

## Outcome

One current contract uses version 1 with Command terminology and engine settings.
The user explicitly requested a version reset and removal of backward compatibility.

## Scope

### In scope

- Driver protocol, command SQLite and local-result versions, fixtures, current guide
  cleanup, and verification that owned code has no historical readers or aliases.

### Out of scope

- Changing dependencies, OCI Distribution v2, protobuf package names, release tooling,
  Instance layouts, protected harness files, deleting data or rewriting spec history.

## Requirements

- **REQ-001:** The only supported Driver protocol MUST be 1. Command SQLite stores
  MUST use user_version 1 and local results inframill.dev/local-result/v1. Core/debug
  release defaults MUST remain 0.0.0-dev. Current CommandDefinition/CommandExecution,
  command/commands/execution/executions fields and top-level engine configuration
  MUST remain unchanged. Instance/Blueprint/RPC/layout versions remain current v1.
- **REQ-002:** There MUST be no compatibility aliases, historical parsers, automatic
  migrations, version dispatch or automatic data rewriting/deletion. Unknown fields
  and unsupported versions MUST reject through current-schema validation. Identical
  version numbers MUST NOT cause nonconforming stored requests to be accepted:
  persisted records MUST be checked read-only before Instance ownership or metadata
  writes. Matching current data is accepted without attempting to infer its age.
- **REQ-003:** README MUST document the current formats and one concise prerelease
  policy, without rename/move/upgrade instructions for retired contracts. AGENTS.md
  MUST direct agents to assume prerelease until the user declares a stable release,
  with no compatibility support or version bumps for prerelease changes unless
  requested. Historical spec/ADR records remain, with the version reset explicitly superseding SPEC-0011.
- **NFR-001:** No harness edits, live infrastructure operations, linting or implicit
  vet. Preserve data on invalid store inspection; both existing test gates MUST pass.

## Acceptance criteria

- **AC-001** (REQ-001): Driver and CLI fixtures prove protocol 1, new stores use
  SQLite version 1 and reopen, and local exec/history emits local-result/v1 with
  current command/execution fields. Existing lifecycle and daemon tests pass.
- **AC-002** (REQ-002, NFR-001): Unsupported versions 2/99 and current-version records
  with unknown or missing command fields reject without modifying Instance files.
  Source review finds no compatibility reader, alias or migration branch.
- **AC-003** (REQ-003, NFR-001): Guide and agent instructions describe current v1 and prerelease policy;
  both gates pass, including spec/architecture and generated drift checks.

## Interfaces and examples

Driver info: protocol 1 with commands; requests: protocol 1 with command.
Local results: api_version inframill.dev/local-result/v1 with command.
SQLite: user_version 1 with request.command and the existing commands table.
CLI and RPC names remain those of SPEC-0011.

## Failure behavior

Invalid Drivers are offline; invalid stores fail safely without migration or replay.
Only the current schema is decoded; prior version numbers confer no compatibility.

## Security and safety

Store preflight is read_only and cannot contact infrastructure. Runtime command
safety classes, effects and verification are unchanged. Tests use disposable data.
User runtime data must never be removed to make a format change work.

## Compatibility and migration

This user-authorized prerelease reset supersedes SPEC-0011 REQ-004 and ADR-0009's
version-2 selection. It does not restore their old names or any historical formats.
No compatibility promise, conversion tool, dual reader or automatic reset of data.
All dependency and external-protocol versions remain unchanged. See ADR-0010.

## Open questions

None.
