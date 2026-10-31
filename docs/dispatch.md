# Dispatch provider

Dispatch 0.1.0 pushes directories from its Instance workspace to selected nodes
with recursive SCP and runs Make targets over SSH. Kit registers the independent
`dispatch` executable alongside OpenTofu and Ansible. Dispatch uses system OpenSSH
clients and has no pinned Ansible tool dependency.

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
        type: array
        minItems: 1
        uniqueItems: true
        items:
          type: string
          enum: [api, worker]
  dispatch.make:
    type: object
    additionalProperties: false
    required: [directory, target]
    properties:
      directory:
        type: array
        minItems: 1
        uniqueItems: true
        items:
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
  --arguments '{"directory":["postgres"]}' --stream --timeout 15m
bin/inframill instance exec example dispatch.make --namespace production \
  --arguments '{"directory":["postgres"],"target":"start"}' --stream --timeout 15m
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
registry, starts the service and runs verification. `verify` checks the running
service, `status` reports its Compose status, and `stop` preserves data and
credentials. After changing namespace secrets and repeating the push, `start`
applies the supplied application passwords or Inframill credentials and verifies
the service. PostgreSQL preserves its administrator password.

The Millway mapping selects `web` nodes and uses the data node's `app1` database
and worker Inframill endpoint. Millway's native `verify` CLI checks database TLS
access and fresh authenticated Inframill version responses inside the running
container. Make and Dispatch report the CLI output and fail when it fails, without
additional checks. The service uses Millway's explicit `start` CLI command and preserves
the non-root image.

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

Dispatch sends credential bytes through SSH stdin during installation. Values
never enter command arguments, generated scripts or structured results. A remote
temporary directory has mode `0700` and is removed after installation.
Remote parent directories use mode `0700` and files use `0600`, owned by the
connecting user or root when `become` is enabled. Injection occurs after copying
the source directory and preserves the exact secret bytes. Repeat pushes refresh the
files from the current namespace secrets. Make-only commands use existing remote
files and do not resolve or replace credentials.

Public certificates can be ordinary rendered configuration, as in pv-cloud's
`tls.certificate`. The complete source directory is copied, including dotfiles.

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

Push selected directories, each to its configured nodes:

```sh
bin/inframill instance exec example dispatch.push --namespace production \
  --arguments '{"directory":["api","worker"]}' --stream --timeout 15m
```

Run a target against the existing remote directory after pushing:

```sh
bin/inframill instance exec example dispatch.make --namespace production \
  --arguments '{"directory":["api","worker"],"target":"deploy"}' --stream --timeout 15m
```

Both commands accept a required `directory` list of unique mapping names.
`make` also requires one `target` string, applied to every selected directory.
String directory inputs remain supported for saved Blueprints. Current pv-cloud
forms expose multi-selection, including a one-element list for individual payloads.
All selected mappings, sources and required push secrets are checked before
remote changes. Node selection comes from each directory's own mapping.

Make runs in the configured destination. Paths and targets are shell-quoted;
targets cannot be command-line switches or shell fragments. Put Make variables
and dependencies in the payload's Makefile.

Push processes up to eight directory/host transfers concurrently. Make processes
directories in selection order, with up to eight hosts concurrently within each
directory, and stops before the next directory if any target fails. For pv-cloud
startup, select `postgres`, `inframill`, then `millway`. Other nodes may have
completed their work when a node fails. Operations do not roll back completed
transfers or target effects.

## Transfer and results

Push copies the entire source directory with `scp -p -r` into a private temporary
directory on each selected node. A remote shell installs its contents into the
configured destination using `cp -pR`, then writes the mapped secret files. The
temporary directory is removed on completion. Failed transfers attempt cleanup;
cancellation or an unreachable host can prevent remote cleanup.

Push copies the **contents** of `source` into `destination`. It creates the
destination with mode `0700`, preserves source permission bits and modification
times, and installs as the connecting user (or root with `become`). Additional
remote files and build outputs are preserved. Source deletions do not remove
remote files. There is no mirror/delete mode in this version. Preserving mtimes
lets Make track updated inputs across pushes.

There are no archive or exclusion steps. Links and special files are rejected.
Legacy `exclude` entries in saved mappings are accepted and ignored.
Each source is limited to 10,000 entries, 256 MiB of file contents, and 32 MiB per
file. The directory must remain unchanged while a push is running.

Managed nodes need SSH/SFTP access, standard POSIX file utilities, and Make plus
the dependencies used by their Makefiles. `become: true` uses noninteractive
`sudo` for installation and Make. The default is false. `connection: local` uses
local shell commands and recursive `cp` for development fixtures.

Successful results contain the mapping name, destination, whether files were
pushed, per-host exit codes and the requested Make target's exit code. Every
selected host must finish successfully. Archive checksums and Ansible task
counters are no longer returned.
With multiple directories, `directories` contains these results keyed by mapping
name; a single directory retains the original result shape.

Make stdout/stderr stream as host-tagged JSON diagnostics while the target runs.
Namespace secret values known to the runner are redacted. Structured results
contain no command output. Diagnostics retain the shared runner's 32 MiB limit.
`--tool /absolute/path/to/ssh` overrides the SSH client; SCP is resolved beside it.

Both commands declare `read_write`, preserving whole-Instance scheduling with
the other providers. Preparation only validates and reads inputs; temporary
files, tool execution and remote effects happen during Execute.

## Development verification

```sh
toolai check
toolai go test --packages ./internal/providers/dispatch
toolai build release
python3 tests/dispatch_check.py
```

The standard suite uses temporary local source/destination directories and real
copy and Make commands. SSH-boundary fixtures execute the remote scripts locally.
Tests check complete directory transfer, permissions, modification times, literal
paths and file contents, individual targets, failure diagnostics and secret redaction.

The daemon check additionally requires the native provider outputs, local tools
and `bin/inframill`. It renders a temporary Blueprint, publishes real OpenTofu
node output and exercises Dispatch under a configured provider alias. It checks
Make rebuilds after source changes and reports failed targets.
This check also runs as part of the native/full acceptance suite.
