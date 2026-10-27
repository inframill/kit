# Dispatch provider

Dispatch 0.1.0 pushes directories from its Instance workspace to selected nodes
and runs Make targets there. It invokes the bundled Ansible Core 2.20.1 executable
directly; it does not call or require the Ansible provider. Kit registers the
independent `dispatch` executable alongside OpenTofu and Ansible.

## Workspace and host input

The engine supplies Dispatch's workspace root and the Instance's shared artifact
directory separately:

```text
instance/
  artifacts/opentofu-nodes-v1.json
  workspace/dispatch/
    kit-provider.json
    api/Makefile
    api/...
    worker/Makefile
    worker/...
```

Dispatch reads the existing schema-1 node artifact published by OpenTofu apply.
It validates the producer ID/version, artifact scope, execution identity,
validity flag and node records. An Ansible-generated inventory is not required.
The artifact is checked again immediately before execution, so retiring or
invalidating it prevents new Dispatch effects.

Bind Dispatch in your Blueprint:

```yaml
command_arguments:
  dispatch.push:
    type: object
    additionalProperties: false
    required: [directory]
    properties:
      directory:
        type: string
        enum: [api, worker]
  dispatch.make:
    type: object
    additionalProperties: false
    required: [directory, target]
    properties:
      directory:
        type: string
        enum: [api, worker]
      target:
        type: string
        pattern: '^[a-zA-Z0-9_][a-zA-Z0-9_./-]{0,127}$'
provider_settings:
  dispatch:
    version: '0.1'
    settings:
      id: dispatch
      settings_file: kit-provider.json
      producer_id: opentofu
      producer_version: 0.4.0
      artifact: opentofu-nodes-v1.json
      connection: ssh
      host_key_policy: accept-new
```

Command declarations use the configured provider ID. With an alias such as
`delivery`, use `delivery.push` and `delivery.make` in both `command_arguments`
and command submissions. The Blueprint schemas validate arguments before
admission; Dispatch checks the selected mapping, source and node artifact during
direct execution.

`connection` defaults to `ssh`. Host-key policy defaults to `strict`; `accept-new`
records unknown hosts and still rejects changed keys. Authentication uses the
same SSH agent, configuration and identities as the existing Ansible provider.
See [provider SSH access](usage.md#providers) for Docker mounts and agent setup.
The `local` connection is available for loopback-only development fixtures.

Put the settings and payloads under `templates/dispatch/` in the Blueprint.
The engine renders `.gotmpl` files and copies ordinary files into
`workspace/dispatch/`. For example, `templates/dispatch/kit-provider.json.gotmpl`:

```json
{
  "schema": 1,
  "address_mode": {{ toJSON .ansible.address_mode }},
  "ssh_user": {{ toJSON .ssh.user }},
  "directories": {
    "api": {
      "source": "api",
      "destination": "/opt/myapp/api",
      "select": {"roles": ["web"]},
      "exclude": [".git/**", "node_modules/**", "**/*.env"],
      "become": true
    },
    "worker": {
      "source": "worker",
      "destination": "/opt/myapp/worker",
      "select": {"names": ["worker-1", "worker-2"]}
    }
  }
}
```

This example uses the pv-cloud Values fields `ansible.address_mode` and
`ssh.user`; adapt the template to your Blueprint's schema. Built-in pv-cloud
`0.6.0` and later include a `postgres` mapping for `data` nodes; `0.7.0` and
`latest` also include an `inframill` mapping for `worker` nodes.
After OpenTofu apply and Ansible guest preparation, install it with:

```sh
bin/inframill instance exec example dispatch.push --namespace production \
  --arguments '{"directory":"postgres"}' --stream --timeout 15m
bin/inframill instance exec example dispatch.make --namespace production \
  --arguments '{"directory":"postgres","target":"start"}' --stream --timeout 15m
```

The current payload runs PostgreSQL through Compose with host networking in
`/opt/pv-cloud/postgres`. Its `start` target creates `app1` and `app2` with separate
owner passwords supplied by required namespace secrets `app1-password` and
`app2-password`, applies memory and connection budgets, and verifies TLS access
and database isolation. All services use the shared `tls.certificate` and
`pv-cloud-tls-private-key` namespace secret. External PostgreSQL ingress is
configured by OpenTofu specifically for data nodes. See the
[pv-cloud instructions](../blueprints/README.md#pv-cloud) for credentials,
role requirements and lifecycle targets. Tags before `0.6.0` do not bind Dispatch.

pv-cloud `0.10.0` and `latest` use a single target enum: `start`, `status`, `verify`,
and `stop`.
Unknown targets reject before command admission. The selected payload's Makefile
determines which targets it supports:

| Directory | Targets |
| --- | --- |
| `postgres` | `start`, `status`, `verify`, `stop` |
| `inframill` | `start`, `status`, `verify`, `stop` |
| `millway` | `start`, `status`, `verify`, `stop` |

All three payloads default to `start`, which validates inputs, logs into the configured
registry, starts the service and waits for readiness. `verify` checks the running
service, `status` reports its Compose status, and `stop` preserves data and
credentials. After changing namespace secrets and repeating the push, `start`
applies the supplied application passwords or Inframill credentials and verifies
the service. PostgreSQL preserves its administrator password.

The Millway mapping selects `web` nodes and uses the data node's `app1` database
and worker Inframill endpoint. Its verification checks real database TLS access,
the migration ledger against the pinned image manifest, fresh authenticated
Inframill version responses, and the application's HTTPS API and UI. Host-side
helpers preserve the unchanged non-root Millway image.

Sources are clean relative directories beneath the provider-supplied root;
`.` and paths escaping that root are rejected. Destinations are clean absolute
POSIX paths other than `/`. Every mapping has a unique identifier. An alias such
as `delivery` uses `templates/delivery/` and `workspace/delivery/`; Dispatch uses
the supplied root directly without appending its own name.

Files must already exist in the daemon's workspace. Dispatch does not upload a
laptop directory to a remote daemon. Blueprint refreshes also do not change an
existing Instance's source snapshot. Instance rerender replaces provider-created
workspace files, as described in [Instance lifecycle](usage.md).

## Secret files

A mapping can inject namespace secrets into files beneath its destination:

```json
"secret_files": {
  "secrets/root_token": "inframill-root-token",
  "secrets/tls_private_key": "pv-cloud-tls-private-key"
}
```

Keys are clean relative file paths; values are namespace secret names. Only the
selected mapping's secrets are resolved, during `push` execution and before any
remote effects. Missing, empty or whitespace-only secrets reject, as do values
larger than 1 MiB. A mapping accepts at most 100 secret files. Secret files cannot
also be parents of other secret files.

Dispatch stages credentials in an execution-owned directory with mode `0700` and
files with mode `0600`, then uses Ansible copy with `no_log` and disabled diffs.
Secret bytes do not enter the source archive, generated playbook, command
arguments or structured results. Local staging is removed when execution returns;
an abrupt process kill can leave files until the workspace is cleaned.
Remote parent directories use mode `0700` and files use `0600`, owned by the
connecting user or the mapping's become user. Injection occurs after extraction
and before Make, and preserves the exact secret bytes. Repeat pushes refresh the
files from the current namespace secrets. Make-only commands use existing remote
files and do not resolve or replace credentials.

Use source excludes such as `secrets/**` to keep credentials accidentally placed
in a payload directory out of the archive. Public certificates can be ordinary
rendered configuration, as in pv-cloud's `tls.certificate`. Rebuild the Dispatch
executable and controlling Kit image after adding this support.

## Selectors

Directory mappings accept `select`:

```json
{"names": ["web-1", "web-2"], "roles": ["web"], "architecture": "arm64"}
```

Names are exact node names. Roles match any listed role. Architecture is `arm64`
or `amd64`. Values within a list match any entry; fields intersect. An empty
selector matches all artifact nodes. Unknown names/roles, duplicates and an
empty match reject before connecting to nodes.

`address_mode` is `public_ipv4`, `private_ipv4` or `fqdn`. Every selected node
must have a nonempty, unique address in that field; there is no fallback.
Unselected nodes do not need a usable address in that mode.

## Commands

Push one configured directory:

```sh
bin/inframill instance exec example dispatch.push --namespace production \
  --arguments '{"directory":"api"}' --stream --timeout 15m
```

Run a target against the existing remote directory after pushing:

```sh
bin/inframill instance exec example dispatch.make --namespace production \
  --arguments '{"directory":"api","target":"deploy"}' --stream --timeout 15m
```

`push` accepts only the required `directory` string. `make` accepts exactly two
required strings: `directory` and `target`. Node selection comes from the directory
mapping. Run `make` once for each desired target after a successful push.

The command module uses `argv`, a fixed working directory and disabled
argument-variable expansion. Targets cannot be command-line switches or shell
fragments. Put Make variables and dependencies in the payload's Makefile.

Ansible schedules nodes in parallel using its default forks. Other nodes may have
completed their work when a node fails. Operations do not roll back completed
transfers or target effects.

## Transfer and results

Push creates one gzip-compressed tar snapshot and sends the same bytes to each
selected node with `ansible.builtin.copy`. Ansible's command module extracts it
inside the configured destination. A remote staging directory is cleaned with
an Ansible `always` block, and local temporary files are removed when execution
returns. Cancellation or an unreachable host can prevent remote cleanup.

Push copies the **contents** of `source` into `destination`. It creates the
destination with mode `0700`, preserves source permission bits and modification
times, and extracts as the connecting user (or the become user). Additional
remote files and build outputs are preserved. Source deletions do not remove
remote files. There is no mirror/delete mode in this version. Preserving mtimes
lets Make track updated inputs across pushes.

Excludes are globs relative to the source root. `*`, `?` and character classes
match within one path segment; `**` matches zero or more complete segments.
Excluding a directory excludes its subtree. `.git/**` matches the root `.git`
directory, while `**/.git/**` matches at any depth. No files are excluded
implicitly. Links and special files are rejected unless excluded. Each source
is limited to 10,000 entries, 256 MiB of file contents, and 32 MiB per file.

Managed nodes need SSH access, Python, `tar` with gzip and `--no-same-owner`
support, and Make plus the dependencies used by their Makefiles. Dispatch does
not install these tools. A mapping's optional `become` applies to its transfer
and Make tasks; the default is false.

Successful results contain the mapping name, destination, whether files were
pushed, the SHA-256 of the transferred archive, per-host task counters and
the requested target's exit code. Success requires the complete expected task count for every
selected node and exit code zero for every requested target. Failed, unreachable,
skipped, ignored or rescued tasks reject the execution.

Make stdout/stderr are emitted as host-tagged JSON diagnostics when each target
finishes, including failed targets. They are not streamed line by line while a
target runs. Namespace secret values known to the runner are redacted; Ansible's
`no_log` suppresses result details. Structured results contain no command output.
Diagnostics and machine output retain the shared runner's 32 MiB limits.

Both commands declare `read_write`, preserving whole-Instance scheduling with
the other providers. Preparation only validates and reads inputs; temporary
files, tool execution and remote effects happen during Execute.

## Development verification

```sh
python3 providers/build.py test
DISPATCH_ANSIBLE_PATH="$PWD/.cache/ansible-2.20.1/bin/ansible-playbook" \
  python3 providers/build.py test --name dispatch
python3 providers/build.py build --name dispatch
python3 providers/build.py build --name dispatch --native
python3 tests/dispatch_check.py
```

The opt-in real-tool test uses only temporary local source/destination directories
through Ansible's local connection. It checks archive transfer, exclusions,
permissions, literal paths and file contents, individual targets, failure
diagnostics and secret redaction. The standard suite uses stubs and requires no remote nodes.

The daemon check additionally requires the native provider outputs, local tools
and `bin/inframill`. It renders a temporary Blueprint, publishes real OpenTofu
node output and exercises Dispatch under a configured provider alias. It checks
Make rebuilds after source changes and reports failed targets.
This check also runs as part of the native/full acceptance suite.
