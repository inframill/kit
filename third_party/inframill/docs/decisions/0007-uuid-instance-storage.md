# ADR-0007: UUID-addressed Instance storage

Status: Accepted
Date: 2026-09-13
Spec: SPEC-0009

## Context

Instances have immutable UUID and project/name identities, but their current disk
paths follow names. The user authorized UUID paths after reviewing safety and
implementation gaps. Historical UUID-shaped layouts also exist in rejection tests,
so directory shape alone is not a format discriminator.

## Decision

Use one canonical instances/<UUID> leaf, with project/name stored in metadata
and used by existing CLI selectors. Require storage_layout: uuid-v1 in instance.json
while preserving other protocol/schema versions. Validate UUID/path equality and
name uniqueness. Retain cross-process namespace and per-Instance ownership locks,
strict root discovery, and per-Instance execution-failure isolation.

Use root-relative publication/deletion plus owned-directory identity checks; do
not persist failure metadata through a substituted path. Keep standalone Backend
root preflight initially, deferring any cached-discovery optimization. No additional
persistent name index, symlink aliases or renaming feature.

The user selected rejecting/preserving existing layouts with a separate root for
new work, and explicitly excluded migration and compatibility for this prerelease
change. No converter, compatibility reader or automatic relocation is included.
This decision is accepted with the user’s implementation request on 2026-09-13.

This decision supersedes ADR-0000 and SPEC-0000's name-addressed filesystem
clauses. It does not change immutable names, Driver trust or API identities.

## Consequences

Each Engine-created replacement Instance gets a new UUID path; names stay readable
in metadata and CLI output. Drivers receive updated absolute paths through their
existing protocol. UUID addressing improves identity reasoning but does not sandbox
same-user executables. Strict preflight retains a startup scanning cost.

Old and unmarked UUID storage is not silently adopted. No user data is moved or
deleted automatically. The transition decision is resolved; SPEC-0009 is accepted
for implementation. No new dependency or protected harness change is required.
