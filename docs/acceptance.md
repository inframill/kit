# Local acceptance — 2026-09-21

Kit and the prebuilt Inframill engine are `0.3.0-dev`, targeting engine revision
`07440bac8583f0b3d0dcce7be0656a8272595c4d`. Driver and production blueprint versions
remain `0.2.0`.

## Driver subproject and image assembly

Driver implementations now live under `drivers/roots/<name>/<version>/`, with
shared production code under `drivers/roots/internal/`. The standalone Python
builder, Go module, release selection, tests and README are contained in `drivers/`.
The root image assembler consumes verified `drivers/dist` outputs and never
compiles drivers or reads their implementation sources.

Checks passed after the final source move:

- `task check-fast`: 23 image-tooling Python tests, 9 driver-builder Python tests,
  11 blueprint tests, both Go test suites and image release validation.
- Both drivers built and verified for Linux AMD64 and ARM64. Native Darwin ARM64
  outputs also built and passed artifact verification.
- `task bundle` assembled both image architectures from those prebuilt outputs.
- Both image architectures passed exact default configuration, driver discovery,
  engine version, persistent history, custom configuration, malformed configuration
  and read-only storage checks.
- Both architectures passed local-resource OpenTofu/Ansible workflows and all four
  pv-cloud provider mocks. AMD64 ran under emulation.
- The image and all blueprint fixture versions published successfully to disposable
  local registries. Repeated blueprint pushes preserved their artifact digests.

The verified image is in `dist/bundles/0.3.0-dev/`, with index
`sha256:9d0f977ad6138a29dc8025b0cdc03035b524e4ed6311e5988f6117a56c483ffc`.
Each driver ships its executable, build record, source snapshot and available
Go dependency notices under `/opt/inframill/drivers/<name>/<version>/`.
This report was updated after the image's source snapshot was created.

## Scope

The full native daemon/cache/namespace-secret acceptance suite was not rerun for
this extraction. Driver protocol tests, version coexistence, cancellation and
shared artifact tests passed; the container workflows above used real local tools.

Native AMD64 hardware, real SSH guests, live Hetzner/S3 interoperability and GHCR
publication remain outside these checks. Provider mocks create no cloud resources.
Temporary registry containers, networks, volumes and image tags were cleaned up.
