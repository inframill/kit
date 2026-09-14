# SPEC-0004 implementation plan

Status: Implemented
Spec: `specs/0004-internal-generated-code/spec.md`

## Approach

Update protobuf go_package and Buf output locations, emit CLI package gen, update
handwritten imports and fixture paths, and regenerate all output through Buf.
Remove obsolete generated trees. Record the import boundary in ADR-0003.
Stage all new generated sources and change records; verify a disposable source
export from the Git index builds without regenerating output.

## Requirement mapping

| Requirement | Design element | Verification |
| --- | --- | --- |
| REQ-001 | Internal RPC transport and CLI-owned gen package | Architecture checks, exact build and index export build |
| REQ-002 | Separate generated-only outputs and clean fixture | Extension test, drift and deterministic generation |
| NFR-001 | Import-only change to consumers; unchanged wire schema | Go and engine regression tests, diff review |

## Components and boundaries

internal/gen holds shared RPC transport. internal/cli/gen depends on transport
and the CLI runtime. Neither transport package imports CLI implementation.
The handwritten generator remains under internal/cli/generator and compiles
without generated code. Buf never cleans internal or internal/cli themselves.

## Data and interface changes

Only Go import paths, package naming and descriptor go_package metadata change.
No message fields, RPC paths or TypeScript output locations change.

## Test strategy

Run connect:generate then connect:generate-ts sequentially, go build cmd/main.go,
check:fast, test:engine and check:handoff. Verify output equality and stale-file
detection for both Go trees in disposable directories and hash handwritten
source before/after generation. All tests use local fixtures. Build a temporary
export of the Git index to detect files missing from the reviewable change.

## Rollout and rollback

Rebuild the binary with the new imports. Source rollback requires regeneration;
runtime data is unaffected and must never be deleted.
