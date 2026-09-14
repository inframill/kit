# SPEC-0011: Commands and command executions

Status: Implemented
Owner: Inframill maintainers
Created: 2026-09-14
Updated: 2026-09-14

Version selection is superseded by SPEC-0012 / ADR-0010. Command terminology
and interfaces remain current.

## Problem

Operation names describe actions while Command names describe their recorded runs.
This obscures the user's model: the Engine submits commands, the Backend routes
and schedules them, and Drivers execute them.

## Outcome

Commands name available actions; command executions identify individual runs.
The user approved this prerelease rename across APIs, CLI, protocol and storage.

## Scope

### In scope

- Owned Go contracts, Driver protocol, protobuf and generated bindings, CLI paths,
  presentation, persisted requests, fixtures, current documentation and examples.

### Out of scope

- Driver serving helpers, new validation phases, structured diagnostics, credential
  configuration, real Drivers, scheduling changes, data migration and harness edits.

## Requirements

- **REQ-001:** Action metadata MUST use CommandDefinition in the domain, Driver and
  protobuf APIs; prepared actions MUST use PreparedCommand. Driver selection MUST
  use driver.CommandRequest. Domain execution records MUST use CommandExecution,
  CommandExecutionQuery and CommandExecutionPage, with an Executions collection.
  Engine/Backend catalogs MUST use ListCommandDefinitions; execution queries MUST
  use GetCommandExecution and ListCommandExecutions. SubmitCommand, CommandRequest,
  CommandID, CommandStatus, CommandError, Wait and log APIs retain their roles.
- **REQ-002:** Action selectors MUST be named command in requests and local results;
  Driver info MUST expose commands. RPC methods/messages MUST be
  ListCommandDefinitions, GetCommandExecution and ListCommandExecutions; catalog
  responses MUST contain commands and execution responses execution/executions.
  Errors formerly OPERATION_FAILED/OPERATION_CANCELLED MUST use
  COMMAND_FAILED/COMMAND_CANCELLED. Driver argv remains info, validate, run COMMAND.
- **REQ-003:** CLI catalog paths MUST be command list/show. Execution paths MUST
  be command execution get/list/wait/stdout/stderr. Instance exec/delete, local
  exec/history, positional IDs, command-id flags, formatting, waits, output and
  exit behavior MUST retain their behavior. Removed paths MUST reject; no aliases.
  Help, completions, columns and examples MUST use the new vocabulary.
- **REQ-004:** Driver protocol MUST advance to 2; old protocol/operation(s) fields
  MUST reject. commands.sqlite MUST retain its filename and commands table, advance
  PRAGMA user_version to 2, and store request.command. Unsupported versions MUST
  reject before modifying their database, instance.json or other Instance files,
  with a failed in-memory Instance or safe error. No migration, replay or deletion.
  Current-version old/mixed request fields MUST reject. Local result format MUST
  advance to inframill.dev/local-result/v2 and history pages MUST use executions.
- **REQ-005:** instance.json schema, InstanceID directories, Blueprint formats,
  artifact/log locations and command IDs MUST remain unchanged. Current lifecycle,
  safety/effect/verification declarations, redaction, cancellation, idempotency,
  admission and failure isolation MUST remain intact.
- **NFR-001:** Generated sources MUST be regenerated deterministically. No protected
  harness edits, automated linting, implicit Go vet or live infrastructure checks
  are allowed. Both existing test gates MUST pass.

## Acceptance criteria

- **AC-001** (REQ-001/002): Domain, Driver, Backend, Connect and generated-client tests
  compile and exercise command definitions, submit and execution query lifecycles.
- **AC-002** (REQ-003): CLI tests prove catalog/execution path separation, positional
  binding, inherited flags, waits/logs, help/completions and rejection of old paths.
- **AC-003** (REQ-004): Tests reject protocol 1 and old/mixed Driver/request fields;
  new execution records reopen successfully; old SQLite versions remain byte-for-byte
  unchanged with Instance metadata preserved, including through Backend startup.
- **AC-004** (REQ-005, NFR-001): Both gates pass, including CLI and daemon integration,
  architecture and Go/TypeScript drift checks; diff review confirms owned scope.

## Interfaces and examples

```sh
inframill command list
inframill command show debug.echo
inframill instance exec INSTANCE_ID debug.echo --wait
inframill command execution list --instance-id INSTANCE_ID
inframill command execution get INSTANCE_ID COMMAND_ID
inframill command execution wait INSTANCE_ID COMMAND_ID
inframill command execution stdout INSTANCE_ID COMMAND_ID
```

Driver info uses protocol: 2 and commands keyed by local names. Requests use
command: echo; stored and RPC submission requests use command: debug.echo.
The protobuf package stays inframill.v1 with existing field numbers; renamed RPC
paths and JSON fields intentionally break old clients. Update clients and server
in lockstep; no old-service or JSON aliases are provided.

## Failure behavior

Old Driver versions become offline. Invalid current requests reject without run.
Unsupported SQLite versions remain untouched and unavailable; healthy Instances
remain usable. Old CLI paths reject before network or native initialization.
Successful commands and failed/interrupted executions retain their existing effects.

## Security and safety

The rename introduces no new infrastructure effects. Inspection is read_only;
local fixtures are disposable. Submission, execution and deletion retain their
existing safety classes and verification. Configuration and secrets never become
arguments or new persisted fields. Store-version preflight opens read-only.

## Compatibility and migration

This explicitly supersedes vocabulary, Driver protocol 1, command SQLite version 1,
and local-result/v1 requirements in SPEC-0000 and related accepted specs/ADRs,
including SPEC-0002 CLI paths and SPEC-0006 Manager types, under user authorization.
Blueprint/Instance versions and storage_layout uuid-v1 remain unchanged. Existing
files are not migrated, renamed or deleted. Users retain old data with the prior
binary or explicitly select a fresh data root for new executions. See ADR-0009.

## Open questions

None.
