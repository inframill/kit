# Blueprint subproject

Build and publish all maintained Blueprint releases from this directory. The
single `build.py` script owns source validation, deterministic archives, OCI
packaging and registry publication. It does not import Kit scripts, compile Go,
read the parent `VERSION`, or require a sibling Inframill checkout.

Each release is **one architecture-independent OCI artifact** containing
`blueprint.yaml` and `template/`. The same tag and digest work on Linux AMD64 and
ARM64. There is no base image, executable, architecture-specific build or manifest
index to select. These are source packages consumed by Inframill, not containers
that can be started with `docker run`.

## Setup

Use Python 3.10 or newer (3.14 is tested). From this directory:

```sh
python3.14 -m venv .venv
.venv/bin/python -m pip install -r requirements.txt
```

Or use `task setup PYTHON=python3.14`. Python dependencies validate YAML and JSON
Schema; building needs no Docker daemon. Publishing uses the registry HTTP API
and your existing `docker login` credentials, including Docker credential helpers.
No ORAS installation is needed.

## Commands

```sh
.venv/bin/python build.py list       # show every declared release
.venv/bin/python build.py build      # build all releases locally

docker login ghcr.io
.venv/bin/python build.py push       # push every existing package
# Or perform both stages:
.venv/bin/python build.py publish    # build all, then push all
```

The public tasks are `task setup`, `task build`, `task push`, and `task publish`.
Listing releases and computing source digests use the Python script directly.
From the Kit root, use `task blueprints:setup`, `task blueprints:build`,
`task blueprints:push`, or `task blueprints:publish`. The root only includes this
Taskfile. Direct script calls resolve project paths relative to the script, so
they also work from other working directories.

Select a name and optionally a version:

```sh
.venv/bin/python build.py build --name pv-cloud --version 0.2.0
task build -- --name pv-cloud --version 0.2.0
```

Every entry in `releases.json` is selected by default. Test fixtures are excluded.
`build` writes `dist/<name>/<version>/oci/` and a `build.json` record.
`push` verifies these existing files and never rebuilds. `publish` finishes every
build before it starts any upload. Commands exit nonzero on the first failure.

The default destination is `ghcr.io/inframill/blueprints/<name>:<version>`.
Override it with flags or the `REGISTRY` and `NAMESPACE` environment variables:

```sh
.venv/bin/python build.py push --registry ghcr.io --namespace my-org
.venv/bin/python build.py build --dist /tmp/blueprint-output
.venv/bin/python build.py push --dist /tmp/blueprint-output
```

The script prints immutable `repository@sha256:...` references after publication.
Repeated identical builds and pushes are safe. Changed source, corrupt blobs,
different local output, or a remote version tag pointing at different content
are rejected. Coordinate publication across machines; registry tags have no
atomic compare-and-swap operation. Build and push never modify release metadata.

Python archive encoding differs from the former Go packager, so this migration
changes artifact digests even for unchanged source. Previously published tags
remain immutable: use a new blueprint version to publish different package bytes.
Old Kit release locks retain historical records; they no longer select builds.

## Add a release

1. Add source under `<name>/<version>/blueprint.yaml` and `template/`.
   Each driver has a `template/<driver-id>/` directory. Keep binaries out of these
   portable packages; driver tools belong in the Kit runtime.
2. Print the source seal (this works before adding release metadata):

   ```sh
   .venv/bin/python build.py source-digest --name example --version 0.1.0
   ```

3. Add an entry to `releases.json` following the existing pv-cloud entry. Include
   the exact name/version, engine compatibility target (`inframill_version`),
   driver IDs/names/versions, protocol `1`, both platforms/architectures, status,
   and the printed `source_sha256`.
4. Run the tests below and `task build`. Publish after testing the release
   against its intended engine and drivers.

Only `blueprint.yaml` and `template/` may occur in a source version. Links, special
files, credentials, state and runtime directories are rejected. Source timestamps,
file ownership and host architecture do not enter the archive. Executable bits
are preserved. `blueprint.yaml` supports JSON or YAML without aliases, duplicate
keys or YAML-only scalar types. Schemas are checked offline, driver requirements
must agree with the release, and required driver settings templates must exist.
Driver-specific settings and execution are verified through the Kit integration
checks; the standalone packager does not load driver implementations.

`--root` selects another standalone source project with its own `releases.json`;
`--config` overrides the manifest path. Relative `--root`, `--config` and `--dist`
arguments are interpreted from your working directory.

## Verification and local registry

```sh
.venv/bin/python -m unittest discover -s tests -p "test_*.py"
# Requires Docker; creates a persistent loopback-only registry:
docker compose -f registry.compose.yaml up -d
.venv/bin/python build.py publish --registry localhost:5000 --plain-http
docker compose -f registry.compose.yaml down
```

Plain HTTP is allowed only for explicit local test registries (`localhost`,
`127.0.0.1`, or the test container hostname `registry`). Otherwise TLS is required.
`PLAIN_HTTP=true` is equivalent to `--plain-http`.

Unit tests cover detached builds, both fixture versions, determinism, tampering,
unsafe inputs, authentication, repeat publication, and immutable tags using a
local HTTP test registry. They create no public packages.

The following optional checks integrate with the parent Kit project and require
its `task setup`, `task build`, `task inframill:copy`, and Docker prerequisites.
Run them from the Kit repository root:

```sh
BLUEPRINT=pv-cloud BLUEPRINT_VERSION=0.2.0 python3 scripts/check.py blueprint:check
BLUEPRINT=pv-cloud BLUEPRINT_VERSION=0.2.0 \
  REFERENCE=ghcr.io/inframill/blueprints/pv-cloud@sha256:... \
  python3 scripts/check.py blueprint:verify
```

Integration fixtures and example Values live under `tests/fixtures/` and
`examples/`. The parent `task check-full` also runs these packages through the
AMD64 and ARM64 Kit runtimes. Blueprint build/test dependencies are set up with
`task blueprints:setup` before running the parent's checks.
