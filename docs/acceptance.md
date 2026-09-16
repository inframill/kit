# Local acceptance — 2026-09-16

Local acceptance passed with Kit 0.6.0-dev and prebuilt Inframill 0.6.0-dev.
The public Go dependency was checked against sibling source revision
`783640e5e56c0efa39417e7749aaaf635787cbc9`. The checks used the supplied sibling
source and prebuilt binaries.

## Recorded artifacts

| Artifact | Digest |
| --- | --- |
| pv-cloud 0.2.0 | `sha256:a3b4eb5ea3da749d795a9d921dce37be6c62c610982790fd98ce462859988f13` |
| Kit image 0.6.0-dev index | `sha256:db05156c2e0f0314920066b4e01adfb5f77ab313e991cc4412d7870a3cf1b8c5` |

Outputs are in `dist/blueprints/pv-cloud/0.2.0/` and `dist/bundles/0.6.0-dev/`.
Their build records contain the component locks, source identity and checksums.
The image was built as `ghcr.io/inframill/kit:0.6.0-dev`. Publication to the disposable
registry used `/inframill/kit` and returned the same index digest. Both image variants
reported Kit 0.6.0-dev; blueprint and driver versions are 0.2.0.

Both image variants verify that `/usr/local/bin/inframill` is a byte-for-byte copy
of the corresponding locked sibling input:

| Architecture | Inframill SHA-256 |
| --- | --- |
| Linux AMD64 | `00854b8ce46b35448b9bd9bec99149fee3919e297debddc16639ebd3778c8447` |
| Linux ARM64 | `712ab6a0162bef18d05c393207706d2d3aadaf3e2e4fc077703dab8665d3ff90` |

## Executed checks

`task check-fast` passed: eighteen Python tests, all Go packages/integration tests
with vet disabled, the Kit build and release validation. `task bundle` and
`task check-full` passed for Kit 0.6.0-dev, including blueprint packaging to the disposable
registry. Python syntax, documentation links and `git diff --check` also passed.

Version-policy tests reject mismatched major/minor versions, descriptor identities,
image/Kit versions and core targets. Independent Kit and engine patches/prereleases
within the same major/minor line pass. The selected Kit descriptor supplies the
engine target; no Go version constant is required. Command tests reject mismatched
lines before builds, pushes, packaging or native copying can proceed.

After that version-selection change, `task check-fast`, `task blueprints:package`
and `task inframill:copy` passed again (fast-check log: `.cache/alignment-fast.log`).
The blueprint digest stayed unchanged. The image and full runtime checks recorded
here precede this tooling change; the image was not rebuilt for it.

| Acceptance area | Evidence |
| --- | --- |
| Protocol | Real upstream Manager/Serve discovery and JSON dispatch; distinct version schemas; structured messages/results; rejection of `validate_manifest`, the retired `config` field, protocol argv, missing/contradictory final replies and nonzero exit after success; process-group cancellation |
| Blueprint definition | Unified `blueprint.yaml` with inline Values schema and flat driver settings; strict rejection of unknown/duplicate fields, multiple documents, aliases/merge keys, invalid tags/types and retired source filenames; offline schema compilation and source sealing |
| Preparation | Static settings validation without files, command-specific file requirements, no preparation writes/tools/secrets, syntax checks without node inventory |
| Secrets | Required-secret errors before effects, restricted child environment, chunked/multiline/escaped output filtering, credential-bearing result rejection; daemon/local snapshot tests and checks for synthetic secrets in saved daemon files |
| Outcomes | Saved-plan source/reference/mode/integrity checks, failed-apply inventory invalidation and plan retirement; Ansible complete-host recap validation; real local tool lifecycles |
| Versions | Two production driver names, distinct test driver versions/contracts, multiple local-resource blueprint versions, generic packaging of pv-cloud and fixtures; exact selection and mismatches |
| Engine | New Instance and Command IDs verified as UUIDv7 on both architectures; real daemon runs Kit init/plan/apply/Ansible/destroy; history and JSON reply logs; available/unavailable health; project isolation, replace/clear, running/queued snapshots, restart loss; timed-out remote wait leaves execution running; reused local Instance receives no previous invocation's secrets |
| Packages | Deterministic packaging and graph integrity tests; disposable registry tag/digest pulls, idempotent pushes and conflicting-tag rejection; new Instance initialization from cache without authorized network acquisition; tampered cached source rejected without creating an Instance |
| Architectures | Local-resource workflows and four pv-cloud provider mocks pass inside both Linux images; native ARM64 and emulated AMD64; core byte hashes match supplied inputs |
| CLI | `server start`, `local exec` and `server command execution wait` use 0.6.0-dev paths; both images run `local driver list`, `local history` against active daemon storage and `server instance list` and `server driver list` with an explicit endpoint, using default and custom configurations |
| Container configuration | Default YAML and unchanged image command; named-volume persistence of Instances and successful command history across container replacement; read-only custom YAML with a changed server port; host-bound data resolved relative to the config while working in `/tmp`; unchanged config bytes; rejection of malformed YAML and read-only data on both architectures |
| Release | Root `VERSION` selects `0.6.0-dev` for builds, image tags, pushes and checks; eighteen Python tests include strict file validation and ignored overrides; `BLUEPRINT_VERSION` independently selects multiple fixture packages; manual-only publication, sealed production source and core requirements, sibling source pin and prebuilt binary verification, isolated tests |

Native pv-cloud checks rendered ARM64, AMD64 and mixed Values, ran Ansible syntax
checks and passed four OpenTofu provider-mock cases. Container verification repeated
real fixture workflows and provider mocks on both architectures. Test images,
registry containers, networks and daemon processes were cleaned up.

Logs: `.cache/0.6-bundle.log` and `.cache/0.6-full.log`. This report was
updated after verification; the image's source snapshot contains the earlier report.
Current contracts are documented in [architecture](architecture.md) and [usage](usage.md).

Retired production sources, descriptors and examples have been removed from the
working tree. Test fixtures retain multiple versions using the current blueprint
layout and driver protocol. The image recorded above predates this cleanup and
still contains its original source snapshot.

Cleanup verification passed `task check-fast`, an uncached run of
`go test -vet=off -count=1 ./tests/integration`, blueprint packaging, driver discovery
and documentation file-link checks. Packaging retained the pv-cloud digest above.
The catalog test now requires every embedded driver descriptor to have an executable
implementation. Fast-check log: `.cache/cleanup-fast.log`.

## External release checks

These were not performed: native AMD64 hardware execution, real ARM64/AMD64 SSH
guests and service checks, live Hetzner/S3 interoperability, and GHCR publication
with anonymous blueprint retrieval. Emulation and provider mocks do not substitute
for those checks. Local verification created no cloud resources and published only
to disposable local registries.
