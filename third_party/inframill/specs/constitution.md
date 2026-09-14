# Inframill development constitution

## 1. Specifications are the behavioral authority

Observable behavior is defined before it is implemented. Code may clarify how a
requirement works, but it may not silently invent or override product behavior.

## 2. One domain, thin adapters

The Connect RPC adapter exposes the shared Go application commands and results.
Adapters translate protocols; they do not own orchestration rules. The binary
composes local execution through the same Engine, Backend and Drivers, or uses
the generated Connect client for daemon management. Client disconnection does
not cancel accepted daemon work.

## 3. Infrastructure safety is part of correctness

Every operation has an explicit safety class: `read_only`, `reversible`,
`mutating`, or `destructive`. Mutating behavior is never hidden behind a command
described as validation, inspection, or planning.

## 4. Explicit operations own effects

Operations declare their safety and effects, validate arguments and run
noninteractively. Drivers own tool-specific execution semantics. Tests never
mutate live infrastructure merely to validate code.

## 5. Determinism and idempotence are required

Identical versioned inputs produce identical generated artifacts. Operations that
claim idempotence must prove it with tests or repeatable verification.

## 6. Structured contracts are first-class

Configuration, operation inputs, results, diagnostics, and errors have typed
schemas. Persisted metadata and extension contracts are explicitly versioned
when incompatible readers or implementations can coexist. Serialization belongs
to adapters rather than provider-neutral domain models. Automation must not
depend on scraping human prose.

## 7. Secrets never become artifacts

Secrets are resolved only when needed, passed through controlled channels, and
redacted from output. They must not appear in source control, command arguments,
generated metadata, fixtures, or operation history.

Known secret environment values remain redacted from Driver logs, errors and
structured command results by the executable boundary.

## 8. Verification closes the loop

Implementation is incomplete until each acceptance criterion has recorded
evidence. A skipped check is disclosed with its reason; it is not treated as a
pass.

## 9. Prerelease changes are explicit

Inframill is prerelease and makes no stable compatibility promise for configuration,
Go/RPC APIs, CLI commands, Blueprints, or stored formats. Accepted specs record
breaking changes and their effects; current documentation describes the resulting
contract. Compatibility layers and migration tools require an explicit requirement.
Never delete user data automatically to accommodate an incompatible change.

## 10. Simplicity earns extensibility

Prefer a small typed core and declarative Blueprints. Do not introduce a plugin
SDK, service, database, queue, or abstraction without a current accepted
requirement and a documented decision.
