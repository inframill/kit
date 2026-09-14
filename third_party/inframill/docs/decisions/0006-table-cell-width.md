# ADR-0006: Bounded human table cells

Status: Accepted
Date: 2026-09-13
Spec: SPEC-0008

## Context

The user requests short CLI table strings and full JSON to prevent long values
from making tables unwieldy. Earlier specs required untruncated table identifiers.

## Decision

Apply a 20-display-column limit per header/cell line in the shared table renderer.
Include ... within the limit for truncated strings. Use the pinned go-pretty width
helpers for Unicode-aware sizing. Preserve line breaks and normalize tabs/CRs.
JSON and source values remain complete, as do non-table metadata and sections.

This supersedes the table no-truncation rules in SPEC-0000 and SPEC-0002 under the
user's explicit request. It applies to SPEC-0007 Driver health tables as well.

## Consequences

Tables stay compact and consistent across commands and output destinations.
IDs and error messages can be abbreviated; use JSON to retrieve full values.
No changes to command semantics, APIs, persistence or generated bindings.
