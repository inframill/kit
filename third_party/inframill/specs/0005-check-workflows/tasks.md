# SPEC-0005 tasks

Status: Implemented

## Preparation

- [x] `T-001` Accept the requested gate behavior and manual-only lint policy.

## Implementation

- [x] `T-010` Restructure test/generation tasks and separate linting.
- [x] `T-011` Update agent instructions, README and PR template.
- [x] `T-012` Add task ordering, coverage and no-lint regression checks.

## Verification

- [x] `T-020` Run check-fast from missing generated files in a disposable export.
- [x] `T-021` Run check-fast and check-full; never invoke lint tools.
- [x] `T-022` Review protected source, generated output and current task references.
- [x] `T-023` Stage additions, record evidence and set implementation status.

## Verification evidence

| Criterion | Evidence | Result |
| --- | --- | --- |
| AC-001 | `task check-fast`; TestREQ001FastWorkflow and TestREQ001GenerationCleanBoundaries through check-full | Passed 2026-09-13 |
| AC-001 | Export staged source using `git checkout-index --all --prefix TEMP/`; remove generated Go trees, seed stale files and run `task check-fast` | Passed: missing bindings regenerated, stale files removed, generated hashes identical, handwritten hashes unchanged, no main or dist binary created |
| AC-002 | `task check-full`; TestREQ002FullWorkflow | Passed: fast, 6 valid specs, architecture, CLI integration, TypeScript regeneration, 2 daemon suites / 18 tests, both generated drift checks |
| AC-003 | TestREQ003AutomaticTasksNeverLint expands all automatic tasks with `task --dry`; inspect nested generator fixture and manual lint task source | Passed; automatic Go tests use -vet=off; no lint/format-check/autofix tool executed |
| AC-004 | `task --list-all`, active-reference review, staged source and protected diff review | Only check-fast/check-full expose checks/tests; instructions and setup diagnostics updated; no protected source or runtime-data changes |

The disposable fast run and first full run were initially blocked by sandbox
loopback-port restrictions. Both passed after approved execution outside the
sandbox. Linting was intentionally not run: it is manual maintainer work under
the requested policy, not an unmet acceptance criterion.
