# Kit providers

Kit builds one executable containing independently versioned OpenTofu, Ansible and
Dispatch implementations. The first argument selects the exact provider release.
Each invocation starts one provider process and serves the Inframill JSON protocol
on stdin/stdout. Diagnostics go to stderr.

```sh
./exec list
./exec version
./exec ansible-0.4.0 version
./exec ansible-0.4.0 --tool /absolute/path/to/ansible-playbook
printf '%s\n' '{"protocol":1,"method":"info","params":{}}' | ./exec opentofu-0.4.0
```

`version` reports the Kit binary version. `NAME-VERSION version` reports that
provider's embedded release metadata. Selectors are exact; unavailable versions
fail with a diagnostic. Versions identify Kit implementations, independently of
the external tool versions. OpenTofu and Ansible default to their versioned tool
paths in the Kit image; `--tool` overrides that path. Dispatch defaults to SSH on
`PATH` and resolves SCP beside it. The external tools are separate programs and
are not embedded in the Go executable.

## Source layout

```text
cmd/exec/main.go                one production entry point
pkg/providers/<name>/v0_4_0/    versioned implementation, release.json and tests
internal/catalog/              exact selectors and provider factories
internal/cli/                  argument handling and runmill.Serve
internal/artifacts/            shared artifact integrity and rooted access
internal/toolrun/              subprocesses and filtered diagnostics
go.mod, go.sum                 one root Go module
releases.json                  provider selections, Go version and SDK pin
scripts/executable.py          executable builds and source snapshots
scripts/artifacts.py           shared artifact verification
tests/                         Python, protocol and acceptance tests
```

The provider registry reads identity and requirements from each implementation's
embedded `release.json`. Its factories explicitly import the corresponding Go
packages. Tests verify the registry agrees with `releases.json`; provider versions
are never simulated by relabeling production code.

## Build and verify

Use Python 3.9+, Git, the Go version pinned in `releases.json`, and a clean
Inframill checkout matching its SDK revision. The default checkout is
`../inframill`; set `INFRAMILL_SOURCE` or pass `--inframill-source` to override it.
Builds use a temporary module override and leave the root `go.mod` unchanged.
Docker is needed only for image assembly and container acceptance.

Run from the repository root:

```sh
task providers:build                         # Linux AMD64 and ARM64
task providers:build -- --native             # host OS/architecture
task providers:build -- --platform linux/arm64
task providers:build -- --refresh            # preserve stale output in a backup
task providers:verify
task test
```

The underlying commands are `python3 build.py build-exec`, `verify-exec` and
`test`. Builds include every selected provider, so there are no per-provider
`--name` or `--version` build flags. The root `VERSION` supplies the executable's
Kit version. Output defaults to:

```text
dist/exec/<KIT-VERSION>/<os>/<arch>/
  exec             shared executable
  build.json       Kit version, included releases, pins and checksums
  sources/         one corresponding source snapshot
  notices/         one set of available dependency license notices
```

`EXEC_DIST_DIR` or `--dist` overrides the `dist/exec` directory. Existing outputs
are reused only when sources, selections, dependency pins, checksums and
architecture still match. `--refresh` replaces stale unpublished output after a
successful build and saves the previous contents beside it under `.previous-*`.

`task bundle` consumes these prebuilt outputs and the pinned engine binaries. It
checks compatibility and integrity without compiling Go or reading the SDK
checkout. The image installs the shared executable at `/usr/local/bin/exec` and
its build record, source snapshot and notices under `/opt/inframill/providers/`.
Old per-provider artifacts under `providers/dist` are no longer consumed.

## Inframill configuration

Each provider binding uses the same executable and its own prefix arguments:

```yaml
engine:
  providers:
    ansible:
      path: /usr/local/bin/exec
      args:
        - ansible-0.4.0
        - --tool
        - /opt/inframill/tools/ansible/2.20.1/bin/ansible-playbook
    dispatch:
      path: /usr/local/bin/exec
      args: [dispatch-0.1.0]
```

Configured IDs remain independent of selectors. Inframill supplies each binding's
`workspace/<configured-id>/` directory. Providers use it directly for settings,
playbooks, roles and payloads. Blueprint templates must match those configured
IDs; shared artifacts remain separate. `task config` prints workstation paths for
the native executable and locally installed tools.

The OpenTofu local backend stores state at
`artifacts/<configured-id>-terraform.tfstate`, with named workspace states beneath
that path with a `.d` suffix. Blueprints must declare `backend "local" {}`. Init
migrates existing workspace state when artifact state is absent. Run init before
rerendering older Instances, then again afterward to restore backend/plugin
metadata. State and backups remain in artifacts across partial applies and rerenders.

## Add a version

1. Add `pkg/providers/<name>/v<major>_<minor>_<patch>/` with the implementation,
   tests and embedded `release.json`. Keep older packages when both versions must
   ship. Replace version dots with underscores and preserve prerelease hyphens in the Go directory name.
2. Register the package and its factory in `internal/catalog`. Identity comes
   from its release metadata, including exact tool requirements and protocol.
3. Add its name/version to root `releases.json` and select it in the image release
   lock when preparing a new Kit release. Update configuration selectors as needed.
4. Run tests, build both Linux architectures, and verify the artifacts.

## Tests and protocol

`task test` runs Python tooling/callback tests, Go implementation/shared-code tests,
and subprocess tests of the production `cmd/exec` entry point. Version-coexistence,
secret handling and process cancellation tests add synthetic providers using the
`testproviders` build tag. Normal builds exclude them; there is no separate test
dispatcher. Test tools are temporary stubs and create no cloud resources.

Provider metadata advertises command descriptions and access modes. Blueprint
`command_arguments` declarations own schemas and admission validation. The
protocol supports `info`, `validate_settings` and `run`; execution defensively
checks inputs, artifacts, credentials and outcomes. The SDK owns process
observation and group signalling; child tools stay in the provider process group.

The optional `task check-full` suite exercises native tools, disposable daemons,
local Blueprint workflows, registries and both container architectures. See
[usage](usage.md), [architecture](architecture.md) and [releases](releases.md).
