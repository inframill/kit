#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
generated_dir="$(mktemp -d)"
trap 'rm -rf "$generated_dir"' EXIT
go tool buf generate --template connect-rpc/buf.gen.yaml --output "$generated_dir"
diff -ru internal/gen "$generated_dir/internal/gen"
diff -ru internal/cli/gen "$generated_dir/internal/cli/gen"
