# Working in Kit

Toolai owns Go tooling and aggregate workflows through `toolai.json`. Task owns
focused external jobs and local runtime operations. Read README.md and the affected
docs before changing provider behavior. No subagents unless explicitly requested.

- Provider implementations and embedded protocol/release metadata live in
  `internal/providers/<name>`. The catalog is the active provider selection.
- Runmill source lives in `pkg/runmill`; engine version/revision, Runmill source hash,
  binary checksums and runtime tool pins live in `runtime/dependencies.json`. Kit has no Inframill Go module
  dependency. Do not reintroduce parallel version or release locks.
- Import only Runmill and engine binaries from the sibling. Full engine sources
  must not be staged in Kit or included in images or release archives.
- Build, checks, setup and publishing are explicit independent operations.
  `toolai build release` prepares bundles; `toolai check full` consumes them.
- Preserve source/checksum/architecture/provider/runtime and publication verification.
  Update tests around observable contracts when replacing implementation details.
- Default checks need no Docker or installed infrastructure tools. Protocol tests
  compile temporary fixtures. Acceptance uses disposable local resources, never
  cloud credentials or production infrastructure. Do not run operational docker:up
  or publishing commands as part of validation.
- Go lint is opt-in (`toolai check lint`). Do not apply lint fixes unless requested.
- Preserve existing work; leave changes unstaged and uncommitted unless requested.

<!-- toolai:begin -->
## Toolai development workflow

Use Toolai for this project's Go development workflow. Use direct Go commands
for focused tests and diagnostics that Toolai does not expose.

- Follow the repository-local `toolai` skill for development workflow, operational safeguards, generation, and failure recovery. Scale planning and testing to the change; small, clear tasks can proceed directly.
- For repository Python automation, follow the skill's scripts guidance and the local `scripts/README.md` when present. Reuse namespaced jobs, keep tests beside their modules, and distinguish validation from operational effects. Select relevant Python checks for script-only changes; Task is optional and scripts remain project-owned.
- Inspect existing changes and preserve unrelated work. Continue within the user's authorized scope, asking only when material ambiguity or an action outside that scope requires a decision. Keep credentials and sensitive data out of output and artifacts.
- Root check steps are defined in `toolai.json` under `checks.steps`; untagged steps always run, and positional tags add steps matching any requested tag. Inspect the steps and select appropriate tags; use `--directory` for another project with root check or build. Clean, generate, check, and build are independent operations. Go commands do not run sqlc/RPC tools or `gopackage` checks.
- During implementation, use `toolai go compile` when compilation feedback is useful.
- After completing Go code changes and before handing them back, run `toolai check` when checks are configured; otherwise run the relevant Toolai commands directly.
- Run the project's configured pre-PR validation before preparing a PR. Reuse successful validation while the checked files remain unchanged.
- Use `toolai doctor` to diagnose missing tools and prerequisites.
- Use `toolai go mod tidy` to apply needed module changes, then review the module diff.
- Use `toolai go lint --fix` for supported fixes, review the diff, and rerun affected checks.
- Treat `.golangci.yml` as generated: each `toolai go lint` run overwrites it from a built-in preset.
- When `gopackage` is configured, preserve its package import boundaries. After changing imports or package layout, run `toolai gopackage check` explicitly or through a configured root check step. Rules contain only `package` and `mode` (`stdlib`, `pure`, `tree`, or `unrestricted`); all matching rules apply. The check covers direct production imports, excludes tests, and respects the active build configuration and `--build-tags`.
- Root `toolai build` and `toolai generate` require explicit nonempty `build.steps` and `generate.steps`. Include every desired operation; there are no fallback steps. Build, generate, and check steps share `toolai`/`exec` arrays, optional `directory`, and `env` overrides. Check and build steps support `tags`; empty or omitted tags always run, otherwise any requested tag selects the step once. Generate runs all steps and does not support tags.
- For configured RPC or sqlc generation, edit source inputs and `toolai.json`, then use the corresponding Toolai generator. `connectRpc.directory` selects the Buf workspace (default `connect-rpc`), with sources in its `proto/` subdirectory and Buf configs and the lock alongside it. Output directories are replaced; keep handwritten code elsewhere. Run sqlc/RPC checks explicitly or include them in configured root check steps. Only `toolai connect-rpc deps update` updates the workspace's `buf.lock`; migration execution remains project-owned.
- Review the final diff and report commands actually run, results, and any skipped or blocked checks. A compilation-only check does not establish that tests or lint passed. Documentation-only changes do not require the full Go suite.
<!-- toolai:end -->
