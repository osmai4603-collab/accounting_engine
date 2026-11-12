#!/usr/bin/env bash
# High-confidence secret scan used locally and in CI. It is intentionally
# conservative: it fails only on credential shapes that are never legitimate
# test data. Use gitleaks for the full ruleset in CI.
set -euo pipefail

patterns='AKIA[0-9A-Z]{16}|-----BEGIN (RSA|EC|OPENSSH|PGP) PRIVATE KEY-----|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|AIza[0-9A-Za-z_-]{20,}|xox[baprs]-[A-Za-z0-9-]{10,}'

matches="$(grep -rInE --exclude-dir=.git --exclude-dir=node_modules --exclude=go.sum --exclude=coverage.out "${patterns}" . || true)"
if [[ -n "${matches}" ]]; then
  echo "Potential secrets detected:" >&2
  echo "${matches}" >&2
  exit 1
fi
echo "secret scan: clean"
