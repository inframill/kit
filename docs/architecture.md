# Kit architecture

Kit targets the Inframill API selected by its Go module dependency. One root Go module builds
independently versioned implementations into a shared executable. The root `ghcr.io/inframill/kit` image bundles
prebuilt provider outputs with the engine and tools. Blueprints are local source directories.

## Ownership

- Inframill owns namespace lifecycle, namespace-scoped Blueprints and Instances,
  Blueprint-owned Values and command argument validation, rendering, namespace secret snapshots,
  queues, persistence, history and the external provider transport.
- `internal/providers/<name>/` owns each implementation,
  embedded release metadata and tests. `cmd/main.go` is the only entry point.
- `internal/catalog` maps exact `NAME-VERSION` selectors to implementations.
  `internal/cli` consumes prefix arguments and calls upstream `runmill.Serve`.
- `internal/artifacts` owns rooted file access, hashes and shared node records.
  `internal/toolrun` owns subprocesses, restricted environments, bounded output
  and filtered diagnostics.
- Toolai owns Go builds and aggregate workflows. `scripts/kit` owns focused
  engine staging, runtime setup and release packaging. Task exposes those external
  jobs and local runtime operations. Tests live under `tests` and beside Go code.

## Command lifecycle

`ValidateSettings` examines only the provider's static settings object. Engine
renders `templates/<configured-id>/` into the matching provider workspace, each
with its own `kit-provider.json`. Kit configures IDs `opentofu`, `ansible` and `dispatch`.
Each Blueprint declares its supported qualified commands and JSON argument schemas
in `command_arguments`. Inframill compiles these when loading the revision and
validates submissions before admission without starting a provider process.
Provider discovery supplies command descriptions and access modes; Blueprint and
Instance discovery supply the revision's argument schemas. Instance discovery also
includes the engine-owned cancel, delete and rerender commands.

Runmill invokes `Provider.Execute` directly in one run process with the namespace's
secret snapshot. Execute defensively decodes arguments, validates rendered settings
and checks files, artifact bindings and credentials before effects. There is no
`Prepare` callback or `validate_command` protocol method.

Only Execute can create artifacts or launch tools. Known required credentials are
resolved before effects and passed through a selected child environment. No secrets
are read from inherited `INFRAMILL_SECRET_*`, Values, Arguments or Engine storage. Tool
logs are bounded and filtered for known injected values, including chunked and
JSON-escaped forms. Machine output is parsed privately and projected into stable
results; credential-bearing projections reject. Transport remains upstream-owned.

## Identity and verification

Every saved plan binds the shared artifact directory, producer ID,
implementation/tool versions, configuration/settings/source hashes,
mode and plan bytes. Apply/destroy with `plan_id` recheck this before effects and
retire the plan before mutation; failures require new planning. When `plan_id` is
omitted, they execute the current configuration noninteractively with automatic
approval, without selecting a saved plan. Invalid supplied plans never fall back
to direct execution. Both paths invalidate node inventory before tool mutation
and republish only after verified outputs. Destroy checks managed state.

Ansible validates node schema, identity, producer, host uniqueness and exact address
selection. Its private configuration and callback prevent user environment overrides.
A successful process exit needs a complete successful host recap. Syntax-check does
not require node artifacts; check mode reports prediction.

Dispatch independently consumes the verified node artifact, selects hosts using
the directory mapping, copies the entire directory with SCP and runs Make directly
over SSH. Namespace secrets travel through SSH stdin into protected files.
Hosts execute concurrently, and success requires every selected host to finish
with exit code zero. Shared artifacts remain input. See [Dispatch](dispatch.md).

## Versions and publication

Kit's version comes from `toolai.json`. The normal `go.mod`/`go.sum` dependency
selects the SDK and engine source. Provider versions/protocols are embedded metadata
beside their implementations. `runtime/dependencies.json` owns external tool pins.
Kit and engine versions are independent. No release locks duplicate these inputs.

Standard provider binaries live in `bin/`. Engine sources and binaries are staged
under `.build/engine`; the image stage uses verified prepared outputs. Release
bundles under `dist/kit/<version>` contain the image archive, one generated manifest,
checksums, sources and dependency notices. Input fingerprints prevent source changes
during assembly. A completed bundle verifies independently of either source checkout.
Published tag conflicts are rejected; publishing is explicit and never builds.

Blueprints remain local directories under `blueprints/<repository>/<tag>/`. Their
content hashes and saved Instance snapshots remain engine-owned. Dependency locks
within blueprints describe their infrastructure providers, not Kit releases.
See [releases](releases.md) for commands and the release directory layout.

## Container configuration and storage

The bundle copies the checked-in `runtime/inframill.yaml` unchanged to
`/etc/inframill/inframill.yaml`. It declares HTTP defaults, logging and all bundled
providers with absolute executable, tool and data paths. Update this file when
changing bundled provider or tool versions. Local `task config` output is generated
separately with workstation paths. The image's default
command is `inframill server start --config /etc/inframill/inframill.yaml`. Users may replace
that entire file with a read-only bind mount. Configuration is read at startup;
the image has no merge, rewriting or automatic reload layer.

`/var/lib/inframill` is the writable data mount, independent of the configuration
directory. Users explicitly select a named volume or bind mount. Inframill
resolves relative YAML paths from the selected config directory; absolute defaults
keep the storage destination stable. Persisted data survives container replacement,
including namespace secrets and registry credentials stored unencrypted in SQLite.
Namespaces must be created explicitly and own `<dataDir>/<namespace>/blueprints/` and
`<dataDir>/<namespace>/instances/<InstanceID>/`, alongside `namespace.json` and
the authoritative `data.sqlite` store for Instance/Blueprint metadata, events and
credentials. Existing namespaces initialize in the background; history recovery
and execution counts initialize eagerly. Individual Instance failures remain
isolated, while shared catalog failures mark the namespace failed.
Blueprint caches are isolated by namespace. Namespace deletion hides its data and
drains admitted operations. Instance deletion hides the row, kills running commands
and cancels queued work. Both retain all stored files without invoking providers.
Older shared-root and prior catalog schemas are unsupported;
there is no automatic migration. See the [Docker guide](../README.md#run-with-docker).

Every namespace-owned RPC requires an `X-Namespace-ID` header; the CLI requires
`--namespace`, including for ID-based lookups. Scoped responses omit namespace
identity, and Instance selectors use local names or canonical uppercase ULIDs.
Namespace administration and provider discovery remain global. Namespace administration
request/response fields and persisted ownership records use `id`. Namespace IDs,
Instance names and Provider/command identifiers accept `[a-zA-Z0-9-]+`, retaining case.
Old namespace `name` records are rejected without migration. `namespace get`
returns its ID, timestamps, initialization status and backend results. Pagination
cursors are bound to scope.

Instance and execution IDs use ULIDs. Kit validates these IDs before using saved
plans or shared node artifacts. The `ulid-v1` storage layout rejects old UUID data
without migration; upgrades from UUID storage require a fresh data directory.

See [usage](usage.md) for command behavior and [releases](releases.md) for build and
publication commands.
