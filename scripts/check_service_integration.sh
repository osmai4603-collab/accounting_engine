#!/bin/sh
set -eu

if [ -z "${DATABASE_DSN:-}" ]; then
	echo "DATABASE_DSN must point to PostgreSQL" >&2
	exit 1
fi

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

binary=.accounting-service-integration
http_port=${HTTP_PORT:-18080}
grpc_port=${GRPC_PORT:-15051}
service_pid=

if [ -e "$binary" ]; then
	echo "$binary already exists; refusing to overwrite it" >&2
	exit 1
fi

cleanup() {
	if [ -n "$service_pid" ]; then
		kill "$service_pid" 2>/dev/null || true
		wait "$service_pid" 2>/dev/null || true
	fi
	rm -f "$binary"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

go build -o "$binary" ./cmd/accounting-service
HTTP_PORT="$http_port" \
GRPC_PORT="$grpc_port" \
DATABASE_DSN="$DATABASE_DSN" \
	"./$binary" &
service_pid=$!

HTTP_PORT="$http_port" GRPC_PORT="$grpc_port" go run ./test/service_integration
