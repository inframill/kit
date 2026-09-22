# Local acceptance — Inframill 0.7.0-dev

Kit targets Inframill `0.7.0-dev`, with OpenTofu and Ansible providers `0.3.0`
and pv-cloud `0.2.0`. The current image lock records the exact SDK revision and
checksums of all four prebuilt engine binaries.

## Current contract

The `providers/` subproject builds Runmill executables. Runtime configuration uses
`engine.providers`; Blueprint bindings use `provider_settings`. Discovery uses
`ListProviders`, and command RPCs use execution IDs and JSON document envelopes.
There are no compatibility aliases or legacy release locks.

Plans and node artifacts bind to the host-supplied shared artifact directory.
Plans also verify source/configuration hashes, tool/provider versions and binary
integrity. S3 requires an explicit, stable state key in rendered settings.

## Verification

- `task check-fast` passed: 33 image-tooling tests, 15 blueprint tests, 9 provider
  builder tests, all provider Go suites and release validation.
- Both providers built and verified for Linux AMD64, Linux ARM64 and native
  Darwin ARM64. The checksum-verified native engine reports `0.7.0-dev`.
- `task bundle` built the dual-architecture Kit image.
- `task check-full` passed: native OpenTofu/Ansible workflows, provider discovery,
  execution history/replies, namespace secret snapshots, restart behavior, offline
  cache reuse, tampered-cache rejection and safe catalogue deletion.
- Both Linux architectures passed default/custom configuration, persistent volume
  and bind storage, container replacement, read-only data rejection, local-resource
  workflows and all four pv-cloud provider mocks. AMD64 used emulation.
- New regression cases cover explicit S3 state keys, unsafe execution IDs and
  rejection of saved plans copied to another artifact directory.

Local image: `dist/bundles/0.7.0-dev/`
Index: `sha256:cd917c33ebc76bec3fa4ae9f38932c013acafec8f3db63ca924c7e04c4f00cf3`

SDK revision: `a5d5f6aeba37` (full revision and binary checksums are in the lock).

This report was finalized after the image source snapshot was created. Images and
Blueprints were pushed only to disposable local registries; test containers,
volumes, networks and tags were cleaned up. No GHCR publication or real cloud/SSH
workflows were performed.
