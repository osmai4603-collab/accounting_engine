#!/usr/bin/env bash
set -e

echo "Checking public API purity under pkg/ ..."

# Find all packages under pkg/ and check their imports for internal/
imports=$(go list -f '{{if .Imports}}{{.ImportPath}}: {{join .Imports ", "}}{{end}}' ./pkg/...)

violations=0
while IFS= read -r line; do
  if [[ "$line" == *"internal"* ]]; then
    echo "VIOLATION: Public package imports internal: $line"
    violations=$((violations + 1))
  fi
done <<< "$imports"

if [ $violations -gt 0 ]; then
  echo "ERROR: Found $violations purity violation(s) in pkg/!"
  exit 1
fi

echo "SUCCESS: pkg/ api purity check passed (zero internal imports)."
