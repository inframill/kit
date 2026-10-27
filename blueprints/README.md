# Local blueprints

`pv-cloud` and `hello-world` are local source directories for the pinned Inframill release.
Each has a `latest/` directory containing `blueprint.yaml` and `templates/`.
Edit those files directly; there
is no build, packaging, release manifest or publication step.

Inframill `0.1.22-dev` requires `command_arguments` in every Blueprint. It maps
qualified configured provider IDs to JSON object schemas and explicitly selects
the supported commands. An empty mapping exposes only engine-owned built-ins.
Kit's Blueprints declare their command contracts; `hello-world` exposes the debug
commands and `pv-cloud` exposes OpenTofu, Ansible and Dispatch operations.
Inspect an Instance's complete contract with
`inframill instance commands NAME --namespace ID -o json`. Provider discovery
reports descriptions and access modes without schemas.

Older saved revisions without `command_arguments` reject on this engine. Keep
existing data intact; fetching updated sources changes the catalogue and new
Instances, while existing Instances retain their saved revision.

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

The current blueprint release is `built.in/pv-cloud:0.8.0`; `latest` has the same
content. It uses Kit OpenTofu and Ansible `0.4.0` and Dispatch `0.1.0`.
Resource names use the immutable Instance name through `{{ (instance).Name }}`;
Values do not contain `instance_name`. This requires an Inframill build with the
`instance` template function (SPEC-0085).
For existing deployments, keep the Instance name equal to the previous
`instance_name` value to preserve generated resource names. Existing Instances
retain their saved blueprint sources until explicitly replaced.
All top-level Values fields are object groups: `network`, `ssh`, `nodes`,
`cloudflare`, `ansible`, `inframill`, and optional `registry`. Network settings use `network.zone` and
`network.private_networks`; SSH settings use `ssh.user`, `ssh.public_keys`, and
`ssh.source_ips`.

The updated `0.8.0` and `latest` sources require Inframill `0.1.22-dev` or later. Their static
`required_secrets` declaration checks five namespace names before creating an
Instance: `hetzner`, `cloudflare`, `inframill-root-token`, `inframill-tls-key`, and
`pv-cloud-registry-token`. Store all five before `instance init`, even when the
selected nodes have no worker role or registry login is omitted.
Missing names produce `MISSING_SECRET` before any Instance is created; add
the entries and retry with the same Instance name. This checks presence only;
Providers still validate credentials before effects.

Secret references are fixed to those names. When moving from `0.7.0`, remove
`cloud`, `cloudflare.secret_ref`, `inframill.tls_key_secret_ref`, and
`inframill.root_token_secret_ref` from Values and store the credentials under the
required names. Older tags and existing Instance snapshots keep their prior behavior.
The repository and image configurations enable all three providers. [Example Values](examples/pv-cloud.json)
define two AMD64 Docker nodes named `data-1` and `worker-1`, both assigned `common`
and `docker`. They also have `data` and `worker`, respectively. Docker and Compose
installation and service checks belong to the dedicated `docker` role; `common`
is available for shared guest setup. ARM64 and AMD64 examples live
in `../tests/fixtures/blueprints/examples/`.
The guest cloud-config disables optional vendor-data and owns SSH user/key setup,
avoiding deprecated password settings injected by Hetzner. Ansible requires
cloud-init to complete successfully. Role plays select inventory group members,
so node names never assign services. Dispatch selects PostgreSQL destinations by the
`data` role and Inframill destinations by the `worker` role. Both require `docker`.
Ansible prepares the hosts, and explicit Dispatch pushes install the Compose payloads.
Cloud labels identify nodes with `node=<name>`.
The reserved inventory names `all` and `ungrouped` are rejected as node names.

Each node gets one DNS-only Cloudflare A record pointing to its public IPv4:
`<node>-<lowercase-instance-id>.<zone>`. DNS names are published in `server_fqdns`
and the shared node artifact. Names use the immutable Instance ID, so separate
Instances do not collide. Node names must be DNS labels of at most 36 characters;
underscores and trailing hyphens are rejected.

Set `cloudflare.zone_id` to the zone's 32-character ID. The
example zone ID is a placeholder. Store the API token as `cloudflare` with
`inframill secret upsert --namespace production --file /private/path/cloudflare.json`,
where the file contains `{"name":"cloudflare","value":"REPLACE_WITH_API_TOKEN"}`.
Scope the token to the selected zone with permission to edit DNS records and read
the zone. Kit injects it as `CLOUDFLARE_API_TOKEN` for plan/apply/destroy; the token
is never rendered into Values, tfvars or provider configuration. The Cloudflare
OpenTofu provider is pinned to `5.27.0`.

```sh
inframill namespace create production
inframill blueprint fetch built.in/pv-cloud:0.8.0 --namespace production
inframill secret upsert --namespace production --file /private/path/hetzner.json
inframill secret upsert --namespace production --file /private/path/cloudflare.json
inframill secret upsert --namespace production --file /private/path/inframill-tls-key.json
inframill secret upsert --namespace production --file /private/path/inframill-root-token.json
inframill secret upsert --namespace production --file /private/path/pv-cloud-registry-token.json
inframill instance init example --namespace production \
  --blueprint built.in/pv-cloud:0.8.0 --values-file blueprints/examples/pv-cloud.json
```

After OpenTofu apply has published nodes, prepare the guests and install PostgreSQL:

```sh
inframill instance exec example ansible.apply --namespace production \
  --stream --timeout 15m
inframill instance exec example dispatch.push --namespace production \
  --arguments '{"directory":"postgres"}' --stream --timeout 15m
inframill instance exec example dispatch.make --namespace production \
  --arguments '{"directory":"postgres","target":"install"}' --stream --timeout 15m
```

Dispatch copies `templates/dispatch/postgres/` to `/opt/pv-cloud/postgres` with
sudo on every `data` node. The `install` target starts `postgres:16`, waits for its
health check and preserves the named Docker volume and password on repeat runs.
PostgreSQL listens on `127.0.0.1:5432` on each host; the database and user are both
`postgres`. The first install generates a random password in
`/opt/pv-cloud/postgres/secrets/postgres_password`, accessible only to root and
mounted as a Compose secret. Back up this file with the database; deleting or
changing it does not reset credentials in an initialized database. Dispatch pushes
preserve it and exclude local `secrets/` files.

Use `dispatch.make` with `{"directory":"postgres","target":"status"}` to
inspect the service, or target `stop` to stop it while keeping its volume and password.
Version `0.6.0` replaces the host PostgreSQL package role; existing package-managed
databases require a separate data migration before switching. Existing Instances
keep their original blueprint snapshots.

Version `0.7.0` adds an `inframill` mapping targeting every node with the `worker`
role. Each selected worker runs an independent Inframill server with its own
persistent data volume. The example names are numbered; names themselves do not
select services.

Configure `inframill.image` with a compatible Kit image tag or digest and put your
public PEM certificate chain in `inframill.tls_cert`. Replace the example's
placeholder certificate before deployment and preserve its PEM line breaks. The
current schema marks this string with `x-inframill-ui: {widget: textarea}` for
clients supporting multiline input. Until the UI supports this hint, edit Values
JSON with `\n` escapes instead of pasting into a single-line Form field. Its subject
names must cover every worker endpoint you intend to use, such as the generated
Cloudflare hostname.
Supply the private key and root token through the namespace secrets
`inframill-tls-key` and `inframill-root-token` before Instance creation:

```sh
inframill secret upsert --namespace production --file /private/path/inframill-tls-key.json
inframill secret upsert --namespace production --file /private/path/inframill-root-token.json
inframill instance exec example dispatch.push --namespace production \
  --arguments '{"directory":"inframill"}' --stream --timeout 15m
inframill instance exec example dispatch.make --namespace production \
  --arguments '{"directory":"inframill","target":"install"}' --stream --timeout 15m
```

Each secret input is a JSON object with `name` and `value`; use your credential
tooling to populate the PEM private key and token without putting them in Values
or command arguments. The private key must be usable without a passphrase, and
the token must be a nonempty single line. The Dispatch executable must be rebuilt
with `secret_files` support; for native development run
`python3 providers/build.py build --name dispatch --native --refresh`. Rebuild the
Kit image when running the controlling daemon in Docker.

Dispatch writes these two secret files with mode `0600` under
`/opt/pv-cloud/inframill/secrets/`, then runs the payload's `install` target. The
certificate chain is rendered to `tls/fullchain.pem`. Installation checks the
certificate/key pair, recreates the container to load configuration and credential
changes, and waits for an authenticated HTTPS API probe. Inframill serves native
TLS directly on port `443` using Docker host networking on the Linux worker.
The service shares the worker network stack and does not publish Docker ports.
Port `443` must be available on each selected worker; only HTTPS is served. The
root token is loaded from a Compose secret into the server process environment
at startup.

For private container images, add the optional `registry` Values group:

```json
"registry": {
  "host": "ghcr.io",
  "username": "example-user"
}
```

Store the registry password or pull token as the namespace secret
`pv-cloud-registry-token`, using a secret input file with `name` and `value` as
above. The token is never included in Values or rendered configuration. When
`registry` is configured, Dispatch injects `secrets/registry_token` with mode
`0600` for both mappings. Missing or empty tokens reject the push. The secret's
presence is required before Instance creation even when the `registry` group is
omitted. An empty `registry` group is invalid.

Both payloads provide a `login` target. Their `install` target runs it before
starting Compose and stops if login fails. Login reads the injected token through
`docker login --password-stdin`; the host and username come from `registry.json`.
Login and Compose run as root through the mapping's `become: true` and use root's
Docker credential configuration. Omitting `registry` skips login, including when
an earlier push left a token file behind. Namespace `registry upsert` credentials
are used for Blueprint fetching and are not forwarded to worker Docker clients.

After adding registry settings to the Blueprint sources, fetch the updated tag
and initialize a new Instance; existing Instances retain their saved Blueprint
schema and templates. Repeat `dispatch.push`, then run `dispatch.make` with
`target: "install"` to refresh the registry token and log in before starting
services. A `dispatch.make` invocation of `login` or `install` uses the existing
remote token file.

Repeat the push to rotate the certificate, key or token. `dispatch.make` targets
`status` and `stop` inspect or stop existing deployments without fetching secrets.
Stop retains data and credential files. To narrow operations to a worker, set
`"select":{"names":["worker-2"]}` in the directory mapping; the default mapping
targets every `worker` node.
Provision worker-side provider SSH identities in `/opt/pv-cloud/inframill/ssh/`
and local Blueprint sources in `/opt/pv-cloud/inframill/blueprints/` as needed.
Dispatch preserves these directories across pushes. Clients must trust the
certificate's issuer and supply `ROOT_TOKEN` in their environment, for example:

```sh
inframill server version --endpoint https://WORKER_HOSTNAME
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
  pv-cloud/0.6.0/blueprint.yaml
  pv-cloud/0.6.0/templates/...
  pv-cloud/0.7.0/blueprint.yaml
  pv-cloud/0.7.0/templates/...
  pv-cloud/0.8.0/blueprint.yaml
  pv-cloud/0.8.0/templates/...
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

