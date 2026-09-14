#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
generated_dir="$(mktemp -d)"
trap 'rm -rf "$generated_dir"' EXIT
go tool buf generate --template connect-rpc/buf.gen.ts.yaml --output "$generated_dir"
diff -ru tests/engine/gen "$generated_dir/tests/engine/gen"
