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
inframill blueprint fetch built.in/hello-world --namespace demo
inframill instance init hello-world --namespace demo \
  --blueprint built.in/hello-world \
  --values-file blueprints/examples/hello-world.json
printf '%s\n' '{"message":"Hello, world!"}' | \
  inframill instance exec hello-world debug.echo --namespace demo --arguments-file - --wait
```

The example Values render `workspace/debug/message.txt` as `Hello, world!`.
`debug.echo` takes its message separately through command Arguments.

## pv-cloud

The current blueprint release is `built.in/pv-cloud:0.5.0`; `latest` has the same
content. It uses the Kit OpenTofu and Ansible providers, both version `0.4.0`.
Resource names use the immutable Instance name through `{{ (instance).Name }}`;
Values do not contain `instance_name`. This requires an Inframill build with the
`instance` template function (SPEC-0085).
For existing deployments, keep the Instance name equal to the previous
`instance_name` value to preserve generated resource names. Existing Instances
retain their saved blueprint sources until explicitly replaced.
The repository and image configurations enable both providers. [Example Values](examples/pv-cloud.json)
define two AMD64 Docker nodes named `data` and `worker`, both assigned `common` and
`docker`. Docker installation and service checks belong to the dedicated `docker`
role; `common` is available for shared guest setup. ARM64 and AMD64 examples live
in `../tests/fixtures/blueprints/examples/`.
The guest cloud-config disables optional vendor-data and owns SSH user/key setup,
avoiding deprecated password settings injected by Hetzner. Ansible requires
cloud-init to complete successfully. Role plays select inventory group members,
so node names never assign services. A node named `data` only gets PostgreSQL when
assigned the `data` role. Cloud labels identify nodes with `node=<name>`.
The reserved inventory names `all` and `ungrouped` are rejected as node names.

Each node gets one DNS-only Cloudflare A record pointing to its public IPv4:
`<node>-<lowercase-instance-id>.<zone>`. DNS names are published in `server_fqdns`
and the shared node artifact. Names use the immutable Instance ID, so separate
Instances do not collide. Node names must be DNS labels of at most 36 characters;
underscores and trailing hyphens are rejected.

Set `cloudflare_zone_id` to the zone's 32-character ID and
`cloudflare_secret_ref` to a namespace secret name, such as `cloudflare`. The
example zone ID is a placeholder. Store the API token separately with
`inframill secret upsert --namespace production --file /private/path/cloudflare.json`,
where the file contains `{"name":"cloudflare","value":"REPLACE_WITH_API_TOKEN"}`.
Scope the token to the selected zone with permission to edit DNS records and read
the zone. Kit injects it as `CLOUDFLARE_API_TOKEN` for plan/apply/destroy; the token
is never rendered into Values, tfvars or provider configuration. The Cloudflare
OpenTofu provider is pinned to `5.27.0`.

```sh
inframill namespace create production
inframill blueprint fetch built.in/pv-cloud:0.5.0 --namespace production
inframill instance init example --namespace production \
  --blueprint built.in/pv-cloud:0.5.0 --values-file blueprints/examples/pv-cloud.json
```

See [usage](../docs/usage.md) for secrets and provider commands. OpenTofu state uses
the local backend in the Instance's persisted `artifacts/opentofu-terraform.tfstate`,
which survives rerender. Run `opentofu.init` after rerender and keep and back up the
daemon data volume. Existing Instances must run init with the updated provider
before rerender to migrate workspace state into artifacts.

## Tags and local editing

The reserved `built.in` host selects local files; it never contacts a registry.
The daemon resolves `<allowed-root>/<repository>/<tag>/`. Kit's sources are:

```text
blueprints/
  hello-world/latest/blueprint.yaml
  hello-world/latest/templates/...
  pv-cloud/latest/blueprint.yaml
  pv-cloud/latest/templates/...
  pv-cloud/0.4.0/blueprint.yaml
  pv-cloud/0.4.0/templates/...
  pv-cloud/0.5.0/blueprint.yaml
  pv-cloud/0.5.0/templates/...
```

`built.in/pv-cloud` is shorthand for `built.in/pv-cloud:latest`. To maintain another
tag, add a complete source directory such as `pv-cloud/dev/`,
then fetch `built.in/pv-cloud:dev`. Repository paths can
be nested and use OCI lowercase naming rules. Tags support dotted versions.
Tag directories are mandatory; the daemon never falls back to a different tag or
files directly under the repository directory. Ambiguous matches and links reject.

After editing, fetch that tag again and initialize a new Instance. Fetch takes a
validated snapshot: unchanged content keeps its digest; edits update only that
tag. Fetching another tag does not change `latest`. Existing Instances retain their
original snapshots, and multiple tags can share one digest.

Use `built.in/NAME@sha256:<digest>` to initialize from a cached revision. A pinned
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

