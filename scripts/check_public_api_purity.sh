#!/usr/bin/env bash
# Verifies the public API purity rules in ADR-0007:
#   1. contract packages (pkg/api/**, pkg/spi/**) never import internal/*;
#   2. no package under pkg/** or the root facade exposes an internal/ type in
#      its exported API.
# Composition packages (root facade, pkg/sdk, storage adapters) may import
# internal/*, but must not leak internal types through exported symbols.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
internalapi_tool="$repo_root/scripts/internalapi/main.go"

is_internal_import() {
  [[ "$1" =~ (^|/)internal(/|$) ]]
}

run_matcher_self_test() {
  local path
  for path in accounting_engine/internal/posting accounting_engine/pkg/foo/internal \
    internal github.com/osmai4603-collab/accounting_engine/internal; do
    if ! is_internal_import "$path"; then
      echo "Expected internal import to be detected: $path" >&2
      exit 1
    fi
  done
  for path in accounting_engine/pkg/internalized accounting_engine/pkg/external \
    github.com/example/internal-tools accounting_engine/pkg/internal_api \
    github.com/osmai4603-collab/accounting_engine/pkg/sdk; do
    if is_internal_import "$path"; then
      echo "Unexpected internal import match: $path" >&2
      exit 1
    fi
  done
  echo "purity matcher tests: passed"
}

run_fixture_test() {
  local dir
  dir="$(mktemp -d)"
  trap 'rm -rf "$dir"' RETURN
  mkdir -p "$dir/internal/secret" "$dir/pkg/api/v1/public" "$dir/pkg/spi/public"
  printf 'module example.com/fixture\n\ngo 1.26.0\n' >"$dir/go.mod"
  printf 'package secret\n\ntype Value struct{}\n' >"$dir/internal/secret/secret.go"
  printf 'package public\n\nimport "example.com/fixture/internal/secret"\n\nfunc Get() int { return 0 }\n\nvar _ = secret.Value{}\n' >"$dir/pkg/api/v1/public/public.go"
  printf 'package public\n\nimport "example.com/fixture/internal/secret"\n\nfunc Export() secret.Value { return secret.Value{} }\n' >"$dir/pkg/spi/public/public.go"

  local imports violations=0
  imports="$(cd "$dir" && go list -f '{{range .Imports}}{{$.ImportPath}} {{.}}{{"\n"}}{{end}}' ./pkg/api/... ./pkg/spi/...)"
  while read -r package imported; do
    [[ -n "${imported:-}" ]] || continue
    if is_internal_import "$imported"; then
      violations=$((violations + 1))
    fi
  done <<<"$imports"
  if ((violations == 0)); then
    echo "fixture test FAILED: contract package importing internal was not detected" >&2
    exit 1
  fi

  local exposure
  exposure="$(cd "$dir" && go run "$internalapi_tool" example.com/fixture/pkg/spi/public 2>/dev/null | grep -c 'internal/' || true)"
  if ((exposure == 0)); then
    echo "fixture test FAILED: exported internal type was not detected" >&2
    exit 1
  fi
  echo "purity fixture test: internal import and exported internal type detected as expected"
}

check_no_internal_imports() {
  local pattern="$1" label="$2"
  echo "Checking internal imports for ${label} ..."
  local imports
  imports="$(go list -f '{{range .Imports}}{{$.ImportPath}} {{.}}{{"\n"}}{{end}}' $pattern)"
  local violations=0
  while read -r package imported; do
    [[ -n "${imported:-}" ]] || continue
    if is_internal_import "$imported"; then
      echo "VIOLATION: contract package $package imports internal package $imported"
      violations=$((violations + 1))
    fi
  done <<<"$imports"
  if ((violations > 0)); then
    echo "ERROR: Found $violations import purity violation(s) in ${label}!"
    return 1
  fi
}

check_exported_symbols() {
  local label="$1"
  shift
  echo "Checking exported symbols for internal type references (${label}) ..."
  if ! go run "$internalapi_tool" "$@"; then
    echo "ERROR: exported API exposes internal types in ${label}!"
    return 1
  fi
}

case "${1:-}" in
  --self-test)
    run_matcher_self_test
    run_fixture_test
    exit 0
    ;;
  --matcher-self-test)
    run_matcher_self_test
    exit 0
    ;;
  --fixture-test)
    run_fixture_test
    exit 0
    ;;
esac

status=0
check_no_internal_imports "./pkg/api/..." "pkg/api contract packages" || status=1
check_no_internal_imports "./pkg/spi/..." "pkg/spi contract packages" || status=1

packages=(.)
while read -r package; do
  packages+=("$package")
done < <(go list ./pkg/... ./storage/...)
check_exported_symbols "root + pkg + storage adapters" "${packages[@]}" || status=1

if ((status != 0)); then
  echo "FAILED: public API purity violations detected"
  exit 1
fi

echo "SUCCESS: public API purity check passed (contract imports clean, exported API internal-free)."
