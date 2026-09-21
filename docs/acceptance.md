# Local acceptance — 2026-09-21

Kit and the prebuilt Inframill engine are `0.2.0-dev`. The public Go dependency
is pinned to sibling revision `e012760daf7fa759a0b1c106ab6d3d3ec0e46770`.
The bundle lock records checksums for all four supplied engine binaries.
Blueprint and driver versions remain `0.2.0`.

## Executed checks

- `task check-fast` passed: 18 Python tests, Go package and integration tests with
  vet disabled, Kit build and release version validation.
- `task inframill:copy` verified and copied the supplied Darwin ARM64 binary.
- `task blueprint:check` passed for local-resource 0.1.0 and pv-cloud 0.2.0 via
  disposable registries and daemons. pv-cloud rendered ARM64, AMD64 and mixed
  inputs and passed all four OpenTofu provider-mock cases.
- `task blueprint:verify` passed against a digest-pinned local-resource fixture
  in the disposable registry, including release requirement validation.
- `task bundle` built both Linux architectures from the supplied engine binaries.
- `task check-full` passed using native ARM64 and emulated AMD64 containers on
  the initial image (`sha256:a0dee86c68c7956274762b0dc6ac4b4ecd365fd0bfd8fd755239b4a086a83585`).
- The final image refresh includes the completed verification script and source
  documentation. Its runtime binary hashes are identical to the initial image.
  Configuration, persistence, CLI and failure checks passed again on both
  architectures using `.cache/verify-final-image.py`.
- Two full-suite attempts on the final image were blocked by GitHub HTTP 504
  responses while downloading the pinned hcloud 1.68.0 checksum file. The first
  reached ARM64 runtime provider initialization; the retry stopped at native
  provider initialization. No code or dependency pin was changed for that error.
  Final-image provider-mock revalidation remains incomplete; those mocks passed
  on both architectures in the initial full run.

## Coverage

| Area | Evidence |
| --- | --- |
| Current API | Flat InitInstance requests, namespace filtering and secret RPCs, daemon command execution and history, fixed handler configuration without settings |
| Secrets | Namespace isolation; replace/clear; separate running and queued snapshots; restart loss; timed-out CLI wait leaves admitted work running; synthetic credentials absent from saved files |
| Blueprints | Two fixture versions and pv-cloud package/fetch/init; idempotent pushes and conflicting-tag rejection; offline cache reuse after restart; corrupt sources become failed catalogue entries; rejected initialization creates no Instance; deleting a failed revision preserves existing Instances |
| Containers | Default YAML and unchanged image command; named-volume history survives replacement; read-only custom YAML and config-relative bound data; malformed YAML and read-only data reject on both architectures |
| Workflows | Real built-in OpenTofu resources and loopback Ansible through daemons; plan/apply/destroy outcomes; both container architectures repeat fixture workflows and all four pv-cloud provider mocks |
| Identity | Instance and command UUIDv7 checks; runtime engine hashes match the supplied locked binaries; both images report Kit 0.2.0-dev |

The pv-cloud OCI digest remains
`sha256:a3b4eb5ea3da749d795a9d921dce37be6c62c610982790fd98ce462859988f13`.
The local image is in `dist/bundles/0.2.0-dev/`; its `build.json` records the exact
OCI index digest, binary hashes and bundled source snapshot. The final index is
`sha256:57a80c088a63cc9013e0fdfd29c5ed7fe3f6ea56620dc6db41bd74682222dc5e`.
This report was updated after the final image snapshot. Publication during
acceptance uses only disposable registries. Containers, networks and test volumes
are cleaned up after verification.

Logs: `.cache/0.2-native.log`, `.cache/0.2-pv.log`, `.cache/0.2-verify.log`,
`.cache/0.2-bundle.log`, `.cache/0.2-final-image.log`,
`.cache/0.2-full-network-error.log` and `.cache/0.2-full.log` (latest retry).
The initial verified image is preserved in `.cache/0.2-first-bundle/`.

## External release checks

Native AMD64 hardware, real SSH guests, live Hetzner/S3 interoperability and GHCR
publication are outside these local checks. Test workflows use built-in local
resources, provider mocks and disposable registries; they create no cloud resources.
