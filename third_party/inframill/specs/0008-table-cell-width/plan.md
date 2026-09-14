# SPEC-0008 implementation plan

Status: Implemented
Spec: `specs/0008-table-cell-width/spec.md`

## Approach

Apply display-width-aware truncation to headers and row cells in output.View.Write
using the existing go-pretty text helpers. Preserve independent source strings and
leave all JSON paths and metadata rendering unchanged. Record ADR-0006.

## Requirement mapping

| Requirement | Design element | Verification |
| --- | --- | --- |
| REQ-001 | Shared per-line cell formatting | Renderer boundary/Unicode tests |
| REQ-002 | Presentation-only transformation | Remote JSON round trips, local output tests |
| NFR-001 | Existing dependency and gate workflow | Fast/full checks and generated drift |

## Components and boundaries

Only shared CLI presentation changes. Existing callers inherit the limit without
new protocol-specific truncation logic or domain dependencies.

## Data and interface changes

Human table cells become abbreviated. JSON, RPC, Go APIs and storage stay complete.

## Error and safety design

Preserve existing short-write/error handling and secret redaction. Truncation is
visible through ... and never changes values supplied to commands.

## Test strategy

Run renderer and output tests through task check-fast, then task check-full once.
Update table expectations for abbreviated IDs; keep identity assertions in JSON
when tests concern command behavior. All fixtures remain local and disposable.

## Rollout and rollback

Use -o json to obtain complete identifiers and other values. No data migration.

## Alternatives considered

Wrapping still produces unwieldy tables. Per-command truncation duplicates policy.
Terminal-dependent widths make piped and interactive output inconsistent.
