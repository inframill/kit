# Inframill Kit: versioned drivers, blueprints, and runtime bundles

Status: Implemented locally; release acceptance and verification results are tracked below.

## Objective and boundaries

Build one Go executable, `inframill-kit`, with an extensible registry of Inframill
protocol-1 driver implementations. Start with `opentofu` and `ansible`; support
multiple independently maintained versions of each driver at the same time.
Build and maintain multiple blueprints, starting with `pv-cloud`, with independent
release versions for each blueprint. Publish each blueprint version separately as
an Inframill-compatible OCI artifact primarily to GitHub Container Registry
(`ghcr.io`), with a local OCI registry for development and tests; do not embed
blueprints in the executable. Blueprint packages must support both ARM64 and
AMD64, using `arm64` and `amd64` as canonical architecture names. Build a separate distribution bundle containing
Inframill, Inframill Kit, and the pinned tool/runtime dependencies required by its
selected driver versions.

Inframill remains the control plane. Inframill Kit owns tool execution, tool-specific
validation and verification, and the artifact contract between the drivers.
The inspected Inframill README and public protocol support executable aliases and
opaque driver configuration. Implement version selection and compatibility checks
in Kit and its build tooling; verify their integration without assuming core
dependency resolution or automatic version upgrades.
Reference inspected: commit `acb50af152b4412110639ffaf7de1779f97304aa`.
Kit imports the public driver protocol from the sibling `../inframill` Go module.
The bundle copies prebuilt Linux ARM64 and AMD64 binaries from that project's
`dist/` directory, verifying the per-platform paths and checksums in the bundle
lock. It does not build Inframill. The local `third_party` directory is removed.

Confirmed project name: Inframill Kit. Repository: `inframill/kit`; Go module:
`github.com/inframill/kit`; executable: `inframill-kit`. The runtime image repository
is `ghcr.io/inframill/runtime`, and each blueprint OCI repository is
`ghcr.io/inframill/blueprints/<name>`. The first is
`ghcr.io/inframill/blueprints/pv-cloud`. Default `REGISTRY=ghcr.io` and
`NAMESPACE=inframill`; allow both to be overridden for another owner or registry.

Preserve `old-blueprints/` as migration input; the original files are unchanged.
Confirmed first-release scope: fresh deployments only. Adoption of infrastructure
and remote state created by the older software is deferred; no legacy state
migration, resource import, or compatibility layer is part of this release.

## Component identities, versions and maintenance

Version these independently; a Kit release is not the version of every component:

| Component | Identity and version | Release binding |
| --- | --- | --- |
| Blueprint | Blueprint name + SemVer | OCI repository/tag and immutable manifest digest |
| Driver implementation | Driver name + SemVer | Exact implementation selected in a Kit build |
| Tool runtime | Tool name + upstream version | Verified binaries/dependencies required by a driver version |
| Kit executable | Kit SemVer | Build digest and catalog of included driver versions |
| Runtime bundle | Bundle SemVer | Immutable image digest and lock of all included components |
| Protocol/artifact schema | Explicit schema version | Supported protocol and artifact reader/writer versions |

Recommended release format: full `MAJOR.MINOR.PATCH` for both blueprints and driver
implementations, following [Semantic Versioning 2.0.0](https://semver.org/). Single
segments such as `2` identify a compatibility line, and two segments such as `2.3`
identify a minor line; neither is an exact release identifier. Require full
versions in release descriptors, driver selectors and locks. Do not introduce
floating major/minor release aliases in the initial implementation.

Define the compatibility contract before declaring `1.0.0`:

| Increment | Blueprint contract | Driver contract |
| --- | --- | --- |
| MAJOR | Incompatible inputs, outputs, state/resource identity or documented deployment behavior; e.g. removing an input or requiring state migration | Incompatible configuration, commands, arguments, results, artifact support or execution behavior |
| MINOR | Backward-compatible capabilities, such as an optional role disabled by default | Backward-compatible commands or optional configuration; deprecations |
| PATCH | Backward-compatible fixes to existing templates or validation | Backward-compatible execution, parsing or verification fixes |

Changing an image, provider, tool pin, default or required driver version is not
automatically a patch: classify its effect on the documented contract and test it.
A compatible release can still change infrastructure when explicitly applied;
SemVer does not promise an effect-free upgrade or provide a migration mechanism.
Version each named blueprint and driver independently. For example, maintain
`pv-cloud` releases `1.4.2` and `2.0.0` while its OpenTofu driver has its own
`1.3.1` release. Start development at `0.1.0`; before `1.0.0`, document breaking
changes and use minor increments for them as a project convention. Use full
prerelease versions such as `1.0.0-rc.1` for release candidates. Keep build revision
metadata in descriptors rather than using it to distinguish release tags.

Protocol and artifact schema identifiers may remain single integers such as `1`;
they identify a wire contract, not a distributable component release.

Driver implementation versions are distinct from OpenTofu or Ansible Core versions.
For example, two OpenTofu driver versions may use the same tool release, or require
different releases. Bundle both tool releases when required, with isolated paths;
isolate Ansible's Python dependencies and callback versions as well.

Proposed repository structure:

```text
blueprints/<name>/versions/<version>/{manifest.yaml,schema.json,template/}
drivers/<name>/versions/<version>/
releases/blueprints/<name>/<version>.yaml
releases/drivers/<name>/<version>.yaml
releases/kits/<version>.yaml
releases/bundles/<version>.lock.yaml
```

Release descriptors record exact source revisions, compatibility requirements and
checksums/digests as appropriate. Build outputs are namespaced by component name,
version and platform so versions cannot overwrite one another. Keep maintained
version lines available together; fixes to an older line produce a new patch
release and do not replace an existing release's contents. Shared helpers are
allowed, but every published build remains reproducible from its recorded source
and dependency lock. Record supported/deprecated/end-of-life status per component
line; ending support does not silently delete artifacts required by existing Instances.

Published release tags are immutable by policy: publication must reject an existing
name/version with a different digest. Explicit development tags may be mutable;
Instance execution always uses a resolved digest. No implicit `latest`, highest
installed version, or fallback to another driver version. Blueprint changes,
driver upgrades and bundle upgrades are separate explicit operations. Existing
Instances retain their blueprint digest and required driver version; a new bundle
must preserve those requirements or reject the configuration. Updating the bundle
does not migrate Instance values, state, or saved plans.

## Executable and blueprint delivery

Proposed interfaces:

```text
inframill-kit driver <name> --driver-version <exact-version> info|validate|run COMMAND
inframill-kit drivers list
inframill-kit version
```

Configured driver IDs point to the same executable with explicit name/version
prefix arguments. Illustrative versions below are examples, not release commitments:

```yaml
engine:
  dataDir: .inframill
  drivers:
    opentofu-1-0-0:
      path: ./bin/inframill-kit
      args: [driver, opentofu, --driver-version, 1.0.0]
    opentofu-2-0-0:
      path: ./bin/inframill-kit
      args: [driver, opentofu, --driver-version, 2.0.0]
    ansible-1-0-0:
      path: ./bin/inframill-kit
      args: [driver, ansible, --driver-version, 1.0.0]
handlers:
  - type: connect-engine
    settings:
      path: /inframill.v1.EngineService/
```

Use a registry keyed by `(driver name, exact implementation version)` rather than
a fixed two-driver switch. Each implementation owns its configuration schema,
commands, tool requirements and artifact compatibility. `info` reports the selected
driver's name and implementation version; `version` reports Kit's release and
`drivers list` enumerates included implementations. Missing or unknown selections
fail explicitly. A Kit build can include multiple versions of each driver.

Inframill uses configured IDs for command prefixes and workspace directories.
For example, a blueprint selecting `opentofu-1-0-0` must use that key under
`drivers`, place files under `template/opentofu-1-0-0/`, and invoke
`opentofu-1-0-0.plan`. Use generated examples/configuration to keep these aligned.
Each blueprint's opaque driver configuration must also declare an exact
`required_driver_version`; Kit's effect-free validation checks it against the
selected implementation, including when no Instance exists yet. This prevents an
operator remapping an existing ID to a different implementation unnoticed. The
exact config field is a Kit contract, not a new core manifest field.

Keep each blueprint version as ordinary source under
`blueprints/<name>/versions/<version>/`. Its required delivery workflow builds a
deterministic OCI artifact and pushes it to that blueprint's OCI repository.
Packaging selects existing versioned source and verifies its release descriptor;
`VERSION` does not merely relabel the current source. Inframill fetches the
published reference and Instances use the
resulting digest-pinned reference. `--blueprint-dir` remains a development aid.
There is no blueprint embedding or export command in Inframill Kit, and the runtime
container does not carry baked-in blueprints.

Confirmed release workflow: manually invoked build/package and push Task targets,
targeting GHCR by default. No GitHub Actions build or publication workflows are
part of this implementation. Example release interfaces (versions are illustrative;
authenticate to GHCR locally before pushing):

```text
task blueprint:package BLUEPRINT=pv-cloud VERSION=1.0.0
task blueprint:push BLUEPRINT=pv-cloud VERSION=1.0.0
task bundle VERSION=1.0.0
task bundle:push VERSION=1.0.0
```

Keep a local registry service with a pinned image and persistent storage for
development and integration tests. Example local interfaces:

```text
task registry:up
task blueprint:package BLUEPRINT=pv-cloud VERSION=0.0.0-dev
task blueprint:push BLUEPRINT=pv-cloud VERSION=0.0.0-dev REGISTRY=localhost:5000
```

The package target produces an OCI image layout. The push target preserves its
manifest and reports the published digest. Blueprint, driver, Kit and runtime
releases have independent versions. Publishing a blueprint does not rebuild Kit
or the runtime; releasing a driver does not republish blueprints. Rebuild a bundle
only to change its selected components. Registry addresses are
configurable. Document host and container connectivity explicitly: localhost
inside the runtime container does not address the registry container. Configure
`INFRAMILL_HTTP_REGISTRIES` on the Inframill process for the exact development
registry authority when using HTTP; local pulls must allow anonymous access.
Use HTTPS for GHCR; it must not appear in the HTTP registry allowlist.

### GHCR publication and access

Publish bundle releases as `ghcr.io/<namespace>/runtime:<version>` with both Linux
architectures under one image index. Publish each portable blueprint artifact as
`ghcr.io/<namespace>/blueprints/<name>:<version>`. Preserve the blueprint's custom
artifact/layer media types and empty config; verify GHCR round trips with actual
Inframill rather than assuming a successful Docker image push proves compatibility.
Report full digest references for both products. Serialize publication for each
repository/version and reject conflicting release contents; do not assume GHCR
enforces the project's tag immutability policy.

Support authenticated pushes from manually invoked local Task commands. Use an
existing registry credential store, with PAT (classic) `write:packages`
authentication documented via password-stdin. Both blueprint and bundle push
tooling must support that credential store. Connect packages to `inframill/kit`;
set source labels on runtime images and link blueprint packages through GHCR
settings. Inframill reconstructs blueprint manifests during integrity checks, so
blueprint envelopes must omit extra OCI annotations. Separate GitHub source repositories
named `inframill/runtime` or `inframill/blueprints` are not required. Keep
credentials out of artifacts and logs. See [GitHub's Container Registry documentation](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry).

Package visibility is a separate release setting: GHCR initially creates private
packages, while public packages permit anonymous pulls. The inspected Inframill
consumer only supports anonymous blueprint reads, so its current GHCR delivery
path requires public blueprint packages. Private blueprint delivery requires
authenticated acquisition support in Inframill before it can be accepted; a local
Docker login does not supply credentials to that consumer. Runtime images can be
public or private, using the container client's authentication for private pulls.
Choose visibility explicitly during release setup. See [GHCR access and visibility](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry).

Manual release commands use explicit component release descriptors to keep
blueprint and bundle publication independent. Build targets write versioned local
OCI layouts containing all required blobs, manifests and the runtime's combined
platform index. They do not publish to GHCR. Push targets require those existing
outputs, verify their descriptors/checksums and upload the exact built artifacts;
they do not rebuild or silently select newer source. A missing or mismatched build
output fails with the required build command.

Document local multi-platform builder setup for ARM64 and AMD64, including using
native builders or emulation for the build. Preserve the native execution checks
required for release acceptance; manually collect results from both architectures
before pushing the combined runtime index. Routine checks use disposable local
registries. Provide explicit manual verification targets for GHCR pulls and smoke
checks after publication; routine build/check commands do not publish remotely.

Match the consumer contract inspected in Inframill's `internal/blueprint/package.go`:

- OCI image manifest, schema version 2, with artifact type
  `application/vnd.inframill.blueprint.v1`.
- Config blob exactly `{}`, media type `application/vnd.oci.empty.v1+json`.
- Exactly one gzip tar layer, media type
  `application/vnd.inframill.blueprint.layer.v1.tar+gzip`.
- Archive root contains `manifest.yaml`, `schema.json`, and `template/`, including
  explicit parent directory entries and the provider lockfile. Package source
  templates, not rendered Instance values or runtime data.
- Deterministic ordering, timestamps and modes; SHA-256 descriptors; no links,
  special files, unsafe paths, or unexpected files. Enforce the consumer's size
  and entry limits before publication.

Authoring belongs in this repository's build tooling. Inframill's existing
packaging helper is internal and cannot be imported by Inframill Kit; implement the
documented format here and test compatibility through a real registry round trip.

Use Inframill's public `pkg/driver` wire types at a pinned revision, without
importing its internal debug driver or requiring a sibling checkout in releases.
Keep the tool drivers reusable: manifest configuration selects files, secret
references, output names and inventory inputs; Hetzner resource logic stays in
the blueprint.

## Blueprint and driver compatibility

Each blueprint release descriptor declares exact required driver implementations,
configured IDs, supported Inframill protocol/revision range, artifact schemas,
execution platforms and deployment target architectures.
Validate that descriptor against the packaged manifest so release metadata cannot
disagree with runtime requirements. Driver release descriptors declare tool pins,
supported platforms, configuration schema versions and artifact schemas they read
and write. The bundle lock resolves the selected driver versions to exact Kit and
tool builds; it may support many blueprint releases without containing their source.

Check compatibility during packaging, bundle assembly and driver validation. Fail
before tool execution for missing implementations, mismatched configuration or
unsupported artifact schemas. Do not assume that matching major versions or a
newer tool implies compatibility. Maintain a tested matrix of supported blueprint
versions, driver combinations and platforms; test declared combinations rather
than promising every possible cross-product.

Adding a blueprint should require its source, release metadata and tests, without
changes to driver execution or packaging logic. Adding a driver or driver version
requires an implementation and registration plus its dependency/compatibility
metadata, without modifying unrelated drivers or existing blueprint releases.
Use pv-cloud as the first production blueprint and a small local-resource
blueprint as a second independently versioned example and integration fixture.

## ARM64 and AMD64 support

Treat ARM as 64-bit ARM (`arm64`/AArch64) and AMD as x86-64 (`amd64`, including
Intel x86-64). Required first-release execution platforms are `linux/arm64` and
`linux/amd64`; 32-bit ARM and x86 are outside this scope.

Publish one architecture-independent source package per blueprint name/version,
with the same tag and digest usable on both execution platforms. Include templates,
playbooks and provider lock checksums for both platforms; keep host executables and
Python environments in the platform-specific runtime bundle. Packaging identical
source on ARM64 and AMD64 must produce identical blueprint digests. The inspected
Inframill registry consumer accepts a single OCI image manifest, so retain that
artifact contract rather than introducing a platform-selecting blueprint index.

Distinguish the machine running Inframill/Kit from the machines being provisioned.
An AMD64 runtime must be able to provision ARM64 targets and vice versa. Blueprint
inputs must explicitly select target architecture wherever it affects resource
selection or configuration; never infer it from the packaging or execution host.
For pv-cloud, expose architecture per node and validate the selected server type,
OS image and architecture combination. Use the same package for ARM64, AMD64 and
mixed-architecture node sets. Preserve architecture in the normalized node artifact
so Ansible can assert remote facts match the intended target before role changes.

Blueprint release metadata must declare both execution platforms and both target
architectures. Validate available static constraints without infrastructure access;
check live provider availability during planning, with clear failures for unsupported
combinations. Roles, cloud-init, package repositories, downloaded binaries and any
container images must support the selected target architecture. Record per-architecture
checksums for downloads; explicitly map tool-specific names such as `aarch64` and
`x86_64` to the canonical names. Missing architecture support is a release failure,
not grounds to silently substitute an image, server type or executable.

Build runtime images for both platforms, with per-platform tool/provider binaries,
Python dependencies and base-image digests in the bundle lock. Publish the runtime
under one versioned multi-platform image index and record the index and child image
digests. Architecture support does not introduce separate blueprint or driver
SemVer streams. Removing a supported architecture is a breaking contract change.

## Shared driver implementation

- Strict, bounded JSON decoding; exactly one JSON success response on stdout.
- Effect-free `info`, independent of tools and credentials. Phase-aware
  `validate` checks only available context without executing tools, writing files,
  contacting infrastructure, or resolving secrets.
- Check the required implementation version in both `validate` and `run` before
  any effects. Keep configuration parsing and command dispatch scoped to that
  implementation; reject unknown or unsupported versions explicitly.
- Execute tools using argument arrays, bounded parsing, noninteractive options,
  controlled environments, and the inherited process group for cancellation.
  Tool output goes to driver stderr, preserving Inframill's redaction boundary.
- Resolve only required `INFRAMILL_SECRET_*` references during `run` and map them
  to child environment variables. Do not forward unrelated secrets to tools.
- Explicit executable selection with deterministic bundle paths and a documented
  development override through driver prefix arguments. Do not depend on custom
  environment variables surviving Inframill's environment allowlist. Resolve tools
  from the selected driver's dependency lock; verify the actual tool version at
  `run` time, including overrides, without making `info` or `validate` execute tools.
- Declare each command's safety, effects and verification. Planning may contact
  infrastructure and acquire locks; check mode is not a general safety sandbox.
- Atomic, private, bounded artifact writes, binding checks, and refusal of links,
  unsafe names, malformed files and mismatched artifacts.

## OpenTofu driver

Initial command set: `init`, `validate`, `plan`, `apply`, `output`, `destroy`.
`<configured-opentofu-id>.validate` executes the tool's validation command; it is separate from
the effect-free driver protocol `validate` invocation.

`init` prepares the workspace and configured backend without interactive state
migration or automatic provider upgrades. `validate` requires prepared providers.
`plan` handles detailed exit codes correctly and returns a structured summary.
Proposal: save a private plan identified by its producing CommandID; `apply`
requires that plan identifier and verifies its Instance binding, configuration,
blueprint digest, driver implementation version, tool/provider versions and file
integrity. Reject reuse across implementation upgrades; require a fresh plan.
OpenTofu remains responsible for its
own stale-state checks. Destruction uses an explicit destroy plan and a separate
destructive command.

Implementation decision: save bounded private plan files (0600), consume them after
a successful apply, and reject changes in Instance, blueprint, source, configuration
or driver/tool version. The bundled blueprints use metadata and public SSH keys as
resource inputs. Confidential resource inputs require configured OpenTofu encryption
through a `TF_ENCRYPTION` secret reference; see README recovery guidance.

Saved plans can contain sensitive values. Credential references must resolve to
provider/backend environment variables, not Terraform input variables or backend
arguments. Do not publish raw plan/state JSON in results. The initial supported inputs and encryption boundary are documented in README;
private storage remains required even for metadata-only plans.

After a successful apply, validate the configured node output and atomically
publish a normalized node artifact. `output` can regenerate this artifact from
state after an interrupted publication. Apply failure or cancellation must leave
any previous node artifact unusable until state has been reconciled. Destroy
invalidates the node artifact and verifies the resulting state has no managed
resources. Verification describes its actual scope; state checks do not prove
the absence of every possible orphan in the provider account.

## OpenTofu-to-Ansible artifact

Define a versioned `nodes` JSON contract containing Instance binding, blueprint
digest, producer driver name/version, producer CommandID, and node name, roles,
architecture, public IPv4, private IPv4 and optional FQDN. Keep artifact schema versions separate
from driver release versions. Readers explicitly declare supported schema versions
and reject unsupported ones; test every declared producer/consumer combination.
Namespace artifacts by configured producer ID and schema version, and configure
the consumer's source explicitly so coexisting driver versions cannot overwrite
or accidentally consume one another's output.
The artifact contains an allowlisted projection of outputs, never complete state.

The blueprint selects the OpenTofu output (`inframill_nodes` initially), artifact
filename and Ansible address mode. Ansible checks binding, validity status, role
names, selected addresses and host uniqueness. A missing private address or FQDN
is an error when that address mode is selected; there is no silent fallback.
The current first-attached-network private address convention should be made
explicit. Failed or cancelled infrastructure changes invalidate prior inventory;
out-of-band changes require an explicit output refresh.

Inframill supplies shared artifacts but does not interpret this node contract or
automatically chain the commands.

## Ansible Core driver

Initial command set: `inventory`, `syntax-check`, `check`, `apply`.

Generate deterministic inventory from the node artifact and configured SSH user;
map roles to groups and restrict execution to declared playbooks. Support typed,
bounded arguments such as playbook, tags and host limits instead of arbitrary
shell commands or unrestricted extra CLI arguments.

Use Ansible Core built-ins only. Capture structured task/recap data through a
small bundled callback compatible with each selected driver's pinned Core version. Failure,
unreachable hosts, an unexpectedly empty selection and missing recap evidence
must not report success. Add explicit service assertions to pv-cloud so success
proves the intended services are enabled and running, beyond process exit status.
Check mode reports a prediction, not proof that changes were applied.

Use noninteractive SSH-agent authentication initially; do not write private-key
secrets to disk. Prefer explicit known-host trust, with a documented opt-in
first-use policy if needed. Wait for SSH and cloud-init with bounded timeouts
before package changes to avoid first-boot races.

## pv-cloud migration

- Create the first release under `blueprints/pv-cloud/versions/<version>/` with
  its release descriptor, exact driver requirements and compatibility tests.
- Replace manifest `systems` with `drivers`; rename `template/terraform` and
  `template/ansible` to match the selected configured driver IDs, for example
  `template/opentofu-1-0-0` and `template/ansible-1-0-0`; define driver configuration semantics in Inframill Kit instead of
  assuming the old `instance.*` path resolver exists in Inframill.
- Retain Hetzner servers, selective private networks, firewall, cloud-init,
  public/private address outputs, and common/web/data roles. The existing roles
  install Docker, Nginx and PostgreSQL using only built-in Ansible modules.
- Use `HCLOUD_TOKEN` rather than `TF_VAR_hcloud_token`, and environment-only S3
  credentials. Separate the cloud credential reference from resource naming.
- Keep an existing S3-compatible state bucket as a prerequisite. Define an
  explicit state identity and collision policy; for fresh deployments prefer a
  key derived from the Inframill InstanceID at driver runtime. Do not adopt or
  migrate legacy state keys.
- Check the pinned OpenTofu S3 options and backend locking against the target
  object store. Inframill's workspace lock does not coordinate other state clients.
- Regenerate and commit the provider lock using OpenTofu for both `linux/arm64`
  and `linux/amd64` execution platforms;
  validate the current provider pin and mock-test syntax before retaining them.
- Retain the schema's uniqueness, private IPv4 CIDR, disjointness and reference
  checks. Tighten unsafe string interpolation, duplicate roles and address-mode
  handling. Do not assume JSON Schema `default` inserts values.
- Expose deliberate image, SSH source CIDR and network-zone settings. The old
  source hardcodes Ubuntu 26.04, eu-central, globally reachable SSH and disabled
  host-key checking; choose and test defaults explicitly.
- Add per-node `architecture: arm64|amd64`, compatible server-type/image
  validation and examples for both architectures and a mixed node set. Check
  Docker, Nginx and PostgreSQL roles on both target architectures.
- Preserve the selective-network test cases, including public-only nodes.

## Taskfile workflows, bundle script and licensing

Confirmed build interface: use a root `Taskfile.yml`, with no Makefile or Make
targets. Task targets are the documented entry points; scripts are implementation
helpers where useful, including the requested bundle script.

Proposed target catalog:

| Target | Purpose |
| --- | --- |
| `task build KIT_VERSION=...` | Build the Go executable with the driver versions selected by its release descriptor |
| `task driver:check DRIVER=... DRIVER_VERSION=...` | Check a selected driver implementation and its declared tool/artifact compatibility |
| `task check-fast` | Compile and run focused protocol/driver/package tests across maintained versions |
| `task check-full` | Run fast checks and the declared tool, registry and container compatibility matrix |
| `task bundle VERSION=...` | Build both Linux platform images and the runtime image index from that bundle version's lock |
| `task bundle:push VERSION=... REGISTRY=ghcr.io NAMESPACE=inframill` | Push the built platform images and combined runtime index; report digests |
| `task bundle:verify REFERENCE=...` | Verify a published index by digest and smoke-test the selected native platform; repeat on both architectures |
| `task registry:up` | Start the local OCI registry with persistent storage |
| `task registry:down` | Stop the local registry while preserving its stored artifacts |
| `task blueprint:check BLUEPRINT=... VERSION=...` | Validate a blueprint version and required drivers across ARM64/AMD64 execution and target cases |
| `task blueprint:package BLUEPRINT=... VERSION=...` | Build one portable OCI layout for the selected blueprint version, covering both architectures |
| `task blueprint:push BLUEPRINT=... VERSION=... REGISTRY=ghcr.io NAMESPACE=inframill` | Push the built artifact, enforce release immutability and report its digest |
| `task blueprint:verify REFERENCE=...` | Fetch a published blueprint by digest through Inframill and run harmless validation |
| `task blueprints:package RELEASE_SET=...` | Package an explicit list of blueprint name/version pairs, including multiple versions of one blueprint |
| `task blueprints:push RELEASE_SET=... REGISTRY=ghcr.io NAMESPACE=inframill` | Push that set with per-artifact digest/failure reporting and safe reruns |

Build/package targets stay separate from push and preserve outputs for a later
manual push. Push targets do not depend on build targets. Tests use disposable registry data
and do not alter the user's local registry volume. No linting is part of test gates.
All selectors must reject unknown names/versions and unsafe paths. Release-set
targets use committed explicit selections, not an implicit scan for newest versions.
Bulk publication is not atomic; report partial success and preserve already
published immutable artifacts when retrying.

Have `task bundle` invoke `scripts/bundle.sh`, driven by a committed per-bundle
dependency lock. Require prebuilt Inframill binaries from the sibling `dist/`
directory; never invoke its build/release tasks. Pin each binary path, SHA-256
and ELF architecture, Inframill revision/version, Inframill Kit version/build digest,
every selected driver name/version and its OpenTofu or Ansible Core, Python and
Python dependencies, plus any preloaded provider checksums and container base
digest. Blueprint provider locks belong to each blueprint release; preloading must
cover the bundle's declared supported selections. Verify downloaded bytes; never
silently select `latest`. Record component versions and checksums in the output.

Bundle contents: Inframill, Inframill Kit, selected versions of OpenTofu, Ansible Core and Python/dependencies,
OpenSSH client, CA certificates, example configuration, license
notices and corresponding-source materials/access required for redistribution.
Generate engine configuration with a distinct ID for every included driver
name/version, and keep tool paths and Python environments isolated by version.
Deduplicate only identical verified dependencies. Validate that Kit contains all
selected implementations and that each resolves its own toolchain.
Preload the pinned providers if the bundle must initialize without registry
downloads. Provisioning still requires cloud, object-store, and guest-package access.

Confirmed first-release format: a Linux container. Required architectures are
amd64/arm64, with persistent Inframill storage and documented SSH-agent/known-host
mounts. Native Linux/macOS archives are deferred beyond the first release; Python,
compiled wheels, shared libraries and relocatable launchers would require
per-platform builds and testing, and copying a venv is insufficient.

OpenTofu is MPL-2.0; Ansible Core is GPL-3.0-or-later. These are open-source
licenses with redistribution conditions, not an all-permissive dependency set.
Keep the tools as separate executables and preserve their licenses; shipping
Ansible Core does not make the entire bundle MIT. If “safe license” means only
permissive licenses, the requested Ansible Core component conflicts with that goal.
Review licenses of the exact pinned dependencies as part of release packaging.

Sources checked:

- https://raw.githubusercontent.com/opentofu/opentofu/main/LICENSE
- https://raw.githubusercontent.com/ansible/ansible/devel/pyproject.toml
- https://raw.githubusercontent.com/ansible/ansible/devel/COPYING
- https://docs.ansible.com/projects/ansible-core/devel/installation_guide/intro_installation.html
- https://opentofu.org/docs/cli/commands/plan/
- https://opentofu.org/docs/language/settings/backends/s3/
- https://raw.githubusercontent.com/hetznercloud/terraform-provider-hcloud/main/hcloud/provider.go

## Implementation sequence and acceptance

1. Agree on this scope and write testable driver, artifact, blueprint and bundle
   requirements. Resolve license expectations and
   saved-plan behavior. Linux container delivery, fresh deployments, and separate
   OCI blueprint and bundle publication primarily to GHCR, local development/test
   registry support, manual Taskfile build/push entry points, multiple
   blueprints, independently maintained versions of each blueprint and driver,
   and ARM64/AMD64 blueprint support are confirmed. Define release descriptors, immutable version identities,
   compatibility rules and the initial supported-version matrix.
2. Scaffold the Go executable, root Taskfile, versioned source/release catalogs,
   implementation registry, shared protocol/runtime and fake-tool fixtures.
   Prove discovery, exact version selection and validation through real Inframill,
   including two coexisting versions of each of two fixture driver names.
3. Implement OpenTofu commands, plan binding, credentials and artifact publication.
   Test detailed exit codes, stale plans, partial failures and cancellation.
4. Implement inventory, Ansible commands and structured verification. Test wrong
   binding, missing addresses, host selection, unreachable hosts and task failures.
5. Migrate pv-cloud and add the independently versioned local-resource blueprint.
   Render each declared blueprint/driver combination through actual Inframill;
   run OpenTofu validation/mock tests and Ansible syntax checks using the selected
   pinned tools. Exercise at least two versions of a blueprint with different
   driver requirements, without changing the earlier release. Render ARM64,
   AMD64 and mixed target fixtures; reject architecture/server-type/image
   mismatches and verify architecture-specific dependency selection.
6. Implement deterministic blueprint packaging, local registry setup, authenticated
   GHCR publishing and manual Task-based release commands. Resolve package visibility
   and repository access during release setup.
   Verify identical source produces identical digests, push/fetch by tag and
   digest through real Inframill for multiple blueprint names and versions, and
   execute harmless validation commands using the fetched packages. Verify older
   digests remain retrievable after publishing newer releases, conflicting release
   tags reject, and bulk publication can safely resume after partial failure.
   Verify malformed packages reject and secrets or runtime files are excluded.
   Fresh-cache fetches must exercise the registry. Build on ARM64 and AMD64,
   compare blueprint digests, and fetch/validate the same digest on both platforms.
   During manual release verification, verify the published GHCR blueprint through anonymous,
   fresh-cache Inframill pulls by tag and digest on both platforms. Exercise GHCR's
   authentication challenge and confirm the custom artifact format is preserved.
7. Implement bundle creation and smoke-test its real execution paths. Run a
   disposable local-resource OpenTofu-to-Ansible workflow through Inframill without
   cloud credentials, with the fixture blueprint acquired from the local registry.
   Exercise simultaneous driver versions and isolated toolchains, missing-version
   and compatibility failures before effects, cross-version saved-plan rejection,
   and compatible/incompatible node artifact readers. Prove a newer bundle can
   execute an older pinned Instance when its requirements are retained and rejects
   it clearly when they are absent; no implicit upgrades.
   Run the workflow on both native execution architectures, including provider
   initialization with the committed lock. Exercise Ansible roles against
   disposable ARM64 and AMD64 guests, including execution/target cross-architecture
   cases. Verify runtime image selection and each platform's dependency checksums.
   Verify the published GHCR runtime index contains both required platforms, then
   pull and smoke-test each platform image by digest using the chosen access mode.
   Cross-compilation, emulation or syntax checks alone do not satisfy native
   execution acceptance; report any unavailable coverage explicitly.
8. Document manual registry login, Taskfile build/check/bundle and single/bulk
   blueprint package/push/verify commands, local multi-platform builder setup,
   adding blueprints/drivers, releasing and backporting maintained versions,
   deprecation, compatibility matrices, exact selection and explicit upgrades,
   Inframill blueprint fetch, secrets,
   init/plan/apply/configure/destroy, recovery and
   history. Explicitly distinguish `<configured-opentofu-id>.destroy` from `builtin.delete`, which
   only removes local Inframill data. Do not present automatic rollback or retries.

No real Hetzner create/apply/destroy commands are part of development verification.
A live pv-cloud acceptance deployment needs a separately specified disposable
environment and explicit authorization. Report any unverified provider/backend
behavior rather than treating fixtures as proof of production interoperability.

## Implementation and verification record — 2026-09-14

Implemented in this checkout:

- The Go Kit executable implements the public Inframill driver protocol, with
  explicitly selected `opentofu` and `ansible` versions `0.1.0` and `0.2.0`.
  Version/configuration mismatches fail before tool execution. The newer versions
  add refresh-only plans and Ansible tag selection, respectively.
- OpenTofu uses readonly provider locks, Instance-scoped backend state keys,
  private bound saved plans, pre-effect artifact invalidation and normalized node
  publication. Ansible derives inventory from those artifacts and requires a
  complete successful structured recap. Credentials resolve at execution time.
- Versioned sources and sealed release descriptors exist for `pv-cloud@0.1.0`
  and `local-resource@0.1.0`, `local-resource@0.2.0`. pv-cloud renders ARM64,
  AMD64 and mixed node sets. Packaging and publication select arbitrary declared
  blueprint name/version pairs, independently of driver version numbers.
- Manual Task targets build, check, package, push and verify. Blueprint packages
  are deterministic architecture-independent OCI source artifacts; the separate
  runtime index contains Linux ARM64 and AMD64 images. Runtime locks pin tools,
  Python distributions, base images and source downloads. Builds produce local
  OCI layouts plus digest/checksum records; push publishes those exact bytes.
- GHCR defaults to the `inframill` namespace. Docker credential helpers support
  manual authenticated pushes. No GitHub Actions or automatic publication was
  added. A disposable local registry supports integration verification.
- Kit uses the sibling public protocol module; the bundle copies that project's
  prebuilt Linux releases. The local `third_party` snapshot is removed. Runtime
  distribution includes component notices and OpenTofu/Python source archives.
  The original `old-blueprints/` migration input is unchanged.

Verification completed during implementation:

- Go unit and public Inframill Manager integration tests pass, including driver
  discovery, exact-version rejection, process-group cancellation, saved-plan
  binding/failure invalidation, artifact schema/address checks, successful recap
  requirements, deterministic packaging, corruption and unsafe-source rejection,
  and offline JSON Schema validation.
- Both local-resource versions completed init, validate, plan, apply, inventory,
  Ansible syntax/check/apply, and destroy through real Inframill and pinned tools.
- pv-cloud ARM64, AMD64 and mixed fixtures rendered and passed Ansible syntax
  checks. Its provider configuration validates; all four OpenTofu provider mock
  tests pass, including architecture mismatch rejection.
- A disposable registry accepted all three packages and idempotent repeated
  publication. Real Inframill fetched and executed package tags and a digest from
  an independent fresh cache; the older version remained usable after the newer
  version was published. Runtime index publication to that registry succeeded.
- Both runtime image variants executed the two local-resource versions through
  Inframill using registry-fetched packages, then installed their platform's
  locked hcloud provider and passed all four pv-cloud mock tests. Linux ARM64 was
  native on Docker Desktop; Linux AMD64 used emulation.

Release acceptance still requires external environments:

- Native Linux AMD64 execution and independent native architecture builds/digest
  comparison. Emulated AMD64 results do not satisfy the native acceptance gate.
- Actual ARM64 and AMD64 SSH guests, including cross-architecture execution and
  Docker/Nginx/PostgreSQL service verification. Local fixtures and syntax/mock
  tests do not prove guest provisioning behavior.
- GHCR publication, visibility/linking setup and fresh-cache anonymous blueprint
  pulls plus runtime digest pulls on both native platforms. Nothing was pushed
  to GHCR in this implementation. Private blueprint consumption remains an
  upstream limitation; push credentials do not authenticate Inframill pulls.
- A separately authorized disposable Hetzner/S3 deployment for provider, backend
  conditional-write locking, SSH and service interoperability. No real cloud
  infrastructure was created, modified or destroyed by these checks.

Final local acceptance run: `task check-full` passed against the final bundle,
including conflicting-tag rejection and both platform execution paths. The log is
`.cache/check-full.log`. `git diff --check`, Go formatting and Python syntax checks
also passed.

Initial artifact digests (runtime superseded by the binary-input update below):

| Artifact | Digest |
| --- | --- |
| pv-cloud 0.1.0 | `sha256:3684e1b4bcaf18305f2a1a6f0ee3a1a72819dffb252334bd0481885ea3c99af4` |
| local-resource 0.1.0 | `sha256:e50ac1b2bb62abfc156b8a064f536ce06a23ffe9072f9b3b08700e18fdd621f4` |
| local-resource 0.2.0 | `sha256:66361bbbc6a1e3d58c25130105658901a9722914a56eda510f3c1e995a9ed6f5` |
| Runtime 0.1.0 index | `sha256:488a19d980589f2cdcc641f2b4204d0806286aa900e22ccb0dc54ea5931765a2` |

The OCI layouts and build records are under `dist/`. These are unpublished local
artifacts. The initial runtime used the source snapshot; rebuild it after the binary-input update.

## Prebuilt Inframill inputs — implemented 2026-09-14

`task bundle` now copies existing Inframill Linux AMD64 and ARM64 binaries from
`../inframill/dist/0.0.0-dev/`. `INFRAMILL_DIST_DIR` overrides the input root;
`inframill.binaries` in the bundle lock pins each relative filename and SHA-256.
The build rejects missing files, changed contents, non-Linux executables and
architecture mismatches. Each image verifies the copied executable's version.
Kit never invokes an Inframill build or release task.

The local `third_party/` directory is removed. Kit's Go module uses the sibling
`../inframill` public driver protocol. Bundle source material contains Kit and
available dependency notices, without embedding the sibling source tree.
`task inframill:copy` selects the native binary from the same versioned release
for local checks; it avoids the older unversioned `dist/inframill` binary.

Verification: all six binary-input tests and `task check-full` passed. Both
runtime variants completed the actual driver workflows and four provider mock
tests (native ARM64, emulated AMD64). Copied image executables match the sibling
release checksums byte-for-byte, and the images contain no `third_party` tree.
`git diff --check` passed. Logs are `.cache/binary-copy-bundle.log` and
`.cache/binary-copy-full.log`. No GHCR publication was performed.

The current local runtime index is
`sha256:5e860b923f8f07c4f2a712d1ffc395a15f8c7bb9dce7c42df95f1601b947814e`.
Its layout/build record are under `dist/bundles/0.1.0/`; prior unpublished output
was moved to `.cache/` before rebuilding. The external release acceptance gates
listed above remain unchanged.
