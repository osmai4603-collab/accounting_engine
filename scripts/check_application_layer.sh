#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

imports="$(go list -f '{{.ImportPath}} {{range .Imports}}{{.}} {{end}}' ./cmd/... ./pkg/sdk)"
violations=0
while read -r package package_imports; do
  for imported in $package_imports; do
    if [[ "$imported" == "github.com/osmai4603-collab/accounting_engine/internal/posting" ]]; then
      echo "VIOLATION: $package imports posting directly; route through the application layer" >&2
      violations=$((violations + 1))
    fi
  done
done <<<"$imports"

if ((violations > 0)); then
  exit 1
fi

echo "application-layer boundary check: passed"
