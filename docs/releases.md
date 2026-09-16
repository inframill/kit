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

To package one blueprint, use `task blueprint:package BLUEPRINT=pv-cloud VERSION=0.1.0`.
Publish it with `task blueprint:push BLUEPRINT=pv-cloud VERSION=0.1.0`.

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
The current baseline is Kit `0.3.0` with Inframill `0.3.0`. A Kit-only fix can ship as
`0.3.1` while still using Inframill `0.3.0`. Moving to Inframill `0.4.x` requires a
Kit `0.4.x` release after compatibility checks. The bundle lock always pins the exact
engine version and binary checksums.

The build tooling enforces this policy. Standalone Kit builds reject a different
major/minor version from the compiled Inframill target. Image builds, pushes and
native engine copies additionally require the image and Kit versions to match
exactly, and the locked engine to equal Kit's declared target. Run
`task release:check` to validate the selected release without building or pushing;
`task check-fast` includes this check.

Blueprints and drivers use independent versions. The current pv-cloud, OpenTofu
driver and Ansible driver remain `0.1.0`.

## Adding releases

New blueprint sources go in `blueprints/<name>/versions/<version>`. Their release
files declare core/driver requirements and a sealed source digest, obtained with
`task blueprint:source-digest`. Add actual production selections to
`releases/sets/default.yaml`. New driver releases require an explicit factory and
metadata; a YAML file alone never registers code. Locks and deterministic source
hashes prevent accidental content replacement.

| Variable | Selects | Default |
| --- | --- | --- |
| `VERSION` | Blueprint release | `0.1.0` |
| `DRIVER_VERSION` | Driver for `task driver:check` | `0.1.0` |
| `KIT_VERSION` | Executable release for `task build` | `0.3.0` |
| `BUNDLE_VERSION` | Image build, push, full checks and native engine copy | `0.3.0` |

For example, `task bundle BUNDLE_VERSION=0.3.0` and
`task bundle:push BUNDLE_VERSION=0.3.0` select the same image release. The bundle lock
pins the Kit executable version; `KIT_VERSION` applies to the standalone build.

`KIT_DIST_DIR` changes the output root for packaging, image builds, pushes and checks.
Outputs are immutable: remove an unpublished development output explicitly before
rebuilding changed sources under the same version.
