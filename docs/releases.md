# Building and publishing releases

## Build inputs

The repository has two independent source projects:

- [`providers/`](../providers/README.md) builds one executable per provider version for
  Linux AMD64 and ARM64. Source, metadata, the Python builder and tests live there.
- [`blueprints/`](../blueprints/README.md) builds architecture-independent OCI source
  packages using its own Python script, metadata, tasks and tests.

The root project assembles the Kit image from prebuilt artifacts. It never compiles
or rebuilds a provider. Prepare inputs explicitly:

```sh
task providers:test
task providers:build
task release:check
task bundle
```

`task build` aliases `task bundle`. Both require complete artifacts for each image
selection at `providers/dist/<name>/<version>/linux/<arch>/`. Build records include
binary and source checksums, the platform and the exact Inframill SDK pin. Image
assembly rejects missing artifacts, mismatched architecture, tampering, incompatible
engine pins and tool requirements. Provider sources and the SDK checkout are not
inputs to image assembly.

Prebuilt Inframill binaries must also exist in the pinned release layout. The
Dockerfile installs checksum-pinned OpenTofu and version-pinned Python/Ansible dependencies and
copies `runtime/inframill.yaml` unchanged. Provider paths in that configuration are
`/opt/inframill/providers/<name>/<version>/provider`.

| Setting | Purpose | Default |
| --- | --- | --- |
| `PROVIDERS_DIST_DIR` | Prebuilt provider input directory | `providers/dist` |
| `INFRAMILL_DIST_DIR` | Prebuilt engine input directory | `../inframill/dist` |
| `KIT_DIST_DIR` | Image output directory | `dist` |
| `REGISTRY` | Publication registry | `ghcr.io` |
| `NAMESPACE` | Publication namespace | `inframill` |

The image output is `KIT_DIST_DIR/bundles/<VERSION>/`. Existing image outputs are
immutable; explicitly move or remove unpublished development output before a
changed rebuild. Provider and blueprint scripts have independent `--dist` options.

## Publish

```sh
docker login ghcr.io -u YOUR_GITHUB_USERNAME
task bundle:push
```

This publishes the existing image to `REGISTRY/NAMESPACE/kit:<VERSION>`. Image
publication verifies the local OCI graph and image lock and rejects a remote tag
pointing to different content. It loads the verified OCI artifact into Docker and
uses `docker push`. Configure credentials and any insecure local registries in
Docker yourself; the script never logs in or creates registries. Image builds
never publish automatically.

Blueprints use their own workflow:

```sh
task blueprints:setup PYTHON=python3.14
task blueprints:build
docker login ghcr.io
task blueprints:push
```

Blueprint build loads each artifact into Docker; push invokes `docker push` for
its tag. Authentication and remote tag policy are handled by Docker and the
registry. `task blueprints:publish` performs build followed by push. Use
`-- --name pv-cloud --version 0.3.0` to select one release. Provider builds publish
no registry artifacts; their output is consumed by image assembly.

After publication, verify the immutable reference:

```sh
task bundle:verify REFERENCE=ghcr.io/inframill/kit@sha256:...
```

Create namespaces with `namespace create NAME` before use. Blueprint
fetch/show/get/delete require `--namespace`; fetches use that namespace's registry
credentials when configured, otherwise anonymous access. Supply private registry
credentials with `registry replace NAMESPACE --file FILE`; Docker login
does not configure the daemon. Kit image pulls use Docker credentials.

## Version policy

The root `VERSION` selects `releases/<version>.lock.json` for the image. Kit and
its pinned engine must share a major/minor line; patch releases may differ.
The current baseline is Kit and Inframill `0.18.0-dev`, with OpenTofu and Ansible
providers `0.3.0`. Provider and blueprint versions are independent of the image.

The 0.10 engine introduces explicit namespaces and namespace-owned storage.
Shared-root data from 0.9 is unsupported, with no automatic migration; preserve
the old data and select a fresh volume or directory. Namespace support does not
change Blueprint or provider formats.

The 0.11 engine adds all-namespace blueprint and credential metadata lists when
`--namespace` is omitted. Secret and registry lists now accept `--namespace NAME`
instead of a positional namespace and return namespace-qualified records in JSON.
Storage and mutation scopes remain unchanged from 0.10.

The 0.12 CLI moves resource commands directly under `inframill`, for example
`inframill instance list` and `inframill blueprint fetch`. Update scripts that use
`inframill server RESOURCE ...`; those routes have no compatibility aliases.
`server start`, `server version` and `other` utilities keep their existing paths.
RPC APIs, storage and provider contracts are unchanged by this CLI rearrangement.

The 0.13 CLI uses `get` for resource inspection and `blueprint resolve` for ready-only
resolution. Execution commands move from `command execution` to `execution` and use
`--instance UUID_OR_NAMESPACE/NAME`; `execution logs ID` replaces stdout/stderr verbs
and supports `--stream` and `--follow`. Credential replace/clear require `--namespace`.
Completion moves to `completion`, and the offline debug provider to `debug provider`.
Old paths and flags have no compatibility aliases. Instance and execution lists
support `--all`; command discovery can be scoped with `--instance`. The corresponding
`ListCommandDefinitions` RPC adds an optional `instance_id`; the Go method now takes
a `CommandDefinitionQuery`. Storage, blueprint format and Runmill protocol are
unchanged from 0.12, so the providers and pv-cloud remain at `0.3.0`.

The 0.14 CLI replaces `command list/get` with `provider commands [NAME]`, with no
compatibility aliases or `--instance` scope. Discovery lists cached commands from
all available providers and excludes built-in operations; use `instance delete`
for deletion. `ListCommandDefinitions` now takes an empty RPC request, with its
former `instance_id` field reserved; the Go method takes only the context.
The engine also fixes namespace lifecycle and credential cleanup consistency.
Storage, blueprint format and Runmill protocol remain compatible, so the providers
and pv-cloud remain at `0.3.0`.

The 0.15 engine makes namespace deletion asynchronous: deletion requests persist
across restarts, reject new work and allow admitted operations to finish. The
default cleanup worker removes idle flagged namespaces and retries failures.
Namespace lists expose `active`, `deleting` and `failed` status. Provider and
blueprint contracts are unchanged, so providers and pv-cloud remain at `0.3.0`.

The 0.16 engine requires `X-Namespace-ID` on every namespace-owned RPC and
`--namespace` on the corresponding CLI commands, including lists and ID lookups.
Instance names are local to the selected namespace; qualified names and
all-namespace resource queries are removed. Scoped responses omit namespace
identities. Instance and execution IDs are canonical uppercase ULIDs; old UUID
storage is rejected without migration, so upgrades require a fresh data directory.
Kit updates saved-plan and node-artifact validation to the ULID contract.

The 0.17 engine adds `GetNamespace` and `namespace get NAME`, returning the name
and lifecycle status without acquiring resource scope. Namespace administration
and provider discovery reject namespace headers/flags. Provider protocol and
blueprint bindings remain unchanged; their current development versions remain
`0.3.0`. Rebuild providers against the new SDK pin before assembling this image.

The 0.18 engine exposes a ready Blueprint's Values schema through the
`GetBlueprint` RPC and `blueprint resolve` CLI output. `blueprint get` remains
a metadata inspection command. Storage, Blueprint source format and provider
protocol are unchanged; providers and pv-cloud remain at `0.3.0`. Rebuild
providers against the new SDK pin before assembling this image.

Inframill 0.18.0-dev uses Runmill providers and requires `provider_settings.<id>` bindings containing a
major.minor `version` string and a nested `settings` object. `templates/` must
contain exactly one directory per configured ID, and the engine passes that
individual workspace to its provider. Kit uses IDs `opentofu` and `ansible`;
pv-cloud is `0.3.0`. Only the current development contract is supported.

The image lock selects provider **names and versions**, engine binaries/checksums,
and image platforms. [`runtime/Dockerfile`](../runtime/Dockerfile) owns the base
image digest, Debian snapshot, build timestamp, OpenTofu checksums, and Ansible
and Python dependency versions. Provider metadata
lives in each version's `release.json`; `providers/releases.json` selects versions
and pins their compiler and SDK dependency. Blueprint releases have their own
`blueprints/releases.json`.

To add a provider version, follow the [provider instructions](../providers/README.md#add-a-version),
then separately select it in the image lock and update `runtime/inframill.yaml`.
To add a blueprint, follow its [release instructions](../blueprints/README.md#add-a-release).
To add an image release, update `VERSION` and add its image lock. Exact versions
are required; changed published bytes require a new tag, including development tags.

`VERSION`, `KIT_VERSION` and `BUNDLE_VERSION` environment variables do not override
the root file. Builds do not infer versions from Git or change release metadata.

Update tool pins in `runtime/Dockerfile`, along with the provider's `tool_version`
and paths in `runtime/inframill.yaml` when needed. Image assembly checks the
provider's required tool version against the Dockerfile before building. Native
`task setup` reads the same Dockerfile defaults. The runtime helper installs
OpenTofu and downloads matching source archives; the Dockerfile installs Ansible
and its explicitly versioned dependencies with pip and runs `pip check`.

The selected release lock ships at `/usr/share/inframill/bundle.lock.json`.
Pip records installed package versions and download hashes in
`/usr/share/inframill/python-packages.json`; matching source archives and their
checksums are recorded under `/usr/share/inframill/sources/`. These records are
build outputs, not release-lock inputs.
