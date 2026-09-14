# SPEC-0003 implementation plan

Status: Implemented
Spec: `specs/0003-cli-layout/spec.md`

## Approach

Remove the native package registration, relocate CLI packages into focused
subpackages, and update generation and tests without changing shared services.
Record package boundaries in ADR-0002.

## Requirement mapping

| Requirement | Design element | Verification |
| --- | --- | --- |
| REQ-001 | Remove root registration and snapshot entry | Removed-command tests |
| REQ-002 | Focused internal/cli subtree | Architecture and CLI tests |
| REQ-003 | Separate Buf output for generated bindings | Clean extension and drift checks |
| REQ-004 | Direct shared builder and test-only fixture | Blueprint and engine tests |
| NFR-001 | No domain or protected-code changes | Diff review and handoff |

## Components and boundaries

- cli owns Cobra root composition and process exit mapping.
- config is a Cobra-free leaf shared with appconfig for YAML validation.
- runtime owns RPC options, validation, execution and waiting, using config/output.
- output owns rendering; output/remote owns protobuf presentation definitions.
- localexec adapts native inputs to shared application services.
- generator owns protogen emission and bootstraps without generated packages.
- generated contains generated constructors only; Buf cleans only this CLI directory.
- tests/engine/fixture wraps the shared Blueprint builder for disposable tests.

Dependencies flow from composition to adapters and leaves, never back to cli.

## Data and interface changes

Remove package/package build without aliases. Move generated CLI files from
gen/cli to internal/cli/generated; RPC transport stays in gen. No format changes.

## Error and safety design

Unknown command rejection remains effect-free and returns 2. Preserve existing
redaction, cancellation, input validation and operation safety classifications.

## Test strategy

Run removed-command, architecture, config, output, local execution and generator
tests through check:fast. Run test:engine against disposable local daemons and
fixtures, then check:handoff. Verify deterministic output and stale-file detection
for both trees. No external infrastructure is needed.

## Rollout and rollback

Build the single application binary with relocated sources present. Reverting
the source change restores the prior CLI; runtime data requires no changes.

## Alternatives considered

A flat cli package obscures boundaries; top-level CLI siblings violate the
requested ownership. Keeping the fixture command in the shipped application
would preserve the command surface the maintainer explicitly removed.
