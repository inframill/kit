# Local acceptance — 2026-09-21

Kit and the prebuilt Inframill engine are `0.3.0-dev`, targeting engine revision
`07440bac8583f0b3d0dcce7be0656a8272595c4d`. Driver and production blueprint versions
remain `0.2.0`.

## Python Kit builder and isolated subprojects

Driver implementations now live under `drivers/roots/<name>/<version>/`, with
shared production code under `drivers/roots/internal/`. The standalone Python
builder, Go module, release selection, tests and README are contained in `drivers/`.
The root image assembler consumes verified `drivers/dist` outputs and never
compiles drivers or reads their implementation sources. Root image tooling is now
one standard-library Python script, `build.py`; the root Go module and the old
`cmd/`, `internal/` and `scripts/` directories have been removed. Acceptance
helpers and builder tests live in `tests/`.

Checks passed after consolidating the root builder:

- `task check-fast`: 33 image-tooling Python tests, 9 driver-builder Python tests,
  11 blueprint tests, the driver Go test suite and image release validation.
- Existing driver artifacts for Linux AMD64/ARM64 were verified and consumed
  without rebuilding them. The Python native engine copy also passed on Darwin ARM64.
- `task build` assembled both image architectures using only Python and Docker
  at the root; the root Go module was absent.
- Both image architectures passed exact default configuration, driver discovery,
  engine version, persistent history, custom configuration, malformed configuration
  and read-only storage checks.
- Both architectures passed local-resource OpenTofu/Ansible workflows and all four
  pv-cloud provider mocks. AMD64 ran under emulation.
- The image and all blueprint fixture versions published successfully to disposable
  local registries. Repeated blueprint pushes preserved their artifact digests.
- The final image passed local OCI integrity verification, repeat publication
  without repushing, and digest-pinned remote driver discovery on both platforms.

The verified image is in `dist/bundles/0.3.0-dev/`, with index
`sha256:bb5912bc41d5fdee6ef106e4c5646b4d4b1a031cddb0ead216fb6b25d9f1395e`.
Each driver ships its executable, build record, source snapshot and available
Go dependency notices under `/opt/inframill/drivers/<name>/<version>/`.
This report was updated after the image's source snapshot was created.

## Scope

The full native daemon/cache/namespace-secret acceptance suite was not rerun for
this cleanup. Driver protocol tests, version coexistence, cancellation and
shared artifact tests passed; the container workflows above used real local tools.

Native AMD64 hardware, real SSH guests, live Hetzner/S3 interoperability and GHCR
publication remain outside these checks. Provider mocks create no cloud resources.
Temporary registry containers, networks, volumes and image tags were cleaned up.
