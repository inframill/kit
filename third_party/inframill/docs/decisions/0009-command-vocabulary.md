# ADR-0009: Commands describe actions and executions describe runs

Status: Accepted
Date: 2026-09-14
Spec: SPEC-0011

Version selection is superseded by SPEC-0012 / ADR-0010. Command terminology
and interfaces remain current.

## Context

The user models Engine submission and Driver execution as commands. Operation
metadata and Command execution records previously used different nouns for that
flow. Prerelease allows a coordinated breaking vocabulary change.

## Decision

Use CommandDefinition for action metadata and CommandExecution for individual
runs, with CommandRequest and PreparedCommand at the submission/Driver boundary.
Use ListCommandDefinitions, GetCommandExecution and ListCommandExecutions in APIs;
CLI command list/show exposes metadata and command execution owns get/list/wait/logs.
Rename action selectors to command and Driver metadata to commands. Keep IDs,
instance.json and file locations. Advance Driver protocol, SQLite command store
and local-result to version 2; reject old stores read-only without migration.

This supersedes conflicting names/version/path requirements in ADR-0000, ADR-0001,
ADR-0004 and their accepted specs under the user's explicit request. Execution
semantics, other schemas and package ownership remain unchanged.

## Consequences

New Drivers and clients have one vocabulary. Clients and executables must update
with the application. Prior stores remain available to prior binaries and are
never automatically migrated or deleted. No compatibility aliases or SDK redesign.
