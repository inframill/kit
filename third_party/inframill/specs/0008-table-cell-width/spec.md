# SPEC-0008: Bounded CLI table cells

Status: Implemented
Owner: Inframill maintainers
Created: 2026-09-13
Updated: 2026-09-13

## Problem

Long identifiers, references and Driver health reasons expand CLI tables beyond
usable terminal widths. The user requests a maximum of about 20 characters while
JSON preserves the full strings.

## Outcome

Every CLI table uses a fixed 20-column maximum for each header and cell line.
Truncated strings end with ...; users select JSON for complete values.

## Scope

Shared table rendering, including local Driver health/history and remote lists,
details and confirmations. No configurable width, terminal-size detection or
changes to JSON, raw logs, diagnostics, help, nested JSON sections or pagination
footers are included.

## Requirements

- **REQ-001:** Shared CLI table rendering MUST cap each header and cell line at
  20 terminal display columns, including a trailing ASCII ... when truncated.
  Shorter strings and exactly 20-column strings MUST retain their contents.
  Width MUST account for wide Unicode characters and preserve valid UTF-8.
  Existing line breaks MUST remain; CRLF/CR normalize to LF and tabs to four
  spaces before limiting. The same cap applies with or without a terminal/color.
- **REQ-002:** Truncation MUST occur only in table presentation. Original source
  values, complete JSON output, raw logs, nested JSON sections, pagination cursors,
  observed-at footers, errors/exit codes and command behavior MUST remain intact.
  Long IDs in tables MUST truncate just like other cells; JSON provides full IDs.
- **NFR-001:** Existing dependencies and the shared output renderer MUST be used.
  No domain, RPC, generated schema, storage, harness or infrastructure changes;
  no linting. Both approved test gates MUST pass.

## Acceptance criteria

- **AC-001** (REQ-001): Renderer tests prove short/exact/long cells and headers,
  wide Unicode, combining marks, multiline strings, tabs and preserved input.
- **AC-002** (REQ-002): Remote format tests prove truncated tables and exact JSON
  round trips. Native Driver and history tests prove the same separation; raw log
  and metadata tests prove unmodified non-table output.
- **AC-003** (NFR-001): task check-fast and task check-full pass with updated
  presentation assertions and no generated changes.

## Interfaces and examples

A 36-character UUID displays its first 17 ASCII characters followed by ... in a
table. inframill driver list -o json continues to return full names and reasons.
The limit covers cell content; table borders and padding add their normal width.

## Failure behavior

Truncation is normal successful presentation. Existing output failure handling
and exit codes remain unchanged. Long values never require a retry.

## Security and safety

This is a read_only presentation change. It performs no infrastructure or storage
actions; verification uses disposable fixtures and rendered strings. Existing
secret redaction continues before presentation.

## Compatibility and migration

The user's explicit request supersedes SPEC-0000 REQ-015 and SPEC-0002 REQ-006's
untruncated table-cell requirements, including full IDs and error messages inside
tables. SPEC-0007 table summaries now use this limit. Full structured values and
non-table metadata remain available. No stored format or user data changes.

## Open questions

None.
