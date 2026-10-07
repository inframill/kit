# Releasing Kit

## 0.26.0-dev

This release uses Inframill 0.26.0-dev (`8c69c3fbf0ac`). OpenTofu and Ansible
remain at provider version 0.4.0 and are rebuilt against this exact SDK revision;
the Runmill provider API, tool pins and Blueprint requirements are unchanged.

The engine adds persistent per-namespace resource events, read/replay and watch
APIs, JSON CLI commands, and optional size-based journal retention. Execution
events carry status, revision and optional PID/exit code. Credential events contain
counts only. Restart and expired replay positions explicitly require a refresh
of current resources; the feed is not a complete audit history.

Existing 0.25 command databases keep their schema. Missing event journals initialize
on open without reconstructing prior events. The older-schema restriction noted
for 0.25 still applies to upgrades from earlier releases. Retention stays disabled
by default; setting `engine.eventJournalMaxBytes` explicitly enables pruning.

Release checks exercise both providers' event summaries, CLI live streaming and
replay, namespace/credential isolation, bounded retention and restart refresh,
alongside the existing native and Linux AMD64/ARM64 workflows.

## 0.25.0-dev

This release uses Inframill 0.25.0-dev (`6efa420`) and OpenTofu/Ansible
providers 0.4.0. Compared with Kit 0.23.0-dev, the engine adds namespace-owned
scheduling and lifecycle management, tighter namespace/registry/secret isolation,
required command access modes, process-group signalling, PID/OS exit-code records,
and durable per-Instance execution revisions. The provider access modes reflect
actual workspace/artifact writes; only OpenTofu `validate` is read-only.

Runtime paths and the local pv-cloud Blueprint now select providers 0.4.0. Fetch
the updated Blueprint before creating new Instances. Existing Instance snapshots
retain their old provider requirements and rendered settings.

The engine rejects previous `commands.sqlite` schemas intact; it does not migrate
them. Preserve existing data and use a new empty data directory for this release.
Saved OpenTofu plans and node artifacts are bound to their producer version and
must be recreated with the new provider. For live infrastructure, retain the
existing state/backend and follow its normal recovery/import procedure before
any apply; a new Instance does not automatically adopt infrastructure.

Release acceptance includes native and Linux ARM64/AMD64 provider workflows,
reader overlap and writer fairness, scoped process-group kills, nullable PID/exit
outcomes, and revisions/history surviving daemon restart.

## Prepare, build and verify

Build the desired Inframill release in its checkout first. Kit consumes that
checkout's `VERSION`, Git revision and prebuilt binaries; it never pulls upstream
or changes the upstream version as part of a Kit release.

From Kit, after the one-time `task setup` for native test tools:

```sh
task release
```

This prepares release metadata, refreshes Linux AMD64/ARM64 and native providers,
builds the Kit image and runs `check-full`. Docker must be running. Provider outputs
that no longer match their sources or SDK pin are rebuilt; previous outputs are
preserved in `.previous-*` directories beside the replaced platform directory.
Unchanged provider outputs are reused. Failed provider builds preserve the previous output.

The Kit version defaults to the sibling Inframill `VERSION`. For an independent
Kit patch or prerelease on the same major/minor line:

```sh
task release -- --version 0.21.1-dev
```

To prepare only the metadata for review, without building or publishing:

```sh
task release:prepare
# Or: task release:prepare -- --version 0.21.1-dev
```

Preparation updates only these release records:

- `VERSION`: the selected Kit/image version.
- `releases/<version>.lock.json`: engine revision, four binary checksums, provider
  selections and Linux image platforms.
- `providers/releases.json`: the matching Inframill SDK version and revision.

`task inframill:copy` also generates a missing lock automatically before copying
the native binary, creating `releases/` if needed. It keeps the Kit version in
`VERSION` and reuses an existing lock. Use `task release:prepare` to select a new
upstream release or an independent Kit version.

Provider selections come from `providers/releases.json`. The upstream checkout
must have clean build inputs and all four macOS/Linux AMD64/ARM64 release binaries.
Preparation validates architectures and the native engine version before writing.
Existing release locks are immutable: identical inputs are safe to prepare again;
changed inputs require a new Kit version. Historical locks remain release records.

Routine engine releases need no README, blueprint or provider implementation version
edits. API or tool changes can still require code/configuration updates and tests.
The Go compiler pin remains explicit in `providers/releases.json`.

## Publish

Review the generated metadata and successful checks, then publish explicitly:

```sh
docker login ghcr.io
task bundle:push
```

This publishes the existing image to `REGISTRY/NAMESPACE/kit:<VERSION>`. Publication
verifies its OCI graph and release lock, uses `docker push`, and rejects a remote tag
pointing to different content. The builder never logs in or publishes automatically.
Verify a published image by its immutable reference:

```sh
task bundle:verify REFERENCE=ghcr.io/inframill/kit@sha256:...
```

An existing `dist/bundles/<VERSION>/` is immutable. Resume with checks or push after a
successful build; choose a new version for changed published content. If discarding
an unpublished build, move it aside explicitly before rebuilding.

## Build inputs

Root image assembly consumes prebuilt providers and engines. It does not compile
providers or read the SDK checkout. Provider records contain source/binary checksums,
platforms, tool requirements and the exact SDK pin; incompatible or corrupt inputs reject.

| Setting | Purpose | Default |
| --- | --- | --- |
| `INFRAMILL_SOURCE` | Clean checkout for preparation and provider builds | `../inframill` |
| `INFRAMILL_DIST_DIR` | Prebuilt engine binaries | `../inframill/dist` |
| `PROVIDERS_DIST_DIR` | Prebuilt provider inputs for image assembly | `providers/dist` |
| `KIT_DIST_DIR` | Image output directory | `dist` |
| `REGISTRY` | Publication registry | `ghcr.io` |
| `NAMESPACE` | Publication namespace | `inframill` |

For a non-default checkout, set both `INFRAMILL_SOURCE` and `INFRAMILL_DIST_DIR`.
The full `task release` workflow writes providers to `PROVIDERS_DIST_DIR` and uses
those same outputs for image assembly. Standalone provider builds accept `--dist`.

Individual steps remain available:

```sh
task release:prepare
task providers:build -- --refresh
task providers:build -- --native --refresh
task inframill:copy
task check-fast
task bundle
task check-full
```

## Ownership of version pins

`VERSION` selects one self-contained image lock. Kit and its engine share major/minor;
patches and prereleases can differ. Environment variables never override `VERSION`.
Normal build/push commands do not infer versions from Git or modify release metadata.

Provider versions change independently. See [provider versioning](../providers/README.md#add-a-version)
when changing provider implementations. Local blueprints have no release manifest;
edit sources and fetch `build.in/pv-cloud` or `build.in/hello-world` again.

[`runtime/Dockerfile`](../runtime/Dockerfile) owns tool, base image, Debian and Python
dependency pins. Tool changes also need matching provider `release.json` requirements
and paths in `runtime/inframill.yaml`. `task setup` reads these same tool pins.

The image carries its lock at `/usr/share/inframill/bundle.lock.json`, provider build
records and source/license notices. Python package versions/download hashes live in
`/usr/share/inframill/python-packages.json`; matching source archives and checksums
live in `/usr/share/inframill/sources/`.
