# Local acceptance — 2026-09-16

Local acceptance passed with Kit 0.4.0 and prebuilt Inframill 0.4.0.
The public Go dependency was checked against sibling source revision
`97f414cb2e7eb1738c7b3d9519a9ca99b9f29d6a`. The checks used the supplied sibling
source and prebuilt binaries.

## Recorded artifacts

| Artifact | Digest |
| --- | --- |
| pv-cloud 0.1.0 | `sha256:29b994b89a66d8b663deb4706180659e4b0b1248ef6dc320a48eb95209dc440f` |
| Kit image 0.4.0 index | `sha256:ea0ffa488a5d362fc2c4ee948ba3532541903311e7dfe47055ee73301199ba97` |

Outputs are in `dist/blueprints/pv-cloud/0.1.0/` and `dist/bundles/0.4.0/`.
Their build records contain the component locks, source identity and checksums.
The image was built as `ghcr.io/inframill/kit:0.4.0`. Publication to the disposable
registry used `/inframill/kit` and returned the same index digest. Both image variants
reported Kit 0.4.0; blueprint and driver versions remain 0.1.0.

Both image variants verify that `/usr/local/bin/inframill` is a byte-for-byte copy
of the corresponding locked sibling input:

| Architecture | Inframill SHA-256 |
| --- | --- |
| Linux AMD64 | `77824aadb1a216bd7f1da15df4a4e0c9b3f5c58404dfde33a85beafb8e8d0b66` |
| Linux ARM64 | `f3500f78f4ddec30bf5faf49218a3cdef619abd26c623b4d7fe017204b5ccf0c` |

## Executed checks

`task check-fast` passed: sixteen Python tests, all Go packages/integration tests
with vet disabled, the Kit build and release validation. `task bundle` and
`task check-full` passed for Kit 0.4.0, including blueprint packaging to the disposable
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
| CLI | `server start`, `local exec` and `server command execution wait` use 0.4.0 paths; both images run `local driver list`, `local history` against active daemon storage and `server instance list` with an explicit endpoint, using default and custom configurations |
| Container configuration | Default YAML and unchanged image command; named-volume persistence of Instances and successful command history across container replacement; read-only custom YAML with a changed server port; host-bound data resolved relative to the config while working in `/tmp`; unchanged config bytes; rejection of malformed YAML and read-only data on both architectures |
| Release | Manual-only publication, sealed production source and core requirements, sibling source pin and prebuilt binary verification, isolated tests |

Native pv-cloud checks rendered ARM64, AMD64 and mixed Values, ran Ansible syntax
checks and passed four OpenTofu provider-mock cases. Container verification repeated
real fixture workflows and provider mocks on both architectures. Test images,
registry containers, networks and daemon processes were cleaned up.

Logs: `.cache/0.4-bundle.log` and `.cache/0.4-full.log`. This report was
updated after verification; the image's source snapshot contains the earlier report.
Current contracts are documented in [architecture](architecture.md) and [usage](usage.md).

## VERSION-file follow-up

The subsequent build-tooling change uses the root `VERSION` file for Kit/image
selection and `BLUEPRINT_VERSION` for independent blueprint selection.
`task check-fast` passed with eighteen Python tests and all Go tests. Conflicting
`VERSION`, `KIT_VERSION` and `BUNDLE_VERSION` task arguments still built and validated
Kit 0.4.0. Packaging the local-resource 0.2.0 fixture with `BLUEPRINT_VERSION` returned
its existing digest. Log: `.cache/version-file-fast.log`.

The image and full-check results above predate this tooling follow-up; the image was
not rebuilt or republished. The sibling checkout has since advanced beyond the
source revision pinned for the 0.4.0 image.

## External release checks

These were not performed: native AMD64 hardware execution, real ARM64/AMD64 SSH
guests and service checks, live Hetzner/S3 interoperability, and GHCR publication
with anonymous blueprint retrieval. Emulation and provider mocks do not substitute
for those checks. Local verification created no cloud resources and published only
to disposable local registries.
