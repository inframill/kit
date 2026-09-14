# SPEC-0009 implementation plan

Status: Implemented
Spec: `specs/0009-uuid-instance-storage/spec.md`

## Approach

Implement a single supported UUID layout with an explicit per-Instance discriminator.
Preserve current command/name semantics and existing startup validation. Keep the
work in four implementation slices followed by full verification. The user authorized implementation on 2026-09-13.

## Requirement mapping

| Requirement | Design element | Verification |
| --- | --- | --- |
| REQ-001/002 | UUID path helper and storage_layout metadata | Creation, reopen and historical-layout rejection |
| REQ-003 | Flat discovery with ID/name uniqueness checks | Corruption, mixed-layout and failure isolation |
| REQ-004 | Existing namespace lock and staged publication | Concurrent name/ID collisions; cancellation/failure fixtures |
| REQ-005 | Owned-directory identity, guarded failure publication, root-relative removal | Substitution, sibling preservation, delete/init overlap |
| REQ-006 | Existing name selection plus UUID-based subprocess paths | CLI, artifacts, logs and real-daemon lifecycle |
| REQ-007 | Explicit unsupported-layout failure | Byte preservation and fresh-root fixtures |
| NFR-001 | Unchanged boundaries and constructors | Architecture, Driver health and both test gates |

## Components and boundaries

### Slice 1: metadata, path derivation and discovery

- internal/backend/storage.go: add the required storage_layout field. Validate it
  and Instance api_version before returning an actionable identity. Current readInfo
  returns an identity for
  some unsupported metadata; do not let the new marker failure fall into the
  failed-Instance path that opens storage or rewrites metadata.
- internal/backend/discovery.go: replace project/leaf enumeration with direct UUID
  leaves; enforce directory/metadata identity, duplicate names and layout markers.
  Return sorted UUIDs. Keep strict root rejection before workers start.
- Add a small owned path helper accepting validated InstanceID, not project/name.
  Retain existing rejection of unsafe/symlink entries and known staging exclusions.
- Preserve missing-root behavior and Backend.New's current lazy storage behavior.

### Slice 2: initialization, ownership and deletion

- internal/backend/backend.go: remove project-directory creation and name-based
  target calculation. Stage under the instances root, validate/render as today,
  publish to the UUID target, and sync the root. Preserve .initialization.lock
  across independent Backend objects and metadata-based name uniqueness checks.
- SpawnInstance: verify the requested UUID against the leaf and stored identity,
  keep .owner.lock and failure isolation, and retain the original directory file
  identity to detect replacement before destructive work.
- internal/backend/instance.go and files.go: contain publication/removal beneath an
  opened instances root using APIs available in the installed Go version. Hold the
  namespace lock for deletion checks/removal as well as initialization publication.
  Review lock ordering: Backend initialization mutex -> namespace file lock; deletion
  must not acquire the Backend active-map mutex while holding the namespace lock.
- Guard cached Spawn, admission, state publication and the failLocked persistence path against a suspect replacement:
  it currently writes instance.json through i.root when owned. Add an internal way
  to mark failed/close safely without publishing through an invalidated path.
- Avoid a broad all-filesystem refactor. UUIDs and these guards do not isolate a
  malicious Driver with the same OS permissions; document the actual threat boundary.

### Slice 3: composition, paths and fixtures

- internal/cli/localexec/workspace.go: keep --name and local/default behavior.
  Resolve names from discovered metadata; never join the name to a data path.
  Preserve the distinction between supported dataDir/instances/<UUID> and obsolete
  dataDir/<UUID> detected by lockWorkspace. Review ordering so unsupported Instance
  layouts are rejected before acquisition/recovery publishes new Instance state.
- internal/server/server.go: continue passing dataDir/instances. Startup must reject
  incompatible roots before serving or worker recovery. No protected harness changes.
- Driver scope already joins workspace/artifacts to i.root, so its protocol requires
  no changes. Assert new absolute paths in actual debug/custom subprocess fixtures.
- Convert test path assumptions to returned UUIDs. Particularly update daemon.ts
  instancePath, dispose/release cleanup, lifecycle.test.ts, tests/cli local/history,
  selection/config/remote lifecycle fixtures, and backend/discovery/Blueprint tests.
  Negative tests asserting absence of a name path must instead assert no Instance
  publication, or they could pass while UUID-based data was incorrectly created.
- Leave RPC/Go interfaces, generated bindings, Blueprint caches and table output alone.

### Slice 4: verification, docs and handoff

- Run task check-fast after meaningful slices; run task check-full once when complete.
  All tests use -vet=off via existing tasks; no lint entry points.
- Update root README layout, artifact/barrier examples and transition diagnostics.
  Retain readable names in product examples and explain how full IDs map to storage.
- Record exact evidence and only then mark spec/plan/tasks Implemented.

## Data and interface changes

Canonical leaf changes from project/name to UUID. instance.json gains a required
storage_layout discriminator. SQLite schema and version, Driver wire protocol,
RPC descriptors and Go interfaces remain unchanged. Existing data is incompatible
and is rejected unchanged; conversion and compatibility support are out of scope.

No new persistent name index is needed. Engine/local exec already select names from
metadata-backed Instance objects. Discovery uses a temporary seen-name map; creation
checks names under the cross-process lock. An in-memory index alone cannot enforce
uniqueness against another Backend process.

## Error and safety design

Root discovery remains strict. Direct path derivation must not quietly remove the
old Backend.SpawnInstance behavior of rejecting ambiguous sibling storage. Initially
retain the full discovery guard for standalone SpawnInstance, even though the chosen
path is derived directly from UUID. This deliberately does not claim constant-time
opening or linear startup; avoiding repeated scans requires a separate validated
snapshot/invalidation design. Defer that optimization rather than weakening safety.

Tests must differentiate unsupported identity/layout (global rejection) from valid
supported identity with broken execution files (per-Instance failure). Preserve old
bytes on unsupported formats. Name reuse allocates a fresh UUID through Engine;
no tombstone database or historical UUID registry is introduced.

Root-relative removal constrains which tree can be affected. Directory identity and
metadata checks catch replacements made before the destructive operation. They do
not promise immunity to malicious concurrent mutation by a same-user Driver.

## Test strategy

| Area | Required cases |
| --- | --- |
| Layout | UUID leaves, markers, valid names retained, permissions, restart |
| Legacy | Named/empty-project, flat-at-dataDir, unmarked UUID, mixed, unknown marker, UUID-named project |
| Identity | Invalid UUID, ID mismatch, duplicate names, unreadable identity, symlink leaves/metadata |
| Isolation | Supported identity with corrupt manifest/SQLite, healthy sibling still usable |
| Concurrency | Same-name/same-ID competing initializers; owner conflict; delete/init overlap |
| Destruction | Requested leaf only; replaced directory/symlink/metadata; safe failure publication; partial deletion |
| End to end | Default/named exec/history; remote lifecycle; UUID change on name reuse; artifact/log persistence |
| Recovery | Interrupted commands without replay; barriers cleaned via UUID paths; no workspace/source digest change |
| Boundaries | Driver health without storage; full JSON IDs; unchanged protobuf/generated drift; harness untouched |

Use existing tests as starting points, not as authority for the old path contract:
TestNamedStorageLifecycle, TestNamedStorageConcurrentCollisions,
TestNamedStorageDiscoveryRejections, TestNamedStorageReadableFailureIsolation,
TestLegacyAndUnsupportedStoresArePreserved, TestLocalExecReuseAndResults,
TestLocalHistoryOutput, TestConfiguredDataSharedWithDaemon and daemon lifecycle suites.
Add targeted negative/sentinel fixtures and record REQ IDs on the changed tests.
No live infrastructure or current user data should be used for verification.

## Gaps and blockers

| Finding | Treatment | Blocking? |
| --- | --- | --- |
| Current accepted specs require name-addressed storage | Accepted spec/ADR explicitly supersede those clauses | Resolved |
| Existing-data transition | User chose reject/preserve with no migration or compatibility | Resolved by user |
| Historical UUID layout shares the desired shape | Required uuid-v1 layout marker; reject unmarked data | Resolved in accepted design |
| Name uniqueness no longer follows path collisions | Scan validated names under existing cross-process lock | Resolved in accepted design |
| Direct lookup could bypass global ambiguity guards | Preserve preflight initially; defer scan optimization | Resolved, with performance tradeoff |
| Failure handler writes to its recorded pathname | Prevent persistence through replaced/invalidated paths | Required safety implementation work |
| Many fixtures assume names; crash cleanup does too | Update returned-UUID helpers and negative-publication assertions | Required test work |
| Driver files may embed absolute paths | No relocation or conversion is attempted in this scope | Not a blocker |
| Same-user Drivers can write outside their supplied paths | Keep trusted-Driver model; UUID layout is not isolation | Explicit limitation, not a blocker for this scope |

No missing library, external service, schema generator change or protected harness
edit was found. Go's installed os.Root supports the root-relative publication and
removal operations needed here. The transition decision is resolved. No unresolved
technical blocker remains; the user authorized implementation of this spec.

## Rollout and rollback

User-selected scope: preserve old Instance roots untouched and point the new binary
at a separate empty dataDir for intentionally new work. Reject old or mixed layouts
with clear diagnostics. Do not recreate deployments against live infrastructure as
a migration shortcut. No automatic moves, converter, compatibility parser, alias,
old-binary support or cross-version rollback mechanism is included.

Restoring a previous application version is a separate operator decision; this
change only promises that rejected old data is preserved, not that another version
can understand new data. No inventory of live Driver artifacts is required because
none will be moved or transformed.

## Alternatives considered

- Name-based symlink aliases add another mutable lookup and are not needed.
- A new persistent registry adds consistency/recovery obligations without a present need.
- Keeping api_version alone cannot distinguish historical UUID-shaped data; a
  versioned layout marker makes acceptance explicit without changing other protocols.
- Immediate direct opens with no global preflight would change existing failure safety.
- Automatically renaming directories ignores opaque Driver paths and mixed-format crashes.
