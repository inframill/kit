# SPEC-0001 implementation plan

Status: Implemented

## Approach

Inline essential task bodies, remove wrapper layers and arrange the catalog into
four sections. Inline one-use gate steps. Keep Go and TypeScript generation/checks
separate and preserve ordered commands rather than parallel dependencies.

## Requirement mapping

| Requirement | Implementation | Verification |
| --- | --- | --- |
| REQ-001 | Root Taskfile only, 15 named tasks | Task catalog and file audit |
| REQ-002 | Direct build/release/install and shared native build for run | Disposable smoke builds/install, run dry run, release guards |
| REQ-003 | Root fast/handoff, Go/Jest and Connect tasks | Gates and generation dry runs |
| REQ-004 | README/Connect guide and script cleanup | Reference/link audit |

## Test strategy

Use task --list-all and dry runs to check dispatch and generator ordering. Run
check:spec and check:fast, smoke native build/install and all release targets with
custom output paths, then run test:engine and check:handoff once. Do not launch the
user's configured server or publish to any registry. Existing tests cover server
behavior; run dispatch is inspected without starting it. Generated drift checks
verify the generator commands without rewriting committed artifacts.
