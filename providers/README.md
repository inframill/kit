# Provider subproject

This directory owns provider implementations, release metadata, the Python builder,
shared Go code and tests. It does not read the parent Kit `VERSION` or image lock.
Each provider version is a separate executable with its own source root:

```text
providers/
  build.py                 # one Python build/test/verification script
  releases.json            # selected releases, Go version and Inframill SDK pin
  go.mod, go.sum           # independent Go module
  roots/opentofu/0.3.0/           # implementation, release.json, tests and cmd/main.go
  roots/ansible/0.3.0/            # implementation, callback, tests and cmd/main.go
  roots/internal/          # shared execution/artifact code and protocol runner
  tests/                   # builder tests and protocol integration tests
  dist/<name>/<version>/<os>/<arch>/
    provider                 # prebuilt executable for this platform
    build.json             # identity, dependency pins and checksums
    sources/               # corresponding source snapshot
    notices/               # available Go dependency license notices
```

## Requirements

Use Python 3.9 or newer, Go 1.27.0 and Git. The Python script uses only the standard
library; no virtual environment or Python dependencies are required. Docker is
not needed to build or test providers.

The Inframill Go SDK checkout must match the clean revision pinned in
`releases.json`. By default it is `../../inframill` relative to this directory.
Use `--inframill-source /path/to/inframill` or `INFRAMILL_SOURCE` to select another
checkout. The builder uses a temporary Go module override and leaves `go.mod`
unchanged. This also works when this subproject is copied outside Kit.

## Commands

Run from this directory:

```sh
python3 build.py list
python3 build.py test
python3 build.py build
python3 build.py verify
```

`build` builds **every selected provider for both Linux AMD64 and ARM64** with CGO
disabled. These are separate binaries; unlike blueprint text packages, one provider
binary cannot run on both architectures. The script checks ELF architecture and
records checksums before exposing each completed output.

Task wrappers are `task build` and `task test`. From the repository root, use
`task providers:build` and `task providers:test`. Paths resolve relative to `build.py`,
so direct script calls also work from other working directories.

```sh
python3 build.py build --name opentofu --version 0.3.0
python3 build.py build --native                 # native development binaries
python3 build.py build --platform linux/arm64  # one explicit platform
python3 build.py build --dist /tmp/provider-output
task build -- --name ansible --version 0.3.0
```

Existing outputs are verified and reused only when their source and dependency
pins still match. Changed sources or corrupt output fail; use a new version for a
release, or explicitly remove an unpublished development output before rebuilding.
Native builds support Darwin and Linux on AMD64 and ARM64 and are written beside
the Linux release builds. No build command authenticates, creates a registry or
publishes an image.

## Provider interface

Configure the exact executable for the required name and version. There is no
shared `inframill-kit` dispatcher or `--provider-version` argument in production.

```sh
dist/opentofu/0.3.0/linux/arm64/provider version
printf '%s\n' '{"protocol":1,"method":"info","params":{}}' \
  | dist/opentofu/0.3.0/linux/arm64/provider
```

Use the executable matching your host architecture, or build with `--native`.
`--tool /absolute/path` overrides the external OpenTofu or Ansible executable.
The default tool paths are the versioned paths installed by the Kit image.
Requests and responses use the Inframill JSON provider protocol.

## Tests

`python3 build.py test` runs Python builder tests, Go implementation/shared-code
tests and executable protocol tests. Tests cover artifact integrity, architecture
selection, version coexistence, settings validation, secret handling and process
cancellation. Test tools are temporary stubs; no cloud resources are created.
The synthetic multi-version dispatcher under `tests/harness` is used only by tests
and is never included in release outputs.

The parent Kit's optional full acceptance suite also uses `tests/daemon_check.py`
to exercise real tools, blueprints and both container architectures. Those checks
require the parent image and development prerequisites; standalone provider tests
do not depend on the parent project.

## Add a version

1. Create `roots/<name>/<version>/` with its implementation, tests, `release.json` and
   `cmd/main.go`. Retired implementations remain available in Git history.
2. Embed that version's metadata as shown by the current providers. Declare the
   exact tool version, protocol and both Linux platforms in `release.json`.
3. Add its name/version to `releases.json`. Keep shared SDK and Go pins explicit.
4. Run tests, build both architectures and verify the outputs.

To include a provider in an image, separately select its name/version in the root
Kit release lock and configure its executable in `runtime/inframill.yaml`.
The root `task bundle` reads **only prebuilt `providers/dist` artifacts**, verifies
their checksums, architecture, engine compatibility and tool requirements, then
copies them into `/opt/inframill/providers/<name>/<version>/`. It fails if a required
output is missing and never compiles a provider. `PROVIDERS_DIST_DIR` overrides that
input directory for image assembly.

## Provider workspaces

Inframill 0.17.0-dev passes each provider its `workspace/<configured-id>/` directory.
Providers use that supplied directory directly for settings, tools, playbooks and
roles. Kit configures IDs `opentofu` and `ansible`, so Blueprint sources belong
under `templates/opentofu/` and `templates/ansible/`. Configured aliases work when
Blueprint directory names match those IDs. Shared artifacts remain separate.
