# SPEC-0001: Essential root tasks

Status: Implemented
Owner: Inframill maintainers
Created: 2026-09-12
Updated: 2026-09-12

## Problem

The Task catalog spreads a small Go application's workflows across six includes,
with duplicate wrappers, aliases, individual gate steps and tool-reporting commands.

## Outcome

One clearly arranged root Taskfile contains the essential build, development,
verification and generation workflows.

## Requirements

- **REQ-001:** The only owned Taskfile MUST be the root Taskfile.yml, with no
  includes. Arrange tasks by binary lifecycle, development, checks and generation.
  Keep default, build, release, install, run, go:fix, go:test, test:engine,
  check:spec, check:fast, check:handoff, connect:generate, connect:generate-ts,
  connect:check and connect:check-ts. Remove other targets and duplicate aliases.
- **REQ-002:** Preserve native build/install overrides, independent required-VERSION
  four-target releases and refusal to overwrite release directories. Run MUST build
  and execute the selected native output. Builds install no tools or containers.
- **REQ-003:** Preserve all fast/handoff checks, scoped owned Go packages, cache
  overrides and the separate real-daemon Jest suite. Go-only workflows MUST NOT
  require Node. Protobuf lint MUST precede generation/checks, with dependent
  generators executed sequentially. Generated files and pkg/harness stay unchanged.
- **REQ-004:** README and Connect guidance MUST document only the retained tasks.
  Replace packaging and dependency-install wrappers with direct CLI/ORAS/npm usage.
  Delete the unused local-publication script. No compatibility aliases, application
  behavior changes, runtime data edits or registry publication are included.

## Acceptance criteria

- **AC-001** (REQ-001/004): Task catalog lists exactly the retained workflows; no
  include Taskfiles, obsolete live references or orphan publication script remain.
- **AC-002** (REQ-002): Native build/install/run dispatch and all four release builds
  pass smoke checks using temporary destinations; bad/missing VERSION and release
  overwrite attempts fail before changing existing release files.
- **AC-003** (REQ-003): Fast, real-daemon and final handoff gates pass. Generation
  dry runs prove lint ordering and root-relative paths; both generated drift checks
  pass without modifying protected/generated files.

## Compatibility and safety

This is a prerelease Task CLI cleanup. Removed task names are intentionally not
aliased. Direct CLI operations remain available. Build/install writes are local;
run serves the configured application; formatting/generation writes owned source;
checks use temporary fixtures. No publication or live infrastructure validation.
