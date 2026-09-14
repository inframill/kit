# SPEC-0005: Regenerating test gates and manual linting

Status: Implemented
Owner: Inframill maintainers
Created: 2026-09-13
Updated: 2026-09-13

## Problem

The fast check assumes generated sources already exist, mixes package and CLI
integration tests, and invokes a Go lint tool. The handoff check adds linting but
does not include every owned test suite. Agents are instructed to invoke linting.

## Outcome

Two predictable test gates regenerate their required sources and run tests.
All linting is separate, explicitly manual work; agents never run Go lint tools.

## Requirements

- **REQ-001:** `task check-fast` MUST sequentially clean and regenerate both Go
  output trees, compile the native application to /dev/null without producing a binary, and run package tests under cmd,
  internal and pkg/inframill. It MUST work with missing generated Go files and
  require neither Node nor lint tools. Generation MUST remove stale generated
  files while preserving handwritten source and protected pkg/harness.
- **REQ-002:** `task check-full` MUST run check-fast first, then all other owned
  test suites: architecture, CLI integration and the real-daemon TypeScript
  suite. It MUST validate specs and generated drift, regenerating TypeScript
  fixtures before the daemon suite. Missing integration dependencies MUST fail
  with setup guidance, never silently skip tests. Upstream harness tests remain
  outside the owned test scope.
- **REQ-003:** Test, build, generation and generated-drift tasks MUST NOT invoke
  lint tools, formatting checks or lint autofixes. Every automatic Go test
  invocation, including nested generator fixtures, MUST disable implicit vet.
  Go formatting/lint checks, protobuf lint and shell/whitespace checks MUST be
  available only under the separate manual `lint` task. Automatic lint autofixes MUST be absent.
- **REQ-004:** Agent instructions MUST prohibit Go lint tools, go vet, lint
  autofixes and the manual lint task, including indirect execution through a gate.
  Agents MUST use check-fast during implementation and check-full once before
  handoff; standalone gofmt on owned handwritten files remains permitted.
  README, Connect guidance and the PR template MUST describe the current tasks.
- **REQ-005:** check-fast and check-full MUST be the only check/test task entry
  points. Remove go:test, test:engine, check:spec, check:fast, check:handoff,
  connect:check and connect:check-ts without aliases. Keep tests and internal
  helper scripts as implementation details invoked by these two gates.
- **NFR-001:** Application behavior, generated formats, protected harness files
  and runtime data MUST remain unchanged. Generated code stays included in Git.

## Acceptance criteria

- **AC-001** (REQ-001): Task execution plans prove generation precedes build and
  package tests; a disposable source export with missing bindings and stale
  generated fixtures passes check-fast and preserves handwritten source.
- **AC-002** (REQ-002): Execution-plan tests prove full includes fast before all
  other suites and TypeScript generation before daemon tests; check-full passes.
- **AC-003** (REQ-003): Execution-plan tests cover every automatic task and reject
  lint commands or Go tests without -vet=off. Manual lint tasks are inspected
  without executing linters. No lint tools run during implementation.
- **AC-004** (REQ-004/005, NFR-001): Documentation and task references agree; source
  review proves protected source/runtime data are untouched and generated output
  remains deterministic and staged for Git.

## Interfaces and examples

`task check-fast` is the Go-only development gate. `task check-full` adds all
owned integration tests and requires Node.js 22+ and installed tests/engine
dependencies. Maintainers can manually run `task lint`. There are no other
check/test tasks or lint-autofix tasks.

## Failure behavior

Tasks stop on generation, compilation or test failure. Generation can leave
missing or partial generated output after failure; rerunning generation repairs
it without touching runtime data. Manual lint results do not gate agent handoff.

## Security and safety

Check tasks mutate generated source and local build/test outputs only. They use
disposable local fixtures, never live infrastructure. Cleaning is limited to
internal/gen, internal/cli/gen and (full only) tests/engine/gen.

## Compatibility and migration

This request supersedes SPEC-0001 task names and mandatory lint ordering, and
older specs' references to check:fast/check:handoff and mandatory automated lint.
The new names replace those gates without aliases. Build/install/release behavior
and cache/output overrides remain unchanged. Historical verification records
remain historical evidence. No runtime data migration is involved.

## Open questions

None.
