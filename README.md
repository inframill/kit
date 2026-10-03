# Inframill Kit

Kit provides versioned OpenTofu and Ansible providers, reusable blueprints, and a
ready-to-run image for Inframill.

- **[`providers/`](providers/README.md)** builds one executable per provider version and architecture.
- **`ghcr.io/inframill/kit`** bundles Inframill, the providers, OpenTofu and Ansible.
- **[`blueprints/`](blueprints/README.md)** contains local `pv-cloud` and `hello-world` sources.

The Kit image supports Linux AMD64 and ARM64. Blueprint sources are architecture
independent; pv-cloud supports AMD64, ARM64 and mixed guest architectures.

## Current versions

| Component | Version |
| --- | --- |
| Kit image | 0.21.0-dev |
| Inframill engine | 0.21.0-dev |
| OpenTofu provider / tool | 0.3.0 / 1.11.5 |
| Ansible provider / Ansible Core | 0.3.0 / 2.20.1 |
| pv-cloud blueprint | Local source |
| hello-world blueprint (debug provider) | Local source |

Kit and its image follow Inframill's major/minor release line, with independent Kit
patch releases. The root [`VERSION`](VERSION) file selects the Kit image
version for builds, pushes and checks. Task arguments and environment values
cannot override it. Provider releases are selected independently in
`providers/releases.json`. Local blueprints are selected by name and content digest.

## Run with Docker

The image includes [`runtime/inframill.yaml`](runtime/inframill.yaml) unchanged at
`/etc/inframill/inframill.yaml` and starts the server with both Kit providers configured. Docker
selects the AMD64 or ARM64 variant for your machine. No local Go or Python setup is
needed to use the image.

```sh
docker run -d --name inframill --rm \
  -p 127.0.0.1:8080:8080 \
  --mount type=volume,src=inframill-data,dst=/var/lib/inframill \
  ghcr.io/inframill/kit:0.21.0-dev
```

This uses the bundled YAML and persists data in the named volume `inframill-data`.
Connect your Inframill CLI or API client to `http://localhost:8080`. The port is
published on loopback because the engine has no built-in authentication; remote
access requires an authenticated gateway or private tunnel.

The default command is `inframill server start --config /etc/inframill/inframill.yaml`.
With the Inframill 0.21.0-dev CLI installed locally, query the running server with:

```sh
inframill server version --endpoint http://localhost:8080
inframill namespace create production --endpoint http://localhost:8080
inframill instance list --namespace production --endpoint http://localhost:8080
```

You can also use the CLI inside the container:

```sh
docker exec inframill inframill instance list --namespace production
docker exec inframill inframill provider list
```

Server requests use `--endpoint` or `INFRAMILL_ENDPOINT`, not the YAML configuration.
If you change the server port, update both the published port and client endpoint.
`provider list` reports the daemon's cached startup availability. Restart the
daemon to probe providers again.

| Container path | Purpose |
| --- | --- |
| `/etc/inframill/inframill.yaml` | Default configuration; replace with a read-only file mount |
| `/opt/inframill/blueprints` | Optional read-only mount for local blueprint sources |
| `/var/lib/inframill` | Persistent blueprint cache, Instances, workspaces and command history |
| `/opt/inframill/providers/<name>/<version>/provider` | Versioned provider executable referenced by the configuration |

### Use your own configuration

The checked-in [default configuration](runtime/inframill.yaml) includes HTTP port
and timeouts, JSON logging, the data directory, and executable/tool paths for
OpenTofu 0.3.0 and Ansible 0.3.0. Copy it to start a custom configuration:

```sh
cp runtime/inframill.yaml inframill.yaml
```

Extract the complete default configuration from the image, then edit it:

```sh
docker run --rm --entrypoint cat ghcr.io/inframill/kit:0.21.0-dev \
  /etc/inframill/inframill.yaml > inframill.yaml
```

Keep `engine.dataDir: /var/lib/inframill` and the absolute provider executable paths
unless you also change the corresponding mounts. The supplied file replaces the
whole default configuration; it is not merged or rewritten. Keep the
`connect-engine` handler without settings; the RPC route is fixed by Inframill.

To use the repository's `.inframill.yaml` and persist data in `.inframill/`, run:

```sh
task docker:up
```

This pulls `ghcr.io/inframill/kit:<VERSION>` before replacing any existing
`inframill` container. It creates `.inframill/` if needed and binds those paths plus `blueprints/` from
the repository root. The checked-in configuration selects the debug provider;
use the provider definitions from `runtime/inframill.yaml` for OpenTofu and Ansible.

To stop and remove the container while preserving the data, configuration and image:

```sh
task docker:down
```

This also succeeds if the container is already absent.

The equivalent manual commands for the current version are:

```sh
docker pull ghcr.io/inframill/kit:0.21.0-dev
mkdir -p .inframill
docker stop inframill
docker rm inframill
docker run -d --name inframill --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -v "$PWD/.inframill:/var/lib/inframill" \
  -v "$PWD/.inframill.yaml:/etc/inframill/inframill.yaml" \
  -v "$PWD/blueprints:/opt/inframill/blueprints:ro" \
  ghcr.io/inframill/kit:0.21.0-dev
```

The `.inframill/` directory is reused on subsequent runs. To use another host
directory, create it with `mkdir -p data` and replace the data mount with:

```sh
--mount type=bind,src="$PWD/data",dst=/var/lib/inframill
```

Changing mount sources selects different storage; it does not copy existing data.
Inframill 0.21.0-dev resolves relative YAML paths from the configuration file's directory
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

For upgrades from 0.15 or earlier, preserve the old data volume and use a fresh
volume (for example, `src=inframill-data-0.17`) or empty data directory. The new
`ulid-v1` layout rejects older UUID storage without modifying or migrating it.
Create namespaces with
`inframill namespace create ID` before fetching blueprints or initializing
Instances. See [namespace usage](docs/usage.md#namespaces).

Without a data mount, data is lost when the container is removed. Namespace secrets
and registry credentials are held in memory and must be supplied again after a
restart, even with a volume.

## Build the subprojects and image

Provider sources, release metadata, the Python builder and tests live in the
[provider subproject](providers/README.md). Each version has a root such as
`providers/roots/opentofu/0.3.0/`. The [local blueprints](blueprints/README.md)
are fetched directly from their source directories.

```sh
task providers:test
task providers:build         # all providers, Linux AMD64 and ARM64
task bundle                # assemble the image from providers/dist
```

The root project uses a single [`build.py`](build.py) script to assemble, verify
and push the Kit Docker image. It never compiles
or implicitly rebuilds providers. It checks the prebuilt binaries, source snapshots,
checksums, platforms and engine/tool compatibility before copying them into the
image. Task wraps these commands (`task bundle` remains an alias for `task build`):

```sh
python3 build.py build     # both Linux architectures, local output only
python3 build.py verify    # verify the existing local image
python3 build.py push      # push that image; run docker login yourself first
python3 build.py test      # root builder tests
```

`publish` runs build then push; `push` uses the existing image. The root script
also provides `validate`, `setup`, `config` and `copy-engine` for release checks
and local acceptance setup. Providers retain their own builder; blueprints need no build step.

Provider builds need Python 3.9+, Go 1.27.0 and the pinned Inframill SDK checkout
(default `../inframill` beside this repository). Image assembly needs Docker Buildx
with Linux AMD64/ARM64 support, Python 3.9+ (standard library only), and the checksum-pinned
prebuilt engine binaries in `../inframill/dist/0.21.0-dev/`. It does not need provider
sources or the SDK checkout. Set `PROVIDERS_DIST_DIR` or `INFRAMILL_DIST_DIR` to use
other prebuilt artifact locations.

For local blueprint development, `task config` configures the repository's source
path. `task docker:up` mounts `blueprints/` read-only at `/opt/inframill/blueprints`.
Fetch with `inframill blueprint fetch local:pv-cloud --namespace production` or
`local:hello-world`. After editing sources, fetch again and initialize a new Instance.
See [local blueprint usage](blueprints/README.md) for provider configuration and examples.

The Kit image defaults to `ghcr.io/inframill/kit:<VERSION>`. Builds do not publish
automatically. See [releases](docs/releases.md) for image inputs and publishing.

## Verification

```sh
task check-fast
```

Fast checks run image tooling and provider tests. For optional full
acceptance, install Python 3.12–3.14 for Ansible and prepare native outputs:

```sh
task setup
task providers:build -- --native
task inframill:copy
task bundle                # requires prior task providers:build for both Linux architectures
task check-full
```

Full checks exercise native tools, blueprint workflows, disposable registries and
both Linux runtime images. AMD64 can run under emulation. Use `task config` to
print a local configuration pointing at native provider outputs in `providers/dist`.
Absolute `OPENTOFU_PATH` and `ANSIBLE_PATH` overrides select installed tools matching
the tool versions in [`runtime/Dockerfile`](runtime/Dockerfile). `task setup` uses
those same pins. The image release lock selects only the engine, providers and
platforms. Run `task check-full` to verify the current source and artifacts.

## Documentation

- [Providers](providers/README.md): independent version roots, Python builds and tests.
- [Blueprints](blueprints/README.md): local sources, development workflow and validation.
- [Usage](docs/usage.md): configuration, commands, secrets and provider behavior.
- [Architecture](docs/architecture.md): ownership, execution and artifact contracts.
- [Releases](docs/releases.md): image assembly, publication and version selection.
- [Distribution notices](runtime/NOTICES.md): bundled programs, licenses and sources.
