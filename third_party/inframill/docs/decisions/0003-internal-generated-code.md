# ADR-0003: Internal ownership of generated Go bindings

Status: Accepted
Date: 2026-09-13
Related spec: SPEC-0004

## Context

The maintainer requested internal generated Go packages and selected
internal/cli/gen as the command-binding directory.

## Decision

Generate shared protobuf and Connect transport under internal/gen and CLI
command bindings under internal/cli/gen, using package gen for command bindings.
Keep the generator under internal/cli/generator and TypeScript test bindings
under tests/engine/gen. Buf cleans only the two generated Go subtrees; drift
checks compare both entire trees. This supersedes ADR-0002's output locations.
Include generated Go source in Git so ordinary builds need no generation step.

## Consequences

Go internal import rules prevent external modules from importing application
bindings directly. External clients can generate bindings from the schema;
RPC routes and wire contracts are unchanged. Shared transport stays independent
of CLI policy. No compatibility wrapper or runtime migration is introduced.
