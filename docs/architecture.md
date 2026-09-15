# Inframill Kit architecture

Inframill is the control plane: it manages Instances, resolves and renders
blueprints, invokes external drivers, and records execution history. Kit provides
OpenTofu and Ansible drivers through Inframill's public protocol 1.

## Programs and ownership

| Program | Responsibility | Distribution |
| --- | --- | --- |
| `inframill` | Control plane | Prebuilt binary copied from sibling `../inframill/dist` |
| `inframill-kit` | Versioned driver execution and verification | Built here and installed in the runtime |
| `kit-build` | Local build, configuration and OCI packaging/push tools | Invoked by Task; not installed in the runtime |

Kit imports the public `pkg/driver` protocol from the sibling Go module. No
Inframill source snapshot is kept here or embedded in the image. Bundle creation
never invokes the sibling's build or release tasks.

## Versions and metadata

Blueprint, driver, Kit and bundle versions are independent full SemVer identities.
The initial production catalog contains `opentofu@0.1.0` and `ansible@0.1.0`, with
refresh-only planning and Ansible tag selection included. A second version should
represent an actual maintained release, not an example of version support.

`releases/drivers/<name>/<version>.yaml` is embedded by `releases/catalog.go` and
read by the compiled catalog. `internal/catalog` owns exact selection and driver
ID/configuration generation. `releases/kits` declares the included versions;
bundle locks select the versions to configure. Missing versions never fall back.

`blueprints/<name>/versions/<version>` contains immutable source packages.
`releases/blueprints` seals each source layer and pins exact driver requirements.
`releases/sets/default.yaml` is the explicit normal publication set. Currently it
contains only pv-cloud; adding other real blueprints needs no packaging changes.

Tests keep two local-resource blueprint versions under `tests/fixtures`, separate
from production release sets. They both use the initial production drivers.
Integration tests compile an executable with the `kit_testdrivers` build tag to
exercise two driver versions at once. Synthetic versions are absent from normal
builds. Tests still cover missing/version mismatches and saved-plan isolation.

## Toolchains and runtime

`runtime/tools.lock.json` records OpenTofu assets for supported development/runtime
platforms, Python and Ansible versions, and hashes of the Python distribution and
source locks. A bundle lock pins this toolchain file by SHA-256, along with base
image digests, Inframill binary paths/checksums, Go version and driver selection.
`release_config.py` resolves that lock for setup, bundle creation and checks.
Registry setup and tests share the image pin in `runtime/registry.compose.yaml`.

Blueprints are deterministic architecture-independent OCI source artifacts. The
runtime is an OCI image index with Linux AMD64 and ARM64 children. Each child
contains both Inframill and Kit, with matching tool dependencies. Its Inframill
binary must match the expected checksum, ELF architecture and executable version.

Both local and runtime configuration use the Go configuration generator. Blueprint
execution tests read actual driver IDs from release requirements, independently
of blueprint version numbers. Tool paths are isolated by upstream tool version.

## Driver execution

Kit implements the public `driver.Driver` interface and delegates transport to
`driver.Serve`. Config, blueprint, instance and command validation use separate
phases. `Prepare` validates inputs without executing tools or resolving secrets;
the returned command performs effects only when `Execute` is called.

OpenTofu initializes with readonly provider locks and Instance-scoped backend
keys. Saved plans are private and bound to the Instance, blueprint digest,
driver/tool versions, configuration and source hash. Apply/destroy require a saved
plan and invalidate node artifacts before effects. Successful apply publishes
allowlisted nodes; destroy verifies managed state is empty. There is no automatic
migration, retry or rollback.

Ansible derives inventory from the bound node artifact and uses the explicitly
selected address. SSH is noninteractive. Execution requires a complete structured
recap without failed, unreachable, ignored or rescued failures. Check mode is a
prediction. Blueprint assertions provide guest architecture/service verification.

Secret references resolve only during execution. Cloud/backend credentials are
passed through a restricted environment, not rendered into input variables or
arguments. Saved plans may contain infrastructure metadata and public keys; use
native OpenTofu encryption for blueprints with confidential resource values.

## Publication and verification

Task invokes build/check scripts directly. Packaging/push operations stay in
`kit-build` and `internal/packaging`. Builds write OCI layouts and digest records;
push verifies and uploads those existing bytes. Changed existing tags are rejected.
Publication is manual, primarily to GHCR; no automatic workflows are installed.

Fast checks cover protocol, configuration, plan/artifact binding, package integrity
and binary inputs. Full checks add actual Inframill/tool execution, provider mocks,
a disposable registry and both runtime platforms. Native and emulated coverage
are reported separately in [acceptance.md](acceptance.md).

`old-blueprints/` remains migration reference material. Adopting legacy state is
outside the initial fresh-deployment scope.
