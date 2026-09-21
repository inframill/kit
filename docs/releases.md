# Building and publishing releases

Run these commands from the repository root. Builds and pushes are separate manual
steps. `bundle` is the build task name for the Kit image.

## Package and push

```sh
task blueprints:package
task bundle

docker login ghcr.io -u YOUR_GITHUB_USERNAME
task blueprints:push
task bundle:push
```

To package one blueprint, use `task blueprint:package BLUEPRINT=pv-cloud BLUEPRINT_VERSION=0.2.0`.
Publish it with `task blueprint:push BLUEPRINT=pv-cloud BLUEPRINT_VERSION=0.2.0`.

Builds never push. Push uploads the existing verified OCI bytes and rejects a tag
pointing to different content. `REGISTRY` and `NAMESPACE` override the defaults;
`PLAIN_HTTP=true` is available for a local test registry. Default publication includes
pv-cloud only. Multi-version local-resource blueprints and synthetic driver factories
are test fixtures and are not included in the production catalog/release set.

`task bundle:push` publishes to `ghcr.io/inframill/kit:<version>` by default.
GHCR image/package names do not require matching Git repositories. The image
source label points at `inframill/kit`. Configure package visibility/access in GitHub.
Inframill's current blueprint consumer fetches anonymously;
private blueprint consumption is not provided by Docker login. Kit image pulls use
normal Docker authentication.

After publication, use the printed immutable digests:

```sh
task blueprint:verify BLUEPRINT=pv-cloud REFERENCE=ghcr.io/inframill/blueprints/pv-cloud@sha256:...
task bundle:verify REFERENCE=ghcr.io/inframill/kit@sha256:...
```

## Version policy

Kit and its image share a version and follow Inframill's major/minor release line.
The current development baseline is Kit `0.2.0-dev` with Inframill `0.2.0-dev`.
Kit patch releases may differ from the engine within the same major/minor line.
Moving to Inframill `0.3.x` requires a Kit `0.3.x` release after compatibility checks.
The bundle lock always pins the exact engine version and binary checksums.
Development tags are also immutable here: changed binary bytes require a new
version/tag, such as `0.2.0-dev.1`, rather than overwriting a published `0.2.0-dev`.

The root `VERSION` selects `releases/kits/<version>.yaml`, whose `inframill_version`
declares the exact engine target. There is no separate engine version constant in
Go. Standalone Kit builds reject a different major/minor version from this target.
For example, Kit `0.2.3` may target Inframill `0.2.1`, but cannot target `0.3.0`.
Image builds, pushes and native engine copies additionally require the image and
Kit versions to match exactly, and the locked engine to equal Kit's declared target. Run
`task release:check` to validate the selected release without building or pushing;
`task check-fast` includes this check.

Blueprint packaging checks its declared engine requirement against that same exact
target. Changing a descriptor declares compatibility; verify the driver API and run
`task check-full` before releasing against a new engine.

Blueprints and drivers use independent versions. The current pv-cloud, OpenTofu
driver and Ansible driver are `0.2.0`, reflecting the new blueprint and driver contract.
Production sources and descriptors contain only maintained releases. Git preserves
retired releases. The `0.1.0` and `0.2.0` versions under `tests/fixtures` exercise
multiple versions using the current protocol; they are not historical production releases.

## Adding releases

New blueprint sources go in `blueprints/<name>/versions/<version>`. Their release
files declare core/driver requirements and a sealed source digest, obtained with
`task blueprint:source-digest`. Add actual production selections to
`releases/sets/default.yaml`. New driver releases require an explicit factory and
metadata; a YAML file alone never registers code. Locks and deterministic source
hashes prevent accidental content replacement.

The root [`VERSION`](../VERSION) file is authoritative for Kit builds, image builds
and pushes, native engine copies and full checks. It contains one SemVer value with
an optional final newline. Missing/invalid content, whitespace and a `v` prefix
reject before building. `VERSION`, `KIT_VERSION` and `BUNDLE_VERSION` task arguments
or environment values cannot override it. Direct `go build` uses `0.2.0-dev`;
`task build` embeds the file's value.

To prepare a Kit release, edit `VERSION` and add matching
`releases/kits/<version>.yaml` and `releases/bundles/<version>.lock.yaml` files.
Keep the declared core target aligned, then run `task release:check`, `task build`
and `task bundle`. Commit the release inputs before publishing with
`task bundle:push`. Builds never rewrite `VERSION`, infer a Git version or publish.
The file does not automatically track the sibling project's development version.
Release descriptors and OCI tags retain their exact version constraints (no build
metadata suffix).

| Variable | Selects | Default |
| --- | --- | --- |
| `BLUEPRINT_VERSION` | Blueprint release | `0.2.0` |
| `DRIVER_VERSION` | Driver for `task driver:check` | `0.2.0` |

`task bundle` and `task bundle:push` use the same release from `VERSION`.

`KIT_DIST_DIR` changes the output root for packaging, image builds, pushes and checks.
Outputs are immutable: remove an unpublished development output explicitly before
rebuilding changed sources under the same version.
