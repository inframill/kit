# SPEC-0002 tasks

Status: Implemented

## Preparation

- [x] `T-001` Inspect baseline/ADR, current CLI/parsers/views, RPC schema, generation
  tasks and architecture/integration tests; write draft spec and implementation plan.
- [x] `T-002` Accept the spec before implementation; record a new ADR for command
  generation, Go presentation policy, shared YAML ownership and workflow boundaries.
  Resolved using the maintainer-provided section extension and the conventions
  in ADR-0001; continuation authorized on 2026-09-12.
- [x] `T-003` Record the existing command matrix, defaults, JSON/table/log output,
  local input/output safeguards and exit classifications in characterization tests.

## Implementation

- [x] `T-010` Introduce Cobra root/runtime composition, injected dependencies and
  safe centralized diagnostics. Keep cmd/main.go and release linker targets intact
  (`REQ-001/002/008`, `AC-001/003`).
- [x] `T-011` Migrate server, package build, Driver passthrough, exec and history;
  extract typed local options and remove local argv parsing after callers migrate.
  Verify explicit-empty flags, local result files, signals and Driver streams
  (`REQ-001/002/008`, `AC-001/003/005`).
- [x] `T-012` Add focused help/examples, version, static completion and lazy remote
  flag/env/config resolution; update architecture allowlists
  (`REQ-002/003/004/013`, `AC-002/003/009`).
- [x] `T-013` Implement terminal color policy and rejection of deferred confirmation/pager
  flags with TTY/non-TTY tests. Record the explicit
  presentation behavior in ADR/README (`REQ-010/011/012`, `AC-008`).
- [x] `T-014` Implement the agreed shared YAML boundary outside pkg/harness; prove
  default-missing versus explicit-missing behavior, CLI-only/combined files,
  precedence and unchanged native config validation (`REQ-013`, `AC-009`).
- [x] `T-020` Implement protobuf-driven generation conventions and one instance list
  slice: flags, request, typed call and fallback rendering. Add Go presentation
  structs, descriptor validation and --columns (`REQ-005/006`, `AC-004/005`).
- [x] `T-021` Prove an added unary fixture generates and compiles without handwritten
  switch edits or config; add failure fixtures for schemas and naming collisions
  (`REQ-005/009`, `AC-004`).
- [x] `T-022` Cover all ten methods and existing remote command variants; implement
  file bindings, catalog selectors, typed wait/deletion hooks and byte-preserving
  log rendering (`REQ-005/006/007/008`, `AC-005/006`).
- [x] `T-023` Integrate complete gen output into existing connect:generate/check;
  prove clean-checkout bootstrap, deterministic output and stale-file detection
  (`REQ-009`, `AC-007`).
- [x] `T-024` Remove old main dispatch/flag helpers and remote response switch.
  Ensure no parallel parser, redundant RPC wrappers or accidental dependency cycles
  remain (`REQ-001/005`, `AC-001/004`).

## Verification

- [x] `T-030` Run relevant unit/executable tests and task check:fast after meaningful
  slices; record results and requirement IDs on practical test cases.
- [x] `T-031` Prove wait/deletion/admission and local execution integration scenarios
  with disposable fixtures; run task test:engine (`AC-001/006`).
- [x] `T-032` Run task check:spec and final task check:handoff once; verify complete
  generation drift and final protected-code/runtime-data diff (`AC-007`).

## Documentation and handoff

- [x] `T-040` Update root README command/help/completion/config/env/columns examples and
  developer generation instructions; keep Connect guidance aligned without adding
  another README.
- [x] `T-041` Record exact verification and any skipped checks; mark Implemented
  only when every acceptance criterion is proven.

## Verification evidence

Implementation and verification performed on 2026-09-12. The maintainer supplied
all existing pkg/harness changes; agent changes are outside that protected tree.
Prompts and pagers were explicitly deferred. The root .inframill.yaml now includes
endpoint http://127.0.0.1:8080, timeout 2m, output table and color auto; the existing
bundled-config test checks native and CLI loading without opening resources.

| Criterion | Evidence | Result |
| --- | --- | --- |
| AC-001 | Existing tests/cli matrix, typed localexec options, architecture allowlists; task check:fast | Passed |
| AC-002 | internal/cli TestREQ003OfflineUtilities and TestREQ003HelpAndCompletionSnapshots; root/leaf text and four completion digest snapshots | Passed |
| AC-003 | TestREQ004PrecedenceAndColumns, TestREQ008Failures; existing executable input/result/signal tests | Passed |
| AC-004 | internal/cligen TestREQ005GeneratedExtensionCompilesAndRuns builds the plugin before gen exists, adds InspectWidget, generates transport and CLI, compiles and invokes a fake typed client; negative schemas and collisions | Passed |
| AC-005 | Existing remote response format semantics, TestREQ006DescriptorValidation, column projection, byte-exact non-UTF-8 logs and output failures | Passed |
| AC-006 | TestREQ007WaitOutcomes; TestConfiguredDataSharedWithDaemon now interrupts a CLI wait and proves one admission survives, completes and is deleted with verified absence | Passed |
| AC-007 | Complete Buf generation to two temporary outputs compared by SHA-256 against gen; injected stale file detected by diff; final handoff below | Passed |
| AC-008 | TestREQ010ColorAndMachineStreams checks terminal/NO_COLOR/TERM/explicit overrides, escape-free machine streams and deferred-flag rejection before requests | Passed |
| AC-009 | cliconfig TestREQ013SectionIsolation, appconfig TestREQ013CombinedApplicationConfiguration, precedence tests and bundled native/CLI config test | Passed |

Commands completed:

- `task check:fast` before implementation: passed, including the existing executable matrix.
- `go test ./internal/cli ./internal/cliconfig ./internal/appconfig ./internal/clioutput/...`: passed.
- `go test ./internal/cligen ./internal/cliruntime ./internal/cli ./internal/clioutput/... ./tests/cli`: passed.
- `task check:fast` after the command/config/generation slices: passed.
- `go test ./tests/cli -run TestConfiguredDataSharedWithDaemon -count=1`: passed,
  including client interruption, one surviving admission and verified deletion.
- `task test:engine`: passed, 2 suites / 18 tests, including TypeScript drift.
- `go test ./internal/cli ./internal/cligen ./internal/cliruntime ./internal/clioutput/remote`:
  passed, including all help/completion snapshots and clean fixture generation.
- `task go:fix`: passed with zero issues after formatting/lint corrections.
- `task check:spec`: passed, 3 feature specs valid.
- Complete `go tool buf generate --template connect-rpc/buf.gen.yaml --output DIR`
  run twice into disposable directories: byte-identical to each other and gen;
  `diff -r` detected a deliberately added stale file. The initial sandbox attempt
  was blocked by the Go build cache; the authorized rerun passed.
- `git diff --check`: passed. Protected tracked-file diff remains the original
  5/1, 64/16 and 3/1 additions/deletions; the maintainer's new harness test is preserved.

Final `task check:handoff` ran once and passed: specs, architecture, formatting,
all owned Go tests (including the bundled native/CLI config), complete generated Go
drift, lint with zero issues, shell syntax, Task parsing and diff whitespace.
All acceptance criteria are proven; no required checks were skipped. Protected
harness tests remain outside the repository-owned gate as specified in AGENTS.md.
No live infrastructure, credentials or user runtime data were used as test fixtures.
