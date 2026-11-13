#!/usr/bin/env bash
# Verifies that every public Go package in the module exposes only public
# contracts: no imports of internal/* and no exported symbol that mentions an
# internal package path.
set -euo pipefail

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
  mkdir -p "$dir/internal/secret" "$dir/pkg/public"
  printf 'module example.com/fixture\n\ngo 1.26.0\n' >"$dir/go.mod"
  printf 'package secret\n\nconst Value = 1\n' >"$dir/internal/secret/secret.go"
  printf 'package public\n\nimport "example.com/fixture/internal/secret"\n\nfunc Get() int { return secret.Value }\n' >"$dir/pkg/public/public.go"
  local imports violations=0
  imports="$(cd "$dir" && go list -f '{{range .Imports}}{{$.ImportPath}} {{.}}{{"\n"}}{{end}}' ./pkg/...)"
  while read -r package imported; do
    [[ -n "${imported:-}" ]] || continue
    if is_internal_import "$imported"; then
      violations=$((violations + 1))
    fi
  done <<<"$imports"
  if ((violations == 0)); then
    echo "fixture test FAILED: public package importing internal was not detected" >&2
    exit 1
  fi
  echo "purity fixture test: internal import detected as expected"
}

check_packages() {
  local pattern="$1" label="$2"
  echo "Checking public API purity for ${label} ..."
  local imports
  imports="$(go list -f '{{range .Imports}}{{$.ImportPath}} {{.}}{{"\n"}}{{end}}' $pattern)"
  local violations=0
  while read -r package imported; do
    [[ -n "${imported:-}" ]] || continue
    if is_internal_import "$imported"; then
      echo "VIOLATION: Public package $package imports internal package $imported"
      violations=$((violations + 1))
    fi
  done <<<"$imports"
  if ((violations > 0)); then
    echo "ERROR: Found $violations purity violation(s) in ${label}!"
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
check_packages "." "module root package" || status=1
check_packages "./pkg/..." "pkg/ packages" || status=1

echo "Checking exported symbols for internal type references ..."
if go doc -all . 2>/dev/null | grep -nE '(^|[^a-zA-Z0-9_])internal/'; then
  echo "ERROR: Root public API exposes internal package types"
  status=1
fi

if ((status != 0)); then
  echo "FAILED: public API purity violations detected"
  exit 1
fi

echo "SUCCESS: public API purity check passed (root + pkg, zero internal exposure)."
