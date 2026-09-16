# Local acceptance — 2026-09-16

Local acceptance passed with Kit 0.5.0-dev and prebuilt Inframill 0.5.0-dev.
The public Go dependency was checked against sibling source revision
`0d716b94b97f67bb5e68d5b056cda15828282782`. The checks used the supplied sibling
source and prebuilt binaries.

## Recorded artifacts

| Artifact | Digest |
| --- | --- |
| pv-cloud 0.1.0 | `sha256:29b994b89a66d8b663deb4706180659e4b0b1248ef6dc320a48eb95209dc440f` |
| Kit image 0.5.0-dev index | `sha256:96dbac923c58c7144b627048b7d2dec34d144009c172744a83ff0a3a1d16b797` |

Outputs are in `dist/blueprints/pv-cloud/0.1.0/` and `dist/bundles/0.5.0-dev/`.
Their build records contain the component locks, source identity and checksums.
The image was built as `ghcr.io/inframill/kit:0.5.0-dev`. Publication to the disposable
registry used `/inframill/kit` and returned the same index digest. Both image variants
reported Kit 0.5.0-dev; blueprint and driver versions remain 0.1.0.

Both image variants verify that `/usr/local/bin/inframill` is a byte-for-byte copy
of the corresponding locked sibling input:

| Architecture | Inframill SHA-256 |
| --- | --- |
| Linux AMD64 | `2a3e80926b7409075570a62fa84d718a972c1e9e5bb3e4283d97a08ccf87e930` |
| Linux ARM64 | `36423b53c63fd27d2707d6c4724eec397657e8a4185d1ed92377e1b858ebbf31` |

## Executed checks

`task check-fast` passed: eighteen Python tests, all Go packages/integration tests
with vet disabled, the Kit build and release validation. `task bundle` and
`task check-full` passed for Kit 0.5.0-dev, including blueprint packaging to the disposable
registry. Python syntax, documentation links and `git diff --check` also passed.

Version-policy tests reject mismatched major/minor versions, descriptor identities,
image/Kit versions and core targets. Independent Kit patch releases and prereleases
within the supported major/minor line pass. Image builds, pushes and native copies
use the same release validation.

| Acceptance area | Evidence |
| --- | --- |
| Protocol | Real upstream Manager/Serve discovery and JSON dispatch; distinct version schemas; structured messages/results; rejection of protocol argv, missing/contradictory final replies and nonzero exit after success; process-group cancellation |
| Preparation | Manifest validation without files, command-specific file requirements, no preparation writes/tools/secrets, syntax checks without node inventory |
| Secrets | Required-secret errors before effects, restricted child environment, chunked/multiline/escaped output filtering, credential-bearing result rejection; daemon/local snapshot tests and checks for synthetic secrets in saved daemon files |
| Outcomes | Saved-plan source/reference/mode/integrity checks, failed-apply inventory invalidation and plan retirement; Ansible complete-host recap validation; real local tool lifecycles |
| Versions | Two production driver names, distinct test driver versions/contracts, multiple local-resource blueprint versions, generic packaging of pv-cloud and fixtures; exact selection and mismatches |
| Engine | Real daemon runs Kit init/plan/apply/Ansible/destroy; history and JSON reply logs; available/unavailable health; project isolation, replace/clear, running/queued snapshots, restart loss; timed-out remote wait leaves execution running; reused local Instance receives no previous invocation's secrets |
| Packages | Deterministic packaging and graph integrity tests; disposable registry tag/digest pulls, idempotent pushes and conflicting-tag rejection; new Instance initialization from cache without authorized network acquisition; tampered cached source rejected without creating an Instance |
| Architectures | Local-resource workflows and four pv-cloud provider mocks pass inside both Linux images; native ARM64 and emulated AMD64; core byte hashes match supplied inputs |
| CLI | `server start`, `local exec` and `server command execution wait` use 0.5.0-dev paths; both images run `local driver list`, `local history` against active daemon storage and `server instance list` and `server driver list` with an explicit endpoint, using default and custom configurations |
| Container configuration | Default YAML and unchanged image command; named-volume persistence of Instances and successful command history across container replacement; read-only custom YAML with a changed server port; host-bound data resolved relative to the config while working in `/tmp`; unchanged config bytes; rejection of malformed YAML and read-only data on both architectures |
| Release | Root `VERSION` selects `0.5.0-dev` for builds, image tags, pushes and checks; eighteen Python tests include strict file validation and ignored overrides; `BLUEPRINT_VERSION` independently selects multiple fixture packages; manual-only publication, sealed production source and core requirements, sibling source pin and prebuilt binary verification, isolated tests |

Native pv-cloud checks rendered ARM64, AMD64 and mixed Values, ran Ansible syntax
checks and passed four OpenTofu provider-mock cases. Container verification repeated
real fixture workflows and provider mocks on both architectures. Test images,
registry containers, networks and daemon processes were cleaned up.

Logs: `.cache/0.5-dev-bundle.log` and `.cache/0.5-dev-full.log`. This report was
updated after verification; the image's source snapshot contains the earlier report.
Current contracts are documented in [architecture](architecture.md) and [usage](usage.md).

## External release checks

These were not performed: native AMD64 hardware execution, real ARM64/AMD64 SSH
guests and service checks, live Hetzner/S3 interoperability, and GHCR publication
with anonymous blueprint retrieval. Emulation and provider mocks do not substitute
for those checks. Local verification created no cloud resources and published only
to disposable local registries.
