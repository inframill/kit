# Connect-RPC contract guidance

- `proto/` is the source of truth for Connect domain services and messages.
- Generated Go transport: `../internal/gen/`; CLI glue: `../internal/cli/gen/`.
  Both are generated-only directories; never clean their handwritten parents.
- Generated TypeScript: `../tests/engine/gen/`.
- All Task commands are defined in the root Taskfile.yml. Run
  `task connect:generate` for Go and `task connect:generate-ts` for TypeScript.
  Each cleans only its generated output trees. Never run dependent generators
  in parallel. Generation never invokes lint tools.
- Verify through `task check-fast` and `task check-full`; these are the only
  check/test task entry points. Full includes both generated drift checks.
- All linting is manual through `task lint`. Agents MUST NOT invoke that task,
  Go lint tools or protobuf lint, directly or through another workflow.
- TypeScript requires `npm --prefix tests/engine ci` from the repository root.
  Keep Go generation and checks independent of Node.
