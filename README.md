# Inframill Kit

Kit supplies OpenTofu and Ansible drivers, portable blueprints, and a Linux runtime
for **Inframill 0.2.0**. This is the initial Kit 0.1.0 implementation. It uses only
the current protocol and has no legacy input readers or migrations.

| Component | Version | Delivery |
| --- | --- | --- |
| Kit | 0.1.0 | `inframill-kit` executable |
| OpenTofu driver | 0.1.0 | Kit factory; OpenTofu 1.11.5 |
| Ansible driver | 0.1.0 | Kit factory; Ansible Core 2.20.1 |
| pv-cloud | 0.1.0 | `ghcr.io/inframill/blueprints/pv-cloud` |
| Runtime | 0.1.0 | `ghcr.io/inframill/runtime` |
| Inframill | 0.2.0 | Prebuilt sibling binaries copied into runtime |

Blueprint, driver, Kit, runtime and core versions are independent. Select exact
MAJOR.MINOR.PATCH versions. Multiple names and versions are supported by explicit
release descriptors and implementation factories; there is no latest-version fallback.
Blueprints are architecture-independent OCI source artifacts. The runtime contains
Linux AMD64 and ARM64 images. pv-cloud supports either or mixed guest architectures.

## Build locally

Prerequisites: Go 1.27.0, Task, Python 3 and Docker Buildx with both platforms. Native
Ansible setup needs Python 3.12–3.14; select it with `PYTHON` when needed.

```sh
PYTHON=/path/to/python3.13 task setup
task build
task inframill:copy
task check-fast
```

`task setup` installs checksum-pinned tools into `.cache`. You may set absolute
`OPENTOFU_PATH` and `ANSIBLE_PATH` overrides; execution still verifies tool versions.
Kit imports the public driver package from `../inframill` through Go's local
replacement. The runtime release build requires the clean source revision pinned
in `releases/bundles/0.1.0.lock.yaml`.

Core binaries must already exist:

```text
../inframill/dist/0.2.0/inframill_0.2.0_linux_amd64
../inframill/dist/0.2.0/inframill_0.2.0_linux_arm64
../inframill/dist/0.2.0/inframill_0.2.0_darwin_arm64
```

The lock also records Darwin AMD64 for that development host. `inframill:copy`
selects the host platform from this versioned release and verifies its hash,
architecture and reported version. `INFRAMILL_DIST_DIR` changes the parent directory.
Bundle builds copy the Linux binary bytes, verify their hashes and ELF architecture,
and compile Kit for each platform. They never invoke the sibling build/release task.

```sh
task blueprint:check BLUEPRINT=pv-cloud VERSION=0.1.0
task bundle
task check-full
```

`check-fast` runs Python/Go tests and builds Kit, with Go vet disabled. `check-full`
adds native workflows, provider mocks, a disposable daemon and registry, and tests
both images. Build the bundle first. Tests use local terraform_data resources and
loopback Ansible; they do not deploy cloud resources or publish to GHCR. AMD64 may
run under emulation; [acceptance](docs/acceptance.md) records the actual evidence.

`KIT_DIST_DIR` changes the output root consistently for build, push and checks.
A built version is immutable: explicitly remove an unpublished development output
before rebuilding changed sources under the same version.

## Local execution and secrets

Examples under `examples/` contain plain Values, not Instance envelopes. Generate
configuration using the installed local tools:

```sh
OPENTOFU_PATH="$PWD/.cache/tools/opentofu/1.11.5/tofu" \
ANSIBLE_PATH="$PWD/.cache/ansible-2.20.1/bin/ansible-playbook" \
  task --silent config > /tmp/kit-config.json
```

A pv-cloud invocation has this form; commands such as init/plan/apply contact real
services and require your chosen credentials and infrastructure inputs:

```sh
bin/inframill exec opentofu-0-1-0.init \
  --config /tmp/kit-config.json \
  --blueprint-dir blueprints/pv-cloud/versions/0.1.0 \
  --values-file examples/pv-cloud-0.1.0.json \
  --name production/example --secrets-file /private/path/secrets.json
```

For a published source replace `--blueprint-dir` with `--blueprint` and its tag or
digest reference. These source flags are mutually exclusive. Use `--args-file` for
command arguments and `--result-file` with a new path outside source/data/input
locations for machine results. Stdout is a stream of JSON reply records; tool
diagnostics go to stderr. A result record's status, not mere file existence, decides
success. Cancellation does not roll back tool effects.

Secret files are JSON maps of local names to strings. The example's names are
`hetzner`, `s3_access` and `s3_secret`. Values select these names, never credential
values. Inframill supplies a project snapshot only to driver Execute; Kit does not
read `INFRAMILL_SECRET_*` or arbitrary inherited tool credential variables. Required
credentials must be nonempty and are checked before effects. Only selected values
reach the tool environment. Known injected values are filtered from tool diagnostics;
Kit results and shared artifacts contain only verified projections. Arbitrary
provider/playbook output must not intentionally emit transformed secrets.

For daemon use, manage the in-memory project set with Inframill's `secret replace`,
`secret list` and `secret clear`. Restart discards sets. Running commands retain their
snapshot; queued commands obtain the set current when they start. Local exec needs
`--secrets-file` on each invocation; omission means an empty set.

Remote Instance initialization uses:

```json
{"blueprint_reference":"ghcr.io/inframill/blueprints/pv-cloud:0.1.0","values":{}}
```

Replace the empty Values object with the example's configured Values. The daemon
must fetch the blueprint before initializing the Instance.

## Driver contracts

A blueprint has a static `manifest.yaml`, Values `schema.json`, and `template/`.
Each driver section pins its exact implementation and a `settings_file`. Templates
render nonsecret backend/address/user settings into `kit-driver.json` within that
driver's workspace. Secret references are names in this file, never credentials.
Manifest validation uses no files; Prepare reads and validates rendered inputs with
no tool execution, writes, network or secrets. Execute performs and verifies effects.

OpenTofu supplies init, validate, plan, apply, destroy and output. Plan returns a
`plan_id`; apply/destroy require it. Plans bind the Instance, pinned blueprint,
driver/tool/configuration and source bytes. Kit retires a plan before apply effects,
so failures and partial effects require a fresh plan. It invalidates shared nodes
before mutation and only publishes verified output. Destroy verifies empty managed
state. Refresh-only and destroy plans are distinct modes.

Ansible supplies inventory, syntax-check, check and apply. It consumes a bound node
artifact, selects the requested address without fallback, and checks the complete
host recap. Failed, unreachable, ignored or rescued failures reject. Check mode is a
prediction. Syntax-check needs no previously applied infrastructure. SSH uses an
agent, batch mode and explicit host-key policy; Kit does not write private keys.

The executable's configured prefix is:

```text
inframill-kit driver opentofu --driver-version 0.1.0
```

Inframill sends one JSON request on stdin, without adding protocol arguments. Kit
uses upstream `driver.Serve` for JSON Lines messages and final success/error replies.
For a manual discovery probe, pipe this JSON to that prefix:

```json
{"protocol":1,"method":"info","params":{}}
```

## Manual package and push

```sh
task blueprint:package BLUEPRINT=pv-cloud VERSION=0.1.0
task blueprints:package
task bundle

docker login ghcr.io -u YOUR_GITHUB_USERNAME
task blueprints:push
task bundle:push
```

Builds never push. Push uploads the existing verified OCI bytes and rejects a tag
pointing to different content. `REGISTRY` and `NAMESPACE` override the defaults;
`PLAIN_HTTP=true` is available for a local test registry. Default publication includes
pv-cloud only. Multi-version local-resource blueprints and synthetic driver factories
are test fixtures and are not included in the production catalog/release set.

GHCR image/package names do not require separate `runtime` or `blueprints` Git
repositories. The image source label points at `inframill/kit`. Configure package
visibility/access in GitHub. Inframill's current blueprint consumer fetches anonymously;
private blueprint consumption is not provided by Docker login. Runtime pulls use
normal Docker authentication.

After publication, use the printed immutable digests:

```sh
task blueprint:verify BLUEPRINT=pv-cloud REFERENCE=ghcr.io/inframill/blueprints/pv-cloud@sha256:...
task bundle:verify REFERENCE=ghcr.io/inframill/runtime@sha256:...
```

New blueprint sources go in `blueprints/<name>/versions/<version>`. Their release
files declare core/driver requirements and a sealed source digest, obtained with
`task blueprint:source-digest`. Add actual production selections to
`releases/sets/default.yaml`. New driver releases require an explicit factory and
metadata; a YAML file alone never registers code. Locks and deterministic source
hashes prevent accidental content replacement.

See [architecture](docs/architecture.md), [acceptance](docs/acceptance.md), and
[runtime notices](runtime/NOTICES.md) for ownership, checks and distribution contents.
