# Building and publishing releases

## Build inputs

The repository has two independent source projects:

- [`drivers/`](../drivers/README.md) builds one executable per driver version for
  Linux AMD64 and ARM64. Source, metadata, the Python builder and tests live there.
- [`blueprints/`](../blueprints/README.md) builds architecture-independent OCI source
  packages using its own Python script, metadata, tasks and tests.

The root project assembles the Kit image from prebuilt artifacts. It never compiles
or rebuilds a driver. Prepare inputs explicitly:

```sh
task drivers:test
task drivers:build
task release:check
task bundle
```

`task build` aliases `task bundle`. Both require complete artifacts for each image
selection at `drivers/dist/<name>/<version>/linux/<arch>/`. Build records include
binary and source checksums, the platform and the exact Inframill SDK pin. Image
assembly rejects missing artifacts, mismatched architecture, tampering, incompatible
engine pins and tool requirements. Driver sources and the SDK checkout are not
inputs to image assembly.

Prebuilt Inframill binaries must also exist in the pinned release layout. The
image installs the checksum-pinned OpenTofu and Python/Ansible dependencies and
copies `runtime/inframill.yaml` unchanged. Driver paths in that configuration are
`/opt/inframill/drivers/<name>/<version>/driver`.

| Setting | Purpose | Default |
| --- | --- | --- |
| `DRIVERS_DIST_DIR` | Prebuilt driver input directory | `drivers/dist` |
| `INFRAMILL_DIST_DIR` | Prebuilt engine input directory | `../inframill/dist` |
| `KIT_DIST_DIR` | Image output directory | `dist` |
| `REGISTRY` | Publication registry | `ghcr.io` |
| `NAMESPACE` | Publication namespace | `inframill` |

The image output is `KIT_DIST_DIR/bundles/<VERSION>/`. Existing image outputs are
immutable; explicitly move or remove unpublished development output before a
changed rebuild. Driver and blueprint scripts have independent `--dist` options.

## Publish

```sh
docker login ghcr.io -u YOUR_GITHUB_USERNAME
task bundle:push
```

This publishes the existing image to `REGISTRY/NAMESPACE/kit:<VERSION>`. Image
publication verifies the local OCI graph and image lock and rejects a remote tag
pointing to different content. `PLAIN_HTTP=true` is restricted to explicit local
integration registries. Image builds never log in or publish automatically.

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
`-- --name pv-cloud --version 0.2.0` to select one release. Driver builds publish
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
The current baseline is Kit and Inframill `0.3.0-dev`, with OpenTofu and Ansible
drivers `0.2.0`. Driver and blueprint versions are independent of the image.

The image lock selects driver **names and versions**, engine binaries/checksums,
base image, Debian snapshot, timestamps, and the exact runtime tool dependencies.
It contains no driver implementations or Go compiler selection. Driver metadata
lives in each version's `release.json`; `drivers/releases.json` selects versions
and pins their compiler and SDK dependency. Blueprint releases have their own
`blueprints/releases.json`.

To add a driver version, follow the [driver instructions](../drivers/README.md#add-a-version),
then separately select it in the image lock and update `runtime/inframill.yaml`.
To add a blueprint, follow its [release instructions](../blueprints/README.md#add-a-release).
To add an image release, update `VERSION` and add its image lock. Exact versions
are required; changed published bytes require a new tag, including development tags.

`VERSION`, `KIT_VERSION` and `BUNDLE_VERSION` environment variables do not override
the root file. Builds do not infer versions from Git or change release metadata.

Image builds render pip's `requirements.lock` from `python_dependencies` in a
temporary directory. `python3 scripts/lock-python.py REPORT.json` explicitly
updates those pins in the selected image lock from a resolved pip report. It
rejects an Ansible version differing from `ansible_core`; ordinary builds never
resolve new dependencies. The selected lock ships at
`/usr/share/inframill/bundle.lock.json`.
