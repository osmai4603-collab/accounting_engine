# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.1.0] - 2026-11-16

### Added

- Official Go module path established at `github.com/osmai4603-collab/accounting_engine`.
- Public SDK builder and fluent configuration under `pkg/sdk`.
- Canonical financial API and master data types under `pkg/api/v1/types`.
- Ledger commands, posting invariants, and lifecycle use-cases under `pkg/api/v1/ledger`.
- Event ingestion API and idempotent processing under `pkg/api/v1/events`.
- Financial reporting (Balance Sheet, Income Statement, Trial Balance) under `pkg/api/v1/reporting`.
- Storage Service Provider Interface (SPI) under `pkg/spi/storage`.
- Production-grade PostgreSQL storage driver (`storage/postgres`) with Row-Level Security tenant isolation and atomic sequencing.
- In-memory test storage driver (`storage/memory`) with semantic contract parity.
- Standards modular SPI under `pkg/spi/standards` with bounded implementations for IFRS 15, IFRS 16, IFRS 9, and IAS 12.
- 11 sequential PostgreSQL database migrations with cryptographic checksum verification.
- Complete contract parity test suite (`test/contract_parity`).
- Standalone external consumer tests (`test/external_consumer` and `test/external_adapter_consumer`).
- Public API compatibility gate (`scripts/check_api_compat.sh`) and purity checks (`scripts/check_public_api_purity.sh`).
- Comprehensive documentation index, SDK developer guide, and operational runbooks under `docs/`.

### Deprecated

- Root package legacy facade `accounting_engine.Engine` (`facade.go`) is deprecated in favor of `pkg/sdk.NewBuilder`; slated for removal in `v1.0.0`.

### Security & Governance

- Row-Level Security (RLS) policies enforcing multi-tenant isolation at the database level.
- Tamper-evident cryptographic hash chaining for posted journal entry audit trails.
- Database triggers enforcing immutability of posted financial records.
- Open source license compliance verification script (`scripts/check_licenses.sh`).

[v0.1.0]: https://github.com/osmai4603-collab/accounting_engine/releases/tag/v0.1.0
