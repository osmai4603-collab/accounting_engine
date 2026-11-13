#!/usr/bin/env bash
# Detects unannounced breaking changes in the exported Go API.
#
# The committed baseline lives in docs/api/v1.snapshot. Adding API is allowed
# (MINOR). Removing or changing an existing line is a MAJOR change and fails
# unless the baseline is updated deliberately:
#
#   ./scripts/check_api_compat.sh --update
#
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
baseline="$repo_root/docs/api/v1.snapshot"
current="$(mktemp)"
trap 'rm -f "$current"' EXIT

packages=(
  "github.com/osmai4603-collab/accounting_engine"
  "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
  "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
  "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
  "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
  "github.com/osmai4603-collab/accounting_engine/pkg/sdk"
  "github.com/osmai4603-collab/accounting_engine/pkg/sdk/templates"
  "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
  "github.com/osmai4603-collab/accounting_engine/pkg/spi/events"
  "github.com/osmai4603-collab/accounting_engine/pkg/spi/legacy"
  "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
  "github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
  "github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
  "github.com/osmai4603-collab/accounting_engine/storage/memory"
  "github.com/osmai4603-collab/accounting_engine/storage/postgres"
)

cd "$repo_root"
go run ./scripts/apisnapshot "${packages[@]}" >"$current"

if [[ "${1:-}" == "--update" ]]; then
  mkdir -p "$(dirname "$baseline")"
  cp "$current" "$baseline"
  echo "api compatibility baseline updated: $baseline"
  exit 0
fi

if [[ ! -f "$baseline" ]]; then
  echo "ERROR: missing API baseline $baseline; run ./scripts/check_api_compat.sh --update" >&2
  exit 1
fi

removed="$(comm -23 <(sort "$baseline") <(sort "$current"))"
if [[ -n "$removed" ]]; then
  echo "ERROR: unannounced breaking API changes detected:" >&2
  echo "$removed" >&2
  echo "If this is an intentional MAJOR change, update the baseline deliberately." >&2
  exit 1
fi

echo "API compatibility check: passed (no removals or signature changes)"
