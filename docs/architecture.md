# Kit architecture

Kit 0.6.0-dev targets the Inframill 0.6.0-dev API. The `inframill-kit` executable hosts
drivers. The `ghcr.io/inframill/kit` image bundles that executable with the engine
and tools; build tasks call this image a bundle. Blueprints are separate OCI packages.

## Ownership

- Inframill owns Instances, Values validation/rendering, project secret snapshots,
  queues, persistence, history and the external driver transport.
- `cmd/inframill-kit` consumes the configured prefix and calls upstream `driver.Serve`.
  `internal/catalog` selects explicit implementation factories by exact name/version.
- `internal/drivers/opentofu` and `internal/drivers/ansible` own separate typed
  static settings, rendered settings, arguments, prepared commands and typed outcomes.
- `internal/artifacts` owns bounded rooted file access, source hashes and the shared
  node record. `internal/toolrun` owns subprocesses, restricted environments,
  bounded machine output and filtered diagnostics.
- `cmd/kit-build`, `internal/packaging` and `scripts/` own local configuration,
  authoring, OCI packaging, Kit image assembly and manual publication.

## Command lifecycle

`ValidateSettings` examines only the driver's static settings object. Engine
renders Values into each configured driver's workspace, including a strict
`kit-driver.json`. `Prepare` validates arguments/settings and reads required files
without effects or credentials. For run, the fresh driver process prepares again,
then Execute receives the project's secret snapshot.

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

The current catalog contains OpenTofu and Ansible 0.2.0. Explicit factories select
code; embedded release descriptors supply versions/tool pins. Synthetic test factories
have different settings and argument contracts to exercise future version coexistence.
The default blueprint set contains pv-cloud; local-resource variants are fixtures.

A blueprint release pins core version and exact configured driver requirements.
Packaging validates the unified blueprint definition and each implementation's settings,
required settings templates, Values schema and the sealed source layer. OCI sources
are architecture-independent; Kit image indices contain both Linux architectures.

Image assembly verifies the clean sibling source revision before compiling Kit,
then copies checksum/architecture-verified core binaries from the versioned sibling
dist. Native copying verifies the corresponding host binary too. The bundle task
never compiles Inframill. Build records capture source, dependency and binary hashes.
Push verifies existing bytes and rejects conflicting version tags. Publication is
manual; local acceptance uses a disposable registry.

## Container configuration and storage

The bundle generates `/etc/inframill/inframill.yaml` through the shared configuration
builder, selecting the locked drivers and absolute executable/data paths. Its default
command is `inframill server start --config /etc/inframill/inframill.yaml`. Users may replace
that entire file with a read-only bind mount. Configuration is read at startup;
the image has no merge, rewriting or automatic reload layer.

`/var/lib/inframill` is the writable data mount, independent of the configuration
directory. Users explicitly select a named volume or bind mount. Inframill 0.6.0-dev
resolves relative YAML paths from the selected config directory; absolute defaults
keep the storage destination stable. Persisted data survives container replacement,
while project secrets remain in memory. See the [Docker guide](../README.md#run-with-docker).

See [usage](usage.md) for command behavior and [releases](releases.md) for build and
publication commands.
