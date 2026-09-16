# Inframill Kit

Kit provides versioned OpenTofu and Ansible drivers, reusable blueprints, and a
ready-to-run image for Inframill.

- **`inframill-kit`** runs the drivers used by the Inframill engine.
- **`ghcr.io/inframill/kit`** bundles Inframill, the drivers, OpenTofu and Ansible.
- **`ghcr.io/inframill/blueprints/<name>`** contains separately published blueprint packages.

The Kit image supports Linux AMD64 and ARM64. Blueprint packages are architecture
independent; pv-cloud supports AMD64, ARM64 and mixed guest architectures.

## Current versions

| Component | Version |
| --- | --- |
| Kit executable and image | 0.2.0 |
| Inframill engine | 0.2.0 |
| OpenTofu driver / tool | 0.1.0 / 1.11.5 |
| Ansible driver / Ansible Core | 0.1.0 / 2.20.1 |
| pv-cloud blueprint | 0.1.0 |

Kit and its image follow Inframill's major/minor release line, with independent Kit
patch releases. Blueprints and drivers have their own versions.

Release descriptors select exact `MAJOR.MINOR.PATCH` versions. Blueprints and
drivers support multiple names and versions, with explicit implementation selection.

## Development setup

Run `task` or `task help` for commands and examples. Use `task --summary <name>`
for a task's prerequisites, options and behavior.

Install Go 1.27.0, Task and Python 3.12–3.14. Image builds and full verification also
require Docker Buildx with Linux AMD64 and ARM64 support.

Keep the Inframill checkout alongside this repository at `../inframill`. Kit imports
its public driver package through a local Go replacement. Image builds require the
clean source revision pinned in [the bundle lock](releases/bundles/0.2.0.lock.yaml).

```sh
task setup
task build
task inframill:copy
task check-fast
```

`task setup` installs pinned tools into `.cache` and automatically finds Python
3.12–3.14, even when `python3` is Apple's Python 3.9. Install a supported interpreter
alongside the system Python if needed, or select one with `PYTHON=/path/to/python3.14`.
Absolute `OPENTOFU_PATH` and `ANSIBLE_PATH` overrides must match the pinned tool versions.

Prebuilt engine binaries must exist in `../inframill/dist/0.2.0/`, named
`inframill_0.2.0_<os>_<arch>`. Image builds need both `linux_amd64` and `linux_arm64`;
`inframill:copy` needs the host binary, such as `darwin_arm64`. Set
`INFRAMILL_DIST_DIR` to override the parent `dist` directory. Kit verifies these
binaries and copies them directly; it compiles only its own driver executable.

## Build and publish

```sh
task blueprints:package
task bundle

docker login ghcr.io -u YOUR_GITHUB_USERNAME
task blueprints:push
task bundle:push
```

These publish pv-cloud to `ghcr.io/inframill/blueprints/pv-cloud:0.1.0` and the Kit
image to `ghcr.io/inframill/kit:0.2.0`. Builds never push automatically.
See [releases](docs/releases.md) for individual packages, registry overrides,
verification and adding versions.

## Verification

```sh
task check-fast
task blueprint:check BLUEPRINT=pv-cloud VERSION=0.1.0
task bundle                 # build once before full verification
task check-full
```

Fast checks run Python and Go tests and build Kit. Full checks add local tool
workflows, provider mocks, a disposable daemon and registry, and both Linux images.
They use local resources and loopback Ansible. AMD64 can run under emulation.
See [acceptance](docs/acceptance.md) for recorded results and remaining release checks.

## Documentation

- [Usage](docs/usage.md): configuration, commands, secrets and driver behavior.
- [Architecture](docs/architecture.md): ownership, execution and artifact contracts.
- [Releases](docs/releases.md): manual packaging, publication and version selection.
- [Distribution notices](runtime/NOTICES.md): bundled programs, licenses and sources.
