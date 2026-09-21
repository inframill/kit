# Local acceptance — 2026-09-22

Kit and Inframill are `0.5.0-dev`, pinned to engine revision
`1a2aca9dc1922097e0c0f2ed209077c3fc9ae292`. pv-cloud remains `0.1.0`;
OpenTofu and Ansible drivers remain `0.2.0` and were rebuilt against that SDK.
The engine's checked-in VERSION selected this release, independently of other
version directories present in the sibling dist directory.

## Driver bindings and directories

Blueprints use configured IDs `opentofu` and `ansible`. Each `driver_settings`
entry contains `version: "0.2"` and a nested `settings` object. Inframill enforces
that compatibility requirement; redundant `required_driver_version` fields and
driver checks have been removed. Release manifests still select exact binaries.

`templates/` contains exactly the matching Driver ID directories. The engine
passes `workspace/<configured-id>/` directly to each driver, which no longer
appends its own name. Configured aliases work without double nesting. Instance
artifacts remain shared outside these directories.

Regression tests cover nested bindings, major/minor syntax and agreement,
missing/extra/file template entries, configured aliases, preservation of the
engine-supplied workspace/artifact paths, and rejection of obsolete template roots.
Source seals were regenerated for pv-cloud and both local-resource fixtures.

## Verification

- `task check-fast` passed: 33 root Python tests, 15 blueprint tests, 9 driver
  builder tests, the driver Go suites and release validation.
- Both drivers built and verified for Linux AMD64, Linux ARM64 and native Darwin
  ARM64. The native engine was checksum-verified and reports `0.5.0-dev`.
- pv-cloud `0.1.0` built and loaded locally with the new bindings.
- `task build` assembled the dual-architecture image from the new prebuilt inputs.
- `task check-full` passed, including native OpenTofu/Ansible workflows, all four
  pv-cloud provider mocks, driver discovery, command history, namespace secret
  snapshots, restart behavior, offline cache reuse and tampered-cache rejection.
- Both image architectures passed default/custom configuration, persistent data,
  read-only data rejection, local-resource workflows and all pv-cloud provider
  mocks. AMD64 ran under emulation.
- Image and blueprint artifacts pushed to disposable local registries. Test
  containers, volumes, networks and registry tags were cleaned up.

Local image: `dist/bundles/0.5.0-dev/`
Index: `sha256:bdf65e9621e11a8f5ab8c71a5f7c1341a504e75b4964fd70315cce5725c3169a`

Local blueprint: `blueprints/dist/pv-cloud/0.1.0/`
Manifest: `sha256:e9fa99798fae5e964ad1a4cf4fb90ed7c91a7cbbace43ca801b6245267048d2b`

This report was updated after the image's source snapshot was created. No
artifacts were published to GHCR. Real cloud resources, live SSH guests and native
AMD64 hardware were not exercised.

## Existing Instances

Inframill 0.5 rejects saved Instances with old direct settings entries or obsolete
layouts. Their data is preserved; create new Instances from the updated Blueprint.
