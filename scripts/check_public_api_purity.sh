#!/usr/bin/env bash
set -euo pipefail

is_internal_import() {
  [[ "$1" =~ (^|/)internal(/|$) ]]
}

if [[ "${1:-}" == "--self-test" ]]; then
  for path in accounting_engine/internal/posting accounting_engine/pkg/foo/internal \
    internal accounting_engine/internal; do
    if ! is_internal_import "$path"; then
      echo "Expected internal import to be detected: $path" >&2
      exit 1
    fi
  done
  for path in accounting_engine/pkg/internalized accounting_engine/pkg/external \
    github.com/example/internal-tools accounting_engine/pkg/internal_api; do
    if is_internal_import "$path"; then
      echo "Unexpected internal import match: $path" >&2
      exit 1
    fi
  done
  echo "purity matcher tests: passed"
  exit 0
fi

echo "Checking public API purity under pkg/ ..."

imports="$(go list -f '{{range .Imports}}{{$.ImportPath}} {{.}}{{"\n"}}{{end}}' ./pkg/...)"
violations=0
while read -r package imported; do
  [[ -n "${imported:-}" ]] || continue
  if is_internal_import "$imported"; then
    echo "VIOLATION: Public package $package imports internal package $imported"
    violations=$((violations + 1))
  fi
done <<< "$imports"

if (( violations > 0 )); then
  echo "ERROR: Found $violations purity violation(s) in pkg/!"
  exit 1
fi

echo "SUCCESS: pkg/ api purity check passed (zero internal imports)."
