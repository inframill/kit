# SPEC-0003: Consolidated CLI packages without package command

Status: Implemented
Owner: Inframill maintainers
Created: 2026-09-13
Updated: 2026-09-13

## Problem

CLI implementation is spread across sibling internal packages and gen/cli.
The maintainer requested a cohesive internal/cli subtree and removal of the
application's package command.

## Outcome

All CLI implementation lives under internal/cli in focused subpackages. The
application no longer exposes package or package build. Other CLI behavior,
including .inframill.yaml defaults and deferred prompts/pagers, remains intact.

## Scope

### In scope

- Remove package commands and reorganize handwritten and generated CLI packages.
- Update generation, architecture checks, documentation and disposable fixtures.

### Out of scope

- Domain, RPC, storage, Blueprint format and protected pkg/harness changes.
- Prompts, pagers, replacement packaging commands or compatibility aliases.

## Requirements

- **REQ-001:** package and package build MUST reject with exit 2 before config,
  requests or filesystem effects. Help and completion MUST omit the removed
  command. Other SPEC-0002 CLI contracts MUST remain unchanged.
- **REQ-002:** CLI composition, config, runtime, presentation, local execution,
  generation and generated bindings MUST reside under internal/cli in focused,
  acyclic subpackages. Shared application services and RPC transport MUST remain
  outside this subtree. cmd/main.go MUST remain the sole shipped entry point.
- **REQ-003:** Generation MUST clean only gen and internal/cli/generated, preserve
  handwritten source, and bootstrap without generated bindings. Drift checks
  MUST detect changed and stale files in both generated trees.
- **REQ-004:** Tests needing Blueprint packages MUST use the shared builder
  directly or through a test-only fixture executable. Existing packaging and
  acquisition coverage MUST remain; the fixture MUST NOT be shipped.
- **NFR-001:** The change MUST preserve runtime data, RPC and Blueprint formats,
  and leave pkg/harness untouched.

## Acceptance criteria

- **AC-001** (REQ-001): CLI tests prove removed commands return 2 without requests
  or destination creation; root help and completion omit package.
- **AC-002** (REQ-002): Architecture tests prove old directories are absent,
  new directories exist, and leaf config/output packages do not import Cobra.
  CLI and local execution regressions pass.
- **AC-003** (REQ-003): A clean synthetic RPC extension generates, compiles and
  runs. Both generation trees are deterministic; stale output is detected and
  handwritten source survives regeneration.
- **AC-004** (REQ-004, NFR-001): Blueprint, CLI and disposable daemon tests pass;
  spec, fast and handoff checks pass; diff review confirms protected code and
  runtime data remain unchanged.

## Interfaces and examples

`inframill package build SOURCE OUTPUT` now rejects as an unknown command.
`inframill blueprint fetch REFERENCE` and other existing commands remain available.
The bundled .inframill.yaml retains its cli endpoint, timeout, output and color.

## Failure behavior

Removed commands fail before effects with exit 2 and a diagnostic on stderr.
Other exit codes, validation, cancellation and redaction remain per SPEC-0002.

## Security and safety

Generation mutates generated source only. Verification uses static checks, fake
clients and disposable local fixtures; it never mutates external infrastructure.
Remaining operations retain their declared safety, effects and verification.

## Compatibility and migration

The maintainer explicitly requested this prerelease breaking removal. This spec
supersedes SPEC-0000 REQ-015 and SPEC-0002 REQ-001/002 only where they require the
package command, and supersedes prior CLI source locations. Shared Blueprint
building remains available internally. No runtime data or format migration occurs.

## Open questions

None.
