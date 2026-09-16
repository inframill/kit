# Rewrite acceptance — 2026-09-16

The local rewrite acceptance passed with Kit 0.1.0 and prebuilt Inframill 0.2.0.
The public Go dependency was checked against sibling source revision
`389ff39d87c66b4f7668ec231f0a20e4a5f5f494`. No sibling source or binaries were
changed by this rewrite.

## Verified candidate

| Artifact | Digest |
| --- | --- |
| pv-cloud 0.1.0 | `sha256:29b994b89a66d8b663deb4706180659e4b0b1248ef6dc320a48eb95209dc440f` |
| Runtime 0.1.0 index | `sha256:ddbdcc7241ba559a20fc804e9d9e235a21ec24afdd809adfb48f51facd6864e7` |

Outputs are in `dist/blueprints/pv-cloud/0.1.0/` and `dist/bundles/0.1.0/`.
Their build records contain the component locks, source identity and checksums.
The bundle source record was compared with the final implementation files.

Both image variants verify that `/usr/local/bin/inframill` is a byte-for-byte copy
of the corresponding locked sibling input:

| Architecture | Inframill SHA-256 |
| --- | --- |
| Linux AMD64 | `e67b82df98f2dc8cec97dfa47ce8af0f5f297edb2e5d57437865ebee4ee2b668` |
| Linux ARM64 | `5f2a91a0b30bd1d7c32aa13c89b4fb7c67bd2c5019ab5ccf04500c641585cfea` |

## Executed checks

`task check-fast` passed: twelve Python tests, all Go packages/integration tests
with vet disabled, and the Kit build. `task blueprints:package`, `task bundle` and
`task check-full` passed for the final candidate. Python syntax and
`git diff --check` also passed.

| Acceptance area | Evidence |
| --- | --- |
| AC-01: Protocol | Real upstream Manager/Serve discovery and JSON dispatch; distinct version schemas; structured messages/results; rejection of protocol argv, missing/contradictory final replies and nonzero exit after success; process-group cancellation |
| AC-02: Preparation | Manifest validation without files, command-specific file requirements, no preparation writes/tools/secrets, syntax checks without node inventory |
| AC-03: Secrets | Required-secret errors before effects, restricted child environment, chunked/multiline/escaped output filtering, credential-bearing result rejection; daemon/local snapshot tests and checks for synthetic secrets in saved daemon files |
| AC-04: Outcomes | Saved-plan source/reference/mode/integrity checks, failed-apply inventory invalidation and plan retirement; Ansible complete-host recap validation; real local tool lifecycles |
| AC-05: Versions | Two production driver names, distinct test driver versions/contracts, multiple local-resource blueprint versions, generic packaging of pv-cloud and fixtures; exact selection and mismatches |
| AC-06: Engine | Real daemon runs Kit init/plan/apply/Ansible/destroy; history and JSON reply logs; available/unavailable health; project isolation, replace/clear, running/queued snapshots, restart loss; timed-out remote wait leaves execution running; reused local Instance receives no previous invocation's secrets |
| AC-07: Packages | Deterministic packaging and graph integrity tests; disposable registry tag/digest pulls, idempotent pushes and conflicting-tag rejection; new Instance initialization from cache without authorized network acquisition; tampered cached source rejected without creating an Instance |
| AC-08: Architectures | Local-resource workflows and four pv-cloud provider mocks pass inside both Linux images; native ARM64 and emulated AMD64; core byte hashes match supplied inputs |
| AC-09: Release | Manual-only publication, sealed production source and core requirements, sibling source pin and prebuilt binary verification, isolated tests, rebuilt initial artifacts; superseded drivers and legacy blueprint tree removed |

Native pv-cloud checks rendered ARM64, AMD64 and mixed Values, ran Ansible syntax
checks and passed four OpenTofu provider-mock cases. Container verification repeated
real fixture workflows and provider mocks on both architectures. Test images,
registry containers, networks and daemon processes were cleaned up.

Logs: `.cache/rewrite-fast.log`, `.cache/rewrite-native.log`,
`.cache/rewrite-daemon.log`, `.cache/rewrite-bundle.log` and `.cache/rewrite-full.log`.
The [rewrite plan](inframill-0.2.0-rewrite-plan.md) records the intended contracts.

## External release checks

These were not performed: native AMD64 hardware execution, real ARM64/AMD64 SSH
guests and service checks, live Hetzner/S3 interoperability, and GHCR publication
with anonymous blueprint retrieval. Emulation and provider mocks do not substitute
for those checks. Local verification created no cloud resources and published only
to disposable local registries.
