# Local acceptance — 2026-09-21

Current Kit and the prebuilt Inframill engine are `0.3.0-dev`. The public Go dependency
is pinned to sibling revision `07440bac8583f0b3d0dcce7be0656a8272595c4d`.
The bundle lock records checksums for all four supplied engine binaries.
Blueprint and driver versions remain `0.2.0`.

## Kit 0.3.0-dev release verification

Both release versions and all engine binary pins are updated. Driver and blueprint
versions remain `0.2.0`.

- `task check-fast` passed, including Python/Go tests, the native Kit build and
  release version checks.
- `task inframill:copy` verified and copied the supplied Darwin ARM64 engine.
- `task bundle` built Linux ARM64 and AMD64 images with the default configuration.
- `task check-full` passed, including native daemon workflows, namespace secrets,
  blueprint registry/cache checks, container persistence and custom configuration.
- Both container architectures passed real local-resource workflows and all four
  pv-cloud provider-mock cases. AMD64 ran under emulation.
- `GetServerVersion` reports the locked engine version; both image variants also
  pass plain-text and JSON `inframill server version` checks.

Local artifacts are in `dist/bundles/0.3.0-dev/`; the image index is
`sha256:295972e759a8c9b39b25b965656534d16e1829dfeae2ce3a57260487fc3be078`.
The release was built locally without publication to GHCR. Logs:
`.cache/0.3-fast.log`, `.cache/0.3-bundle.log` and `.cache/0.3-full.log`.
This report was updated after the image snapshot. The following sections retain
the prior release results.

## Previous Kit 0.2.1-dev release verification

Release descriptors retain the engine, driver, blueprint and tool pins from
0.2.0-dev. The image includes the checked-in default configuration.

- `task check-fast` passed, including all Python/Go tests, the native Kit build
  and release version checks.
- `task bundle` built Linux ARM64 and AMD64 images.
- Both image variants report Kit `0.2.1-dev` and Inframill `0.2.0-dev`.
- Targeted image checks passed for the exact bundled config, both drivers,
  startup, persistent command history, custom configurations and invalid/read-only
  storage rejection. Full provider-mock workflows were not repeated for this patch.

Local artifacts are in `dist/bundles/0.2.1-dev/`; the image index is
`sha256:53fcd124b578bd0011380033061b7afff388260620478ffab440fd55ec8deb59`. The release was built locally without
publication to GHCR. Logs: `.cache/0.2.1-fast.log`, `.cache/0.2.1-bundle.log` and
`.cache/0.2.1-image.log`. This report was updated after the image snapshot.

The following sections retain the previous 0.2.0-dev verification results.

## Previous 0.2.0-dev default configuration checks

`runtime/inframill.yaml` is now copied unchanged to
`/etc/inframill/inframill.yaml`. It includes explicit HTTP defaults, JSON logging,
the persistent data path, and both Kit drivers with their exact tool paths.

`task check-fast` and the image build passed. Targeted image checks passed on Linux
ARM64 and emulated AMD64, proving exact configuration bytes, both drivers available,
server startup, persistent command history, custom configuration and rejection of
invalid configuration/read-only storage. Full provider-mock workflows were not
repeated for this configuration-only change.

The previous image is in `dist/bundles/0.2.0-dev/` with index `sha256:72041350e5d4354c74e6bd37f05d7678905a1b062b4f7ac438900a773083b029`.
Logs: `.cache/config-fast.log`, `.cache/default-config-bundle.log` and
`.cache/default-config-image.log`. The prior image is preserved in
`.cache/pre-default-config-bundle/`. This report was updated after the image build.

## Previous version-alignment checks

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
The prior version-alignment image is in `.cache/pre-default-config-bundle/`;
its `build.json` records the exact
OCI index digest, binary hashes and bundled source snapshot. Its index is
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
