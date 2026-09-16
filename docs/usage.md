# Using Kit

## Local execution and secrets

Run these commands from the repository root. Files under `examples/` contain plain Values JSON. Generate
configuration using the installed local tools:

```sh
OPENTOFU_PATH="$PWD/.cache/tools/opentofu/1.11.5/tofu" \
ANSIBLE_PATH="$PWD/.cache/ansible-2.20.1/bin/ansible-playbook" \
  task --silent config > /tmp/kit-config.json
```

A pv-cloud invocation has this form; commands such as init/plan/apply contact real
services and require your chosen credentials and infrastructure inputs:

```sh
bin/inframill exec opentofu-0-1-0.init \
  --config /tmp/kit-config.json \
  --blueprint-dir blueprints/pv-cloud/versions/0.1.0 \
  --values-file examples/pv-cloud-0.1.0.json \
  --name production/example --secrets-file /private/path/secrets.json
```

For a published source replace `--blueprint-dir` with `--blueprint` and its tag or
digest reference. These source flags are mutually exclusive. Use `--args-file` for
command arguments and `--result-file` with a new path outside source/data/input
locations for machine results. Stdout is a stream of JSON reply records; tool
diagnostics go to stderr. A result record's status, not mere file existence, decides
success. Cancellation does not roll back tool effects.

Secret files are JSON maps of local names to strings. The example's names are
`hetzner`, `s3_access` and `s3_secret`. Values select these names, never credential
values. Inframill supplies a project snapshot only to driver Execute; Kit does not
read `INFRAMILL_SECRET_*` or arbitrary inherited tool credential variables. Required
credentials must be nonempty and are checked before effects. Only selected values
reach the tool environment. Known injected values are filtered from tool diagnostics;
Kit results and shared artifacts contain only verified projections. Arbitrary
provider/playbook output must not intentionally emit transformed secrets.

For daemon use, manage the in-memory project set with Inframill's `secret replace`,
`secret list` and `secret clear`. Restart discards sets. Running commands retain their
snapshot; queued commands obtain the set current when they start. Local exec needs
`--secrets-file` on each invocation; omission means an empty set.

Remote Instance initialization uses:

```json
{"blueprint_reference":"ghcr.io/inframill/blueprints/pv-cloud:0.1.0","values":{}}
```

Replace the empty Values object with the example's configured Values. The daemon
must fetch the blueprint before initializing the Instance.

## Drivers

A blueprint has a static `manifest.yaml`, Values `schema.json`, and `template/`.
Each driver section pins its exact implementation and a `settings_file`. Templates
render nonsecret backend/address/user settings into `kit-driver.json` within that
driver's workspace. Secret references are names in this file, never credentials.
Manifest validation uses no files; Prepare reads and validates rendered inputs with
no tool execution, writes, network or secrets. Execute performs and verifies effects.

OpenTofu supplies init, validate, plan, apply, destroy and output. Plan returns a
`plan_id`; apply/destroy require it. Plans bind the Instance, pinned blueprint,
driver/tool/configuration and source bytes. Kit retires a plan before apply effects,
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
inframill-kit driver opentofu --driver-version 0.1.0
```

Inframill sends one JSON request on stdin, without adding protocol arguments. Kit
uses upstream `driver.Serve` for JSON Lines messages and final success/error replies.
For a manual discovery probe, pipe this JSON to that prefix:

```json
{"protocol":1,"method":"info","params":{}}
```

