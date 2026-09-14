# ADR-0010: One current prerelease format at version 1

Status: Accepted
Date: 2026-09-14
Spec: SPEC-0012

## Context

The user requested a clean version reset while prerelease and no backward
compatibility. Owned code already has no historical readers, aliases or migrations.

## Decision

Reset Driver protocol, command SQLite user_version and local-result to 1. Keep
Command terminology, top-level engine configuration, and existing current schemas.
Use strict current-schema validation, including read-only record inspection before
Instance startup writes. Do not infer historical schemas from reused version numbers.

This supersedes ADR-0009/SPEC-0011's version-2 decision under explicit user direction.
The root guide describes the current contract; historical specs remain as records.

## Consequences

One supported format, no compatibility branch or migration tool. Unsupported input
rejects safely and user data is never automatically rewritten or removed. Software
release defaults remain 0.0.0-dev; dependencies and external protocols are unchanged.
