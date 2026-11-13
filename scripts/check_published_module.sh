#!/usr/bin/env bash
# Verifies that an independent module can import the accounting engine from a
# published module proxy, with no replace directive and no access to internal/*.
#
# The script builds a file-based module proxy from the current working tree,
# then builds and runs a consumer module against it.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version="${1:-v0.0.0}"
module_path="github.com/osmai4603-collab/accounting_engine"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

proxy="$work/proxy"
mod_cache="$proxy/$module_path/@v"
mkdir -p "$mod_cache"

python3 - "$repo_root" "$mod_cache/$version.zip" "$version" "$module_path" <<'PY'
import os
import sys
import zipfile

repo, zip_path, version, module_path = sys.argv[1:5]
prefix = f"{module_path}@{version}/"
excluded_dirs = {".git", ".gotmp", "test/external_consumer"}

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
            if rel in {"go.mod", "go.sum"} or rel.endswith(".go") or rel.startswith(("pkg/", "internal/", "storage/", "modules/", "proto/", "scripts/", "docs/", "cmd/", "deployments/")):
                archive.write(os.path.join(root, name), prefix + rel)
PY

cp "$repo_root/go.mod" "$mod_cache/$version.mod"
printf '{"Version":"%s","Time":"2026-11-14T00:00:00Z"}\n' "$version" >"$mod_cache/$version.info"
printf '%s\n' "$version" >"$mod_cache/list"

consumer="$work/consumer"
mkdir -p "$consumer"
cat >"$consumer/go.mod" <<EOF
module example.com/publishedconsumer

go 1.26.0

require $module_path $version
EOF

cat >"$consumer/main.go" <<'EOF'
package main

import (
	"context"
	"fmt"

	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	"github.com/osmai4603-collab/accounting_engine/storage/memory"
)

func main() {
	driver := memory.New()
	engine, err := sdk.NewBuilder(sdk.WithStoragePorts(driver.Ports())).Build(context.Background())
	if err != nil {
		panic(err)
	}
	if err := engine.Close(); err != nil {
		panic(err)
	}
	fmt.Println("published module consumer: ok")
}
EOF

export GOPROXY="file://$proxy,https://proxy.golang.org,direct"
export GOSUMDB=off
export GOFLAGS=-mod=mod
export GONOSUMDB="github.com/osmai4603-collab/*"

(
  cd "$consumer"
  go mod tidy
  if grep -q '^replace ' go.mod; then
    echo "ERROR: published-module consumer resolved a replace directive" >&2
    exit 1
  fi
  go build ./...
  go run .
)
