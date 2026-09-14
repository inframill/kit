# SPEC-0004 tasks

Status: Implemented

## Preparation

- [x] `T-001` Accept requested internal ownership and CLI gen naming.

## Implementation

- [x] `T-010` Update output locations, imports, generator fixtures and architecture checks.
- [x] `T-011` Regenerate output, remove obsolete directories and update documentation.
- [x] `T-012` Restore files omitted from commit 9fa29d5 and stage all additions.

## Verification

- [x] `T-020` Run exact build, spec, fast, integration and handoff checks.
- [x] `T-021` Verify deterministic generation, stale detection and source preservation.
- [x] `T-022` Review protected code and runtime-data diff; record evidence and status.
- [x] `T-023` Build an export from the Git index without regenerating source.

## Verification evidence

| Criterion | Evidence | Result |
| --- | --- | --- |
| AC-001 | `go build cmd/main.go`; `task check:fast` architecture checks | Passed 2026-09-13 |
| AC-001 | `git checkout-index --all --prefix=TEMP/` then `go build cmd/main.go` in that disposable export, without running generation | Passed using staged sources only |
| AC-002 | `task connect:generate`; clean extension test through check:fast; generated drift check through check:handoff | Passed |
| AC-002 | Two temporary Buf generations, hashes against both repository output trees, handwritten parent sentinels, stale generated fixtures and diff exit 1 checks | Deterministic; handwritten parents preserved; stale output cleaned and detected in both trees |
| AC-003 | `task check:spec`; `task check:fast`; `task test:engine`; `task check:handoff`; `git diff --cached --check` | Passed; 0 lint issues; 2 engine suites / 18 tests |
| AC-003 | `git diff HEAD --name-only -- pkg/harness` and staged-diff review | No protected-code, runtime-data, RPC schema or TypeScript changes during restoration |

Restoration: commit 9fa29d5 retained updated imports and removed the original
generated files but omitted both new generated Go trees and these change records.
Earlier verification used untracked files in the working tree. Stage restored
additions and verify the Git index export, not only the populated working tree.
