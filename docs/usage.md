# Using Kit

## Inframill CLI

`inframill server start` starts the daemon; `inframill server version` queries its
version. Resource commands are top-level: `inframill namespace`, `instance`,
`blueprint`, `secret`, `registry`, `execution` and `provider`. They send requests
to the daemon. Shell completion uses `inframill completion`; `inframill debug provider`
runs the offline debug provider protocol. Each Kit provider is a separate versioned executable.

Only server startup accepts `--config` (default `.inframill.yaml`). Relative data, local blueprint search
and provider paths resolve beside that file. The `connect-engine` handler accepts
no settings; its route is fixed. Resource requests and `server version` accept `--endpoint` and `--timeout`,
falling back to `INFRAMILL_ENDPOINT` and `INFRAMILL_TIMEOUT`, then
`http://127.0.0.1:8080` and `2m`. Requests reject `--config` and never load YAML.
Request flags belong to resource groups and `server version`, not the root,
`server start`, `completion` or `debug` groups. Generate shell completion with
`inframill completion zsh`; regenerate saved completion scripts after upgrading.

`inframill provider list` reports the daemon's cached startup availability.
Restart the daemon after changing its provider configuration.
`inframill server version` reports the selected daemon's build version;
`inframill --version` reports the local CLI version.

## Namespace events

Read a page of resource changes or watch new changes as JSON Lines:

```sh
bin/inframill namespace events read --namespace production --after-sequence 0 --limit 100
bin/inframill namespace events watch --namespace production --timeout 24h
bin/inframill namespace events watch --namespace production --after-sequence 42 --timeout 24h
```

Watch without a position starts at the current tail; explicit zero replays retained
history. Read pages expose `nextSequence` for pagination. Sequences and execution
revisions are decimal strings; retain their full integer precision. On
`resyncRequired` or `stream.reset`, refresh current resource queries. Events omit
credentials, command arguments/results and log output. Positions belong to one
namespace lifetime and must be discarded after its deletion.

Each namespace persists its feed in `events.jsonl`. History is unlimited by default.
To enable retention, add `eventJournalMaxBytes: 67108864` under `engine` in your
daemon configuration, then restart. Zero disables retention; nonzero limits must
be at least 131072 bytes. Enabling or lowering the limit can prune existing history.
Expired positions explicitly request a refresh. The normal total CLI timeout
applies to watches; reconnect explicitly after the last event you processed.

## Running a deployment

Run these commands from the repository root. `blueprints/examples/pv-cloud.json`
contains plain Values JSON with both AMD64 and ARM64 nodes. The same Blueprint
supports either architecture or a mix, selected per node. Generate configuration using
the installed local tools and `task providers:build -- --native` outputs, then start the daemon:

```sh
OPENTOFU_PATH="$PWD/.cache/tools/opentofu/1.11.5/tofu" \
ANSIBLE_PATH="$PWD/.cache/ansible-2.20.1/bin/ansible-playbook" \
  task --silent config > /tmp/kit-config.json
bin/inframill server start --config /tmp/kit-config.json
```

In another terminal, create a namespace, fetch a local blueprint and initialize an Instance.
Initialization validates Values and renders files without executing a provider:

```sh
bin/inframill namespace create production
bin/inframill blueprint fetch build.in/pv-cloud \
  --namespace production
bin/inframill instance init example --namespace production \
  --blueprint build.in/pv-cloud \
  --values-file blueprints/examples/pv-cloud.json
bin/inframill secret replace --namespace production --file /private/path/secrets.json
bin/inframill instance exec example --namespace production opentofu.init --wait
```

The generated configuration allows the local `blueprints/` directory.
`build.in/pv-cloud` selects `blueprints/pv-cloud/latest/`; append `:TAG` to use
`blueprints/pv-cloud/TAG/`. These references require no registry. Re-fetch after
editing source files and initialize a new Instance to use the new snapshot. Commands such as init/plan/apply contact
real services and require your chosen credentials and infrastructure inputs.
Use `--arguments '{"playbook":"site"}'` for inline execution arguments or
`--arguments-file` for a file (the two flags are mutually exclusive), `--wait` to wait for an outcome and
`-o json` for structured output. A failed command has a failed outcome; cancelling
or timing out a client wait does not cancel admitted work or roll back tool effects.
Creation inputs are immutable; use a new Instance name to change them.

To follow live stdout and stderr until completion, use `--stream`:

```sh
bin/inframill instance exec example --namespace production opentofu.init --stream
```

`--stream` implies waiting and keeps stdout raw; status notices accompany stderr.
It rejects JSON output and `builtin.delete`. Stopping the CLI leaves admitted work
running and prints continuation commands with independent log offsets.

To check local sources, run
`BLUEPRINT=pv-cloud python3 tests/check.py blueprint:check`: it starts a disposable
daemon, fetches the source directory, renders examples, checks Ansible syntax and
runs OpenTofu provider mocks. The installed local tools are required; no blueprint
registry is involved. For Docker development, `task docker:up` mounts `blueprints/`
at the configured `/opt/inframill/blueprints` path. See [local blueprints](../blueprints/README.md).

Inspect providers, Instances and command history with:

```sh
bin/inframill server version
bin/inframill provider list
bin/inframill instance list --namespace production
bin/inframill provider commands
bin/inframill provider commands opentofu.plan
bin/inframill execution list --instance example --namespace production --all -o json
bin/inframill execution get EXECUTION_ULID --instance example --namespace production
bin/inframill execution wait EXECUTION_ULID --instance example --namespace production
bin/inframill execution logs EXECUTION_ULID --instance example --namespace production --follow
```

Instance inspection (`instance get`) and execution accept a local name or canonical uppercase ULID.
Both require `--namespace`, including ID-based operations.
Execution history uses `--instance`. `provider commands` lists cached commands
across all available providers, without running tools or checking secrets.
`provider commands NAME` shows one command and its accepted arguments. Discovery
has no Instance scope and does not guarantee applicability to an Instance.
Built-in operations are excluded; use `instance delete` to delete an Instance.
Logs default to raw stdout; add `--stream stderr` for stderr. JSON log output is a
single snapshot and cannot use `--follow`. Paginated Instance and execution lists
support `--all`, with `--limit` controlling page size under one total timeout.
If a wait times out, use the printed ULID-based reconnect command to resume waiting;
the admitted execution continues on the daemon.

## Namespaces

Namespaces must exist before initializing Instances, managing blueprints or
setting credentials. Create each namespace once with `namespace create ID`;
`namespace list` includes empty namespaces and persists across restarts.
Every namespace-owned operation requires `--namespace`, including lists and
Instance/execution ID lookups. Instance names are local to that namespace;
qualified `namespace/name` selectors and namespace defaults are unsupported.

```sh
bin/inframill namespace list
bin/inframill namespace get production
bin/inframill instance list --namespace production
bin/inframill blueprint list --namespace production
bin/inframill secret list --namespace production
bin/inframill registry list --namespace production
```

Scoped output omits namespace identifiers. Credential lists expose names and hosts
only. Namespace create/get/delete take a positional namespace ID; namespace
administration, server version and provider discovery reject `--namespace`.
Queries read the daemon's local inventory and do not fetch remote resources.

Namespace IDs accept `[a-zA-Z0-9-]+` and preserve case. IDs beginning with a hyphen
use `namespace create -- -Team-` and `--namespace=-Team-` in scoped CLI commands.
Instance names and Provider/command identifiers use the same character grammar;
instance and execution IDs remain ULIDs. Blueprint inputs have separate schema rules.
Namespace RPC create/get/delete bodies and metadata use `id`, for example
`{"id":"production"}`. Stored namespace records and deletion journals also
use `id`; old `name` records reject without migration. Preserve old data and use a
fresh data directory or volume when upgrading.

Each namespace owns `<dataDir>/<namespace>/namespace.json`, `blueprints/` and
`instances/<InstanceID>/`. Fetch a blueprint separately into every namespace that
uses it; catalogues and cached content are isolated.

`namespace delete ID` durably requests deletion and returns immediately. The
namespace rejects new work while admitted operations finish. The cleanup worker
then removes its local blueprints, Instances, history and credentials, checking
every 10 seconds by default. `namespace list` and `namespace get ID` report `active`, `deleting` or
`failed`; failed cleanup is retried automatically. Deletion cannot be undone.
It does not destroy external infrastructure;
run the appropriate provider destroy commands before deleting storage you still
need to manage. Recreating a deleted namespace starts empty.

The current `ulid-v1` storage layout rejects older UUID records and journals
without modification or migration. Preserve old data and start with a fresh data
directory or volume when upgrading from those formats.

## Namespace registry credentials

Every blueprint fetch requires `--namespace`. Local Kit blueprints do not need
registry credentials. For other private OCI blueprints, supply registry credentials
to an existing namespace before fetching:

```sh
bin/inframill registry replace --namespace production --file /private/path/registries.json
bin/inframill registry list --namespace production
bin/inframill blueprint fetch ghcr.io/example/blueprints/custom:1.0.0 \
  --namespace production
```

The credential file is a JSON map of registry hosts to username/password objects;
for example, with synthetic values:

```json
{"ghcr.io":{"username":"example-user","password":"synthetic-token"}}
```

Credentials are held in memory and must be supplied again after daemon restart.
`registry clear --namespace production` clears the namespace's set. An unmatched host
uses anonymous access. The daemon does not load Docker login credentials, and
registry credentials remain separate from provider secrets. Blueprint cache
entries belong to the selected namespace. Fetch errors include structured
diagnostics for authentication, access, missing references and network failures.

## Namespace secrets

Secret files are JSON maps of local names to strings. The pv-cloud example uses
`hetzner` for its cloud API token. Values select secret names, never credential
values. Inframill supplies a namespace snapshot only to provider Execute; Kit does not
read `INFRAMILL_SECRET_*` or arbitrary inherited tool credential variables. Required
credentials must be nonempty and are checked before effects. Only selected values
reach the tool environment. Known injected values are filtered from tool diagnostics;
Kit results and shared artifacts contain only verified projections. Arbitrary
provider/playbook output must not intentionally emit transformed secrets.

Manage the in-memory namespace set with `secret replace`, `secret list`
and `secret clear`, using `--namespace production` to select the set. Restart discards sets. Running commands retain their
snapshot; queued commands obtain the set current when they start.

The `InitInstance` RPC requires the HTTP header `X-Namespace-ID: production`
and accepts these top-level fields:

```json
{"name":"example","blueprint_reference":"build.in/pv-cloud","values":{"json":"{}"}}
```

Set `values.json` to the serialized Values JSON. The CLI still accepts plain Values files. Fetch the blueprint before
initializing the Instance in the same existing namespace. Blueprint list/get reports ready and failed catalogue
entries; resolve requires a usable revision. Deleting a cached revision by its digest pin
preserves existing Instances and their source snapshots.
With `-o json`, `blueprint get`, `provider get` and `provider commands NAME` return the selected
resource directly; `blueprint resolve` returns `blueprint` metadata and the raw
Values schema as an ordinary `valuesSchema` object. The `GetBlueprint` RPC wraps
that schema in a `JsonDocument`; human CLI output shows a Values schema section.
Blueprint metadata also exposes declared `features` as sorted identifiers; an empty
list may be omitted in JSON. Human details and list output display these capabilities.

## Providers

A blueprint has a static `blueprint.yaml` containing `api_version`, `description`,
an inline `values_schema`, and `provider_settings`, alongside `templates/`.
Each entry has a required major.minor version and nested provider settings:

```yaml
provider_settings:
  opentofu:
    version: "0.4"
    settings:
      id: opentofu
      settings_file: kit-provider.json
      backend: local
      node_output: inframill_nodes
      artifact: opentofu-nodes-v1.json
```

Inframill checks the connected provider's major/minor before accepting the Blueprint.
Release manifests pin exact provider binaries; the Blueprint binding declares the
compatible major/minor line.
Templates render nonsecret settings into `kit-provider.json` within
`workspace/opentofu/` and `workspace/ansible/`. Sources live in matching
`templates/opentofu/` and `templates/ansible/` directories. The engine supplies
each provider's directory directly; providers do not append another directory.
Secret references are names in settings files, never credentials.
Settings validation uses no files; Prepare reads and validates rendered inputs with
no tool execution, writes, network or secrets. Execute performs and verifies effects.

OpenTofu supplies init, validate, plan, apply, destroy and output. Plan returns a
`plan_id`; apply/destroy require it. Plans bind the shared artifact directory, provider/tool/configuration and source bytes. Kit retires a plan before apply effects,
so failures and partial effects require a fresh plan. It invalidates shared nodes
before mutation and only publishes verified output. Destroy verifies empty managed
state. Refresh-only and destroy plans are distinct modes.

Ansible supplies inventory, ping, syntax-check, check and apply. It consumes a bound node
artifact, selects the requested address without fallback, and checks the complete
host recap. Failed, unreachable, ignored or rescued failures reject. Check mode is a
prediction. Syntax-check needs no previously applied infrastructure. SSH uses an
agent, batch mode and explicit host-key policy; Kit does not write private keys.

Use ping after OpenTofu apply/output to check SSH authentication and remote Python
without applying a playbook. It accepts optional `hosts`, but no playbook or tags:

```sh
bin/inframill instance exec example ansible.ping --namespace production --stream
bin/inframill instance exec example ansible.ping --namespace production \
  --arguments '{"hosts":["web-1"]}' --stream
```

Ping uses `ansible.builtin.ping`, not ICMP, with a 30-second task timeout.
Ansible streams module/task status, safe connection failure categories and host
counters to stderr; arbitrary task results and arguments stay private. The pv-cloud
playbook waits up to 300 seconds for SSH while guests boot. A running execution
at that step is waiting for connectivity; restarting the daemon interrupts it.
The CLI's default two-minute timeout only stops waiting, so allow more time for
an apply after checking connectivity:

```sh
bin/inframill instance exec example ansible.apply --namespace production \
  --arguments '{"playbook":"site"}' --stream --timeout 15m
```

For Docker, `task docker:up` mounts the project's `ssh/` directory at `/root/.ssh`.
Ansible and other providers invoking SSH use the standard identities there, such
as `ssh/id_ed25519`, along with `ssh/config` and `ssh/known_hosts`. Set directory
permissions to `700` and private-key permissions to `600`. For a nonstandard key
name, add an `IdentityFile /root/.ssh/your_key` entry to `ssh/config`. Paths in that
configuration must refer to the container filesystem. SSH can write `known_hosts`
to this mount, preserving trusted host keys across container replacements.
Re-run `task docker:up` to add the mount to an existing container.

Alternatively, load the matching private key into your host SSH agent with `ssh-add`.
`task docker:up` also forwards it when `SSH_AUTH_SOCK` is set, using Docker Desktop's
agent socket on macOS and the host socket on Linux. `KIT_SSH_AUTH_SOCK` overrides
the Docker-host socket path; set it to an empty string to disable forwarding.
The container can authenticate with keys loaded in that agent. Public keys in
blueprint values only authorize guest access; they do not supply client credentials.
Also check `ssh_user`, guest readiness, and that `ssh_source_ips` allows your client
address. Native daemons inherit the agent from the shell that starts them.

The executable's configured prefix is:

```text
/opt/inframill/providers/opentofu/0.4.0/provider
```

Inframill sends one JSON request on stdin, without adding protocol arguments. Kit
uses upstream `runmill.Serve` for JSON Lines messages and final success/error replies.
For a manual discovery probe, pipe this JSON to that prefix:

```json
{"protocol":1,"method":"info","params":{}}
```

## Execution context and state

Runmill supplies settings, workspace/artifact paths and an execution ID, without
Instance or Blueprint metadata. Kit binds plans and node artifacts to a hash of
the supplied artifact directory. Regenerate plans and node output after moving
data to a different path. Execution IDs used for plan filenames must be canonical uppercase ULIDs (26 characters).

pv-cloud uses local OpenTofu state at
`<dataDir>/<namespace>/instances/<InstanceID>/workspace/opentofu/terraform.tfstate`.
The daemon's persisted data volume holds the state; no S3 bucket or AWS credentials
are required. Back up this storage and keep it until the managed infrastructure
has been destroyed. Deleting an Instance or namespace also deletes its local state.

pv-cloud 0.3.0 removes `infra_state_bucket` from Values. Existing Instances retain
their saved blueprint and backend; this release does not move existing S3 state.

## RPC fields

RPC command submission uses `command_name` and `arguments: {"json":"..."}`.
Values, arguments, results and argument schemas use JSON document envelopes.
CLI JSON unwraps document envelopes into their JSON values; raw RPC clients retain
the `json` wrapper. Execution references use `execution_id`; statuses use `COMMAND_EXECUTION_STATUS_*`.
Provider discovery uses `ListProviders` and reports `availability`.
`ListCommandDefinitions` takes an empty request and returns cached commands from
all available providers, excluding built-in operations. The former `instance_id`
field is reserved and no longer scopes discovery.

Every namespace-owned RPC requires exactly one `X-Namespace-ID` header containing
a valid namespace ID. Namespace selectors are absent from request bodies and
response resources. Missing, empty, duplicate or padded headers reject; foreign
Instance IDs return not-found. The header selects scope and is not authentication.

`CreateNamespace`, `GetNamespace`, `ListNamespaces`, `DeleteNamespace`,
`GetServerVersion`, `ListProviders` and `ListCommandDefinitions` require the header
to be absent. `GetNamespace` accepts `{"id":"production"}` and returns the
namespace identity and lifecycle status.

`ListBlueprints`, `ListNamespaceSecretNames`, `ListNamespaceRegistryHosts` and
`ListInstances` query only the selected namespace. Secret `names` contains objects
such as `{"name":"hetzner"}`; registry `hosts` contains objects such as
`{"host":"ghcr.io"}`. Instance lists retain pagination; execution history remains
scoped to an Instance. Cursors cannot be reused in another namespace.
