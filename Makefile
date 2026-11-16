# Local CI entry points. The same commands run in .github/workflows/ci.yml.
#
#   make ci            run every local gate (needs PostgreSQL for integration)
#   make test          go test ./...
#   make race          go test -race ./...
#   make coverage      write coverage.out and enforce COVERAGE_MIN
#   make security      dependency vulnerability, gosec, and secret scans
#   make migrations    migration guard + PostgreSQL migration lifecycle tests

GO ?= go
COVERAGE_MIN ?= 54.1
POSTGRES_URL ?= postgres://osm@localhost:5432/accounting_test?sslmode=disable&host=/var/run/postgresql
export POSTGRES_URL

.PHONY: ci fmt-check vet build architecture-check test test-contract-parity test-external-consumer test-external-adapter-consumer test-gapred api-compat published-module integration-service proto race coverage coverage-report security licenses migrations clean

ci: fmt-check vet build architecture-check test test-gapred api-compat published-module integration-service race coverage security migrations

fmt-check:
	./scripts/check_gofmt.sh

vet:
	$(GO) vet ./...

build:
	$(GO) build ./...

architecture-check:
	./scripts/check_public_api_purity.sh --self-test
	./scripts/check_public_api_purity.sh
	./scripts/check_application_layer.sh

test: test-external-consumer test-external-adapter-consumer
	$(GO) test -count=1 ./...

test-contract-parity:
	$(GO) test -count=1 ./test/contract_parity

test-external-consumer:
	cd test/external_consumer && $(GO) test -count=1 ./...

test-external-adapter-consumer:
	./scripts/check_external_adapter_consumer.sh

test-gapred:
	$(GO) test -tags gapred -count=1 ./...

api-compat:
	./scripts/check_api_compat.sh

published-module:
	./scripts/check_published_module.sh

integration-service:
	@test -n "$(POSTGRES_URL)" || (echo "POSTGRES_URL must point to PostgreSQL" >&2; exit 1)
	DATABASE_DSN="$(POSTGRES_URL)" ./scripts/check_service_integration.sh

proto:
	./proto/generate.sh

race:
	$(GO) test -race -count=1 ./...

coverage:
	$(GO) test -count=1 -coverprofile=coverage.out ./...
	COVERAGE_MIN=$(COVERAGE_MIN) ./scripts/check_coverage.sh coverage.out

coverage-report: coverage
	$(GO) tool cover -html=coverage.out -o coverage.html

security: licenses
	$(GO) mod verify
	@command -v govulncheck >/dev/null 2>&1 || (echo "govulncheck is required: go install golang.org/x/vuln/cmd/govulncheck@latest" >&2; exit 1)
	govulncheck ./...
	@command -v gosec >/dev/null 2>&1 || (echo "gosec is required: go install github.com/securego/gosec/v2/cmd/gosec@latest" >&2; exit 1)
	gosec -quiet -severity high -confidence high ./...
	./scripts/scan_secrets.sh

licenses:
	./scripts/check_licenses.sh

migrations:
	$(GO) test -count=1 ./internal/adapters/postgres -run 'TestMigrationsHaveReversiblePairs|TestDangerousMigrationsRequireApproval|TestCleanMigrationLifecycle|TestBaselineMigrationCleanRun|TestFailedMigrationRollsBackAndCanBeRetried|TestMigrationUpgradeFromOldestSupportedSchemaPreservesData|TestValidateSchemaHealth'

clean:
	rm -f coverage.out coverage.html
