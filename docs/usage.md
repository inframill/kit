# Using Kit

## Inframill CLI

`inframill server start` starts the daemon. All Instance, blueprint, secret,
command and history operations use `inframill server` requests. Utilities live
under `inframill other`; each Kit provider is a separate versioned executable.

Only server startup accepts `--config` (default `.inframill.yaml`). Relative data
and provider paths resolve beside that file. The `connect-engine` handler accepts
no settings; its route is fixed. Server requests accept `--endpoint` and `--timeout`,
falling back to `INFRAMILL_ENDPOINT` and `INFRAMILL_TIMEOUT`, then
`http://127.0.0.1:8080` and `2m`. Requests reject `--config` and never load YAML.

`inframill server provider list` reports the daemon's cached startup availability.
Restart the daemon after changing its provider configuration.
`inframill server version` reports the selected daemon's build version;
`inframill --version` reports the local CLI version.

## Running a deployment

Run these commands from the repository root. `blueprints/examples/pv-cloud-0.2.0.json`
contains plain Values JSON with both AMD64 and ARM64 nodes. The same Blueprint
supports either architecture or a mix, selected per node. Generate configuration using
the installed local tools and `task providers:build -- --native` outputs, then start the daemon:

```sh
OPENTOFU_PATH="$PWD/.cache/tools/opentofu/1.11.5/tofu" \
ANSIBLE_PATH="$PWD/.cache/ansible-2.20.1/bin/ansible-playbook" \
  task --silent config > /tmp/kit-config.json
bin/inframill server start --config /tmp/kit-config.json
```

In another terminal, fetch a published blueprint and initialize an Instance.
Initialization validates Values and renders files without executing a provider:

```sh
bin/inframill server blueprint fetch ghcr.io/inframill/blueprints/pv-cloud:0.2.0
bin/inframill server instance init production/example \
  --blueprint ghcr.io/inframill/blueprints/pv-cloud:0.2.0 \
  --values-file blueprints/examples/pv-cloud-0.2.0.json
bin/inframill server secret replace production --file /private/path/secrets.json
bin/inframill server instance exec production/example opentofu.init --wait
```

Use your published tag or digest reference. Commands such as init/plan/apply contact
real services and require your chosen credentials and infrastructure inputs.
Use `--arguments-file` for execution arguments, `--wait` to wait for an outcome and
`-o json` for structured output. A failed command has a failed outcome; cancelling
or timing out a client wait does not cancel admitted work or roll back tool effects.
Creation inputs are immutable; use a new Instance name to change them.

Blueprints are fetched through OCI. To check unpublished sources, run
`BLUEPRINT=pv-cloud python3 tests/check.py blueprint:check`: it packages them into a disposable local
registry, starts a daemon, renders examples, checks Ansible syntax and runs OpenTofu
provider mocks. Docker and the installed local tools are required.

Inspect providers, Instances and command history with:

```sh
bin/inframill server version
bin/inframill server provider list
bin/inframill server instance list --namespace production
bin/inframill server command execution list --instance-id INSTANCE_UUID -o json
bin/inframill server command execution stdout INSTANCE_UUID EXECUTION_UUID
```

## Namespace secrets

Secret files are JSON maps of local names to strings. The example's names are
`hetzner`, `s3_access` and `s3_secret`. Values select these names, never credential
values. Inframill supplies a namespace snapshot only to provider Execute; Kit does not
read `INFRAMILL_SECRET_*` or arbitrary inherited tool credential variables. Required
credentials must be nonempty and are checked before effects. Only selected values
reach the tool environment. Known injected values are filtered from tool diagnostics;
Kit results and shared artifacts contain only verified projections. Arbitrary
provider/playbook output must not intentionally emit transformed secrets.

Manage the in-memory namespace set with `server secret replace`, `server secret list`
and `server secret clear`. Restart discards sets. Running commands retain their
snapshot; queued commands obtain the set current when they start.

The `InitInstance` RPC accepts these top-level fields:

```json
{"name":"production/example","blueprint_reference":"ghcr.io/inframill/blueprints/pv-cloud:0.2.0","values":{"json":"{}"}}
```

Set `values.json` to the serialized Values JSON. The CLI still accepts plain Values files. Fetch the blueprint before
initializing the Instance. Blueprint list/show reports ready and failed catalogue
entries; get requires a usable revision. Deleting a cached revision by its digest pin
preserves existing Instances and their source snapshots.

## Providers

A blueprint has a static `blueprint.yaml` containing `api_version`, `description`,
an inline `values_schema`, and `provider_settings`, alongside `templates/`.
Each entry has a required major.minor version and nested provider settings:

```yaml
provider_settings:
  opentofu:
    version: "0.3"
    settings:
      id: opentofu
      settings_file: kit-provider.json
      backend: s3
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

Ansible supplies inventory, syntax-check, check and apply. It consumes a bound node
artifact, selects the requested address without fallback, and checks the complete
host recap. Failed, unreachable, ignored or rescued failures reject. Check mode is a
prediction. Syntax-check needs no previously applied infrastructure. SSH uses an
agent, batch mode and explicit host-key policy; Kit does not write private keys.

The executable's configured prefix is:

```text
/opt/inframill/providers/opentofu/0.3.0/provider
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
data to a different path. Execution IDs used for plan filenames must be UUIDs.

S3 backends require an explicit `infra_state_bucket.key` in pv-cloud Values.
Choose a unique key per Instance and keep it stable. Example keys are placeholders
and must be unique within your bucket.

## RPC fields

RPC command submission uses `command_name` and `arguments: {"json":"..."}`.
Values, arguments, results and argument schemas use JSON document envelopes.
Execution references use `execution_id`; statuses use `COMMAND_EXECUTION_STATUS_*`.
Provider discovery uses `ListProviders` and reports `availability`.
