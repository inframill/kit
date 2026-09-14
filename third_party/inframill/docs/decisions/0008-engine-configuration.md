# ADR-0008: Application-owned engine configuration

Status: Accepted
Date: 2026-09-14
Spec: SPEC-0010

## Context

Storage and Drivers serve the shared Engine in daemon and local execution, but
are configured under the Connect HTTP handler. The harness already exposes
application-owned top-level sections without changes to its protected source.

## Decision

Group dataDir and drivers under a top-level engine object decoded by appconfig.
Keep Connect handler settings limited to path. Keep main and the other harness
sections unchanged. Native loading retains exactly one connect-engine handler;
remote CLI settings remain isolated from engine validation and resolution.

This user-approved prerelease change supersedes the handler-settings location in
SPEC-0000 REQ-019 and the SPEC-0006 example. Defaults, path resolution and Driver
map/health semantics remain unchanged. Reject old and mixed layouts explicitly.

## Consequences

Configuration reflects shared application ownership and HTTP adapter boundaries.
Users move their configuration fields explicitly. No compatibility aliases,
storage migrations, data deletion or protected harness edits are introduced.
