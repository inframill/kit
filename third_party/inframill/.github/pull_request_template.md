## Specification

- Spec: <!-- specs/NNNN-name/spec.md, or "Not required: <reason>" -->
- Requirements: <!-- REQ-NNN, NFR-NNN -->
- Acceptance criteria: <!-- AC-NNN -->

## Change

Describe the observable result and implementation boundaries.

## Verification

- [ ] `task check-fast` during implementation (regenerate, compile to /dev/null, package tests)
- [ ] `task check-full` before handoff (fast plus all remaining owned tests/checks)
- [ ] Any unmet criteria or skipped checks are recorded

Linting is separate manual maintainer work via `task lint`; agents must not run it.

Evidence:

```text
Paste concise commands and results.
```

## Safety and compatibility

- Safety class: <!-- read_only / reversible / mutating / destructive -->
- [ ] No credentials appear in code, diffs, or fixtures; runtime handling follows accepted specs
- [ ] Compatibility and migration effects are documented
- [ ] Generated files are deterministic or explicitly excluded
