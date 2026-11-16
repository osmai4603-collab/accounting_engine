#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
license_tool="${GO_LICENSES:-go-licenses}"
allowed_licenses="MIT,MIT-0,Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,0BSD,Zlib,BSL-1.0,CC0-1.0,Unlicense"
report_dir="${LICENSE_REPORT_DIR:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/accounting-engine-license-reports}"

if ! command -v "$license_tool" >/dev/null 2>&1; then
	echo "go-licenses is required: go install github.com/google/go-licenses@v1.6.0" >&2
	exit 1
fi

if [[ "$report_dir" != /* ]]; then
	report_dir="$root/$report_dir"
fi
mkdir -p "$report_dir"
report_dir="$(cd "$report_dir" && pwd)"

scan_module() {
	local module_path="$1"
	local report_name="$2"

	(
		cd "$root/$module_path"
		"$license_tool" csv --include_tests --ignore=github.com/osmai4603-collab/accounting_engine ./... \
			> "$report_dir/$report_name.csv"
		"$license_tool" check --include_tests --ignore=github.com/osmai4603-collab/accounting_engine \
			--allowed_licenses="$allowed_licenses" ./...
	)
}

scan_module "." root
scan_module "test/external_consumer" external-consumer
scan_module "test/external_adapter_consumer" external-adapter-consumer
