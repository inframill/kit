# SPEC-0001 tasks

Status: Implemented

## Implementation

- [x] `T-001` Accept scope and review all task callers.
- [x] `T-002` Consolidate and arrange essential tasks; remove unused files (REQ-001/002/003).
- [x] `T-003` Align README and Connect instructions (REQ-004).
- [x] `T-004` Verify catalog, build/release/install, generation and gates (AC-001/002/003).

## Verification evidence

| Criterion | Evidence | Result |
| --- | --- | --- |
| AC-001 | `task --list-all` parsed against the exact 15-task catalog; owned Taskfile and reference/link audits | Passed; root Taskfile only, no includes or obsolete live references |
| AC-002 | `task install OUTPUT=/tmp/inframill-root-tasks.E26Vb1/native INSTALL_DIR=/tmp/inframill-root-tasks.E26Vb1/bin`; native/installed help and debug info | Passed; matching executable bytes, installed mode 0755 |
| AC-002 | `task release VERSION=0.0.0-taskcheck DIST_DIR=/tmp/inframill-root-tasks.E26Vb1/releases`; `file` and `go version -m` inspection | Four correct targets, CGO disabled; native release help/debug metadata passed |
| AC-002 | Missing/invalid VERSION and duplicate release attempts with before/after SHA-256 comparison | Rejected; existing bytes unchanged |
| AC-002 | `task --dry run OUTPUT=/tmp/inframill-task-smoke/native` | Build precedes server, shared selected output path; no user server launched |
| AC-003 | `task check:fast` | Passed; specs, architecture, formatting, build and owned Go tests |
| AC-003 | `task test:engine` | Passed; TypeScript drift and 18 daemon tests in two suites; cache binary rebuilt through root build task |
| AC-003 | `task --dry connect:generate` and `task --dry connect:generate-ts`; `task --dir connect-rpc --dry connect:check` | Lint precedes generation, paths resolve from root or subdirectory |
| AC-003 | `task check:handoff` | Passed; fast gate, Go generated drift, Go lint (0 issues), shell syntax, catalog and diff whitespace |
| AC-003 | `git diff --name-only -- pkg/harness gen tests/engine/gen` | No protected/generated changes |

No application Go code changed, so additional race tests were not rerun. Generator
mutation targets were dry-run; drift checks executed the generators into temporary
directories. Non-native release binaries were inspected, not executed. No user
server, runtime data, registry publication or live infrastructure was used.
