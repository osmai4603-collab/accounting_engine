#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

require_version() {
	command_name=$1
	expected=$2
	if ! command -v "$command_name" >/dev/null 2>&1; then
		echo "$command_name is required; see proto/README.md" >&2
		exit 1
	fi
	actual=$("$command_name" --version 2>&1)
	if [ "$actual" != "$expected" ]; then
		echo "$command_name version mismatch: found '$actual', expected '$expected'" >&2
		exit 1
	fi
}

require_version protoc "libprotoc 27.3"
require_version protoc-gen-go "protoc-gen-go v1.36.12"
require_version protoc-gen-go-grpc "protoc-gen-go-grpc 1.5.1"

protoc \
	-I . \
	--go_out=. \
	--go_opt=module=accounting_engine \
	--go-grpc_out=. \
	--go-grpc_opt=module=accounting_engine \
	proto/accounting/v1/events.proto \
	proto/accounting/v1/ledger.proto \
	proto/accounting/v1/reporting.proto
