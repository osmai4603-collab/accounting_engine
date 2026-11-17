# Release Archive & Artifact Verification — v0.1.0

- **Tag:** `v0.1.0`
- **Date:** 2026-11-16
- **Module:** `github.com/osmai4603-collab/accounting_engine`
- **Release Type:** Initial Developer Preview / Embedded SDK Release
- **Related Records:** [Archived P7-05 Release Readiness](../archive/reports/release_readiness_p7-05.md), [Contract Parity Report](./contract_parity_report.md), [ARD-P7-05](../ard/ARD-P7-05.md), [ARD-P7-06](../ard/ARD-P7-06.md).

---

## 1. Release Identification and Metadata

| Item | Value |
| --- | --- |
| Module Path | `github.com/osmai4603-collab/accounting_engine` |
| Release Tag | `v0.1.0` |
| Go Version | `go 1.26.0` (Toolchain `go1.26.3`) |
| Architecture Standard | Clean Architecture / Decoupled Ports & Adapters |
| Primary Form | Embedded Go Library SDK (`pkg/sdk`) |

---

## 2. Quality and Verification Summary

All verification gates were executed locally and recorded for release archiving:

| Gate / Check | Command | Status | Evidence / Notes |
| --- | --- | --- | --- |
| **Public API Purity** | `./scripts/check_public_api_purity.sh` | **PASS** | No internal packages imported by public API/SPI; no internal types leaked in exported symbols. |
| **API Compatibility** | `./scripts/check_api_compat.sh` | **PASS** | Baseline snapshot `docs/api/v1.snapshot` (985 lines) verified without breaking signature changes. |
| **Unit & Contract Tests** | `go test -count=1 ./...` | **PASS** | 53 test suites passed across all packages, including PostgreSQL adapter integration tests. |
| **Contract Parity** | `go test -count=1 ./test/contract_parity` | **PASS** | 19 semantic invariants verified identically across storage implementations. |
| **Concurrency & Race** | `go test -race -count=1 ./pkg/... ./internal/posting/...` | **PASS** | Core SDK, posting engine, master data, and command line tools passed without data race reports. |
| **External Consumer** | `make test-external-consumer` | **PASS** | Independent external consumer project builds and executes successfully. |
| **Published Module Proxy** | `./scripts/check_published_module.sh v0.1.0` | **PASS** | Tested against clean file-based Go module proxy without `replace` directives. |
| **External Adapter Proxy** | `./scripts/check_external_adapter_consumer.sh --published` | **PASS** | Tested storage SPI consumption from an independent consumer module without replace directives. |
| **Code Formatting** | `./scripts/check_gofmt.sh` | **PASS** | Entire codebase is strictly formatted according to standard `gofmt`. |
| **Architecture Gates** | `make architecture-check` | **PASS** | Posting engine construction boundary and application layer decoupling rules verified. |
| **License Compliance** | `./scripts/check_licenses.sh` | **PASS** | All dependencies verified compliant with permitted open-source licenses (MIT, Apache-2.0, BSD). Reports archived in `/tmp/accounting-engine-license-reports/`. |
| **Vulnerability Audit** | `govulncheck ./...` | **OBSERVED** | Zero vulnerabilities called in third-party imported dependencies; standard library items observed for Go 1.26.3 toolchain. |

---

## 3. Golden Accounting Scenarios

Golden accounting suites validate real-world commercial scenarios across supported industries and standard modules:

- Commercial sales, trade receivables, and deferred revenue matching.
- IFRS 15 5-step model revenue recognition with performance obligation allocation.
- IFRS 16 lease liability and right-of-use asset amortizations.
- IFRS 9 loan impairment provisioning and amortized cost.
- Multi-currency transactions with functional, presentation, and transactional currency revaluation.
- Perpetual inventory subledger integration with COGS generation.

---

## 4. Software Bill of Materials (SBOM) & Third-Party Dependencies

Dependency analysis was performed using `go-licenses`. The top-level dependencies and their licenses:

| Dependency | Version | License |
| --- | --- | --- |
| `github.com/google/uuid` | v1.6.0 | BSD-3-Clause |
| `github.com/jackc/pgx/v5` | v5.12.0 | MIT |
| `github.com/shopspring/decimal` | v1.6.0 | MIT |
| `github.com/aws/aws-sdk-go-v2` | v1.47.8 | Apache-2.0 |
| `google.golang.org/protobuf` | v1.36.12 | BSD-3-Clause |
| `google.golang.org/grpc` | v1.79.3 | Apache-2.0 |

Detailed CSV exports:

- `root.csv`: Full engine root module dependencies.
- `external-consumer.csv`: Independent consumer dependencies.
- `external-adapter-consumer.csv`: Custom storage adapter dependencies.

---

## 5. Artifact Archive Checksums

The release tag `v0.1.0` commits and protects all release documents, ADRs, test suites, and operational policies under git tracking.
