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

The current blueprint release is `built.in/pv-cloud:0.10.0`; `latest` has the same
content. It uses Kit OpenTofu and Ansible `0.4.0` and Dispatch `0.1.0`.
Resource names use the immutable Instance name through `{{ (instance).Name }}`;
Values do not contain `instance_name`. This requires an Inframill build with the
`instance` template function (SPEC-0085).
For existing deployments, keep the Instance name equal to the previous
`instance_name` value to preserve generated resource names. Existing Instances
retain their saved blueprint sources until explicitly replaced.
All top-level Values fields are object groups: `network`, `ssh`, `nodes`,
`cloudflare`, `ansible`, `inframill`, `millway`, `tls`, and optional `postgres` and `registry`. Network settings use `network.zone` and
`network.private_networks`; SSH settings use `ssh.user`, `ssh.public_keys`, and
`ssh.source_ips`.

The `0.10.0` and `latest` sources require Inframill `0.1.23-dev` or later. Their static
`required_secrets` declaration checks eight namespace names before creating an
Instance: `app1-password`, `app2-password`, `hetzner`, `cloudflare`,
`inframill-root-token`, `millway-root-token`, `pv-cloud-tls-private-key`, and `pv-cloud-registry-token`.
Store all eight before `instance init`, even when the
selected nodes have no worker role or registry login is omitted.
Missing names produce `MISSING_SECRET` before any Instance is created; add
the entries and retry with the same Instance name. This checks presence only;
Providers still validate credentials before effects.

The shared TLS certificate is `tls.certificate`; its namespace private-key secret is
`pv-cloud-tls-private-key`. All three payloads use this pair. When
moving from `0.8.0`, move `inframill.tls_cert` to `tls.certificate` and upsert the
existing key under the new secret name. Existing `0.8.0` snapshots still use
`inframill-tls-key`. The service-specific `inframill-root-token` keeps its name.

The infrastructure, registry and TLS secret references are fixed to those names. When moving from `0.7.0`, remove
`cloud`, `cloudflare.secret_ref`, `inframill.tls_key_secret_ref`, and
`inframill.root_token_secret_ref` from Values and store the credentials under the
required names. Older tags and existing Instance snapshots keep their prior behavior.
The repository and image configurations enable all three providers. [Example Values](examples/pv-cloud.json)
define three AMD64 Docker nodes named `data-1`, `worker-1`, and `web-1`, assigned
`common` and `docker`, plus `data`, `worker`, and `web` respectively. Docker and Compose
installation and service checks belong to the dedicated `docker` role; `common`
is available for shared guest setup. ARM64 and AMD64 examples live
in `../tests/fixtures/blueprints/examples/`.
The guest cloud-config disables optional vendor-data and owns SSH user/key setup,
avoiding deprecated password settings injected by Hetzner. Ansible requires
cloud-init to complete successfully. Role plays select inventory group members,
so node names never assign services. Dispatch selects PostgreSQL destinations by the
`data` role, Inframill by `worker`, and Millway by `web`. All require `docker`.
Ansible prepares the hosts, and explicit Dispatch pushes install the Compose payloads.
Cloud labels identify nodes with `node=<name>`.
The reserved inventory names `all` and `ungrouped` are rejected as node names.

Each node gets one DNS-only Cloudflare A record pointing to its public IPv4:
`<node>-<lowercase-instance-id>.<zone>`. DNS names are published in `server_fqdns`
and the shared node artifact. Names use the immutable Instance ID, so separate
Instances do not collide. Node names must be DNS labels of at most 36 characters;
underscores and trailing hyphens are rejected.

Set `cloudflare.zone_id` to the zone's 32-character ID and `cloudflare.zone_name`
to its domain. OpenTofu validates the name against the zone API; the Millway
payload uses it with the Instance ID to derive service endpoints. The
example zone ID is a placeholder. Store the API token as `cloudflare` with
`inframill secret upsert --namespace production --file /private/path/cloudflare.json`,
where the file contains `{"name":"cloudflare","value":"REPLACE_WITH_API_TOKEN"}`.
Scope the token to the selected zone with permission to edit DNS records and read
the zone. Kit injects it as `CLOUDFLARE_API_TOKEN` for plan/apply/destroy; the token
is never rendered into Values, tfvars or provider configuration. The Cloudflare
OpenTofu provider is pinned to `5.27.0`.

```sh
inframill namespace create production
inframill blueprint fetch built.in/pv-cloud:0.10.0 --namespace production
inframill secret upsert --namespace production --file /private/path/app1-password.json
inframill secret upsert --namespace production --file /private/path/app2-password.json
inframill secret upsert --namespace production --file /private/path/hetzner.json
inframill secret upsert --namespace production --file /private/path/cloudflare.json
inframill secret upsert --namespace production --file /private/path/pv-cloud-tls-private-key.json
inframill secret upsert --namespace production --file /private/path/inframill-root-token.json
inframill secret upsert --namespace production --file /private/path/millway-root-token.json
inframill secret upsert --namespace production --file /private/path/pv-cloud-registry-token.json
inframill instance init example --namespace production \
  --blueprint built.in/pv-cloud:0.10.0 --values-file blueprints/examples/pv-cloud.json
```

After OpenTofu apply has published nodes, prepare the guests and install PostgreSQL:

```sh
inframill instance exec example ansible.apply --namespace production \
  --stream --timeout 15m
inframill instance exec example dispatch.push --namespace production \
  --arguments '{"directory":"postgres"}' --stream --timeout 15m
inframill instance exec example dispatch.make --namespace production \
  --arguments '{"directory":"postgres","target":"start"}' --stream --timeout 15m
```

Dispatch copies `templates/dispatch/postgres/` to `/opt/pv-cloud/postgres` with
sudo on every `data` node. The `start` target starts one `postgres:16` container
and reconciles `app1` and `app2`, each owned by a same-named non-superuser login.
Each login handles application access and schema migrations. Repeated start
preserves passwords, data and the running container when its configuration is
unchanged. Adding a database works against an existing volume. Removing an entry
removes its managed network access; start never drops databases, objects or roles
and refuses to take over a database owned by another role.

PostgreSQL uses Docker host networking and listens on port `5432` on the host's
interfaces. OpenTofu creates a separate PostgreSQL firewall that selects only
`data` nodes. External source CIDRs default to all IPv4/IPv6 addresses; configure
`postgres.source_ips` to restrict them. A firewall change needs OpenTofu plan/apply,
then a Dispatch push and start to update PostgreSQL's access rules. PostgreSQL
accepts only TLS connections for exact database/user pairs using SCRAM passwords;
the `postgres` administrator is available only through the container's local
Unix socket. Use the data node's generated DNS record and `sslmode=verify-full`
from external clients. The shared certificate must cover both data and worker
hostnames; one hostname resolving to the worker does not route PostgreSQL to a
separate data node.

The default Values configuration is equivalent to:

```json
"postgres": {
  "databases": [
    {"name": "app1", "password_secret": "app1-password"},
    {"name": "app2", "password_secret": "app2-password"}
  ],
  "source_ips": ["0.0.0.0/0", "::/0"],
  "port": 5432,
  "max_connections": 50,
  "work_mem_mb": 4
}
```

Application passwords are required namespace secrets: `app1-password` and
`app2-password`. Supply them before Instance creation using secret input files
such as `{"name":"app1-password","value":"REPLACE_WITH_A_STRONG_PASSWORD"}`.
Every explicitly configured database entry requires a `password_secret` reference;
custom references are resolved during push. The two default secret names remain
required at Instance creation. Dispatch injects `secrets/app1_password` and
`secrets/app2_password` with mode `0600`; start never generates application
passwords and rejects missing, empty or invalid files before starting Docker.
Passwords must be 16–1024 printable ASCII characters without whitespace; a final
file newline is accepted. The administrator password is still generated once in
`secrets/postgres_password`. Targets never print credentials; back up credential
files alongside the database. After updating a namespace secret and repeating
the push, run `start` to apply the supplied application passwords and verify them.
Unchanged application passwords and the administrator password are preserved.
Already established sessions keep their existing authentication state.

The container memory limit defaults to host RAM minus the larger of 512 MiB or
25 percent; override `postgres.memory_mb` for a shared host. PostgreSQL reserves
25 percent of that budget for shared buffers, uses conservative work and
maintenance memory, and sizes parallel workers from available CPUs. Database
connection limits together leave five of `max_connections` for administration.
Configure `connection_limit`, `statement_timeout_ms` (default 30000),
`lock_timeout_ms` (5000), or `idle_transaction_timeout_ms` (60000) per database.
Timeout zero disables that timeout. Start rejects overcommitted connection/work
memory budgets. Named storage keeps data/WAL outside the container writable layer;
CPU quotas are unrestricted, health checks run every 30 seconds, and the local
logging driver rotates three 10 MiB files with compression disabled. Durable
writes remain enabled. Query statistics are available through `pg_stat_statements`.
These defaults are a starting point for workload measurements, not an automatic
guarantee against every query's memory usage.

All three payloads expose `start`, `status`, `verify`, and `stop`, with `start` as the
default. These targets also run directly on the node, for example
`sudo make -C /opt/pv-cloud/millway start`; Dispatch invokes the same Makefile.
The delivered files and credentials are sufficient for manual operation.
Use `make PYTHON=/path/to/python3 start` to select a host interpreter.
Python handles configuration preparation and PostgreSQL reconciliation, while
Compose runs the services. PostgreSQL's `start` starts the service, reconciles
databases and runs verification. Its `verify` checks TLS/password authentication,
migration rights, isolation, rejected
plaintext/admin TCP connections and effective settings; `status` inspects the
service; `stop` stops it while preserving the volume and credentials. Changed
configuration or TLS material causes start to recreate the container, interrupting
connections briefly. Push excludes credentials and generated runtime files.
Version `0.6.0` replaces the host PostgreSQL package role; existing package-managed
databases require a separate data migration before switching. Existing Instances
keep their original blueprint snapshots.

Version `0.7.0` adds an `inframill` mapping targeting every node with the `worker`
role. Each selected worker runs an independent Inframill server with its own
persistent data volume. The example names are numbered; names themselves do not
select services.

Configure `inframill.image` with a compatible Kit image tag or digest and put your
public PEM certificate chain in `tls.certificate`. Replace the example's
placeholder certificate before deployment and preserve its PEM line breaks. The
current schema marks this string with `x-inframill-ui: {widget: textarea}` for
clients supporting multiline input. Until the UI supports this hint, edit Values
JSON with `\n` escapes instead of pasting into a single-line Form field. Its subject
names must cover every worker and data endpoint you intend to use, such as the generated
Cloudflare hostname.
Supply the private key and root token through the namespace secrets
`pv-cloud-tls-private-key` and `inframill-root-token` before Instance creation:

```sh
inframill secret upsert --namespace production --file /private/path/pv-cloud-tls-private-key.json
inframill secret upsert --namespace production --file /private/path/inframill-root-token.json
inframill secret upsert --namespace production --file /private/path/millway-root-token.json
inframill instance exec example dispatch.push --namespace production \
  --arguments '{"directory":"inframill"}' --stream --timeout 15m
inframill instance exec example dispatch.make --namespace production \
  --arguments '{"directory":"inframill","target":"start"}' --stream --timeout 15m
```

Each secret input is a JSON object with `name` and `value`; use your credential
tooling to populate the PEM private key and token without putting them in Values
or command arguments. The private key must be usable without a passphrase, and
the token must be a nonempty single line. The Dispatch executable must be rebuilt
with `secret_files` support; for native development run
`python3 providers/build.py build --name dispatch --native --refresh`. Rebuild the
Kit image when running the controlling daemon in Docker.

Dispatch writes these two secret files with mode `0600` under
`/opt/pv-cloud/inframill/secrets/`. The certificate chain is rendered to
`tls/fullchain.pem`. The subsequent `start` target checks the
certificate/key pair, recreates the container to load configuration and credential
changes, and waits for an authenticated HTTPS API probe. Inframill serves native
TLS directly on port `443` using Docker host networking on the Linux worker.
The service shares the worker network stack and does not publish Docker ports.
Port `443` must be available on each selected worker; only HTTPS is served. The
root token is loaded from a Compose secret into the server process environment
at startup. The `verify` target runs the same authenticated HTTPS probe against
the running container.

Version `0.10.0` adds a `millway` mapping to `/opt/pv-cloud/millway` on `web` nodes.
It runs the existing Millway UI/API image as UID/GID `65532:65532`, using host
networking, a read-only filesystem, no Linux capabilities and `no-new-privileges`.
The Ansible `web` role sets `net.ipv4.ip_unprivileged_port_start=443` on web nodes,
allowing all non-root processes there to bind ports 443 and higher. It no longer
installs host Nginx. Run Ansible before Millway's first start. Older tags retain
their original role and existing Instance snapshots are not upgraded automatically.

Configure Millway in Values:

```json
"millway": {
  "image": "ghcr.io/inframill/millway:0.1.5-dev",
  "google_client_ids": {"web": "YOUR_CLIENT_ID.apps.googleusercontent.com"},
  "admin_emails": ["admin@example.com"]
}
```

Use Millway `0.1.5-dev` or later with the `start` and `verify` CLI commands.
The image must contain both UI and API, match the web node architecture, and have
the same Google client IDs embedded in its exported UI. `admin_emails` controls
new-account administrator assignment. Optional `web_node`, `database_node` and
`worker_node` select logical nodes, defaulting to `web-1`, `data-1` and `worker-1`;
each needs its corresponding role and `docker`. Millway uses `app1` and its
configured password secret, authenticates to the worker with
`inframill-root-token`, and protects its own administrative API with the distinct
`millway-root-token`. The database pool defaults to at most ten connections,
bounded by app1's connection limit; override with `max_connections`.

The public origin uses the generated web-node DNS name. Register that HTTPS
origin in Google OAuth, and ensure the shared certificate covers web, data and
worker names. Optional `database_host` and `inframill_endpoint` override derived
endpoints; upstream access requires HTTPS and database access uses
`sslmode=verify-full`.

Start PostgreSQL and Inframill first, then:

```sh
inframill instance exec example dispatch.push --namespace production \
  --arguments '{"directory":"millway"}' --stream --timeout 15m
inframill instance exec example dispatch.make --namespace production \
  --arguments '{"directory":"millway","target":"start"}' --stream --timeout 15m
```

The host helper builds protected runtime configuration from namespace secrets.
A temporary PostgreSQL-image helper stages it in a private volume owned by
Millway's UID. No shell, Python, entrypoint or code changes are required in the
Millway image or sibling repository. `start` runs `millway start -config` to apply
migrations through normal application startup, then verifies dependencies. `stop`
preserves the runtime volume and database. Push excludes generated configuration
and credentials.

Millway's `verify` executes `/millway verify -config /etc/millway/millway.json`
inside the running container. The native CLI checks the application's actual
database configuration and makes fresh authenticated Inframill version probes
using Millway's compatibility policy. Make and Dispatch report its output and
fail when the CLI fails. All Millway verification belongs to that CLI; Kit
adds no database, migration, version or HTTP probes. Millway's `verify` does not
apply migrations or start HTTP handlers or workers.

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
`0600` for all three mappings. Missing or empty tokens reject the push. The secret's
presence is required before Instance creation even when the `registry` group is
omitted. An empty `registry` group is invalid.

All three payloads' `start` targets log into the configured registry before
starting Compose and stop if login fails. Login reads the injected token through
`docker login --password-stdin`; the host and username come from `registry.json`.
Login and Compose run as root through the mapping's `become: true` and use root's
Docker credential configuration. Omitting `registry` skips login, including when
an earlier push left a token file behind. Namespace `registry upsert` credentials
are used for Blueprint fetching and are not forwarded to worker Docker clients.

After adding registry settings to the Blueprint sources, fetch the updated tag
and initialize a new Instance; existing Instances retain their saved Blueprint
schema and templates. Repeat `dispatch.push`, then run `dispatch.make` with
`target: "start"` to refresh the registry token and log in before starting
services. A `dispatch.make` invocation of `start` uses the existing
remote token file.

Repeat the push to rotate the certificate, key or token. `dispatch.make` targets
`verify`, `status` and `stop` check, inspect or stop existing deployments without
fetching secrets.
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
  pv-cloud/0.9.0/blueprint.yaml
  pv-cloud/0.9.0/templates/...
  pv-cloud/0.10.0/blueprint.yaml
  pv-cloud/0.10.0/templates/...
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
# Disposable real PostgreSQL containers; requires Docker and openssl:
python3 tests/pv_postgres_check.py
# Requires the Millway UI/API and current Kit images locally:
python3 tests/pv_millway_check.py
# All native workflows, including revision and cache checks:
python3 tests/check.py native
```

Integration checks fetch local sources into disposable daemon data, render the
examples and execute debug commands or Ansible syntax checks and OpenTofu mocks.
They create no cloud resources and require no blueprint registry. `task check-full`
also exercises local fixtures through both Linux runtime images; its disposable
registry is used only to verify Kit image publication.

