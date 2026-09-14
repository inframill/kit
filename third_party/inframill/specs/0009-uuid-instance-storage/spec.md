# SPEC-0009: UUID-addressed Instance storage

Status: Implemented
Owner: Inframill maintainers
Created: 2026-09-13
Updated: 2026-09-13

## Problem

Instance storage uses project/name directories while Engine and RPC operations use
immutable UUIDs. Recreating a name reuses its old filesystem path, and UUID lookup
requires reading name-addressed directories. The user requests separating physical
storage identity from readable names.

## Outcome

Store Instances in <dataDir>/instances/<InstanceID>/, retaining immutable unique
project/name in metadata and existing CLI behavior. Establish explicit format,
discovery, ownership and deletion rules before implementing.

## Scope

### In scope

- Backend publication, discovery, path resolution, ownership and deletion.
- A required storage-layout marker in Instance metadata, separate from wire versions.
- Existing native exec/history and daemon behavior with UUID paths.
- Driver path values, tests, root README and ADR-0007.
- Explicit rejection/preservation of unsupported or mixed layouts.

### Out of scope

- Instance renaming, name aliases/symlinks, a new name database or public RPC.
- Driver sandboxing, remote artifact storage, cross-host locking and automatic repair.
- Broad replacement of all filesystem APIs or guaranteed constant-time startup.
- Migration tools, compatibility readers/aliases, deletion of legacy data, replay
  or reinitialization of old deployments. The user explicitly excludes compatibility
  and migration for this prerelease change.

## Requirements

Accepted for implementation at the user’s request on 2026-09-13.

- **REQ-001:** Canonical Instance directories MUST be direct children named by the
  validated lowercase full InstanceID UUID under the configured instances root.
  Their existing source, workspace/<DriverID>, artifacts, logs, commands.sqlite
  and ownership files MUST retain their roles. Names MUST NOT construct paths.
  project/name remains immutable and unique; existing name validation stays intact.
- **REQ-002:** New instance.json documents MUST include the required discriminator
  storage_layout: uuid-v1, alongside existing id/name and api_version fields.
  Directory UUID, requested UUID and metadata UUID MUST agree. Missing/unknown
  layout markers or unsupported Instance api_version values MUST reject before
  opening SQLite, workers or operation effects.
  Merely moving historical UUID-shaped data MUST NOT make it supported.
  RPC/Driver/Blueprint/local-result versions and SQLite user_version remain unchanged.
- **REQ-003:** Discovery MUST validate all visible entries and metadata identities
  before starting workers, returning UUIDs sorted as today. Duplicate names, invalid
  UUID leaves, ID/path mismatches, symlinked Instance/metadata entries, missing or
  unreadable identities and unsupported/mixed layouts MUST reject root discovery
  while preserving existing Instance files. A valid supported identity with damaged
  execution state MUST still appear as a failed Instance with healthy siblings usable.
  Name-based project directories, including empty ones, MUST reject rather than be
  silently ignored. Existing private coordination/staging entries are not Instances.
- **REQ-004:** InitInstance MUST validate IDs/names and serialize duplicate UUID/name
  checks and publication with the existing cross-process initialization lock. It
  MUST publish a complete private staged directory atomically at instances/<UUID>,
  sync publication as supported today, and never overwrite an existing destination.
  Failure MUST clean only its own unpublished staging data. Name uniqueness MUST
  come from validated metadata, not from directory collisions or only a local map.
- **REQ-005:** Spawn and deletion MUST resolve the selected UUID and verify identity
  and ownership before acting. Deletion MUST continue draining prior work and close
  resources before removing only that UUID leaf. Creation/deletion namespace changes
  MUST coordinate through the existing initialization lock. Replacing the selected
  directory, changing its UUID metadata or inserting a symlink before deletion MUST
  cause rejection with no deletion or failure-metadata write through that suspect
  path. Use root-relative filesystem operations for publication/deletion containment
  and retain the owned directory identity for replacement checks. Partial deletion
  remains failed and must not be mistaken for confirmed absence.
- **REQ-006:** Local exec/history MUST retain project/name selection and reuse of
  the corresponding immutable UUID. Missing history names MUST not create an
  Instance. Deleting and recreating a name through Engine MUST allocate a new UUID
  and path; the old UUID MUST not select the replacement. Driver validation/run
  requests MUST carry the absolute UUID-based workspace/artifact paths, with
  binding.instanceID and binding.name unchanged in meaning. No protocol field changes.
- **REQ-007:** The user-selected transition MUST reject existing name-based or
  unmarked legacy layouts with an actionable diagnostic and preserve their bytes.
  No automatic moves, metadata upgrades, cache clearing or data deletion. A separate
  empty dataDir supports new Instances. There MUST be no migration tool, compatibility
  reader or name-based alias. Standalone Driver health and config-free help remain
  independent of Instance storage. Do not execute old deployments in a new empty
  root merely as a means of moving them.
- **NFR-001:** Keep package boundaries, queues, secret handling, output and Driver
  trust assumptions intact. Backend.New MUST stay free of storage I/O so native
  Driver health listing works without accessing Instances. No harness changes or
  linting. Verification MUST use fakes/disposable local fixtures and both existing
  task check-fast and task check-full gates, with Go vet disabled.

## Acceptance criteria

- **AC-001** (REQ-001/002): Create two named Instances and prove UUID directories,
  required layout markers, metadata/name preservation, private modes and unchanged
  saved source/artifact/log structure; prove no name-based alias directories.
- **AC-002** (REQ-002/003/007): Test old named, old flat-at-dataDir, unmarked UUID,
  unknown marker and mixed roots, including UUID-shaped project names. Test duplicate
  names, mismatched IDs, missing/corrupt identity and symlinks. Reject before opening
  workers/SQLite and preserve existing file contents and symlink targets.
- **AC-003** (REQ-003/004): Retain corrupted-execution-state isolation. Competing
  separate Backend objects/processes initializing the same name or UUID MUST have
  exactly one winner. Cancellation/publication failures preserve sibling data.
- **AC-004** (REQ-005): Verify exclusive ownership and root/target containment; deletion
  affects only the requested leaf, preserves siblings, and rejects a substituted
  directory, symlink or mismatched metadata without writing into the replacement.
  Exercise deletion/init overlap, prior-work drain, and partial-delete failure.
- **AC-005** (REQ-006): Prove local default/named exec reuse, history pagination and
  named lookup, daemon lifecycle, restart interruption without replay, and deletion
  followed by name reuse with a new UUID/path. Check actual subprocess request paths,
  persistent artifact read/write, logs and debug barrier cleanup by UUID.
- **AC-006** (REQ-007, NFR-001): Verify unsupported-root startup/local rejection and
  fresh-root usage, unchanged Driver health/help behavior, README examples and both
  test gates; generated Go/TypeScript bindings MUST have no content changes.

## Interfaces and examples

Canonical disk layout:

```text
<dataDir>/instances/<UUID>/
  instance.json
  commands.sqlite
  .owner.lock
  manifest.yaml
  schema.json
  template/
  workspace/<DriverID>/
  artifacts/
  logs/<CommandID>/
```

Relevant metadata fields (other existing fields remain required):

```json
{"api_version":"inframill.dev/instance/v1","storage_layout":"uuid-v1",
 "id":"00000000-0000-4000-8000-000000000001","name":"project/dev"}
```

Existing CLI paths/flags, Engine/Backend method signatures and Connect messages
remain unchanged. Use complete JSON IDs when navigating UUID paths; human tables
continue the SPEC-0008 width limit.

## Failure behavior

Invalid caller UUID/name remains an invalid request. Unknown UUID/name remains
not found. Unsupported layout or ambiguous identity is a storage/startup error,
not an empty catalog or a signal to initialize replacement data. Native runtime
failures retain exit 3; cancellation retains exit 130. Failed Instance behavior,
operation history and no-replay semantics remain as currently specified.

## Security and safety

UUID path selection improves identity isolation and reduces path reuse. It is
not a sandbox for Drivers running as the host user. Root-relative operations and
identity checks address containment and detectable substitution; hostile concurrent
filesystem mutation by a same-user process is outside this change's guarantees.
Init is mutating; deletion is destructive local storage work, verified by absence.
Discovery/path validation is read_only; spawning/recovery can modify stored state
and must not begin until layout validation succeeds. 

## Compatibility and migration

This spec explicitly supersedes SPEC-0000 REQ-002/004/005/011 and ADR-0000's
name-addressed storage clauses. The layout marker is a deliberate prerelease stored
contract change: old strict readers reject it; new readers reject unmarked data.
The UUID shape alone is insufficient because repository tests already identify a
historical UUID layout. No compatibility aliases or silent format adoption.

The user confirmed this project is prerelease and selected rejection/preservation
with no migration or compatibility requirements. Preserve old roots as-is and
select a separate empty root for intentionally new work. Old data cannot be used
with the new binary. Core must not relocate or rewrite opaque Driver files. There
is no cross-version execution or conversion support to maintain.

## Open questions

None for the selected scope. On 2026-09-13 the user explicitly chose to reject old
layouts and preserve their files, with no migration tool or compatibility layer.

The user authorized implementation on 2026-09-13. No technical or external
dependency blocker was found for the accepted scope.
