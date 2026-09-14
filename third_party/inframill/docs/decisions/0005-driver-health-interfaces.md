# ADR-0005: Driver health query and native CLI

Status: Accepted
Date: 2026-09-13
Spec: SPEC-0007

## Context

The user requests Engine, API and CLI Driver health listing, with the CLI calling
the Engine in process like local exec. SPEC-0006 and ADR-0004 deferred these surfaces.

## Decision

Add a neutral DriverInfo summary through Backend.ListDrivers and Engine.ListDrivers.
The Backend translates cached Manager entries; Engine returns sorted independent
snapshots without requiring Start. Connect exposes the same summary via ListDrivers.
Native driver list constructs Manager, Backend and Engine without starting workers,
HTTP or storage. It probes only through Manager construction and lists via Engine.

This supersedes the RPC/CLI deferral in ADR-0004 and SPEC-0006. ListDrivers is an
explicit exception to ADR-0001's remote CLI generation rule; its native leaf
coexists with driver debug. Other RPC methods retain generated command routing.

## Consequences

Local CLI and daemon report health from their own configured Manager. Connected is
last info success, not infrastructure health. Summaries include ID, reported
name/version, status, reason and check time; full Manager metadata remains separate.
Backend implementations must add the query. Existing package boundaries, secret
handling, data formats and stored Instance state remain intact.
