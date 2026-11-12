#!/usr/bin/env bash
# Fail when any Go file is not gofmt-formatted. This is the same gate CI runs.
set -euo pipefail

unformatted="$(gofmt -l .)"
if [[ -n "${unformatted}" ]]; then
  echo "The following files are not gofmt-formatted:" >&2
  echo "${unformatted}" >&2
  echo "Run: gofmt -w \$(gofmt -l .)" >&2
  exit 1
fi
echo "gofmt: clean"
