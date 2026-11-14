#!/usr/bin/env bash
# External adapter consumption gate (P3-05).
#
# Default mode builds and tests the independent module in
# test/external_adapter_consumer against the working tree (its go.mod carries a
# documented local-only replace) and rejects any internal or bundled-storage
# import.
#
# --published mode proves the same project also builds against a versioned
# module with the replace removed, using a file-based module proxy.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
module_path="github.com/osmai4603-collab/accounting_engine"
module_dir="$repo_root/test/external_adapter_consumer"
# A per-run pre-release version keeps the file proxy authoritative even when an
# older v0.0.0 snapshot exists in the local module cache.
version="${EXTERNAL_CONSUMER_VERSION:-v0.0.0-externaladapter.$(date +%s)}"

check_imports() {
  local dir="$1"
  local violations
  # Match import lines only (optionally aliased). The Go import test is the
  # authoritative parser; this grep gives the shell gate an independent check.
  if violations="$(grep -RInE '^[[:space:]]*([[:alnum:]_.]+[[:space:]]+)?"[^"]*(internal/|storage/memory|storage/postgres)[^"]*"' \
    --include='*.go' "$dir")"; then
    echo "ERROR: forbidden imports in external adapter consumer:" >&2
    echo "$violations" >&2
    exit 1
  fi
}

run_local() {
  cd "$module_dir"
  go build ./...
  go test -count=1 ./...
  check_imports "$module_dir"
  echo "external adapter consumer: local build/test/import gate passed"
}

zip_module() {
  local zip_path="$1"
  python3 - "$repo_root" "$zip_path" "$version" "$module_path" <<'PY'
import os
import sys
import zipfile

repo, zip_path, version, module_path = sys.argv[1:5]
prefix = f"{module_path}@{version}/"
excluded_dirs = {".git", ".gotmp", "test/external_consumer", "test/external_adapter_consumer"}

with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLATED) as archive:
    for root, dirs, files in os.walk(repo):
        rel_root = os.path.relpath(root, repo)
        if rel_root == ".":
            rel_root = ""
        dirs[:] = sorted(
            d for d in dirs
            if os.path.join(rel_root, d).replace(os.sep, "/").strip("/") not in excluded_dirs
        )
        for name in sorted(files):
            rel = os.path.join(rel_root, name).replace(os.sep, "/").strip("/")
            if rel in {"go.mod", "go.sum"} or rel.endswith(".go") or rel.startswith((
                "pkg/", "internal/", "storage/", "modules/", "proto/", "scripts/",
                "docs/", "cmd/", "deployments/",
            )):
                archive.write(os.path.join(root, name), prefix + rel)
PY
}

run_published() {
  local work proxy mod_cache consumer
  work="$(mktemp -d)"
  trap 'rm -rf "${work:-}"' EXIT

  proxy="$work/proxy"
  mod_cache="$proxy/$module_path/@v"
  mkdir -p "$mod_cache"
  zip_module "$mod_cache/$version.zip"
  cp "$repo_root/go.mod" "$mod_cache/$version.mod"
  printf '{"Version":"%s","Time":"2026-11-14T00:00:00Z"}\n' "$version" >"$mod_cache/$version.info"
  printf '%s\n' "$version" >"$mod_cache/list"

  consumer="$work/external_adapter_consumer"
  cp -a "$module_dir/." "$consumer/"
  rm -f "$consumer/go.sum"
  sed -i '/^replace /d' "$consumer/go.mod"
  (cd "$consumer" && go mod edit -require="$module_path@$version")
  if grep -q '^replace ' "$consumer/go.mod"; then
    echo "ERROR: published consumer still contains a replace directive" >&2
    exit 1
  fi

  export GOPROXY="file://$proxy,https://proxy.golang.org,direct"
  export GOSUMDB=off
  export GONOSUMDB="github.com/osmai4603-collab/*"
  export GOFLAGS=-mod=mod

  (
    cd "$consumer"
    go mod tidy
    if grep -q '^replace ' go.mod; then
      echo "ERROR: published consumer resolved a replace directive" >&2
      exit 1
    fi
    go build ./...
    go test -count=1 ./...
    check_imports "$consumer"
  )
  echo "external adapter consumer: published (no replace) build/test/import gate passed"
}

case "${1:-}" in
  "")
    run_local
    ;;
  --published)
    run_published
    ;;
  *)
    echo "usage: $0 [--published]" >&2
    exit 2
    ;;
esac
