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
image installs the checksum-pinned OpenTofu and Python/Ansible dependencies and
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
`-- --name pv-cloud --version 0.2.0` to select one release. Provider builds publish
no registry artifacts; their output is consumed by image assembly.

After publication, verify the immutable reference:

```sh
task bundle:verify REFERENCE=ghcr.io/inframill/kit@sha256:...
```

Inframill's current blueprint consumer fetches anonymously; Docker login does
not enable private blueprint consumption. Kit image pulls use Docker credentials.

## Version policy

The root `VERSION` selects `releases/<version>.lock.json` for the image. Kit and
its pinned engine must share a major/minor line; patch releases may differ.
The current baseline is Kit and Inframill `0.7.0-dev`, with OpenTofu and Ansible
providers `0.3.0`. Provider and blueprint versions are independent of the image.

Inframill 0.7.0-dev uses Runmill providers and requires `provider_settings.<id>` bindings containing a
major.minor `version` string and a nested `settings` object. `templates/` must
contain exactly one directory per configured ID, and the engine passes that
individual workspace to its provider. Kit uses IDs `opentofu` and `ansible`;
pv-cloud is `0.2.0`. Only the current development contract is supported.

The image lock selects provider **names and versions**, engine binaries/checksums,
base image, Debian snapshot, timestamps, and the exact runtime tool dependencies.
It contains no provider implementations or Go compiler selection. Provider metadata
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

Image builds render pip's `requirements.lock` from `python_dependencies` in a
temporary directory. `python3 build.py lock-python REPORT.json` explicitly
updates those pins in the selected image lock from a resolved pip report. It
rejects an Ansible version differing from `ansible_core`; ordinary builds never
resolve new dependencies. The selected lock ships at
`/usr/share/inframill/bundle.lock.json`.
