# SPEC-0012 implementation plan

Status: Implemented
Spec: `specs/0012-prerelease-version-reset/spec.md`

## Approach

Reset owned constants/examples to 1 and retarget rejection tests to unsupported
versions. Generalize the existing read-only store preflight to check current
records as well as the version before any Instance writes. Remove historical
upgrade prose from the root README, retaining current schema documentation.
Make the prerelease default explicit in AGENTS.md as requested by the user.

## Requirement mapping

| Requirement | Design element | Verification |
| --- | --- | --- |
| REQ-001 | Protocol/store/result constants and current fixtures | Driver, CLI, store and daemon tests |
| REQ-002 | Single strict parser, read-only store inspection | Version/schema rejection with byte snapshots |
| REQ-003 | Current-only README and supersession records | Guide/source review |
| NFR-001 | Existing fast/full gates and owned scope | Gates and diff review |

## Components and boundaries

Existing Driver, Backend and CLI ownership remains. Store inspection decodes the
same current commandDocument type; no additional historical types or branches.

## Data and interface changes

Only the three version-2 markers reset to 1. Current names, schemas and paths
remain unchanged. Software defaults already use 0.0.0-dev; dependencies are untouched.

## Error and safety design

Open existing stores mode=ro and validate version and records before ownership.
Reject malformed or mismatched records with safe diagnostics. Current-shaped
records may be read regardless of provenance; no historical interpretation.

## Test strategy

Update positive fixtures and unknown-version cases; extend byte-preservation cases
to malformed records under current version 1. Run task check-fast then task
check-full. Keep general invalid-schema tests; they do not implement compatibility.

## Rollout and rollback

Ship a single current prerelease format. No tool mutates, migrates or deletes
existing user data. Retain historical spec evidence with explicit supersession.

## Alternatives considered

Removing strict validation could misread old data sharing the reset version.
Maintaining version dispatch or a migration path contradicts the user's request.
