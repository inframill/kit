# Kit providers

Kit builds one executable containing independently versioned OpenTofu, Ansible and
Dispatch implementations. The first argument selects the exact provider release.
Each invocation starts one provider process and serves the Inframill JSON protocol
on stdin/stdout. Diagnostics go to stderr.

```sh
bin/kit-exec list
bin/kit-exec version
bin/kit-exec ansible-0.4.0 version
bin/kit-exec ansible-0.4.0 --tool /absolute/path/to/ansible-playbook
printf '%s\n' '{"protocol":1,"method":"info","params":{}}' | bin/kit-exec opentofu-0.4.0
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
cmd/main.go                    one production entry point
internal/providers/<name>/     implementation, release.json and tests
internal/catalog/              exact selectors and provider factories
internal/cli/                  argument handling and runmill.Serve
internal/artifacts/            shared artifact integrity and rooted access
internal/toolrun/              subprocesses and filtered diagnostics
pkg/runmill/                   copied transport implementation and tests
go.mod                         one root Go module, no external dependencies
toolai.json                    application version, Go builds and check steps
scripts/core/                  shared Python execution/staging primitives and unit tests
scripts/kit/                   namespaced engine/runtime/bundle jobs and unit tests
runtime/dependencies.json      runtime tool versions and checksums
tests/                         blueprint, protocol and acceptance tests
```

The provider registry reads identity and requirements from each implementation's
embedded `release.json`. Its factories explicitly import the corresponding Go
packages. Tests exercise the compiled registry and provider metadata; synthetic providers
have their own implementations and identities.

## Build and verification

The compiled catalog explicitly selects the implementations; their embedded release
metadata is the provider-version source of truth. There is no repeated selection
file. Source directories are unversioned while each provider has one implementation.
Provider selectors retain their exact `NAME-VERSION` protocol identity.

Use `toolai go compile` and `toolai check` for development feedback.
`task dev:prepare` builds the sibling engine, imports Runmill, its binaries and pins, installs
pinned tools, builds the development binaries and writes `.build/dev/inframill.json`.
Use `task dev:sync` to import existing verified sibling outputs without building.
`toolai go build` builds all provider platforms, and
`toolai go build --native --output bin/kit-exec` builds the native executable used
in the examples above. Standard binaries live in
`bin/kit-exec-<os>-<arch>`; `bin/kit-exec` is native. Runtime/image packaging consumes
those outputs. Engine imports use a clean sibling checkout with prebuilt binaries
matching the version, revision and checksums in `runtime/dependencies.json`.
Release preparation verifies the copied Runmill source hash and engine binary
identity without retaining the full engine source. The aggregate `toolai build`
imports the pinned engine binaries and builds the local image.
See [releases](releases.md) for bundle preparation and verification.

## Inframill configuration

Each provider binding uses the same executable and its own prefix arguments:

```yaml
engine:
  providers:
    ansible:
      path: /usr/local/bin/kit-exec
      args:
        - ansible-0.4.0
        - --tool
        - /opt/inframill/tools/ansible/2.20.1/bin/ansible-playbook
    dispatch:
      path: /usr/local/bin/kit-exec
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

## Add or update a provider

1. Add or update `internal/providers/<name>` and its tests and embedded metadata.
2. Register its factory in `internal/catalog`. Identity and tool requirements come
   from provider metadata; no separate selection list needs updating.
3. Update runtime dependency pins and configuration selectors when requirements change.
4. Run `toolai check`, build the release, and run the affected acceptance suites.

## Tests and protocol

`toolai check` runs Python tooling/callback tests, Go implementation/shared-code tests,
and subprocess tests of the production `cmd` entry point. Version-coexistence,
secret handling and process cancellation tests add synthetic providers using the
`testproviders` build tag. Normal builds exclude them; there is no separate test
dispatcher. Test tools are temporary stubs and create no cloud resources.

Provider metadata advertises command descriptions and access modes. Blueprint
`command_arguments` declarations own schemas and admission validation. The
protocol supports `info`, `validate_settings` and `run`; execution defensively
checks inputs, artifacts, credentials and outcomes. `pkg/runmill` owns process
observation and group signalling; child tools stay in the provider process group.

The optional `toolai check full` suite exercises native tools, disposable daemons,
local Blueprint workflows, registries and both container architectures. See
[usage](usage.md), [architecture](architecture.md) and [releases](releases.md).
