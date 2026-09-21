# Building and publishing releases

Run these commands from the repository root. Builds and pushes are separate manual
steps. `bundle` is the build task name for the Kit image.

## Package and push

```sh
task blueprints:build
task bundle

docker login ghcr.io -u YOUR_GITHUB_USERNAME
task blueprints:push
task bundle:push
```

Blueprint packaging is an independent [subproject](../blueprints/README.md).
Run `task blueprints:setup PYTHON=python3.14` once to install its dependencies.
`task blueprints:build` and `task blueprints:push` select every entry in
`blueprints/releases.json`; `task blueprints:publish` performs both stages.
Use `-- --name pv-cloud --version 0.2.0` to select one release.
The Python script runs without Kit's Go tooling or release locks and produces one
portable OCI source artifact for AMD64 and ARM64.

Blueprint builds load the OCI artifacts into Docker; push invokes `docker push`
for each tag. Run `docker login` yourself. Use the same `REGISTRY` and `NAMESPACE`
for build and push. Docker and the registry control authentication, TLS and remote
tag overwrites. Kit bundle publication still rejects conflicting tags and supports
`PLAIN_HTTP=true` for local integration registries. Production selection
currently includes pv-cloud only. Local-resource variants are isolated fixtures.

`task bundle:push` publishes to `ghcr.io/inframill/kit:<version>` by default.
GHCR image/package names do not require matching Git repositories. The image
source label points at `inframill/kit`. Configure package visibility/access in GitHub.
Inframill's current blueprint consumer fetches anonymously;
private blueprint consumption is not provided by Docker login. Kit image pulls use
normal Docker authentication.

After publication, use the printed immutable digests:

```sh
BLUEPRINT=pv-cloud REFERENCE=ghcr.io/inframill/blueprints/pv-cloud@sha256:... \
  python3 scripts/check.py blueprint:verify
task bundle:verify REFERENCE=ghcr.io/inframill/kit@sha256:...
```

## Kit 0.3.0-dev

Kit and its image now target Inframill `0.3.0-dev`, pinned to revision
`07440bac8583f0b3d0dcce7be0656a8272595c4d`. The engine adds `inframill server version`
and the `GetServerVersion` RPC; image checks verify that the running daemon matches
the release pin. All four engine binary paths and checksums have been refreshed.
The default configuration remains included. Driver and blueprint versions stay
`0.2.0`, with blueprint engine requirements updated to `0.3.0-dev`.

## Version policy

Kit and its image share a version and follow Inframill's major/minor release line.
The current development baseline is Kit `0.3.0-dev` with Inframill `0.3.0-dev`.
Kit patch releases may differ from the engine within the same major/minor line.
Moving to Inframill `0.4.x` requires a Kit `0.4.x` release after compatibility checks.
The bundle lock always pins the exact engine version and binary checksums.
Development tags are also immutable here: changed binary bytes require a new
version/tag, such as `0.3.0-dev.1`, rather than overwriting a published `0.3.0-dev`.

The root `VERSION` selects one file, `releases/<version>.lock.json`, for both Kit
and its image. Its `inframill.version` declares the exact engine target. There is
no separate engine version constant in Go. Standalone Kit builds reject a
different major/minor version from this target.
For example, Kit `0.2.3` may target Inframill `0.2.1`, but cannot target `0.3.0`.
The release's `version` must match `VERSION`; Kit and the image use that same
value. Image builds, pushes and native engine copies consume the same lock. Run
`task release:check` to validate the selected release without building or pushing;
`task check-fast` includes this check.

Blueprint packaging uses its own `blueprints/releases.json`, including an explicit
engine compatibility target for each release. Verify driver compatibility and run
`task check-full` before releasing against a new engine.

Blueprints and drivers use independent versions. The current pv-cloud, OpenTofu
driver and Ansible driver are `0.2.0`, reflecting the new blueprint and driver contract.
Production sources and descriptors contain only maintained releases. Git preserves
retired releases. The `0.1.0` and `0.2.0` versions under `blueprints/tests/fixtures` exercise
multiple versions using the current protocol; they are not historical production releases.

## Adding releases

New blueprint sources go in `blueprints/<name>/<version>`. Add their
metadata and sealed source digest to `blueprints/releases.json`, following the
[Blueprint release instructions](../blueprints/README.md#add-a-release).
New drivers require both an explicit implementation factory and an entry in the
release file's `drivers` list; metadata alone never registers code. Locks and
deterministic source hashes prevent accidental content replacement.

The root [`VERSION`](../VERSION) file is authoritative for Kit builds, image builds
and pushes, native engine copies and full checks. It contains one SemVer value with
an optional final newline. Missing/invalid content, whitespace and a `v` prefix
reject before building. `VERSION`, `KIT_VERSION` and `BUNDLE_VERSION` task arguments
or environment values cannot override it. Direct `go build` embeds `VERSION` and
the selected release's driver metadata; `task build` uses the same version.

To prepare a Kit release, edit `VERSION` and add `releases/<version>.lock.json`.
The file contains one driver selection, the engine version and source revision,
one `inframill.binaries` map keyed by OS/architecture (for example `linux/amd64`),
and the image, Go toolchain, Debian snapshot and timestamp pins. Each binary entry
retains its path relative to the sibling `dist` directory and its SHA-256 checksum.
The same file contains complete driver definitions,
OpenTofu downloads and source pins, and every Python package version, distribution
hash and source archive. There are no shared release descriptors or dependency
lock files. Driver and blueprint versions remain independent of the Kit version.

Builds generate pip's `requirements.lock` in a temporary build directory from
`python_dependencies`; it is not a second source of release metadata. The image
includes the selected release at `/usr/share/inframill/bundle.lock.json`.
`python3 scripts/lock-python.py REPORT.json` explicitly updates only the selected
release's Python pins from a resolved pip report. It rejects an Ansible version
that differs from `ansible_core`. Normal builds never resolve new versions.

Keep the declared core target aligned, then run `task release:check`, `task build`
and `task bundle`. Commit the release inputs before publishing with
`task bundle:push`. Builds never rewrite `VERSION`, infer a Git version or publish.
The file does not automatically track the sibling project's development version.
Release descriptors and OCI tags retain their exact version constraints (no build
metadata suffix).

| Variable | Selects | Default |
| --- | --- | --- |
| `BLUEPRINT_VERSION` | Blueprint for `scripts/check.py` integration checks | `0.2.0` |
| `DRIVER_VERSION` | Driver for `task driver:check` | `0.2.0` |

`task bundle` and `task bundle:push` use the same release from `VERSION`.

`KIT_DIST_DIR` changes the output root for Kit image builds, pushes and integration
checks. Standalone blueprint commands use `--dist`, defaulting to `blueprints/dist`.
Outputs are immutable: remove an unpublished development output explicitly before
rebuilding changed sources under the same version.
