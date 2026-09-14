#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

failed=0
spec_count=0

report() {
  echo "spec-check: $*" >&2
  failed=1
}

require_pattern() {
  local file="$1"
  local pattern="$2"
  local description="$3"
  if ! grep -Eq "$pattern" "$file"; then
    report "$file: missing $description"
  fi
}

while IFS= read -r spec_file; do
  ((spec_count += 1))
  spec_dir="$(dirname "$spec_file")"
  spec_name="$(basename "$spec_dir")"

  if [[ ! $spec_name =~ ^[0-9]{4}-[a-z0-9]+(-[a-z0-9]+)*$ ]]; then
    report "$spec_dir: directory must match NNNN-short-kebab-name"
  fi

  for required_file in spec.md plan.md tasks.md; do
    if [[ ! -f "$spec_dir/$required_file" ]]; then
      report "$spec_dir: missing $required_file"
    fi
  done

  require_pattern "$spec_file" "^# SPEC-${spec_name:0:4}: " "matching SPEC title"
  require_pattern "$spec_file" '^Status: (Draft|Accepted|Implementing|Implemented|Superseded)$' "valid Status"
  require_pattern "$spec_file" '^## Problem$' "Problem section"
  require_pattern "$spec_file" '^## Outcome$' "Outcome section"
  require_pattern "$spec_file" '^## Requirements$' "Requirements section"
  require_pattern "$spec_file" '\*\*REQ-[0-9]{3}:\*\*' "REQ-NNN requirement"
  require_pattern "$spec_file" '^## Acceptance criteria$' "Acceptance criteria section"
  require_pattern "$spec_file" '\*\*AC-[0-9]{3}\*\*' "AC-NNN acceptance criterion"

  if [[ -f "$spec_dir/plan.md" ]]; then
    require_pattern "$spec_dir/plan.md" '^Status: (Draft|Accepted|Implementing|Implemented|Superseded)$' "valid Status"
    require_pattern "$spec_dir/plan.md" '^## Requirement mapping$' "Requirement mapping section"
    require_pattern "$spec_dir/plan.md" '^## Test strategy$' "Test strategy section"
  fi

  if [[ -f "$spec_dir/tasks.md" ]]; then
    require_pattern "$spec_dir/tasks.md" '^Status: (Draft|Accepted|Implementing|Implemented|Superseded)$' "valid Status"
    require_pattern "$spec_dir/tasks.md" '`T-[0-9]{3}`' "T-NNN task"
    require_pattern "$spec_dir/tasks.md" '^## Verification evidence$' "Verification evidence section"
  fi
done < <(find specs -mindepth 2 -maxdepth 2 -type f -name spec.md ! -path 'specs/templates/*' | sort)

if (( spec_count == 0 )); then
  report "at least one specification is required"
fi
for required in README.md specs/0000-baseline/spec.md docs/decisions/0000-architecture.md; do
  if [[ ! -f "$required" ]]; then
    report "missing $required"
  fi
done
while IFS= read -r file; do
  if [[ -f "$file" && "$file" != "README.md" ]]; then
    report "only the root README is allowed: $file"
  fi
done < <(git ls-files --cached --others --exclude-standard -- '*README*' '*readme*' | sort -u)

# Decisions and references must resolve after a baseline reset or later changes.
while IFS= read -r adr_file; do
  adr_name="$(basename "$adr_file")"
  if [[ ! $adr_name =~ ^[0-9]{4}-[a-z0-9]+(-[a-z0-9]+)*\.md$ ]]; then
    report "$adr_file: filename must match NNNN-short-kebab-name.md"
  fi
  require_pattern "$adr_file" "^# ADR-${adr_name:0:4}: " "matching ADR title"
  require_pattern "$adr_file" '^Status: (Draft|Accepted|Superseded)$' "valid Status"
  require_pattern "$adr_file" '^## Context$' "Context section"
  require_pattern "$adr_file" '^## Decision$' "Decision section"
  require_pattern "$adr_file" '^## Consequences$' "Consequences section"
done < <(find docs/decisions -maxdepth 1 -type f -name '*.md' | sort)

shopt -s nullglob
while IFS= read -r file; do
  [[ -f "$file" ]] || continue
  case "$file" in
    pkg/harness/*|internal/gen/*|internal/cli/gen/*|tests/engine/gen/*) continue ;;
    *.md|*.go|*.ts|*.sh) ;;
    *) continue ;;
  esac
  while IFS= read -r reference; do
    number="${reference#*-}"
    if [[ "$reference" == SPEC-* ]]; then
      targets=(specs/"$number"-*/spec.md)
    else
      targets=(docs/decisions/"$number"-*.md)
    fi
    if (( ${#targets[@]} != 1 )); then
      report "$file: $reference must resolve to exactly one current record"
    fi
  done < <(grep -Eo '(SPEC|ADR)-[0-9]{4}' "$file" | sort -u || true)
done < <(git ls-files --cached --others --exclude-standard | sort -u)

if (( failed != 0 )); then
  exit 1
fi

echo "spec-check: $spec_count feature specs valid"
