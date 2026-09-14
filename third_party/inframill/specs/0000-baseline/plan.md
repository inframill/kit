# SPEC-0000 implementation plan

Status: Implemented

## Approach

Consolidate the currently implemented tool-neutral application into one prerelease
baseline and ADR. Preserve public APIs, protocol/storage versions, generated code,
protected pkg/harness and runtime data. Reset only release identifiers. Use
reference searches to remove obsolete internal helpers rather than introduce new
abstractions. Retain external subprocess and artifact integration coverage.

## Requirement mapping

| Requirements | Implementation and evidence |
| --- | --- |
| REQ-001/002/007/011 | pkg/inframill, blueprintsource, backend; source/storage tests |
| REQ-003/004/005/006 | Engine and Backend queue, persistence and query tests |
| REQ-008/009/010/013 | driverexec, debugdriver, process; executable CLI tests |
| REQ-014/015/017/018/019/020 | appconfig, localexec, cli; Go CLI and Jest daemon tests |
| REQ-012/016/021 | records, README, ignore rules, cmd versions, binary Task targets; audits/smokes |

## Implementation order

1. Accept consolidated requirements and architecture.
2. Reset records, current guide and release identifiers; remove stale ignores/build output.
3. Remove unused process/Driver helpers and Backend artifact adapter; retain boundary tests.
4. Remap traceability, format, verify and record results.

## Test strategy

Run task check:spec, task go:fix and task check:fast. Exercise the changed
process/Driver/Backend packages with the race detector and the real daemon with
task test:engine. Build the native binary, install to a temporary directory and
cross-build all four release targets under a temporary distribution directory;
inspect target formats and query native debug metadata. Audit owned source and
documentation for obsolete names, broken links and release identifiers, and verify
pkg/harness/generated files have no diff. Run task check:handoff once at the end.
All execution uses disposable fixtures, with no live infrastructure mutations.
