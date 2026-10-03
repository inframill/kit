# Local blueprints

`pv-cloud` and `hello-world` are local source directories for the pinned Inframill release.
Each has a `latest/` directory containing `blueprint.yaml` and `templates/`.
Edit those files directly; there
is no build, packaging, release manifest or publication step.

## Configure the daemon

Native development: `task config` includes the absolute path to this directory
in `engine.localBlueprintPaths`. For a custom configuration, use:

```yaml
engine:
  localBlueprintPaths:
    - /absolute/path/to/kit/blueprints
```

Relative paths resolve beside the daemon configuration file. Restart after changing
the search paths. The daemon reads the sources, so remote clients must use paths
available on the daemon host. `task docker:up` mounts this directory read-only at
`/opt/inframill/blueprints`, matching the checked-in Docker configurations.

## Hello world

This blueprint requires the built-in debug provider. The repository's
`.inframill.yaml` enables it. For a custom configuration, add:

```yaml
engine:
  providers:
    debug:
      path: /usr/local/bin/inframill
      args: [debug, provider]
```

For a native daemon, use the absolute path to `bin/inframill`. The image's default
`runtime/inframill.yaml` enables OpenTofu and Ansible; add debug to use hello-world.

```sh
inframill namespace create demo
inframill blueprint fetch build.in/hello-world --namespace demo
inframill instance init hello-world --namespace demo \
  --blueprint build.in/hello-world \
  --values-file blueprints/examples/hello-world.json
printf '%s\n' '{"message":"Hello, world!"}' | \
  inframill instance exec hello-world debug.echo --namespace demo --arguments-file - --wait
```

The example Values render `workspace/debug/message.txt` as `Hello, world!`.
`debug.echo` takes its message separately through command Arguments.

## pv-cloud

`build.in/pv-cloud` uses the Kit OpenTofu and Ansible providers, both version `0.3.0`.
The repository and image configurations enable both providers. [Example Values](examples/pv-cloud.json) demonstrate
mixed AMD64/ARM64 nodes. Single-architecture examples live in `../tests/fixtures/blueprints/examples/`.

```sh
inframill namespace create production
inframill blueprint fetch build.in/pv-cloud --namespace production
inframill instance init example --namespace production \
  --blueprint build.in/pv-cloud --values-file blueprints/examples/pv-cloud.json
```

See [usage](../docs/usage.md) for secrets and provider commands. OpenTofu state uses
the local backend in the Instance's persisted `workspace/opentofu/terraform.tfstate`;
keep and back up the daemon data volume.

## Tags and local editing

The reserved `build.in` host selects local files; it never contacts a registry.
The daemon resolves `<allowed-root>/<repository>/<tag>/`. Kit's sources are:

```text
blueprints/
  hello-world/latest/blueprint.yaml
  hello-world/latest/templates/...
  pv-cloud/latest/blueprint.yaml
  pv-cloud/latest/templates/...
```

`build.in/pv-cloud` is shorthand for `build.in/pv-cloud:latest`. To maintain another
tag, add a complete source directory such as `pv-cloud/dev/` or `pv-cloud/0.3.0/`,
then fetch `build.in/pv-cloud:dev` or `build.in/pv-cloud:0.3.0`. Repository paths can
be nested and use OCI lowercase naming rules. Tags support dotted versions.
Tag directories are mandatory; the daemon never falls back to a different tag or
files directly under the repository directory. Ambiguous matches and links reject.

After editing, fetch that tag again and initialize a new Instance. Fetch takes a
validated snapshot: unchanged content keeps its digest; edits update only that
tag. Fetching another tag does not change `latest`. Existing Instances retain their
original snapshots, and multiple tags can share one digest.

Use `build.in/NAME@sha256:<digest>` to initialize from a cached revision. A pinned
fetch checks the source in `latest/` against that digest without moving tags.
Deleting a cached pin removes its tag mappings but preserves source files and Instances.

The former `local:NAME` references and untagged source directories are unsupported.
Old `local:` catalogue entries reject without automatic conversion. Preserve old
data; use a fresh data directory for the new daemon if the existing one contains
those entries. Do not delete existing Instance state to make the upgrade start.

## Verification

From the Kit root:

```sh
task inframill:copy
BLUEPRINT=hello-world python3 tests/check.py blueprint:check
# Requires task setup and task providers:build -- --native:
BLUEPRINT=pv-cloud python3 tests/check.py blueprint:check
# All native workflows, including revision and cache checks:
python3 tests/check.py native
```

Integration checks fetch local sources into disposable daemon data, render the
examples and execute debug commands or Ansible syntax checks and OpenTofu mocks.
They create no cloud resources and require no blueprint registry. `task check-full`
also exercises local fixtures through both Linux runtime images; its disposable
registry is used only to verify Kit image publication.

