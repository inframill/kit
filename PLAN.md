# Inframill Kit drivers, pv-cloud blueprint, and runtime bundle

Status: Draft for discussion. No implementation has started.

## Objective and boundaries

Build one Go executable, `inframill-kit`, implementing two Inframill protocol-1 drivers,
`opentofu` and `ansible`. Publish `pv-cloud` separately as an Inframill-compatible
OCI blueprint artifact to a local OCI registry; do not embed it in the executable.
Build a separate distribution bundle containing Inframill, Inframill Kit, a pinned
OpenTofu release, and pinned Ansible Core with its runtime dependencies.

Inframill remains the control plane. Inframill Kit owns tool execution, tool-specific
validation and verification, and the artifact contract between the drivers.
The current Inframill README and public protocol support this without core changes.
Reference inspected: sibling checkout at commit `acb50af`.

Confirmed project name: Inframill Kit. Repository: `inframill/kit`; Go module:
`github.com/inframill/kit`; executable: `inframill-kit`. The runtime image repository
is `inframill/runtime`, and the blueprint OCI repository is
`inframill/blueprints/pv-cloud`, relative to the configured registry host.

Preserve `old-blueprints/` as migration input. It is currently untracked.
Confirmed first-release scope: fresh deployments only. Adoption of infrastructure
and remote state created by the older software is deferred; no legacy state
migration, resource import, or compatibility layer is part of this release.

## Executable and blueprint delivery

Proposed interfaces:

```text
inframill-kit driver opentofu info|validate|run COMMAND
inframill-kit driver ansible info|validate|run COMMAND
inframill-kit version
```

Both configured drivers point to the same executable with different prefix args:

```yaml
engine:
  dataDir: .inframill
  drivers:
    opentofu:
      path: ./bin/inframill-kit
      args: [driver, opentofu]
    ansible:
      path: ./bin/inframill-kit
      args: [driver, ansible]
handlers:
  - type: connect-engine
    settings:
      path: /inframill.v1.EngineService/
```

Keep the blueprint as ordinary source under `blueprints/pv-cloud/`. Its required
delivery workflow builds a deterministic OCI artifact and pushes it to a local
OCI registry. Inframill fetches the published reference and Instances use the
resulting digest-pinned reference. `--blueprint-dir` remains a development aid.
There is no blueprint embedding or export command in Inframill Kit, and the runtime
container does not carry a baked-in copy of pv-cloud.

Provide separate package and push Task targets, plus a local registry service
with a pinned image and persistent storage. Example authoring interfaces:

```text
task registry:up
task blueprint:package BLUEPRINT=pv-cloud VERSION=0.0.0-dev
task blueprint:push BLUEPRINT=pv-cloud VERSION=0.0.0-dev REGISTRY=localhost:5000
```

The package target produces an OCI image layout. The push target preserves its
manifest and reports the published digest. Blueprint and runtime releases have
independent versions; neither rebuilds the other. Registry addresses are
configurable. Document host and container connectivity explicitly: localhost
inside the runtime container does not address the registry container. Configure
`INFRAMILL_HTTP_REGISTRIES` on the Inframill process for the exact development
registry authority when using HTTP; local pulls must allow anonymous access.

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

## Shared driver implementation

- Strict, bounded JSON decoding; exactly one JSON success response on stdout.
- Effect-free `info`, independent of tools and credentials. Phase-aware
  `validate` checks only available context without executing tools, writing files,
  contacting infrastructure, or resolving secrets.
- Execute tools using argument arrays, bounded parsing, noninteractive options,
  controlled environments, and the inherited process group for cancellation.
  Tool output goes to driver stderr, preserving Inframill's redaction boundary.
- Resolve only required `INFRAMILL_SECRET_*` references during `run` and map them
  to child environment variables. Do not forward unrelated secrets to tools.
- Explicit executable selection with deterministic bundle paths and a documented
  development override through driver prefix arguments. Do not depend on custom
  environment variables surviving Inframill's environment allowlist.
- Declare each command's safety, effects and verification. Planning may contact
  infrastructure and acquire locks; check mode is not a general safety sandbox.
- Atomic, private, bounded artifact writes, binding checks, and refusal of links,
  unsafe names, malformed files and mismatched artifacts.

## OpenTofu driver

Initial command set: `init`, `validate`, `plan`, `apply`, `output`, `destroy`.
`opentofu.validate` executes the tool's validation command; it is separate from
the effect-free driver protocol `validate` invocation.

`init` prepares the workspace and configured backend without interactive state
migration or automatic provider upgrades. `validate` requires prepared providers.
`plan` handles detailed exit codes correctly and returns a structured summary.
Proposal: save a private plan identified by its producing CommandID; `apply`
requires that plan identifier and verifies its Instance binding, configuration,
tool/provider versions and file integrity. OpenTofu remains responsible for its
own stale-state checks. Destruction uses an explicit destroy plan and a separate
destructive command.

Saved plans can contain sensitive values. Credential references must resolve to
provider/backend environment variables, not Terraform input variables or backend
arguments. Do not publish raw plan/state JSON in results. Before implementation,
define how other sensitive resource values are handled, including whether plan
encryption is needed for the supported blueprint scope.

After a successful apply, validate the configured node output and atomically
publish a normalized node artifact. `output` can regenerate this artifact from
state after an interrupted publication. Apply failure or cancellation must leave
any previous node artifact unusable until state has been reconciled. Destroy
invalidates the node artifact and verifies the resulting state has no managed
resources. Verification describes its actual scope; state checks do not prove
the absence of every possible orphan in the provider account.

## OpenTofu-to-Ansible artifact

Define a versioned `nodes` JSON contract containing Instance binding, producer
CommandID, and node name, roles, public IPv4, private IPv4 and optional FQDN.
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
small bundled callback compatible with the pinned Core version. Failure,
unreachable hosts, an unexpectedly empty selection and missing recap evidence
must not report success. Add explicit service assertions to pv-cloud so success
proves the intended services are enabled and running, beyond process exit status.
Check mode reports a prediction, not proof that changes were applied.

Use noninteractive SSH-agent authentication initially; do not write private-key
secrets to disk. Prefer explicit known-host trust, with a documented opt-in
first-use policy if needed. Wait for SSH and cloud-init with bounded timeouts
before package changes to avoid first-boot races.

## pv-cloud migration

- Replace manifest `systems` with `drivers`; rename `template/terraform` to
  `template/opentofu`; define driver configuration semantics in Inframill Kit instead of
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
- Regenerate and commit the provider lock using OpenTofu for supported platforms;
  validate the current provider pin and mock-test syntax before retaining them.
- Retain the schema's uniqueness, private IPv4 CIDR, disjointness and reference
  checks. Tighten unsafe string interpolation, duplicate roles and address-mode
  handling. Do not assume JSON Schema `default` inserts values.
- Expose deliberate image, SSH source CIDR and network-zone settings. The old
  source hardcodes Ubuntu 26.04, eu-central, globally reachable SSH and disabled
  host-key checking; choose and test defaults explicitly.
- Preserve the selective-network test cases, including public-only nodes.

## Taskfile workflows, bundle script and licensing

Confirmed build interface: use a root `Taskfile.yml`, with no Makefile or Make
targets. Task targets are the documented entry points; scripts are implementation
helpers where useful, including the requested bundle script.

Proposed target catalog:

| Target | Purpose |
| --- | --- |
| `task build` | Build the `inframill-kit` Go executable |
| `task check-fast` | Compile and run focused protocol/driver/package tests |
| `task check-full` | Run fast checks and tool, registry and container integration checks |
| `task bundle VERSION=...` | Build the Linux runtime container with pinned components |
| `task registry:up` | Start the local OCI registry with persistent storage |
| `task registry:down` | Stop the local registry while preserving its stored artifacts |
| `task blueprint:package BLUEPRINT=pv-cloud VERSION=...` | Build the blueprint OCI layout |
| `task blueprint:push BLUEPRINT=pv-cloud VERSION=... REGISTRY=...` | Push the built artifact and report its digest |

Build/package targets stay separate from push. Tests use disposable registry data
and do not alter the user's local registry volume. No linting is part of test gates.

Have `task bundle` invoke `scripts/bundle.sh`, driven by a committed dependency lock. Pin Inframill
revision/version, Inframill Kit version, OpenTofu, Ansible Core, Python, Python dependencies,
provider checksums and any container base digest. Verify downloaded bytes; never
silently select `latest`. Record component versions and checksums in the output.

Bundle contents: Inframill, Inframill Kit, OpenTofu, Ansible Core, Python/dependencies,
OpenSSH client, CA certificates, example configuration, license
notices and corresponding-source materials/access required for redistribution.
Preload the pinned Hetzner provider if the bundle must initialize without registry
downloads. Provisioning still requires cloud, object-store, and guest-package access.

Confirmed first-release format: a Linux container. Proposed architectures are
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
   requirements. Resolve container architectures, license expectations and
   saved-plan behavior. Linux container delivery, fresh deployments, and separate
   OCI blueprint publication to a local registry, and Taskfile entry points are confirmed.
2. Scaffold the Go executable, root Taskfile, shared protocol/runtime and fake-tool fixtures.
   Prove discovery and validation through real Inframill.
3. Implement OpenTofu commands, plan binding, credentials and artifact publication.
   Test detailed exit codes, stale plans, partial failures and cancellation.
4. Implement inventory, Ansible commands and structured verification. Test wrong
   binding, missing addresses, host selection, unreachable hosts and task failures.
5. Migrate pv-cloud and examples. Render through actual Inframill; run OpenTofu
   validation/mock tests and Ansible syntax checks using pinned tools.
6. Implement deterministic blueprint packaging, local registry setup and push.
   Verify identical source produces identical digests, push/fetch by tag and
   digest through real Inframill, and execute a harmless validation command using
   the fetched pv-cloud package. Verify malformed packages reject and secrets or
   runtime files are excluded. Fresh-cache fetches must exercise the registry.
7. Implement bundle creation and smoke-test its real execution paths. Run a
   disposable local-resource OpenTofu-to-Ansible workflow through Inframill without
   cloud credentials, with the fixture blueprint acquired from the local registry.
8. Document Taskfile build/check/bundle and blueprint package/push workflows,
   Inframill blueprint fetch, secrets,
   init/plan/apply/configure/destroy, recovery and
   history. Explicitly distinguish `opentofu.destroy` from `builtin.delete`, which
   only removes local Inframill data. Do not present automatic rollback or retries.

No real Hetzner create/apply/destroy commands are part of development verification.
A live pv-cloud acceptance deployment needs a separately specified disposable
environment and explicit authorization. Report any unverified provider/backend
behavior rather than treating fixtures as proof of production interoperability.
