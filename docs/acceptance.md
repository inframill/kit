# Local acceptance — 2026-09-21

Kit and the prebuilt Inframill engine are `0.3.0-dev`, targeting engine revision
`07440bac8583f0b3d0dcce7be0656a8272595c4d`. The current release lock pins all four
engine binaries. Production blueprint and driver versions are `0.2.0`.

## Current checks

`task check-fast` passed after cleanup: 20 Kit Python tests, 11 blueprint tests,
Go tests, the native Kit build and release validation. Blueprint tests exercise detached
builds, both fixture versions, deterministic archives, unsafe sources, tampering,
Docker command sequencing and push failures.

Real Docker pushes of pv-cloud and both local-resource fixture versions passed
after cleanup and preserved their exact OCI digests; repeat pushes also passed. The pv-cloud digest is
`sha256:c4fedb716d3d10bf57e075ea336238ed65d6989aa2370235d33350758dfaf960`.
The publisher invokes Docker only; authentication and registry configuration are
managed by the user. Disposable registries belong to the integration harness.

## Runtime coverage

Before the Docker publication change and test-directory cleanup, `task check-full`
passed with native ARM64 and emulated AMD64 Kit runtimes. These results cover the
unchanged blueprint artifact bytes; the full runtime suite has not been rerun for
those harness changes.

| Area | Coverage |
| --- | --- |
| API and secrets | Instance initialization, namespace isolation, replace/clear, running and queued secret snapshots, restart loss and command history |
| Blueprint cache | Package/fetch/init, offline reuse, corrupt-source rejection, failed catalogue entries and safe deletion |
| Containers | Default and custom configuration, both drivers, persistent history, malformed configuration and read-only storage rejection |
| Workflows | Local OpenTofu resources and loopback Ansible, plan/apply/destroy and all four pv-cloud provider mocks on both architectures |
| Identity | UUIDv7 checks, locked engine binary hashes and Kit/engine version reporting |

To repeat the full checks, install the documented prerequisites, run `task bundle`,
then `task check-full`. Test containers, networks and volumes are disposable.

Native AMD64 hardware, real SSH guests, live Hetzner/S3 interoperability and GHCR
publication are outside these local checks. Tests create no cloud resources.
