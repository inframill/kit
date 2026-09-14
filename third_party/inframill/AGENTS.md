# Inframill agent guide

This file is the authoritative repository instruction set for coding agents.
Do not create divergent agent-specific development rules.

## Mission and vocabulary

Inframill is a Go control-plane application that manages Instances through
external Drivers. Read `README.md` before changing
the domain model or public interface.

Assume Inframill is prerelease until the user explicitly declares a stable release.
Prefer a clean current API and stored format. Breaking changes may be made through
accepted specs; do not add backward-compatibility aliases, legacy readers,
migrations or version bumps solely to preserve earlier prerelease contracts unless
the user explicitly requests them. Keep current format versions at 1 and development
versions at `0.0.0-dev` unless the user requests a version change. Dependency and
external protocol versions are independent. Reject unsupported data safely; never
delete user data to accommodate a change.

Use these terms consistently:

- **Blueprint**: reusable implementation used to create Instances.
- **Instance**: one named deployment managed by Inframill.
- **InstanceID**: immutable UUID; the separate immutable Instance name is `<project>/<name>` (SPEC-0000 / ADR-0000).

## Authority order

When instructions disagree, use this order:

1. The user's current request.
2. The current baseline in `specs/0000-baseline/spec.md` and subsequent
   accepted change specs in `specs/`.
3. `specs/constitution.md`.
4. ADR-0000 and subsequent accepted decisions in `docs/decisions/`.
5. `README.md` product direction.
6. Existing implementation and tests.

The baseline and architecture decision are numbered 0000. The root README is the
only README and user/developer guide. New records start at 0001.

Stop and surface the conflict when a requested change contradicts an accepted
spec or ADR. Do not silently reinterpret the contract.

## Spec-oriented workflow

Behavior changes start with a spec. A complete change directory is:

```text
specs/NNNN-short-name/
  spec.md
  plan.md
  tasks.md
```

Use the templates in `specs/templates/`. Tiny fixes may use a lightweight spec,
but they still need an observable acceptance criterion when behavior changes.
Pure comments, formatting, and typo fixes do not require a spec.

Before implementation:

1. Read the relevant spec, plan, tasks, ADRs, and nearby implementation.
2. Confirm the spec contains testable `REQ-NNN` requirements and `AC-NNN`
   acceptance criteria.
3. Resolve material ambiguity in the spec instead of guessing in code.
4. Mark the spec `Accepted` before implementing unless the user explicitly asks
   for an exploratory prototype.

During implementation:

1. Work in task order unless dependency evidence requires a change.
2. Keep changes within the accepted scope.
3. Add requirement IDs to test names or test comments when practical.
4. Record architectural decisions that affect multiple packages, public
   contracts, persistence, security, or future Blueprints as ADRs.
5. Update the spec and plan before implementing newly discovered scope.

After implementation:

1. During implementation, run `task check-fast` after meaningful slices. It cleans
   and regenerates Go bindings, compiles to `/dev/null`, and runs package tests.
2. Run `task check-full` once before handoff. It includes fast, specs, architecture,
   CLI integration, TypeScript regeneration, daemon tests and generated drift.
   Do not repeatedly run full when fast proves the changed surface.
3. These are the only check/test task entry points. Do not add separate check or
   test tasks. Use `gofmt` directly on changed owned handwritten Go files when needed.
4. Update task checkboxes with verification evidence.
5. Set the spec to `Implemented` only when every acceptance criterion is proven.
6. Report unmet criteria and skipped checks explicitly.

## Engineering constraints

- Agents MUST NOT run Go linting, `golangci-lint` (including its fmt and autofix
  modes), `go vet`, lint autofixes, or `task lint`, directly or indirectly.
  All linting, including protobuf lint and shell/whitespace lint checks, is a
  separate manual maintainer workflow. Do not make linting an agent handoff
  requirement. Run Go tests with `-vet=off`, including nested test fixtures, so
  tests do not invoke implicit vet. Standalone `gofmt` is permitted on owned
  handwritten files; the protected harness and generated files remain excluded.
- `pkg/harness` is an upstream copy managed exclusively by the user. Codex and
  other coding agents MUST NOT edit, add, delete, move, reformat, regenerate or
  apply automated fixes to any file under this directory, including its tests.
  This also applies when a harness change would fix a build, test or lint issue.
  Keep Inframill adaptations and integration tests outside it. If a harness
  change is necessary, explain the required change to the user and leave it for
  the user to make themselves; do not apply it.
- Keep the Connect RPC adapter thin; domain behavior belongs in the shared Go
  application layer.
- Prefer typed configuration and result structures over unstructured maps.
- Invoke external tools with argument arrays, never by assembling shell command
  strings.
- Generated artifacts must be deterministic and must not be edited manually.
- Structured results are returned through Connect RPC; daemon diagnostics go to
  stderr. Record Driver stdout/stderr in the workspace command log files.
- Noninteractive operations must never prompt, open editors, start pagers, or
  wait for credentials.
- Never log, persist, or place secrets in process arguments. Redact sensitive
  values in errors and command records. Retain known-secret environment redaction.
- Every operation declares its safety class, effects, and
  verification behavior.
- Keep code and docs aligned with the accepted current contract. Record prerelease
  breaking changes explicitly; add compatibility layers only when explicitly requested.
- Do not edit unrelated user changes in a dirty worktree.

## Safety

Treat operations that change external systems or persistent local state as mutations.
Planning, validation, rendering checks, and local parsing are read-only only when
they cannot contact or mutate external systems.

Never run a mutating infrastructure command merely to validate code. Use static
checks, fakes, disposable fixtures, or explicit user-authorized environments.

## Definition of done

A behavior change is complete only when:

- Its accepted requirements and acceptance criteria are traceable to tests or
  other recorded evidence.
- Owned handwritten code is formatted and both test gates pass. Linting is
  manual maintainer work and is not required for agent completion.
- Human and structured interfaces remain consistent when both are affected.
- Documentation, examples, and schemas match the implementation.
- Credentials are absent from source, diffs and fixtures. Runtime output and
  metadata follow the accepted secret-handling contracts.
- The task list records the exact verification performed.
