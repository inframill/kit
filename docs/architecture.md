# Kit architecture

Kit targets the Inframill API pinned in the selected release lock. The `providers/` subproject builds
independently versioned executables. The root `ghcr.io/inframill/kit` image bundles
prebuilt provider outputs with the engine and tools. Blueprints are local source directories.

## Ownership

- Inframill owns namespace lifecycle, namespace-scoped Blueprints and Instances,
  Values validation/rendering, namespace secret snapshots,
  queues, persistence, history and the external provider transport.
- `providers/roots/<name>/<version>/` owns each implementation, release metadata, command
  entrypoint and tests. `providers/roots/internal/runner` calls upstream `runmill.Serve`.
- `providers/roots/internal/artifacts` owns rooted file access, hashes and shared node
  records. `providers/roots/internal/toolrun` owns subprocesses, restricted environments,
  bounded output and filtered diagnostics.
- `providers/build.py` verifies the pinned SDK checkout and builds separate AMD64 and
  ARM64 executables. The subproject has its own Go module, tasks and tests.
- Root `build.py` owns image assembly, input verification and publication in one
  standard-library Python script. Root acceptance tests live in `tests/`.

## Command lifecycle

`ValidateSettings` examines only the provider's static settings object. Engine
renders `templates/<configured-id>/` into the matching provider workspace, each
with its own `kit-provider.json`. Kit configures IDs `opentofu` and `ansible`. `Prepare` validates arguments/settings and reads required files
without effects or credentials. For run, the fresh provider process prepares again,
then Execute receives the namespace's secret snapshot.

Only Execute can create artifacts or launch tools. Known required credentials are
resolved before effects and passed through a selected child environment. No secrets
are read from inherited `INFRAMILL_SECRET_*`, Values, Arguments or Engine storage. Tool
logs are bounded and filtered for known injected values, including chunked and
JSON-escaped forms. Machine output is parsed privately and projected into stable
results; credential-bearing projections reject. Transport remains upstream-owned.

## Identity and verification

Every saved plan binds the shared artifact directory, producer ID,
implementation/tool versions, configuration/settings/source hashes,
mode and plan bytes. Apply/destroy recheck this before effects. Admission retires
the plan; failures require new planning. Node inventory is invalidated before tool
mutation and republished only after verified outputs. Destroy checks managed state.

Ansible validates node schema, identity, producer, host uniqueness and exact address
selection. Its private configuration and callback prevent user environment overrides.
A successful process exit needs a complete successful host recap. Syntax-check does
not require node artifacts; check mode reports prediction.

## Versions and publication

The root `VERSION` selects one self-contained `releases/<version>.lock.json`,
which declares the exact Inframill target, providers and platforms. The Dockerfile
provides the tools and pins their versions and runtime dependencies. Kit and engine must share major/minor; patches and prereleases
can differ. The image shares Kit's exact version and its lock must pin the declared
engine target. Normal builds use only these explicit selections. `task release:prepare` generates
them from the chosen upstream release before building.

The current providers are OpenTofu and Ansible 0.3.0. Each binary embeds its version's
`release.json`; its executable path selects the implementation. Provider builds use
`providers/releases.json`, independently of the image release. Synthetic test providers
under `providers/tests` exercise version coexistence and distinct contracts.
Blueprints are local directories under `blueprints/`, each containing a definition
and provider templates. Inframill fetches `local:pv-cloud` and `local:hello-world`
from `engine.localBlueprintPaths`, creating namespace-scoped snapshots. Native
configuration includes the repository source path; Docker mounts it read-only.
Edits become new revisions on the next fetch; existing Instances retain their
snapshots. There are no blueprint release manifests, archives or publishing tasks.
Fixtures, examples and source/workflow checks live beside the local blueprints.

Image assembly reads prebuilt `providers/dist/<name>/<version>/linux/<arch>/`
outputs and engine binaries. It checks recorded hashes, ELF architectures and
engine/tool compatibility without accessing provider source or the SDK checkout.
Each provider ships with its build record, corresponding sources and dependency
notices. Native development outputs use the same layout with the host OS/architecture.
Kit image push verifies existing bytes and rejects conflicting version tags. Image
publication is manual; acceptance uses a disposable registry only for Kit images.

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
while namespace secrets and registry credentials remain in memory. Namespaces must
be created explicitly and own `<dataDir>/<namespace>/blueprints/` and
`<dataDir>/<namespace>/instances/<InstanceID>/`, alongside `namespace.json`.
Blueprint caches are isolated by namespace. Namespace deletion persists a request;
the cleanup worker removes local data after admitted operations finish, without
invoking providers. Older shared-root storage is unsupported;
there is no automatic migration. See the [Docker guide](../README.md#run-with-docker).

Every namespace-owned RPC requires an `X-Namespace-ID` header; the CLI requires
`--namespace`, including for ID-based lookups. Scoped responses omit namespace
identity, and Instance selectors use local names or canonical uppercase ULIDs.
Namespace administration and provider discovery remain global. Namespace administration
request/response fields and persisted ownership records use `id`. Namespace IDs,
Instance names and Provider/command identifiers accept `[a-zA-Z0-9-]+`, retaining case.
Old namespace `name` records are rejected without migration. `namespace get`
returns one namespace's ID and lifecycle status. Pagination cursors are bound to scope.

Instance and execution IDs use ULIDs. Kit validates these IDs before using saved
plans or shared node artifacts. The `ulid-v1` storage layout rejects old UUID data
without migration; upgrades from UUID storage require a fresh data directory.

See [usage](usage.md) for command behavior and [releases](releases.md) for build and
publication commands.
