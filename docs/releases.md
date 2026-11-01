# Releasing Kit

Kit, the engine and provider implementations have independent versions. Kit's
version is `toolai.json.version`. Engine and runtime pins have one source of truth
in `runtime/dependencies.json`, including
the immutable base image and signed Debian snapshot. Provider metadata describes
implementation/protocol versions and required external tool versions.

OpenTofu dependency locks inside blueprints remain owned by those blueprints.

## Prepare inputs

The `INFRAMILL` record contains the engine version, full Git revision, normalized
Runmill source hash and SHA-256 checksums for all four platform binaries.
There is no Inframill dependency in Kit's Go module
and no separate engine lock file. `task dev:sync` updates this record from the clean
committed HEAD of `../inframill` and copies its verified binaries plus Runmill Go sources and tests into
Kit's `pkg/runmill`, replacing imports and removing obsolete Go files while keeping
Kit's README. Override the checkout with `INFRAMILL_DIR=/path/to/inframill`.
`task dev:prepare` first builds the engine in the sibling, then imports its outputs,
installs the pinned tools, builds Kit binaries and writes
the native development config. Neither task changes Kit/provider versions or the other runtime tool pins.
Import verifies executable architecture, embedded Git revision, clean-build marker,
native engine version and binary checksums. Packaging rechecks the copied binaries
and Runmill source hash against the runtime pin. All engine binaries live directly
in Kit's `bin/`; `.build/engine` retains only generated identity and dependency notices.
Full engine sources are excluded from Kit staging, images and release archives,
including `sources.tar.gz`, which contains Kit sources and the copied Runmill package.
No Go workspace or upstream module fetch is needed to select the engine. The engine
keeps its dependency checksums in its own checkout. No credentials belong in project
configuration or release archives.

Update provider/tool metadata when implementation requirements change. Native setup
and the Docker image read the same runtime dependency JSON. Bump Kit's version
independently as appropriate.

## Build and check

```sh
toolai doctor
toolai check
task setup
toolai build release
toolai check full
task release:verify
```

The default build prepares standard provider binaries, imports verified pinned engine binaries,
and packages/loads a local image for Linux AMD64 and ARM64. The release tag adds
bundle assembly. Builds do not run checks, fetch a newer engine, change versions, or
publish. Checks consume existing artifacts; missing prerequisites fail clearly.
`toolai check lint` is separate and does not apply rewrites.

| Command | Coverage and prerequisites |
| --- | --- |
| `toolai check` | Module tidiness, package boundaries, compile, vet, Go/protocol tests and Python unit tests; Go and Python 3.9+, without Docker or installed infrastructure tools |
| `toolai check native` | Default checks plus native daemon/blueprint workflows and PostgreSQL/Millway payload acceptance; prepared native provider/engine binaries, `task setup`, Docker/Compose, OpenSSL, and local Kit/Millway images |
| `toolai check image` | Default checks plus prepared bundle publication to a disposable registry and runtime acceptance on Linux AMD64 and ARM64; `toolai build release`, Docker/Buildx and support for both architectures |
| `toolai check full` | Both acceptance suites above plus module verification, `testproviders` compile/vet, and Go race/coverage tests with randomized order |
| `toolai check lint` | Default checks plus Go lint with `testproviders`; golangci-lint required, no fixes applied |

Tags add steps to the default checks. `task test:native` and `task test:image`
run just their focused acceptance jobs. Native setup needs Python 3.12–3.14 for
Ansible. The Millway payload suite uses the `0.1.6-dev` UI/API image for the Docker
host architecture and the current `ghcr.io/inframill/kit` image locally. These
payload fixtures currently use that Kit reference even when build/publication
overrides select another registry. See [native development](../README.md#build-and-verification)
for preparing binaries without image packaging.

```text
bin/                         Provider and engine platform binaries plus native executables
.build/                      Disposable engine/image staging and input fingerprint
dist/kit/<version>/
  image.oci.tar              Exact multi-platform OCI image
  manifest.json              Resolved inputs, versions, binary hashes and image digest
  SHA256SUMS                 Complete bundle file checksums
  sources.tar.gz             Kit source snapshot, including the copied Runmill package
  notices/                   Available dependency distribution notices
```

Development outputs can be rebuilt normally. Source inputs are captured once before
building and checked during packaging, preventing mixed-source bundles. Temporary
staging prevents a failed assembly from replacing a completed bundle. Image assembly
checks binary architecture, embedded versions, copied Runmill source identity, provider registry and
tool compatibility. Docker/Buildx handle OCI export/load; no custom OCI graph or tar
index rewriting is required. Loading checks that Docker retained the recorded digest.

## Verify and publish prepared bytes

```sh
task release:verify -- --bundle /path/to/bundle
task release:load -- --bundle /path/to/bundle
# Authenticate to the intended registry first:
task release:push -- --bundle /path/to/bundle
```

With an explicit bundle path, verification and publication use its metadata and
checksums without consulting source or sibling checkouts. Verification rejects
corrupt, missing, extra, or symlinked files. The OCI archive checksum covers all
image content. Runtime manifest data records installed engine/provider binary hashes;
the outer manifest additionally records the completed image digest.

REGISTRY and NAMESPACE select the publication destination (default ghcr.io/inframill).
The version comes from the bundle. Publishing loads and tags the verified archive,
rejects an existing different digest, and verifies the published digest. It never
builds. A matching published digest is idempotent. Registry-side immutable tags are
the final protection against simultaneous publications from different machines.

Full acceptance publishes only to a disposable local registry, then exercises
persistent storage, configuration, providers, blueprints and lifecycle on both Linux
architectures. No GHCR publication or cloud resource creation occurs in checks.
