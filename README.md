# Inframill Kit

Kit provides versioned OpenTofu, Ansible and Dispatch providers, reusable blueprints, and a
ready-to-run image for Inframill.

- **[`kit-exec`](docs/providers.md)** runs all provider implementations from one binary per platform.
- **`ghcr.io/inframill/kit`** bundles Inframill, the providers, OpenTofu and Ansible.
- **[`blueprints/`](blueprints/README.md)** contains local `pv-cloud` and `hello-world` sources.

The Kit image supports Linux AMD64 and ARM64. Blueprint sources are architecture
independent; pv-cloud supports AMD64, ARM64 and mixed guest architectures.

## Development and releases

Toolai owns Go compilation, builds, tests, and aggregate workflows. Task owns focused
external jobs and local operations. Kit's version lives in `toolai.json`.
`runtime/dependencies.json` owns the engine version, Git revision, Runmill source
hash and platform binary checksums alongside external tool pins. Kit has no external Go module dependencies.
Kit owns its copy of the transport in `pkg/runmill`, which uses only the standard
library. Provider metadata lives alongside each implementation in
`internal/providers/<name>/release.json`.

```sh
toolai doctor
toolai check
task setup                 # install local OpenTofu and Ansible for native acceptance
toolai build release       # provider/engine binaries, local image and dist/kit/<version>/
toolai check full          # native, registry and both Linux runtime acceptance suites
# After reviewing the bundle and logging into the intended registry:
task release:push
```

Go lint is separately selected with `toolai check lint`; no lint rewrites run in
checks. See [the release guide](docs/releases.md) for inputs, output layout and
artifact verification. Builds stay local; publishing is always explicit.

Engine imports require a clean `../inframill` checkout and prebuilt binaries matching
the runtime engine pin. Use `INFRAMILL_DIR=/path/to/inframill` to select another checkout. No Go
workspace or GitHub module access is needed to build Kit or synchronize Runmill.
The separately built engine resolves its own dependencies from its own `go.mod`/`go.sum`.

## Run with Docker

The image includes [`runtime/inframill.yaml`](runtime/inframill.yaml) unchanged at
`/etc/inframill/inframill.yaml` and starts the server with all Kit providers configured. Docker
selects the AMD64 or ARM64 variant for your machine. No local Go or Python setup is
needed to use the image. Run the examples from this checkout; `toolai.json` selects
its release tag. Without a checkout, substitute the desired published tag.

```sh
docker run -d --name inframill --rm \
  -p 127.0.0.1:8080:8080 \
  --mount type=volume,src=inframill-data,dst=/var/lib/inframill \
  "ghcr.io/inframill/kit:$(python3 -c 'from scripts.kit.project import project_version; print(project_version())')"
```

This uses the bundled YAML and persists data in the named volume `inframill-data`.
Connect your Inframill CLI or API client to `http://localhost:8080`. The port is
published on loopback because the default configuration serves unauthenticated
HTTP. For remote access, configure root-token authentication and TLS, or use an
authenticated gateway or private tunnel. See [daemon authentication](docs/usage.md#daemon-authentication).

The default command is `inframill server start --config /etc/inframill/inframill.yaml`.
With the matching Inframill CLI installed locally, query the running server with:

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
| `/usr/local/bin/kit-exec` | Shared provider executable selected with a `NAME-VERSION` argument |

### Use your own configuration

The checked-in [default configuration](runtime/inframill.yaml) includes HTTP port
and timeouts, JSON logging, the data directory, and executable/tool paths for
OpenTofu 0.4.0, Ansible 0.4.0 and Dispatch 0.1.0. Copy it to start a custom configuration:

```sh
cp runtime/inframill.yaml inframill.yaml
```

Extract the complete default configuration from the image, then edit it:

```sh
docker run --rm --entrypoint cat "ghcr.io/inframill/kit:$(python3 -c 'from scripts.kit.project import project_version; print(project_version())')" \
  /etc/inframill/inframill.yaml > inframill.yaml
```

Keep `engine.dataDir: /var/lib/inframill` and the absolute provider executable paths
unless you also change the corresponding mounts. The supplied file replaces the
whole default configuration; it is not merged or rewritten. Keep exactly one
`connect-engine` handler; its optional `rootToken` setting controls authentication
and its RPC route is fixed by Inframill.

To use the repository's `.inframill.yaml` and persist data in `.inframill/`, run:

```sh
task docker:up
```

This uses the locally available `REGISTRY/NAMESPACE/kit:<VERSION>` image
(default `ghcr.io/inframill/kit:<VERSION>`), including images loaded by `toolai build`.
Prepare the image with `toolai build` before starting it; startup does not build or pull prerequisites. It creates `.inframill/` if needed and binds those paths plus `blueprints/` from
the repository root. The checked-in configuration enables debug, OpenTofu,
Ansible and Dispatch; the image default enables OpenTofu, Ansible and Dispatch.

The project `ssh/` directory is mounted at `/root/.ssh`, so providers such as
Ansible use its SSH keys and configuration automatically. Put your private key at
`ssh/id_ed25519` (or another standard SSH identity filename), with mode `600`;
use mode `700` for `ssh/`. For other key names, set `IdentityFile` in `ssh/config`
using the container path, for example `/root/.ssh/deploy_key`. The mount is writable
so SSH can persist `known_hosts` across container replacements. `ssh/` is ignored
by Git and keys are not included in the image. Existing SSH agent forwarding
remains available; see [provider SSH access](docs/usage.md#providers).

To stop and remove the container while preserving the data, configuration and image:

```sh
task docker:down
```

This also succeeds if the container is already absent.

The `.inframill/` directory is reused on subsequent runs. To use another host
directory, create it with `mkdir -p data` and replace the data mount with:

```sh
--mount type=bind,src="$PWD/data",dst=/var/lib/inframill
```

Changing mount sources selects different storage; it does not copy existing data.
Inframill resolves relative YAML paths from the configuration file's directory
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

The current storage contract uses namespace `data.sqlite` and per-Instance
`commands.sqlite`, with ULID Instance/execution IDs. Prior storage formats reject
without migration; preserve existing volumes and use fresh storage for this release.
Create namespaces with `inframill namespace create ID` before fetching blueprints
or initializing Instances. See [namespace usage](docs/usage.md#namespaces).

Without a data mount, data is lost when the container is removed. Namespace secrets
and registry credentials persist unencrypted in `data.sqlite` and its sidecars,
including across restarts and namespace deletion.

## Build and verification

Provider implementations live in `internal/providers/{opentofu,ansible,dispatch}`.
One Go module and `cmd/main.go` produce the `kit-exec` binary. Toolai builds all
Linux/macOS AMD64/ARM64 binaries under `bin/` and a native `bin/kit-exec`.
The engine importer verifies the sibling binaries' architecture, embedded Git
revision, clean-build marker, native version and pinned checksums. It copies all
four platform binaries into `bin/inframill-<os>-<arch>` and the host's binary into
`bin/inframill`. `.build/engine` holds only generated metadata and dependency notices.
Full engine sources are excluded from Kit staging, images and release archives.

```sh
toolai go compile
toolai check
toolai build release
task release:verify
toolai check full
```

Default checks need Go and Python 3.9+, with no Docker or installed infrastructure
tools. Protocol tests build temporary test fixtures with local tool stubs. Full
checks require Docker/Buildx, OpenSSL, Python 3.12–3.14 for Ansible, `task setup`,
and prepared native binaries and release bundle. They use disposable resources,
not cloud accounts. The pv-cloud payload suite requires the Millway 0.1.6-dev image
for the Docker host architecture. Focus acceptance with `toolai check native` or
`toolai check image`; tags add to the default checks.

For native development without image packaging:

```sh
task dev:prepare
bin/inframill server start --config .build/dev/inframill.json
```

`task dev:prepare` first builds Inframill's platform binaries in `../inframill`
using Toolai, then copies all Runmill Go sources and tests from that checkout,
replaces their imports with Kit paths, and updates the `INFRAMILL` record in
`runtime/dependencies.json` to that checkout's version, committed HEAD, Runmill
source hash and binary checksums. It copies engine binaries into Kit's `bin/`,
installs the pinned runtime tools, rebuilds provider binaries for the standard platforms and
the native host, and writes `.build/dev/inframill.json` with local tool and
Blueprint paths. Kit's Runmill README is preserved; obsolete Go files are removed.
The sibling checkout must be clean. Synchronization reads the local Git checkout
without fetching Inframill or modifying Kit's Go module. An existing Go workspace
is ignored for this workflow.

Use `task dev:prepare INFRAMILL_DIR=/path/to/inframill` for another checkout.
`task dev:sync` imports Runmill, existing sibling binaries, notices and engine pins
without building. An update failure restores the previous sources, binaries and
runtime metadata. Stale, dirty or mismatched binary builds are rejected. The task does
not change Kit/provider versions or the other runtime tool pins.
Run `toolai check` separately after preparing or editing sources. Docker images
and release bundles still use `toolai build` and `toolai build release`.

`task setup` supports Linux AMD64/ARM64 and macOS ARM64 using the checked-in
download hashes. `OPENTOFU_PATH` and `ANSIBLE_PATH` override local tool paths;
`PYTHON` selects a Python 3.12–3.14 interpreter for setup. `task build:engine`
verifies and imports existing sibling binaries matching the runtime pin without
building them or updating pins.
`toolai build` additionally prepares and loads the local multi-platform image;
`toolai build release` adds the distributable bundle. See [validation commands](docs/releases.md#build-and-check)
for the prerequisites of each acceptance suite.

Generation, builds, checks, installation and publishing are explicit operations;
`task dev:prepare` explicitly combines source synchronization, installation and native development preparation.
There are no SQL or RPC generators in Kit. Local runtime configuration is printed
with `task config`. `task docker:up` and `task docker:down` manage the repository's
persistent development daemon; tests use separate disposable processes/containers.

## Python automation

[`scripts/`](scripts/README.md) is the repository's namespaced Python automation
package. Task calls its shared CLI for focused Kit jobs:

```sh
python3 -m scripts --help
python3 -m scripts kit --help
python3 -m scripts kit runtime config
task check:python
```

The package uses the Python standard library, explicit invocation contexts and
isolated temporary staging. Its unit tests live beside the modules they cover and run
alongside the remaining repository tests through `task check:python` and
`toolai check`. See the [automation guide](scripts/README.md) for commands,
isolation conventions and adding namespaces.

## Documentation

- [Providers](docs/providers.md): unified executable, independent implementations, builds and tests.
- [Blueprints](blueprints/README.md): local sources, development workflow and validation.
- [Usage](docs/usage.md): configuration, commands, secrets and provider behavior.
- [Architecture](docs/architecture.md): ownership, execution and artifact contracts.
- [Releases](docs/releases.md): image assembly, publication and version selection.
- [Distribution notices](runtime/NOTICES.md): bundled programs, licenses and sources.
