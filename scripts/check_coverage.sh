#!/usr/bin/env bash
# Compare the total coverage in a coverprofile with COVERAGE_MIN.
# Usage: COVERAGE_MIN=54.1 scripts/check_coverage.sh coverage.out
set -euo pipefail

profile="${1:-coverage.out}"
minimum="${COVERAGE_MIN:-0}"

if [[ ! -f "${profile}" ]]; then
  echo "coverage profile ${profile} not found" >&2
  exit 1
fi

total="$(go tool cover -func="${profile}" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"
if [[ -z "${total}" ]]; then
  echo "could not read total coverage from ${profile}" >&2
  exit 1
fi

echo "total coverage: ${total}% (minimum ${minimum}%)"
awk -v total="${total}" -v minimum="${minimum}" 'BEGIN {
  if (total + 0 < minimum + 0) {
    printf "coverage regression: %.1f%% is below the required %.1f%%\n", total, minimum > "/dev/stderr"
    exit 1
  }
}'
