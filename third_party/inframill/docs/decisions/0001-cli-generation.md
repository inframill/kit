# ADR-0001: Protobuf-driven CLI and application settings

Status: Accepted
Date: 2026-09-12
Related spec: SPEC-0002

## Context

The existing CLI repeats command parsing, request construction and presentation
switches. User settings must coexist with the harness-owned server configuration.
The maintainer supplied generic application sections in the protected harness.

## Decision

Use Cobra for one command tree with typed localexec inputs. A local protogen plugin
emits constructors, scalar/enum/message-file bindings and direct typed Connect
calls from unary RPC descriptors. Generate all methods by convention, with narrow
Go adapters for existing positional arguments, list/log bounds, submit/delete/log
variants, catalog selection and waits. SubmitCommand maps to instance exec;
ReadCommandLog supplies command stdout/stderr. Commands otherwise split method
names into verb and singular resource. Go presentation definitions contain only
response paths and formatting. Descriptor validation precedes requests and JSON
bypasses projection. Unknown responses retain complete descriptor-based rendering.

Native config uses harness WithAllowedSections("cli") and validates the returned
section in application code. A separate small CLI reader inspects only cli for
remote/history use; it never initializes server resources. Harness retains native
YAML/environment semantics. Remote cli scalars are literal, with flag/environment
precedence applied before scalar validation. Structural/type errors always reject.

Prompts and pagers are deferred at the user’s request. Automatic color honors
each output destination and NO_COLOR.
Machine streams bypass styling. Waits use a single deadline, submit exactly once,
and verify Instance absence for deletion. Domain and persistence contracts remain
unchanged; no runtime data migration or protected harness edits are required.

## Consequences

Ordinary unary additions need regeneration, not command dispatch changes. Exceptional
UX remains explicit Go policy. The generator bootstraps without generated files;
all generated outputs share the existing deterministic Buf generation/drift gate.
The CLI reader duplicates only document/section shape checks, not server decoding.
