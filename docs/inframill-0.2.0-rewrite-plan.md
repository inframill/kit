# Kit rewrite for Inframill 0.2.0

Status: Implemented; local acceptance passed. No backward compatibility.
Verification: [acceptance report](acceptance.md), 2026-09-16. External release checks are listed separately.
Date: 2026-09-16

## Objective

Rewrite Kit's driver implementations, blueprint integration and verification for
Inframill 0.2.0. Deliver maintained OpenTofu and Ansible drivers, multiple independently
versioned blueprints, and a runtime containing both Linux AMD64 and ARM64 images.
Keep manual build and push, primarily to `ghcr.io/inframill`. Bundle creation must
copy prebuilt Inframill binaries from the sibling project and build only Kit.

Nothing in Kit has been released. Build a new implementation across drivers,
blueprints, configuration, packaging, build tooling and tests against the current
Inframill contract. The existing implementation is reference material for required
capabilities and known failure cases; its architecture, code, schemas, filenames,
commands and release descriptors are not compatibility constraints.

There will be no compatibility adapters, legacy readers, aliases, migrations,
old-runtime support, archived Kit releases or preservation of obsolete generated
artifacts. Remove the superseded implementation and development versions from the
final tree. Git history is sufficient for reference. Multi-version support is a
requirement for future releases, not an obligation to retain today's prototypes.

## Evidence and target

The previous successful Kit acceptance run targeted Inframill revision
`b7008f358d146d5b4f74f0f84865b80e18fdf8e5` and binary version `0.0.0-dev`.
The sibling checkout is now clean at
`389ff39d87c66b4f7668ec231f0a20e4a5f5f494` (`clean engine`). Its native release
binary reports `0.2.0`, and all four platform binaries exist in
`../inframill/dist/0.2.0/`.

The audit used these local sources, rather than assumptions about the release:

- [Current author interface](../../inframill/pkg/driver/driver.go),
  [execution context](../../inframill/pkg/driver/command.go),
  [Serve](../../inframill/pkg/driver/serve.go),
  [request types](../../inframill/pkg/driver/requests.go) and
  [reply handling](../../inframill/pkg/driver/replies.go).
- [Manager](../../inframill/pkg/driver/manager.go),
  [manifest](../../inframill/pkg/driver/manifest.go),
  [binding](../../inframill/pkg/driver/types.go) and
  [tested author example](../../inframill/pkg/driver/example_test.go).
- [Inframill README](../../inframill/README.md),
  [baseline requirements](../../inframill/specs/0000-baseline/spec.md), and
  [implemented Engine review](../../inframill/specs/0001-engine-contract-review/spec.md)
  with [ADR-0001](../../inframill/docs/decisions/0001-engine-contract-review.md).
- Kit's current drivers, pv-cloud manifest/schema/templates, CLI checks,
  packaging implementation and bundle lock.

A compilation-only probe, `go test -vet=off -run '^$'
./internal/driverkit ./tests/integration`, fails on removed metadata types,
validation callbacks, Values, binding fields and the changed Manager constructor.
The September 15 passing results are historical evidence, not 0.2.0 acceptance.

Observed prebuilt binary SHA-256 values:

| Platform | SHA-256 |
| --- | --- |
| linux/amd64 | `e67b82df98f2dc8cec97dfa47ce8af0f5f297edb2e5d57437865ebee4ee2b668` |
| linux/arm64 | `5f2a91a0b30bd1d7c32aa13c89b4fb7c67bd2c5019ab5ccf04500c641585cfea` |
| darwin/amd64 | `37196d9541747db1ee2849bb820024ba16889a612729e91db042e5ef9ddf29be` |
| darwin/arm64 | `d4a1ac5c111e3f71a8e9c61b9231765c540b923ac6d57037bf805a4b643d9acf` |

No Git release tags are present in the local checkout. The binary metadata omits
VCS revision, so its version alone does not prove which commit produced it.
Record source revision and binary hashes separately and prove their compatibility
with executable integration tests. Do not assume a published `v0.2.0` Go module.
The sibling declares Go 1.26; the supplied binaries were built with Go 1.27.0.
Retain Kit's pinned Go 1.27.0 initially; a compiler downgrade is outside this rewrite.

## Changes and their consequences

| Area | Previous Kit contract | Inframill 0.2.0 contract / required change |
| --- | --- | --- |
| Dispatch | Protocol action appended to argv | Configured prefix only; one stdin envelope with `protocol`, `method`, `params` |
| Methods | `info`, phased `validate`, `run COMMAND` | `info`, `validate_manifest`, `validate_command`, `run`; no protocol argv |
| Author API | Three validation callbacks | `Definition`, `ValidateManifest`, `Prepare` |
| Execution | `Execute(ctx)` | `Execute(ctx, driver.Secrets)` |
| Inputs | Drivers traverse Instance Values | Values belong to schema/rendering/metadata; drivers receive no Values |
| Manifest validation | May inspect source and Instance input | Receives only the driver's static manifest section; no paths or Values |
| Binding | InstanceID, Name, Blueprint, Digest, BlueprintReference | Only InstanceID, Name, BlueprintReference |
| Metadata | Safety, effects and verification fields | Description and arguments schema only; actual outcome checks still belong to drivers |
| Replies | One raw result JSON document | JSON Lines messages followed by one success/error envelope |
| Errors | Ordinary error text | `driver.Error` preserves a stable code and human message |
| Secrets | `INFRAMILL_SECRET_*` environment lookup | Project snapshot supplied only to Execute; no automatic environment credentials |
| Output confidentiality | Assumed host filtering | Host preserves driver output unchanged; Kit owns what tools and errors emit |
| Manager | Constructor loads; old ValidationRequest | `New(environ)`, explicit `Load`, `ValidateBlueprint`, `ValidateCommandArgs`, `Prepare` |
| Local CLI | `--instance` envelope | Required plain `--values-file`, exactly one of `--blueprint` / `--blueprint-dir`, optional `--secrets-file` |
| Remote initialization | `blueprint` | `blueprint_reference` with `values`; persistent Engine storage still uses its existing key |
| Engine API | Old names and lifecycle | New/Start/Shutdown, renamed catalog/wait/log methods, Command/Code constants and filter-bound cursors |

Driver protocol remains **1** despite these breaking changes. Blueprint API and
OCI media types also remain v1. Protocol number checks alone cannot establish
compatibility; release locks and real producer/consumer tests must do that.

Engine/RPC driver status is `available` / `unavailable`. The independent
`pkg/driver.Manager` still exposes `Connected` / `Offline`. Tests must assert the
correct vocabulary at each boundary rather than globally renaming these constants.
Kit currently imports `pkg/driver`, not the Engine API or generated RPC bindings.
Engine changes affect CLI/daemon integration and documented behavior; they do not
justify adding an Engine implementation or generated client layer to Kit.

## Target architecture

### Driver packages and entry point

Replace `internal/driverkit`'s combined Config/Arguments structure and name-based
branching with separate implementations and small shared components:

```text
cmd/inframill-kit/                version/list and configured driver dispatch
internal/catalog/                exact release identities and implementation factories
internal/drivers/opentofu/        manifest, settings, commands, plan verification
internal/drivers/ansible/         manifest, settings, inventory, recap verification
internal/toolrun/                 bounded processes, selected environments, diagnostics
internal/artifacts/               rooted file IO and shared node artifact contract
internal/packaging/               release validation, deterministic OCI, immutable push
cmd/kit-build/                    manual authoring and release operations
```

Use explicit factories keyed by driver name and exact version. Metadata is not an
implementation: register only deliberately implemented versions, never infer
handlers from arbitrary YAML descriptors. The Kit release selects included factories;
the bundle selects a validated subset. Unsupported versions fail explicitly.
Compatible patch releases may deliberately reuse implementations with tests.

The proposed configured prefix is
`inframill-kit driver opentofu --driver-version 0.1.0 [--tool /absolute/path]`.
Consume that prefix, reject leftover argv, then call the upstream `driver.Serve`
with empty Args, explicit streams and a cancellation-aware context. Keep `version`
and `drivers list` as Kit administration commands. Driver discovery uses a JSON
`info` envelope, not a trailing `info` argument.

Do not implement another JSON Lines protocol parser/server. Return typed results
and typed errors; emit optional progress through `scope.Message`; route tool logs
through `scope.Logs`. Check write errors and cancellation. Preserve process-group
inheritance, no detached children, noninteractive tools and bounded captured output.

### Static manifest and rendered settings

Use a static per-driver manifest for implementation identity and permitted behavior,
and a strict, driver-owned rendered `kit-driver.json` for nonsecret dynamic settings.
For example, an OpenTofu manifest section can declare:

```yaml
required_driver_version: 0.1.0
id: opentofu-0-1-0
settings_file: kit-driver.json
backend: s3
node_output: inframill_nodes
artifact: opentofu-0-1-0-nodes-v1.json
```

`template/opentofu-0-1-0/kit-driver.json.gotmpl` renders a document such as:

```json
{
  "schema": 1,
  "backend_config": {"bucket": "example-state", "region": "fsn1", "use_path_style": true},
  "environment": {"HCLOUD_TOKEN": "hetzner", "AWS_ACCESS_KEY_ID": "s3_access", "AWS_SECRET_ACCESS_KEY": "s3_secret"}
}
```

Environment values here are **secret names, never credential values**. Templates
use `toJSON` for proper escaping and types. Define customizable secret-name Values
in the new blueprint schema and render their names into settings. Choose clear new
field names without preserving old input shapes. Do not implement `from_value` /
`secret_from_value` resolvers inside the driver.
Ansible settings render address selection and SSH user; its static manifest keeps
producer identity, artifact name, connection policy, declared roles and playbooks.

Validation ownership:

1. Engine validates Values against the blueprint schema and renders templates.
2. `ValidateManifest` validates only strict typed manifest data, exact driver
   version, identifier/path syntax, allowed fields and internal relationships.
3. `Prepare` revalidates the manifest and command arguments, reads bounded settings
   and required rendered files beneath the supplied workspace, and checks
   command-specific prerequisites without tool execution, writes, credentials or
   network calls. Syntax-check must remain possible without an inventory artifact.
4. `Execute` checks required secrets before effects, rechecks mutable effect inputs
   as needed, runs tools and verifies results. Run calls Prepare in a new process;
   no memory/cache from an earlier validation invocation is assumed.

Packaging validates required template paths and release/manifest agreement.
Driver source checks formerly done during blueprint validation move to packaging
or command preparation; do not recover them by reading Engine instance.json or
walking outside the supplied workspace/artifact roots. Dynamic settings schema
errors must reject before the relevant command has effects. The Engine does not
call command preparation as part of every Instance initialization.

Use strict separate OpenTofu and Ansible settings types, size limits, relative
paths, no symlinks/traversal and immutable copies in prepared commands. Keep explicit
Kit artifact/config schema numbers at 1 for the current prerelease contract; reject
old incompatible shapes rather than silently accepting missing fields.

### Execution secrets and diagnostics

Remove `INFRAMILL_SECRET_*` lookups and the legacy secret-reference resolver.
Only `Execute(ctx, secrets)` may obtain credential values. Preserve the upstream
secret-name syntax `^[a-z][a-z0-9_-]{0,127}$`; define command-specific nonempty
requirements. Missing required names return `MISSING_SECRET`, naming the key
without its value, before creating files or invoking a tool.

Build a fresh allowlisted child environment for each execution. Forward only
configured credentials needed by that command, alongside the minimal operational
environment. Manifest/settings must not allow arbitrary environment injection.
Keep AWS/Hetzner and optional TF_ENCRYPTION values out of argv, config files,
rendered files, node artifacts, plan metadata and returned errors/results.

Define and test a credential matrix: offline validation and Ansible syntax/inventory
need none; OpenTofu init needs backend credentials where applicable; state reads
need backend credentials; plan/apply/destroy also need provider credentials as
applicable. Ansible keeps SSH-agent authentication and must not acquire an arbitrary
private-key-file writing facility as part of this rewrite.

Audit every tool-output and error path because the host no longer redacts. The
runner must suppress or redact known injected credential values in forwarded
text, including split writes, multiline values and JSON-escaped representations.
Keep parsed machine output private until projected into an allowlisted typed
result; never include raw rejected JSON in errors. No generic filter can guarantee
removal of arbitrarily transformed secrets: templates/tools must not intentionally
emit them, and supported fixture output paths must be checked with synthetic canaries.
Do not claim control of arbitrary user playbook/provider behavior.

Document daemon `secret replace/list/clear` and local `--secrets-file`, project
selection through Instance name, restart loss, and per-running-command snapshots.
Omission of the local secrets file means an empty set even for an existing Instance.
Do not add Kit-managed secret persistence, fallback environment loading or Engine
secret APIs.

### OpenTofu behavior and artifact identity

Retain init, validate, plan, apply, destroy and output, including refresh-only plans.
Preserve readonly provider locks, noninteractive execution, InstanceID-scoped backend
keys, saved-plan-only apply/destroy and actual empty-managed-state verification.

Bind saved plans and node artifacts to the new complete Binding: InstanceID, Name
and the **exact pinned BlueprintReference** supplied by Inframill. Do not reconstruct
removed Blueprint/Digest fields or assume all references are registry tags.
Exercise both pinned local and OCI sources. Supplement binding with producer/configured
ID, driver/tool version, relevant config/settings hashes, rendered source hash,
plan hash and originating CommandID. Plan metadata should explicitly record its mode.
Exclude mutable runtime caches/state from source hashing; include rendered settings
and relevant provider lock/configuration files.

Before applying: reject wrong Instance/reference/producer/version/mode/config/source
or corrupted plan bytes without effects. Invalidate shared inventory before tool
mutation; failure/cancellation must not leave it valid. Consume a successfully used
plan so it cannot be replayed. Define and test partial-publication/cleanup failures;
never blindly retry a command that may already have changed infrastructure.

Keep node artifacts as an explicit shared contract between independent drivers.
Validate producer version, binding, command identity, validity marker, unique hosts,
architecture and address shape. Preserve flat names, private atomic writes and
bounded reads. Implement only the new artifact contract; no old-artifact reader
or conversion path is included. Tests create fresh artifacts.

### Ansible behavior

Retain inventory, syntax-check, check and apply, host/tag selection, explicit address
choice, host-key policy and controlled callback/configuration. Rebuild inventory
from a verified node artifact; never silently fall back to a different address.
Validate selected playbooks and literal host/tag arguments in preparation.

Keep real outcome checks: zero process exit alone is insufficient. Require a
nonempty structured recap covering exactly the selected hosts, with no failed,
unreachable, ignored or rescued failures and meaningful successful tasks. Check
mode remains a prediction. Keep ARM64/AMD64 assertions and service checks in the
blueprint rather than adding tool-specific behavior to Engine.

### Initial versions and future version support

Start the rewritten Kit, drivers, pv-cloud and bundle at `0.1.0`. These are initial
unreleased Kit identities, independent of the consumed Inframill `0.2.0` binary.
Do not increment versions to accommodate changes to prototypes. Keep full
MAJOR.MINOR.PATCH selection and per-version driver IDs for future releases.

Replace the existing blueprint sources, release descriptors, bundle locks and
examples with the new baseline. Rebuild `blueprints/pv-cloud/versions/0.1.0` and
its sealed release descriptor from the new contracts. Remove obsolete prototype
versions, legacy blueprint snapshots such as `old-blueprints/`, and obsolete Kit
build outputs. There is no old/new release coexistence or migration phase.

Keep packaging generic for multiple blueprint names and independently maintained
versions. The initial production set contains the rewritten pv-cloud only. Build
fresh local-resource fixtures with multiple versions under the new contract.
Exercise multiple driver versions through explicit test factories with distinct
contracts to catch aliasing and fallback. These fixtures test future extensibility;
they do not represent supported legacy production releases.

Once releases actually begin, sealed version identities and immutable tags become
publication guarantees. They do not require preserving current unpublished builds.

Split examples into plain local Values documents and clearly named remote Instance
configurations using `blueprint_reference`. Rewrite scripts to pass Values and source
separately. Use `--result-file` for machine results, check terminal status, and retain
stdout JSON Lines/stderr evidence rather than treating stdout as one result JSON.
Respect result-file placement and no-overwrite rules.

### Packaging, source dependency and runtime

Rewrite packaging and release tooling with deterministic OCI archives, source
sealing, digest verification, credential helper support and conflicting-tag rejection.
Use the media types and limits required by the current Inframill consumer. Validate
manifest structure and schema extensions against 0.2.0 acquisition. Add an
explicit supported Inframill release constraint to Kit release metadata and enforce
it alongside driver requirements; `protocol: 1` alone is insufficient.

Keep the local Go replacement for the sibling public package initially. Release
builds must check the locked source revision and reject relevant uncommitted drift
before compiling Kit; development checks may report the current checkout explicitly.
Record dependency/source identity in build metadata. If a published immutable Go
module becomes available, adopting it is a separate verified dependency change,
not an assumption in this plan. Do not vendor a third_party snapshot.

Create the initial Kit bundle `0.1.0` lock for core `0.2.0` with the observed hashes and paths
`0.2.0/inframill_0.2.0_linux_{amd64,arm64}` relative to `INFRAMILL_DIST_DIR`.
Native copying must select the versioned host binary from the same release, verify
its checksum/format/version, and avoid a stale unversioned `dist/inframill` fallback.
Never run the sibling release task implicitly during a Kit bundle build.

Build Kit for both architectures, verify copied core ELF architecture/checksum,
probe its version in each image, and compare installed image bytes to the inputs.
Use the audited OpenTofu/Ansible/Python/base-image versions as initial dependency
selections, revalidate their assets and compatibility, and generate the new locks.
Reusing upstream dependency versions does not require retaining old Kit code.
Keep source/notices and explicit manual package/build/push Tasks. No automatic GHCR
pushes or new CI publication workflows. Preserve `KIT_DIST_DIR` isolation.

## Implementation sequence and acceptance gates

| Phase | Deliverables | Gate before proceeding |
| --- | --- | --- |
| 1. New baseline | Final manifest/settings/error/result schemas, factories, initial versions, core source pin and removal inventory | New contracts specified; no legacy preservation or migration requirements |
| 2. Common runtime and protocol | New entry point, upstream Serve, factories, typed configuration, shared process/artifact primitives | Real Manager New/Load discovery, JSON info/validation/run, unknown-version rejection, cancellation and protocol failure tests pass |
| 3. OpenTofu vertical slice | Local-resource rendered settings, execution secrets, plan/artifact lifecycle | Actual local init/validate/plan/apply/output/destroy through native core 0.2.0; corruption/cross-identity/mode/replay tests pass |
| 4. Ansible vertical slice | Rendered connection settings, verified inventory, callback and recap | Actual local syntax/check/apply, host/tag and failure tests pass, independent producer/consumer version checks pass |
| 5. Blueprint and authoring rewrite | Fresh pv-cloud 0.1.0, examples, fixture versions, manifest/release validation and rewritten generic packaging | ARM64/AMD64/mixed rendering, provider mocks, deterministic package/source hashes and consumer fetch tests pass |
| 6. Runtime and Engine integration | New core/bundle locks, native copy, both images, rewritten CLI/daemon harness | Local/registry/daemon workflows, project-secret behavior and both image architecture checks pass |
| 7. Removal and handoff | Remove superseded code, prototype versions, old-blueprints, obsolete Kit outputs/tests/docs; write current README, architecture and acceptance | Fast/full gates pass, no legacy code or artifacts in the delivered baseline, exact new digests and limits recorded |

Implement phases in order; a local OpenTofu slice should work before pv-cloud or
container work expands the failure surface. Temporary test doubles belong only in
tests. Temporary coexistence during implementation does not become a delivered
compatibility layer; remove superseded code as each replacement is complete.
Use `gofmt` on changed handwritten Go and Go tests with `-vet=off`; no lint/vet.
Do not rewrite sibling Engine, its protected harness, or generated RPC files.

Required acceptance cases, mapped to the contracts above:

- **AC-01 — Protocol:** New envelope and JSON Lines semantics work through the real
  Manager and binaries; leftover protocol argv, malformed/extra fields, missing
  final replies, nonzero exit after success, output failures and cancellation fail
  correctly. Cover typed error code preservation and progress/log separation using
  targeted Kit cases; rely on upstream tests for exhaustive transport internals.
- **AC-02 — Preparation:** Manifest validation needs no files; Prepare may read only
  required bounded files, does not spawn tools/write/contact services, and succeeds
  without credentials where inputs are otherwise valid. Repeated preparation and
  fresh-process run behave consistently. Old Values/phased-validation shapes reject.
- **AC-03 — Secrets:** Missing/empty-required secrets reject before effects;
  unrelated/inherited credentials are not forwarded. Synthetic credentials do not
  appear in argv, Kit-managed files, results, errors or logs, including chunked and
  escaped diagnostics. Valid secret-name edge cases and per-command requirements pass.
- **AC-04 — Tool outcomes:** Plan/apply/destroy and inventory/recap tests preserve
  current safeguards, including partial effects, stale/missing artifacts, changed
  settings/sources, wrong pinned references, tool failures and descendant cancellation.
- **AC-05 — Versions:** At least two blueprint names and multiple fixture versions
  are packaged/consumed; at least two explicit test driver implementations coexist.
  Exact dependencies, alias isolation and missing/unsupported versions fail closed.
- **AC-06 — Engine integration:** Native CLI uses new flags; a disposable daemon
  discovers Kit, initializes via blueprint_reference, executes commands, returns
  results/logs/history, and exposes available/unavailable status correctly. Test
  project isolation, replacement/clear, running snapshots versus queued commands,
  and restart loss of secrets using synthetic fixtures. Never treat cancellation of
  a remote wait as cancellation of accepted execution; poll its terminal outcome.
- **AC-07 — Packages:** New consumer fetches tags and digests from an empty local
  registry/cache, works offline after acquisition, rejects tampering and conflicting
  tags, and preserves pinned Instance identity. Check the unchanged media-type and
  schema-extension assumptions against the actual binary.
- **AC-08 — Architectures:** Full fixture lifecycles and pv-cloud provider mocks pass
  inside ARM64 and AMD64 images; installed core bytes equal locked sibling binaries.
  Record emulation separately. Native AMD64 and real cross-architecture SSH/service
  checks remain external acceptance if that hardware/environment is unavailable.
- **AC-09 — Release:** Builds never push; manual push uses existing bytes, compatible
  release selections and immutable tags. All checks start with fresh isolated data;
  obsolete Kit-generated builds are removed during the rewrite, and the delivered
  artifacts are rebuilt from the new baseline. Report revision, versions, hashes
  and image index digest. No legacy implementation, decoder, alias or migration remains.

Run fast tests throughout meaningful slices and one full local gate for the final
candidate. Full local verification uses disposable local resources and registry;
GHCR publication and real Hetzner/S3/SSH effects are separate explicit operations.

## Completion

The rewrite is complete when AC-01 through AC-09's locally executable checks pass,
the new artifact paths/digests are recorded, and the repository contains only the
new implementation, blueprints, release baseline, fixtures and current documentation.
Remove obsolete acceptance claims and legacy reference trees from the delivered
project. List unavailable external checks explicitly; do not equate emulated AMD64
with native evidence or provider mocks with a real deployment.

There are no previous Kit releases to preserve or publication-history questions to
resolve. Old Instances, input formats and artifact formats are outside the contract.
Future multiple-version support remains designed and tested using the new baseline.
The sibling Inframill source and supplied 0.2.0 binaries are inputs to this rewrite.

The implementation, removal of superseded code/reference trees and artifact
regeneration are complete. All locally executable gates passed; see the acceptance
report for evidence and final digests. Nothing was published to GHCR. The audit
findings above describe the starting point, not the current implementation.
