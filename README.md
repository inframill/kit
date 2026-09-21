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
| Kit executable and image | 0.3.0-dev |
| Inframill engine | 0.3.0-dev |
| OpenTofu driver / tool | 0.2.0 / 1.11.5 |
| Ansible driver / Ansible Core | 0.2.0 / 2.20.1 |
| pv-cloud blueprint | 0.2.0 |

Kit and its image follow Inframill's major/minor release line, with independent Kit
patch releases. The root [`VERSION`](VERSION) file selects the Kit executable and
image version for builds, pushes and checks. Task arguments and environment values
cannot override it. Blueprints and drivers have their own versions, selected with
`BLUEPRINT_VERSION` and `DRIVER_VERSION`.

Release descriptors select exact `MAJOR.MINOR.PATCH` versions. Blueprints and
drivers support multiple names and versions, with explicit implementation selection.

## Run with Docker

The image includes [`runtime/inframill.yaml`](runtime/inframill.yaml) unchanged at
`/etc/inframill/inframill.yaml` and starts the server with both Kit drivers configured. Docker
selects the AMD64 or ARM64 variant for your machine. No local Go or Python setup is
needed to use the image.

```sh
docker run -d --name inframill --rm \
  -p 127.0.0.1:8080:8080 \
  --mount type=volume,src=inframill-data,dst=/var/lib/inframill \
  ghcr.io/inframill/kit:0.3.0-dev
```

This uses the bundled YAML and persists data in the named volume `inframill-data`.
Connect your Inframill CLI or API client to `http://localhost:8080`. The port is
published on loopback because the engine has no built-in authentication; remote
access requires an authenticated gateway or private tunnel.

The default command is `inframill server start --config /etc/inframill/inframill.yaml`.
With the Inframill 0.3.0-dev CLI installed locally, query the running server with:

```sh
inframill server version --endpoint http://localhost:8080
inframill server instance list --endpoint http://localhost:8080
```

You can also use the CLI inside the container:

```sh
docker exec inframill inframill server instance list
docker exec inframill inframill server driver list
```

Server requests use `--endpoint` or `INFRAMILL_ENDPOINT`, not the YAML configuration.
If you change the server port, update both the published port and client endpoint.
`server driver list` reports the daemon's cached startup availability. Restart the
daemon to probe drivers again.

| Container path | Purpose |
| --- | --- |
| `/etc/inframill/inframill.yaml` | Default configuration; replace with a read-only file mount |
| `/var/lib/inframill` | Persistent blueprint cache, Instances, workspaces and command history |
| `/usr/local/bin/inframill-kit` | Driver executable referenced by the configuration |

### Use your own configuration

The checked-in [default configuration](runtime/inframill.yaml) includes HTTP port
and timeouts, JSON logging, the data directory, and executable/tool paths for
OpenTofu 0.2.0 and Ansible 0.2.0. Copy it to start a custom configuration:

```sh
cp runtime/inframill.yaml inframill.yaml
```

Extract the complete default configuration from the image, then edit it:

```sh
docker run --rm --entrypoint cat ghcr.io/inframill/kit:0.3.0-dev \
  /etc/inframill/inframill.yaml > inframill.yaml
```

Keep `engine.dataDir: /var/lib/inframill` and the absolute driver executable paths
unless you also change the corresponding mounts. The supplied file replaces the
whole default configuration; it is not merged or rewritten. Keep the
`connect-engine` handler without settings; the RPC route is fixed by Inframill.

If the default container above is running, remove it before creating its replacement:

```sh
docker stop inframill
docker rm inframill
docker run -d --name inframill --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  --mount type=volume,src=inframill-data,dst=/var/lib/inframill \
  --mount type=bind,src="$PWD/inframill.yaml",dst=/etc/inframill/inframill.yaml,readonly \
  ghcr.io/inframill/kit:0.3.0-dev
```

The existing named volume is reused. To keep data in a host directory instead,
create it with `mkdir -p data` and replace the data mount with:

```sh
--mount type=bind,src="$PWD/data",dst=/var/lib/inframill
```

Changing mount sources selects different storage; it does not copy existing data.
Inframill 0.3.0-dev resolves relative YAML paths from the configuration file's directory
inside the container: `dataDir: ./data` here means `/etc/inframill/data`.

### Logs, restarts and upgrades

```sh
docker logs -f inframill
docker restart inframill
docker stop inframill
```

Restart after changing configuration. If your editor replaces the mounted file
instead of modifying it in place, recreate the container to mount the new file.
To upgrade, stop and remove the container, then run the selected image version with
the same config and data mounts. Back up data before upgrading and check the release's
storage compatibility. Run one engine against a data directory at a time.

Without a data mount, data is lost when the container is removed. Namespace secrets
are held in memory and must be supplied again after a restart, even with a volume.

## Development setup

Run `task` or `task help` for commands and examples. Use `task --summary <name>`
for a task's prerequisites, options and behavior.

Install Go 1.27.0, Task and Python 3.12–3.14. Blueprint checks require Docker.
Image builds and full verification require Docker Buildx with Linux AMD64 and
ARM64 support.

Keep the Inframill checkout alongside this repository at `../inframill`. Kit imports
its public driver package through a local Go replacement. Image builds require the
clean source revision pinned in [the release lock](releases/0.3.0-dev.lock.json).

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

Prebuilt engine binaries must exist in `../inframill/dist/0.3.0-dev/`, named
`inframill_0.3.0-dev_<os>_<arch>`. Image builds need both `linux_amd64` and `linux_arm64`;
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

These publish pv-cloud to `ghcr.io/inframill/blueprints/pv-cloud:0.2.0` and the Kit
image to `ghcr.io/inframill/kit:0.3.0-dev`. Builds never push automatically.
See [releases](docs/releases.md) for individual packages, registry overrides,
verification and adding versions.

## Verification

```sh
task check-fast
task blueprint:check BLUEPRINT=pv-cloud BLUEPRINT_VERSION=0.2.0
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
