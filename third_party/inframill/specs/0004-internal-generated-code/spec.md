# SPEC-0004: Internal generated Go bindings

Status: Implemented
Owner: Inframill maintainers
Created: 2026-09-13
Updated: 2026-09-13

## Problem

Generated Go RPC bindings currently expose importable packages under gen while
the CLI bindings use a differently named generated directory. The maintainer
requested internal ownership and the name internal/cli/gen for CLI bindings.

## Outcome

Application-owned generated Go code resides under internal, with RPC transport
shared by the server and CLI and command bindings owned by the CLI.

## Scope

Move Go transport to internal/gen and CLI bindings to internal/cli/gen. Update
generator inputs, imports, checks and documentation. Test-only TypeScript stays
under tests/engine/gen. Runtime behavior, prompts and pagers are unchanged.

## Requirements

- **REQ-001:** Protobuf and Connect Go output MUST live under internal/gen, and
  CLI command output MUST live under internal/cli/gen with Go package name gen.
  Former gen and internal/cli/generated directories MUST be absent. Consumers
  MUST use the new paths without compatibility packages. Generated source MUST
  be included in Git so a checkout builds without a generation prerequisite.
- **REQ-002:** Generation and drift checks MUST cover both generated-only trees,
  preserve handwritten source, detect stale output, and bootstrap the generator
  without generated bindings. The synthetic RPC extension MUST compile and run
  using the internal package paths.
- **NFR-001:** RPC services, wire behavior, CLI behavior and stored formats MUST
  remain unchanged. Test-only TypeScript output MUST remain in tests/engine/gen.
  Protected pkg/harness source and runtime data MUST remain untouched.

## Acceptance criteria

- **AC-001** (REQ-001): Architecture checks prove the new packages exist with the
  intended dependencies and former directories are absent; go build cmd/main.go
  succeeds using the new imports. A source export from the Git index also builds.
- **AC-002** (REQ-002): Clean synthetic extension tests and generated drift checks
  pass. Repeated generation is identical, stale files are detected in either
  tree, and regeneration preserves handwritten source.
- **AC-003** (NFR-001): Spec, fast, relevant integration and handoff checks pass;
  diff review confirms no RPC field/service, protected source or runtime-data
  changes and TypeScript remains confined to its existing generated directory.

## Interfaces and examples

Go imports use github.com/sir-hassan/inframill/internal/gen/inframill/v1 and
its inframillv1connect subpackage. Root CLI composition imports internal/cli/gen.

## Failure behavior

Existing CLI exit codes and diagnostics are unchanged. Invalid generation or
generated drift continues to fail the corresponding task.

## Security and safety

Generation writes generated source only. Tests use disposable local fixtures;
no external infrastructure operations are required. Existing operation safety,
effects, verification and secret-handling contracts remain unchanged.

## Compatibility and migration

The maintainer's current request supersedes SPEC-0003 REQ-003 and ADR-0002 only
for generated output locations. This prerelease change prevents external Go
modules from importing the generated bindings directly; clients can generate
bindings from the protobuf schema. No wire or stored-data migration is needed.

## Open questions

None.
