# SPEC-NNNN implementation plan

Status: Draft
Spec: `specs/NNNN-short-name/spec.md`

## Approach

Summarize the smallest design that satisfies the accepted requirements.

## Requirement mapping

| Requirement | Design element | Verification |
| --- | --- | --- |
| REQ-001 | Package, component, or workflow | Test or inspection method |
| NFR-001 | Constraint mechanism | Measurement or check |

## Components and boundaries

Describe ownership, interfaces, dependency direction, and external engines.

## Data and interface changes

Document Instance inputs, schema, Go API, Connect RPC, generated-file, and persistence
changes. Include versioning decisions.

## Error and safety design

Document error codes, redaction, scheduling, verification, cancellation, retries, and
partial-failure behavior.

## Test strategy

List unit, contract, integration, golden, and end-to-end coverage. Identify which
tests require external infrastructure and how they are authorized.

## Rollout and rollback

Describe deployment and recovery. Include compatibility stages or migration
tooling only when required by the accepted spec; protect existing user data.

## Alternatives considered

Record meaningful alternatives and why they were rejected. Create an ADR when a
decision has consequences beyond this spec.
