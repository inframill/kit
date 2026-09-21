# Kit architecture

Kit 0.5.0-dev targets the Inframill 0.5.0-dev API. The `drivers/` subproject builds
independently versioned executables. The root `ghcr.io/inframill/kit` image bundles
prebuilt driver outputs with the engine and tools. Blueprints are separate OCI packages.

## Ownership

- Inframill owns Instances, Values validation/rendering, namespace secret snapshots,
  queues, persistence, history and the external driver transport.
- `drivers/roots/<name>/<version>/` owns each implementation, release metadata, command
  entrypoint and tests. `drivers/roots/internal/runner` calls upstream `driver.Serve`.
- `drivers/roots/internal/artifacts` owns rooted file access, hashes and shared node
  records. `drivers/roots/internal/toolrun` owns subprocesses, restricted environments,
  bounded output and filtered diagnostics.
- `drivers/build.py` verifies the pinned SDK checkout and builds separate AMD64 and
  ARM64 executables. The subproject has its own Go module, tasks and tests.
- Root `build.py` owns image assembly, input verification and publication in one
  standard-library Python script. Root acceptance tests live in `tests/`.

## Command lifecycle

`ValidateSettings` examines only the driver's static settings object. Engine
renders `templates/<configured-id>/` into the matching driver workspace, each
with its own `kit-driver.json`. Kit configures IDs `opentofu` and `ansible`. `Prepare` validates arguments/settings and reads required files
without effects or credentials. For run, the fresh driver process prepares again,
then Execute receives the namespace's secret snapshot.

Only Execute can create artifacts or launch tools. Known required credentials are
resolved before effects and passed through a selected child environment. No secrets
are read from inherited `INFRAMILL_SECRET_*`, Values, Args or Engine storage. Tool
logs are bounded and filtered for known injected values, including chunked and
JSON-escaped forms. Machine output is parsed privately and projected into stable
results; credential-bearing projections reject. Transport remains upstream-owned.

## Identity and verification

Every saved plan binds InstanceID, immutable Name, pinned BlueprintReference,
producer ID, implementation/tool versions, configuration/settings/source hashes,
mode and plan bytes. Apply/destroy recheck this before effects. Admission retires
the plan; failures require new planning. Node inventory is invalidated before tool
mutation and republished only after verified outputs. Destroy checks managed state.

Ansible validates node schema, identity, producer, host uniqueness and exact address
selection. Its private configuration and callback prevent user environment overrides.
A successful process exit needs a complete successful host recap. Syntax-check does
not require node artifacts; check mode reports prediction.

## Versions and publication

The root `VERSION` selects one self-contained `releases/<version>.lock.json`,
which declares the exact Inframill target, drivers and dependency pins. Kit and engine must share major/minor; patches and prereleases
can differ. The image shares Kit's exact version and its lock must pin the declared
engine target. The sibling checkout does not select either release version.

The current drivers are OpenTofu and Ansible 0.2.0. Each binary embeds its version's
`release.json`; its executable path selects the implementation. Driver builds use
`drivers/releases.json`, independently of the image release. Synthetic test drivers
under `drivers/tests` exercise version coexistence and distinct contracts.
Blueprints are a standalone subproject under `blueprints/`. Its `releases.json`
selects production packages independently of Kit's version and declares each
blueprint's engine and driver requirements. Fixtures, examples, test configuration,
tasks and blueprint workflow checks live in that subproject too. One Python script validates
source metadata, offline schemas, driver requirement agreement and source seals,
then builds OCI packages, loads them into Docker and invokes `docker push`.
The publisher does not handle credentials or registry lifecycle. Driver implementation validation remains in
Kit's engine integration checks. OCI sources are architecture-independent; Kit
image indices contain both Linux architectures.

Image assembly reads prebuilt `drivers/dist/<name>/<version>/linux/<arch>/`
outputs and engine binaries. It checks recorded hashes, ELF architectures and
engine/tool compatibility without accessing driver source or the SDK checkout.
Each driver ships with its build record, corresponding sources and dependency
notices. Native development outputs use the same layout with the host OS/architecture.
Kit image push verifies existing bytes and rejects conflicting version tags. Blueprint
push uses standard Docker tag semantics. Publication is
manual; local acceptance uses a disposable registry.

## Container configuration and storage

The bundle copies the checked-in `runtime/inframill.yaml` unchanged to
`/etc/inframill/inframill.yaml`. It declares HTTP defaults, logging and all bundled
drivers with absolute executable, tool and data paths. Update this file when
changing bundled driver or tool versions. Local `task config` output is generated
separately with workstation paths. The image's default
command is `inframill server start --config /etc/inframill/inframill.yaml`. Users may replace
that entire file with a read-only bind mount. Configuration is read at startup;
the image has no merge, rewriting or automatic reload layer.

`/var/lib/inframill` is the writable data mount, independent of the configuration
directory. Users explicitly select a named volume or bind mount. Inframill 0.5.0-dev
resolves relative YAML paths from the selected config directory; absolute defaults
keep the storage destination stable. Persisted data survives container replacement,
while namespace secrets remain in memory. See the [Docker guide](../README.md#run-with-docker).

See [usage](usage.md) for command behavior and [releases](releases.md) for build and
publication commands.
