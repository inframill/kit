# ADR-0000: Tool-neutral executable Drivers

Status: Accepted
Date: 2026-09-12
Related spec: SPEC-0000

## Context

Inframill manages named deployments through user-built Drivers. The prerelease
application needs a small, explicit extension boundary and one current contract.
Core distribution must remain independent of the tools a Driver might invoke.

## Decision

Keep pkg/inframill standard-library-only. Engine owns UUID identities and routes
through neutral BlueprintSource, Backend and Instance interfaces. Instance names
are immutable project/name strings, distinct from UUIDs. Connect translates the
same domain operations; it does not own orchestration.

Backend owns atomic Instance publication, private SQLite/history/logs, directory
ownership and a FIFO worker per Instance. Durable admission precedes acknowledgement
and durable running precedes effects. Ordinary operation failure leaves the worker
usable; storage failure isolates an Instance. Restart interrupts abandoned commands
without replay. builtin.delete removes one named local leaf; it never destroys
infrastructure. Reusing a deleted name creates a new UUID.

Configure Drivers explicitly as executable paths with literal prefix arguments.
Probe info before storage/serving and reject invalid or duplicate metadata. Each
info, validate or run is a fresh process exchanging one JSON document over stdin
and stdout. Metadata declares protocol, independent version and operation schemas,
safety, effects and verification. No resident service, SDK, implicit discovery or
version resolution is needed. Different deployments select versions by configuring
different executable paths; only one Driver per name may be active in a catalog.

Drivers own validation, tool invocation, verification and artifact formats. Supply
absolute workspace/artifact directories, not host byte-access callbacks. The debug
adapter enforces bounded, private atomic artifact access. External Drivers are
trusted programs with the host user's permissions. Merge tool stdout/stderr into
Driver stderr; reserve Driver stdout for JSON. The host records both streams,
redacts known secrets and cancels process groups. Secrets are available only to run.

Build one Go executable. Its driver debug subcommand serves the testing Driver
without loading config or starting HTTP. The host invokes that command through
exactly the same subprocess protocol as a third-party Driver. The debug Driver
covers artifacts, durable recording, logs, failures, cancellation and secret
redaction. Core and debug report independently overridable versions, initially
0.0.0-dev. Build/release/install use direct Task commands; no container or tool
installation is part of binary distribution.

BlueprintSource owns verified OCI acquisition, local immutable metadata and offline
schema validation. Snapshot/render before atomic named publication. Cached tags do
not refresh automatically and existing Instances retain their Blueprint/Values.

Server and local exec require .inframill.yaml or --config FILE. Shared appconfig
selects dataDir (cwd-relative) and Drivers (config-relative paths). The protected
harness owns YAML loading, HTTP lifecycle and diagnostics. Local exec uses the same
Engine/Backend and streams diagnostics to stderr with JSON results on stdout.
History reads saved state without config, source or Drivers. Explicit dotenv input
supplements secret resolution with inherited values winning; it never executes text.

Maintain one baseline, this architecture record and one root README. New change
records begin at 0001. Release identifiers may reset during prerelease; protocol 1,
storage v1 and unsupported-format guards remain intact. Preserve user data and do
not introduce implicit compatibility layers or migrations.

## Consequences

Users can implement Drivers in any language and package selected versions beside
Inframill. Process startup overhead is accepted for a simpler lifecycle and failure
boundary. Driver authors own tool versions, operation verification and safe file
access; the host provides scheduling, persistence and output redaction. Packaging
the debug Driver in the core binary adds no special backend execution path.

Prerelease changes remain explicit and testable. Git retains retired records while
the working tree documents only the current contract. Protected upstream code and
runtime data are outside cleanup scope.
