# SPEC-0000 implementation tasks

Status: Implemented

## Tasks

- [x] `T-001` Accept the current baseline and architecture (all requirements).
- [x] `T-002` Consolidate records/README and reset release identifiers (REQ-012/016/021).
- [x] `T-003` Remove verified unused internal helpers and duplicate artifact adapter (REQ-008/009/013).
- [x] `T-004` Remap test requirements and verify source, integration and distribution (all ACs).
- [x] `T-005` Remove REQ-015's blanket colors/prompts/pagers prohibition and
  uncolored-table constraint at the user's explicit request (2026-09-12).
  This relaxes documentation policy; terminal color behavior is planned separately
  in SPEC-0002 and is not implemented by this amendment.

## Verification evidence

| Criteria | Evidence | Result |
| --- | --- | --- |
| AC-001/002/003/004 | `task check:fast`: architecture, formatting, build and all owned Go tests, including executable CLI artifact persistence, named storage, lifecycle, OCI, config, secrets and history | Passed |
| AC-003 | `go test -race ./internal/backend ./internal/driverexec ./internal/drivers/... ./internal/platform/process` | Passed |
| AC-004 | `task test:engine`: TypeScript generated drift plus real daemon/debug subprocess suites | 2 suites, 18 tests passed |
| AC-005 | `task go:fix` | 0 issues; protected upstream excluded |
| AC-005 | `task build`; native and installed `--help` / `driver debug info` | Passed; debug 0.0.0-dev, protocol 1, eight operations |
| AC-005 | `task install INSTALL_DIR=/tmp/inframill-reset-smoke.a1zrD0/bin OUTPUT=/tmp/inframill-reset-smoke.a1zrD0/native` | Passed; mode 0755, isolated installation |
| AC-005 | `task release VERSION=0.0.0-dev DIST_DIR=/tmp/inframill-reset-smoke.a1zrD0/releases`; `file` and `go version -m` inspection | Four correct darwin/linux amd64/arm64 executables; CGO disabled and trimpath enabled; native metadata passed |
| AC-005 | Release attempts with omitted VERSION, `../invalid`, and existing 0.0.0-dev; SHA-256 comparison before/after | All rejected; release bytes unchanged |
| AC-005 | Reference searches before deleting process Output/Executor/Result, accepted-exit/group/working-directory options, Driver NewOperation/Binding helpers and duplicate Backend artifact adapter | No production consumers of removed behavior; live callers simplified; artifact boundary/restart coverage retained |
| AC-005 | Owned tracked/untracked text audit and relative Markdown link resolution | No tool-specific references or broken links; one root README, one baseline and one ADR; retired record references remapped |
| AC-005 | `git diff --name-only -- pkg/harness gen tests/engine/gen`; release/format identifier audit | No protected/generated changes; only core/debug release defaults reset; dependencies and wire/storage versions unchanged |
| AC-005 | Build-output and ignore audit | Removed two stale dist verification directories; rebuilt ignored dist/inframill; runtime data untouched and still ignored |
| AC-006 | `task check:handoff` | Passed: spec/architecture/format/build/Go tests, protobuf lint/drift, Go lint (0 issues), shell syntax, Task catalog and diff whitespace |

The first fast run was blocked by sandbox loopback/cache permissions. It passed
when rerun with the required access. Native macOS arm64 executables were run;
other release targets were cross-built and inspected, not executed. No live
infrastructure or user runtime state was used. Git history was not rewritten.
