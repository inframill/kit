# Blueprint subproject

Build and publish all maintained Blueprint releases from this directory. The
single `build.py` script owns source validation, deterministic archives, OCI
packaging and calls to `docker load` / `docker push`. It does not import Kit scripts, compile Go,
read the parent `VERSION`, or require a sibling Inframill checkout.

The current packages target Inframill **0.18.0-dev** and its provider binding
contract. pv-cloud is **0.3.0**. OpenTofu files live in `templates/opentofu/`;
Ansible playbooks and roles live in `templates/ansible/`. Each directory contains
its own `kit-provider.json.gotmpl`. These directory names match the configured provider IDs. Each `provider_settings`
entry wraps a major.minor `version` string and a nested `settings` object.

Each release is **one architecture-independent OCI artifact** containing
`blueprint.yaml` and `templates/`. The same tag and digest work on Linux AMD64 and
ARM64. There is no base image, executable, architecture-specific build or manifest
index to select. These are source packages consumed by Inframill, not containers
that can be started with `docker run`.

[`examples/pv-cloud-0.3.0.json`](examples/pv-cloud-0.3.0.json) demonstrates a mixed
AMD64/ARM64 deployment with architecture selected per node. Single-architecture
inputs live under `tests/fixtures/examples/` for integration coverage.

pv-cloud 0.3.0 uses the local OpenTofu backend. Its state lives in the Instance's
persisted `workspace/opentofu/terraform.tfstate`; keep and back up the daemon data
volume. Values no longer include `infra_state_bucket`, and only the cloud token
secret is needed. Existing Instances keep their saved backend and state.

## Setup

Use Python 3.10 or newer (3.14 is tested) and a running Docker daemon with the
[containerd image store](https://docs.docker.com/engine/storage/containerd/).
From this directory:

```sh
python3.14 -m venv .venv
.venv/bin/python -m pip install -r requirements.txt
```

Or use `task setup PYTHON=python3.14`. Python dependencies validate YAML and JSON
Schema. Run `docker login ghcr.io` yourself before pushing. The script never
logs in, reads credentials, exchanges authentication tokens, or creates a registry.
Docker manages authentication and registry access. No ORAS installation is needed.

## Commands

```sh
.venv/bin/python build.py list       # show every declared release
.venv/bin/python build.py build      # build all releases and load them into Docker

docker login ghcr.io
.venv/bin/python build.py push       # push every existing package
# Or perform both stages:
.venv/bin/python build.py publish    # build all, then push all
```

The public tasks are `task setup`, `task build`, `task source-digest`, `task push`,
and `task publish`. Listing releases uses the Python script directly.
From the Kit root, use `task blueprints:setup`, `task blueprints:build`,
`task blueprints:source-digest`, `task blueprints:push`, or `task blueprints:publish`. The root only includes this
Taskfile. Direct script calls resolve project paths relative to the script, so
they also work from other working directories.

Select a name and optionally a version:

```sh
.venv/bin/python build.py build --name pv-cloud --version 0.3.0
task build -- --name pv-cloud --version 0.3.0
```

Every entry in `releases.json` is selected by default. Test fixtures are excluded.
`build` writes `dist/<name>/<version>/oci/` and a `build.json` record, then uses
`docker load` to tag each artifact locally for its destination.
`push` checks the existing build records and invokes only `docker push <tag>` for
each release. It does not rebuild, load, log in, or manage the registry.
`publish` completes all builds and loads, then pushes all tags. Commands exit
nonzero on the first failure. Docker reports upload progress and the remote digest.

The default destination is `ghcr.io/inframill/blueprints/<name>:<version>`.
Override it with flags or the `REGISTRY` and `NAMESPACE` environment variables:

```sh
.venv/bin/python build.py build --registry ghcr.io --namespace my-org
.venv/bin/python build.py push --registry ghcr.io --namespace my-org
.venv/bin/python build.py build --dist /tmp/blueprint-output
.venv/bin/python build.py push --dist /tmp/blueprint-output
```

Use the same registry and namespace for build and push so the local Docker tags
match. If a local tag is missing, run build again. Docker handles TLS and any
insecure-registry settings; the script has no authentication or HTTP options.

Changed source, corrupt local blobs and conflicting local build output are
rejected. Remote tag behavior is standard Docker behavior: pushing can replace
an existing tag unless your registry enforces immutability. Use new release
versions for changed packages. Build and push never modify release metadata.

## Add a release

1. Add source under `<name>/<version>/blueprint.yaml` and `templates/`.
   Use exactly one `templates/<configured-id>/` directory per provider binding,
   such as `templates/opentofu/` and `templates/ansible/`. Top-level files and extra
   directories under `templates/` are rejected. Keep binaries out of these
   portable packages; provider tools belong in the Kit runtime.
2. Print the source seal (this works before adding release metadata):

   ```sh
   task source-digest -- --name example --version 0.1.0
   ```

   From the Kit root, use `task blueprints:source-digest -- --name example --version 0.1.0`.

3. Add an entry to `releases.json` following the existing pv-cloud entry. Include
   the exact name/version, engine compatibility target (`inframill_version`),
   provider IDs/names/versions, matching major.minor Blueprint bindings, protocol `1`, both platforms/architectures, status,
   and the printed `source_sha256`.
4. Run the tests below and `task build`. Publish after testing the release
   against its intended engine and providers.

Only `blueprint.yaml` and `templates/` may occur in a source version. Links, special
files, credentials, state and runtime directories are rejected. Source timestamps,
file ownership and host architecture do not enter the archive. Executable bits
are preserved. `blueprint.yaml` supports JSON or YAML without aliases, duplicate
keys or YAML-only scalar types. Schemas are checked offline, provider requirements
must agree with the release, and required provider settings templates must exist.
Provider-specific settings and execution are verified through the Kit integration
checks; the standalone packager does not load provider implementations.

`--root` selects another standalone source project with its own `releases.json`;
`--config` overrides the manifest path. Relative `--root`, `--config` and `--dist`
arguments are interpreted from your working directory.

## Verification

```sh
.venv/bin/python -m unittest discover -s tests -p "test_*.py"
```

Unit tests cover detached builds, both fixture versions, determinism, tampering,
unsafe inputs, Docker command sequencing and push failure propagation. Docker is
stubbed in unit tests; they create no registries or public packages.

For a registry you have already configured, use matching build/push destinations:

```sh
.venv/bin/python build.py build --registry my-registry.example:5000
.venv/bin/python build.py push --registry my-registry.example:5000
```

The following optional checks integrate with the parent Kit project and require
its `task setup`, `task providers:build -- --native`, `task inframill:copy`, and Docker prerequisites.
Run them from the Kit repository root:

```sh
BLUEPRINT=pv-cloud BLUEPRINT_VERSION=0.3.0 python3 tests/check.py blueprint:check
BLUEPRINT=pv-cloud BLUEPRINT_VERSION=0.3.0 \
  REFERENCE=ghcr.io/inframill/blueprints/pv-cloud@sha256:... \
  python3 tests/check.py blueprint:verify
```

Integration fixtures and example Values live under `tests/fixtures/` and
`examples/`. `tests/integration.py` owns fixture selection and blueprint workflow
checks; `tests/fixtures/suite.json` pins the disposable test registry image.
The parent harness manages test daemons and registries separately from `build.py`.
The parent `task check-full` also runs these packages through the
AMD64 and ARM64 Kit runtimes. Blueprint build/test dependencies are set up with
`task blueprints:setup` before running the parent's checks.
