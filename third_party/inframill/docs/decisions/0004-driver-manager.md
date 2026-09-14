# ADR-0004: Public Driver Manager and configured identity

Status: Accepted
Date: 2026-09-13
Spec: SPEC-0006

## Context

Driver handling crosses internal registry and subprocess adapters. Executable
names prevent configuring several instances and failed probes abort startup.

## Decision

Expose a self-contained pkg/driver Manager with List, Validate and Prepare.
Select by YAML object key, independently of executable name/version. Keep health
for every entry; probe at construction and refresh selected entries on use. List
returns cached independent snapshots. Rename Blueprint systems to drivers.
Move process mechanics under pkg/driver/internal/process and the bundled debug
implementation/protocol server under internal/debugdriver. Backend depends on
Manager and translates types to pkg/inframill. Environment configuration is deferred.

This supersedes ADR-0000's internal Driver registry, executable-name identity and
fail-fast startup decisions under the user's explicit redesign authorization.

## Consequences

Multiple configured IDs can use identical executables or distinct versions.
Unavailable entries remain inspectable without disabling healthy ones. There is
no live connection or resident process. Public types cannot import application
internals. Old config lists and systems manifests reject; runtime data is never
rewritten or deleted automatically. No RPC/CLI health endpoint is introduced.
