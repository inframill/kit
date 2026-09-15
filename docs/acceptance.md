# Release acceptance

## Local checks

Run `task check-fast`, then build a bundle and run `task check-full` with the same
`KIT_DIST_DIR`. Full verification publishes only to a disposable local registry.
It never creates real Hetzner resources or pushes to GHCR.

Required coverage:

- Production driver catalog and synthetic multi-version protocol integration.
- Saved-plan and node-artifact isolation, cancellation and failure handling.
- Deterministic packages, tamper rejection and conflicting-tag rejection.
- Multiple fixture blueprint versions fetched by tag and digest from fresh caches.
- pv-cloud ARM64, AMD64 and mixed rendering, syntax checks and provider mocks.
- Real local-resource OpenTofu/Ansible workflows in both runtime architectures.
- Prebuilt Inframill checksum, architecture and executable-version verification.

## External release checks still required

- Native Linux AMD64 execution and independent native architecture package builds;
  emulated AMD64 is useful evidence but does not satisfy native acceptance.
- Disposable ARM64/AMD64 SSH guests, including cross-architecture execution and
  Docker/Nginx/PostgreSQL service checks.
- GHCR publication, package visibility/linking and fresh-cache anonymous blueprint
  pulls plus runtime digest pulls on both native architectures.
- A separately authorized Hetzner/S3 deployment for provider/backend locking,
  SSH and service interoperability. No live cloud effects are part of local checks.

The pinned Inframill consumer supports anonymous/public blueprint pulls. Docker
login authenticates publication, not private blueprint consumption.

## Cleanup verification — 2026-09-15

- `task check-full KIT_DIST_DIR=.cache/verification` passed, using the native
  release binary selected with `INFRAMILL_NATIVE_BINARY`. This includes all ten
  Python tests, Go tests and the Kit build.
- Kit uses the updated public `driver.Driver`/`driver.Serve` API. Tests cover all
  four validation phases without tool execution or secret resolution, exact
  version selection, saved-plan isolation and child-process cancellation.
- Both moved local-resource fixture versions completed real OpenTofu/Ansible
  lifecycles through the new Inframill release, by tag and from a fresh digest cache.
- pv-cloud passed ARM64, AMD64 and mixed rendering/syntax checks and four
  OpenTofu provider-mock tests. Both runtime architectures completed fixture
  workflows and provider mocks: ARM64 natively in Docker, AMD64 under emulation.
- Disposable registry publication, idempotent pushes and conflicting-tag
  rejection passed. The test image, registry container and network were removed.
- `git diff --check` and Python syntax checks passed. Existing `dist/` artifacts
  and `old-blueprints/` were preserved; verification output is separate.

The sibling release task was run with user authorization; no sibling source was
changed. Inframill `0.0.0-dev` was built from revision
`b7008f358d146d5b4f74f0f84865b80e18fdf8e5`. The bundle copies the locked Linux
binaries from `../inframill/dist/0.0.0-dev/`; it never invokes that release task.
Inspection of both OCI image layers confirmed the installed Inframill binaries
match their sibling inputs byte for byte:

| Architecture | SHA-256 |
| --- | --- |
| AMD64 | `35f8c4552add2973f2d2ea0b2b6270056667297a873d9ccf88694140080168f7` |
| ARM64 | `f7ba38b33c59415ca8562909ada0b75affcd4d0c9641e0bf7981958ba1f7e428` |

Verified bundle: `.cache/verification/bundles/0.1.0`, index digest
`sha256:3bc8e8ca4cb92d6af2cf10320ed2de57a578be6acdd333eb7fcedbca27450ab2`.
No GHCR publication or live cloud/SSH guest checks were performed.

Logs: `.cache/cleanup-full.log`, `.cache/cleanup-native.log` and
`.cache/cleanup-bundle.log`.
