# ADR-0002: A cohesive internal CLI subtree

Status: Accepted
Date: 2026-09-13
Related spec: SPEC-0003

## Context

The maintainer requested all CLI packages beneath internal/cli and removal of
the package command, while allowing subpackages with clear responsibilities.

## Decision

Keep root command composition in cli, with config, runtime, output,
output/remote, localexec, generator and generated subpackages. Config and output
are Cobra-free leaves. Runtime owns remote execution and consumes config/output;
localexec uses shared application services. Leaves never import root composition.
The generator bootstraps without generated code. Buf writes CLI bindings only
to internal/cli/generated and transport to gen, cleaning neither handwritten
CLI source nor shared services. Drift checks compare both complete trees.

Remove package registration without replacement aliases. Shared Blueprint
building stays in internal/blueprint and a test-only executable prepares
disposable engine fixtures. cmd/main.go remains the sole shipped entry point.

## Consequences

This supersedes earlier CLI locations associated with ADR-0001 without changing
its generation, configuration or execution policies. CLI ownership is visible
in one subtree, with import boundaries enforced by architecture tests. The
fixture helper participates in formatting, lint and tests but not releases.
No protected harness source, runtime data or persistence formats change.
