# Local blueprints

`pv-cloud` and `hello-world` are local source directories for the pinned Inframill release.
Each contains `blueprint.yaml` and `templates/`. Edit these files directly; there
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
inframill blueprint fetch local:hello-world --namespace demo
inframill instance init hello-world --namespace demo \
  --blueprint local:hello-world \
  --values-file blueprints/examples/hello-world.json
printf '%s\n' '{"message":"Hello, world!"}' | \
  inframill instance exec hello-world debug.echo --namespace demo --arguments-file - --wait
```

The example Values render `workspace/debug/message.txt` as `Hello, world!`.
`debug.echo` takes its message separately through command Arguments.

## pv-cloud

`local:pv-cloud` uses the Kit OpenTofu and Ansible providers, both version `0.3.0`.
Enable their definitions from `runtime/inframill.yaml` when using the repository's
debug-only configuration. [Example Values](examples/pv-cloud.json) demonstrate
mixed AMD64/ARM64 nodes. Single-architecture examples live in `../tests/fixtures/blueprints/examples/`.

```sh
inframill namespace create production
inframill blueprint fetch local:pv-cloud --namespace production
inframill instance init example --namespace production \
  --blueprint local:pv-cloud --values-file blueprints/examples/pv-cloud.json
```

See [usage](../docs/usage.md) for secrets and provider commands. OpenTofu state uses
the local backend in the Instance's persisted `workspace/opentofu/terraform.tfstate`;
keep and back up the daemon data volume.

## Edit and fetch again

After changing a source, run `blueprint fetch local:NAME` again and initialize a
new Instance. Fetch takes a validated snapshot: unchanged content keeps its digest,
edits create a new revision, and existing Instances keep their original snapshots.
Use `local:NAME@sha256:<digest>` to select a specific cached revision. Local references
have no version tag. Source names must be direct children of an allowed root and
contain only ASCII letters, digits or hyphens; ambiguous matches and links reject.

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

