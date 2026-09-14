# SPEC-0005 implementation plan

Status: Implemented
Spec: `specs/0005-check-workflows/spec.md`

## Approach

Use sequential root Task commands: clean Buf generation, compilation to /dev/null,
package tests for check-fast; then spec/architecture/CLI tests, TypeScript generation,
daemon tests and generated drift for check-full. Buf already uses clean: true
for narrowly scoped output directories. Move all lint commands to the manual lint
task and remove other check/test task entry points.

## Requirement mapping

| Requirement | Design element | Verification |
| --- | --- | --- |
| REQ-001 | check-fast with inline compilation/package tests | Execution-plan tests and disposable missing-source run |
| REQ-002 | check-full covering remaining suites | Execution-plan tests and full gate |
| REQ-003 | Manual lint task; -vet=off everywhere automatic | Execution-plan assertions and source inspection |
| REQ-004 | Root/Connect agent guides, README and PR template | Reference review |
| REQ-005 | Only two check/test tasks; inline other steps | Task catalog assertions |
| NFR-001 | Narrow clean outputs; existing generation contracts | Source hashes and protected diff |

## Components and boundaries

All tasks remain in the root Taskfile. Go generation stays independent of Node.
Package-local Go tests belong to fast; tests/architecture, tests/cli and
tests/engine belong to full. lint owns all manual Go format/lint, Buf lint, shell
syntax and whitespace checks. The former go:fix task is removed. Full runs internal helper scripts
and the remaining test commands directly instead of exposing more check tasks.

## Test strategy

Add architecture-tagged task execution-plan tests using task --dry so no test
can accidentally execute a lint task. In a temporary source export, remove only
generated output and seed stale files, then run check-fast. Run the actual fast
gate after implementation and full once at handoff. Do not run any linter.

## Rollout and rollback

Update callers and instructions to the two new task names. No aliases, external
services, dependency changes or runtime-data migrations are needed.
