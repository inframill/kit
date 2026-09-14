# Inframill Kit

Versioned OpenTofu and Ansible Core drivers, portable blueprints, and a Linux runtime
bundle for Inframill. Build and push manually with Task. Nothing publishes to GHCR
as part of a build or routine check.

| Component | Included versions | Distribution |
| --- | --- | --- |
| Kit | `0.1.0` | `bin/inframill-kit` |
| OpenTofu driver | `0.1.0`, `0.2.0` | Explicitly selected in Kit |
| Ansible driver | `0.1.0`, `0.2.0` | Explicitly selected in Kit |
| pv-cloud blueprint | `0.1.0` | `ghcr.io/inframill/blueprints/pv-cloud` |
| Local-resource blueprint | `0.1.0`, `0.2.0` | `ghcr.io/inframill/blueprints/local-resource` |
| Runtime bundle | `0.1.0` | `ghcr.io/inframill/runtime` |

Every blueprint version is one architecture-independent OCI source artifact. The
runtime is an OCI image index containing `linux/arm64` and `linux/amd64` images.
pv-cloud supports ARM64, AMD64 and mixed node sets, independently of the runtime
host architecture. Its initial supported guest image is Ubuntu 24.04.

Implementation and verification status is recorded in [PLAN.md](PLAN.md).

## Build and check locally

Prerequisites: Go 1.27, Task v3, Docker with Buildx and both platform builders (native
or emulated). Local integration tests also need Python 3.12–3.14. Build tooling
runs with `python3`; set `PYTHON` for the interpreter used by the Ansible test venv.

```sh
PYTHON=/path/to/python3.13 task setup
task build KIT_VERSION=0.1.0
task inframill:copy
task check-fast
```

`task setup` installs checksum-pinned OpenTofu 1.11.5 and Ansible Core 2.20.1 into
`.cache/`, using the committed Python dependency lock. Supported local setup hosts
are Linux ARM64/AMD64 and macOS ARM64. Override `OPENTOFU_PATH` and `ANSIBLE_PATH`
with absolute executable paths when using an existing installation; versions are
still checked against the selected driver's lock.

Kit imports the public driver protocol from the sibling `../inframill` Go module.
The local `third_party` snapshot has been removed. `task inframill:copy` copies
the host-platform binary from the same versioned sibling release to `bin/inframill`
for native local checks. On this macOS ARM64 host that input is
`../inframill/dist/0.0.0-dev/inframill_0.0.0-dev_darwin_arm64`.

The bundle copies these existing Linux release binaries from the sibling project:

```text
../inframill/dist/0.0.0-dev/inframill_0.0.0-dev_linux_amd64
../inframill/dist/0.0.0-dev/inframill_0.0.0-dev_linux_arm64
```

Their paths and SHA-256 checksums are pinned under `inframill.binaries` in
`releases/bundles/0.1.0.lock.yaml`. Both must already exist. The bundle validates
the checksum and ELF architecture, copies the bytes, and sets executable mode.
It never runs Inframill's build/release tasks or falls back to compiling Inframill.
Kit itself is compiled for both architectures from this repository.

Override the binary input directory with `task bundle INFRAMILL_DIST_DIR=/path/to/dist`.
Paths in the lock are relative to that directory. Select new paths/checksums in a
new bundle lock when changing the Inframill release; mismatches fail the build.
The sibling source checkout supplies the protocol dependency and dependency
notices, but its source tree is not embedded in the runtime.

```sh
task blueprint:check BLUEPRINT=local-resource VERSION=0.1.0
task blueprint:check BLUEPRINT=pv-cloud VERSION=0.1.0
task bundle VERSION=0.1.0
task check-full
```

Fast checks cover protocol validation, version selection, cancellation of child
processes, plan/artifact binding, unsafe paths, and OCI integrity. Full checks use
a disposable registry, render through real Inframill, run OpenTofu mock tests, and
execute the local-resource workflow with real tools. They push only to that
throwaway registry, then exercise the existing bundle on both Linux platforms.
Run `task bundle` first. Emulated AMD64 success is recorded separately from native
AMD64 release acceptance. Full checks never create Hetzner infrastructure.

## Manual package and push

Versions use independent `MAJOR.MINOR.PATCH` release identities. A blueprint version
pins exact driver implementations; the bundle lock selects its components. Driver
`0.2.0` adds OpenTofu `refresh_only` plans and Ansible `tags`; `0.1.0` rejects those
options. Driver versions are separate from upstream tool versions.

Authenticate locally using a GitHub PAT (classic) with `write:packages`; the push
tooling uses the Docker credential store, including credential helpers:

```sh
docker login ghcr.io -u YOUR_GITHUB_USERNAME
```

Enter the PAT at the password prompt. For automated credential input use
`--password-stdin`. Do not put tokens in Task arguments or release files.

```sh
task blueprint:package BLUEPRINT=pv-cloud VERSION=0.1.0
task blueprint:push BLUEPRINT=pv-cloud VERSION=0.1.0

task bundle VERSION=0.1.0
task bundle:push VERSION=0.1.0
```

The package target writes `dist/blueprints/<name>/<version>/oci/` and a `build.json`
record. Bundle writes the same layout beneath `dist/bundles/<version>/`. Push checks
every blob against its descriptor and the root against the build record, uploads
those existing bytes, and reports the digest reference. It does not rebuild.
Existing tags with different contents are rejected. Local publication locks
serialize writers on one workstation; coordinate releases across workstations
because GHCR does not provide this project's immutable-tag transaction.

A built version is immutable locally too. To change an unpublished development
build, explicitly move its output directory aside before rebuilding. Published
changes require a new version. Never replace an earlier release to apply a fix.

```sh
task blueprints:package RELEASE_SET=development
task blueprints:push RELEASE_SET=development
```

Release sets list exact name/version pairs and may include multiple versions of a
blueprint. Bulk pushes report each outcome; a partial failure can be rerun safely.
`REGISTRY=ghcr.io` and `NAMESPACE=inframill` are defaults and can be overridden.
Link blueprint packages through GHCR package settings; the canonical Inframill
blueprint envelope cannot carry additional OCI annotations. Runtime images carry
source labels. All packages can link to `github.com/inframill/kit`; no separate `runtime` or
`blueprints` GitHub source repositories are required.

GHCR initially creates private packages. Choose visibility in its package settings.
The pinned Inframill consumer can only fetch anonymous/public blueprint packages;
a Docker login authenticates the push tool, not Inframill. Private blueprint pulls
need a consumer enhancement. Runtime images support normal Docker authentication.
See [GHCR documentation](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry).

After publishing, verify using the printed digests:

```sh
task blueprint:verify BLUEPRINT=pv-cloud VERSION=0.1.0 REFERENCE=ghcr.io/inframill/blueprints/pv-cloud@sha256:...
task bundle:verify REFERENCE=ghcr.io/inframill/runtime@sha256:...
```

Run runtime verification on each native architecture. `bundle:verify` checks the
index and probes the selected platform's drivers. `check-full` covers the actual
local-resource execution path in both image variants.

## Local registry

```sh
task registry:up
task blueprint:push BLUEPRINT=pv-cloud VERSION=0.1.0 REGISTRY=localhost:5000 PLAIN_HTTP=true
task registry:down
```

The registry image is pinned and its named volume persists across `registry:down`.
Full tests use a separate, temporary registry without that volume. Set
`INFRAMILL_HTTP_REGISTRIES=localhost:5000` for local Inframill pulls. Inside a
container, use the registry's container/network address or a configured host gateway,
with that exact authority in the allowlist. GHCR always uses HTTPS.

## Configure and run drivers

```sh
OPENTOFU_PATH="$PWD/.cache/tools/opentofu/1.11.5/tofu" \
ANSIBLE_PATH="$PWD/.cache/ansible-2.20.1/bin/ansible-playbook" \
task config > .inframill.yaml

bin/inframill-kit drivers list
bin/inframill driver list --config .inframill.yaml -o json
```

The generated IDs include the exact version, such as `opentofu-0-1-0`. Blueprint
manifest keys and `template/<id>/` directories use those IDs. Each driver config
also declares `required_driver_version`; remapping an ID to a different version
fails validation. Missing versions never fall back to another implementation.

The runtime includes `/etc/inframill/config.json`, version-specific tool paths,
OpenSSH and CA certificates. Persist `/var/lib/inframill`. Supply the SSH agent
socket through `SSH_AUTH_SOCK` and mount a known-hosts file under the runtime user's
`~/.ssh/known_hosts`. SSH is noninteractive with strict host verification. A blueprint
can explicitly select `host_key_policy: accept-new` if first-use trust is intended.

```sh
docker run --rm -p 8080:8080 \
  -v inframill-data:/var/lib/inframill \
  ghcr.io/inframill/runtime:0.1.0
```

This starts Inframill; provisioning additionally requires credentials, agent/trust
mounts, object-store access, cloud API access and guest package repositories.

## pv-cloud operation

Copy `examples/pv-cloud-0.1.0.json` and replace the sample public key, bucket,
credential references, nodes and SSH source CIDRs. The example contains ARM64 and
AMD64 nodes; separate examples cover each architecture. JSON Schema validates
architectures, server-type families, roles and network references. Provider data
sources check actual image architecture and server-type availability during plan.
The first attached private network supplies the canonical private address.

Manifest `from_value` fields read scalar paths in Instance values; they are not
template expressions. `secret_from_value` reads a secret *reference*, resolved only
in `run` from `INFRAMILL_SECRET_<UPPERCASE_REFERENCE>`. The pv-cloud example uses
`INFRAMILL_SECRET_HETZNER`, `INFRAMILL_SECRET_S3_ACCESS` and
`INFRAMILL_SECRET_S3_SECRET`. Child tools receive only required credential variables.
Cloud/backend credentials never become Terraform input variables or CLI arguments.

Use a preexisting S3-compatible bucket. State keys are
`instances/<InstanceID>/terraform.tfstate`, generated by the driver. S3 lockfiles
are enabled; the actual object store must support the required conditional writes.
No legacy state adoption or automatic migration is implemented.

The command sequence is explicit:

1. `opentofu-0-1-0.init`, then `opentofu-0-1-0.validate`.
2. `opentofu-0-1-0.plan`; retain the returned `plan_id`.
3. `opentofu-0-1-0.apply` with `{"plan_id":"<producing CommandID>"}`.
4. `ansible-0-1-0.inventory`, then `ansible-0-1-0.syntax-check` with `{"playbook":"site"}`.
5. `ansible-0-1-0.check` for a prediction, then `ansible-0-1-0.apply`, with the same playbook argument.
6. To destroy, run `opentofu-0-1-0.plan` with `{"destroy":true}`, then
   `opentofu-0-1-0.destroy` with that new plan ID.

For local execution, use `bin/inframill exec <command> --config .inframill.yaml
--instance <instance.json> --name <project/name> --args-file <args.json>`.
Omit `--args-file` for commands without arguments. Use `--result-file <new-file.json>`
for the structured result, including the plan ID. Published blueprint references
are resolved to digests; `--blueprint-dir` is an explicit development override.

Plans are private, bounded files bound to the Instance, blueprint digest, driver/tool
versions, driver configuration and source checksums. Applying a changed or consumed
plan requires a fresh plan. OpenTofu performs stale-state checks. The provided
blueprints place no private keys/passwords in resource inputs; saved plans still
contain infrastructure metadata and public SSH keys. Storage must remain private.
For blueprints requiring confidential resource values, configure native OpenTofu
state/plan encryption through a `TF_ENCRYPTION` secret reference and validate key
recovery before use. Kit does not claim all saved-plan contents are nonsensitive.

Apply/destroy invalidate inventory before effects. Failure/cancellation leaves it
unusable. After reconciling state, `opentofu-0-1-0.output` regenerates the allowlisted
node artifact. Out-of-band changes require the same explicit refresh. Ansible rejects
wrong bindings, unsupported schemas, duplicate hosts, missing selected addresses,
architecture mismatches and unsuccessful or incomplete recaps.

`opentofu-0-1-0.destroy` changes infrastructure. `builtin.delete` only removes local
Inframill data. Inspect history with `bin/inframill history --workspace .inframill
--name <project/name>`. Neither retries nor rollback happen automatically.

## Add or maintain releases

Blueprint sources live at `blueprints/<name>/versions/<version>/`; release metadata
lives at `releases/blueprints/<name>/<version>.yaml`. Add the source, exact driver
requirements, an example and tests. `task blueprint:source-digest BLUEPRINT=...
VERSION=...` prints the deterministic source-layer digest for `source_sha256` in
the release descriptor. Packaging rejects source drift. Blueprint packaging and
push logic is independent of the blueprint name.

Driver releases have metadata in `drivers/<name>/versions/<version>/` and
`releases/drivers/`. The implementation registry and command/config contracts are
in `internal/catalog` and `internal/driverkit`. Kit release descriptors enumerate
included versions; bundle locks select the required implementations and toolchains.
Adding a new driver requires implementing and registering its handlers and tests.
Adding a new upstream tool version also requires its checksums, isolated runtime
installation and compatibility evidence.

Backport compatible fixes as a new patch version on the older maintained line.
Breaking inputs, state identity, commands or architecture support require a major
release after 1.0.0. Before 1.0.0, breaking changes increment the minor version.
Deprecated releases remain retrievable for pinned Instances. A newer bundle must
retain required drivers to serve older Instances; it never upgrades them implicitly.

The runtime ships license notices, component locks, matching OpenTofu/Ansible and
Python dependency sources, and Kit sources. Inframill is copied from its pinned
binary release; available Go dependency notices are collected from the sibling
module. See
[runtime/NOTICES.md](runtime/NOTICES.md). OpenTofu uses MPL-2.0 and Ansible Core uses
GPL-3.0-or-later; their redistribution conditions apply independently of Kit's MIT
license. New dependency selections require a release-specific license review.
