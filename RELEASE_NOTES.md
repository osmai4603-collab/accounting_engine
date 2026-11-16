# Accounting Engine Release Notes

## Version v0.1.0 (2026-11-16)

**Release Type:** Initial Developer Preview / Embedded SDK Release  
**Official Module Path:** `github.com/osmai4603-collab/accounting_engine`  
**Governing Documents:** [Compatibility Policy](docs/compatibility_policy.md), [SDK Developer Guide](docs/sdk_developer_guide.md), [Release Readiness Report](docs/reports/release_readiness_p7-05.md).

---

### 1. Overview and Architecture

`v0.1.0` marks the first tagged release of the **Accounting Engine** as a standalone, embeddable Go library (Embedded SDK). In accordance with [ADR-0006](docs/adr/ADR-0006-release-form-storage-toolchain.md), the engine runs in-process inside host application backends, exposing a clean, decoupled Go SDK and SPI without requiring standalone network infrastructure.

The release establishes formal contract isolation between public consumers and internal engine implementations:

- External consumers interact exclusively through `pkg/sdk`, `pkg/api/v1/...`, and `pkg/spi/...`.
- No `internal/**` packages are exposed or imported by public consumers.
- No engine-specific database SQL queries or internal schema knowledge are required from consumers.

---

### 2. Key Capabilities in v0.1.0

#### A. Core Double-Entry Posting Engine (`pkg/sdk`, `pkg/api/v1/ledger`)

- **Strict Invariant Enforcement:** Unbalanced entries are rejected before posting; balanced multi-currency entries are strictly validated.
- **Atomic Posting & Sequencing:** Post-and-sequence operations are fully atomic under high concurrency, preventing gaps or duplicate sequence numbers.
- **Deterministic Reversals:** Atomic reversal with mandatory actor ID, operational reason, and original entry linking.
- **Idempotent Ingestion:** Automatic deduplication using idempotency keys, guaranteeing identical outputs on replay without creating duplicate moves or journal entries.

#### B. Storage Drivers & SPI (`pkg/spi/storage`, `storage/`)

- **PostgreSQL (`storage/postgres`):** Production-grade storage with automatic sequential migrations, Row-Level Security (RLS) tenant isolation, tamper-evident audit chaining, and connection pooling.
- **Memory (`storage/memory`):** High-speed, in-memory transactional storage for unit testing and CI, with semantic parity for core lifecycle operations.
- **External Storage SPI (`pkg/spi/storage`):** Fully documented interface allowing third-party host systems to provide custom storage drivers without modifying engine core code.

#### C. Financial Reporting & Trial Balance (`pkg/api/v1/reporting`)

- On-demand calculation of **Balance Sheet**, **Income Statement (P&L)**, and **Trial Balance**.
- Multi-currency support (functional, presentation, and transaction currencies).
- As-of date filtering and period boundary checks.

#### D. Standards & Extensibility SPI (`pkg/spi/standards`)

- Modular standard registration mechanism with explicit module descriptors (`ModuleDescriptor`, `DescribedModule`).
- Topological sort and cycle detection for standard module dependencies.
- Bounded implementations for:
  - **IFRS 15:** Revenue from contracts with customers (performance obligation allocation and recognition).
  - **IFRS 16:** Leases (Right-of-Use asset and lease liability amortization).
  - **IFRS 9:** Financial instruments (amortized cost classification and loss provisioning).
  - **IAS 12:** Income taxes (deferred tax liability/asset calculations).

---

### 3. Deprecations & Breaking Changes

- **Root Package Facade Deprecation:** The root package export `accounting_engine.Engine` (`facade.go`) is formally marked **Deprecated**. Consumers must transition to `pkg/sdk.NewBuilder`. The legacy facade will be removed in `v1.0.0` (see [ADR-0001](docs/adr/ADR-0001-public-entry-point.md)).
- **Module Path Stabilization:** The Go module path is permanently fixed to `github.com/osmai4603-collab/accounting_engine` (see [ADR-0002](docs/adr/ADR-0002-module-path-semver.md)).

---

### 4. Database Migrations

This release introduces 11 sequential, checksum-guarded schema migrations for PostgreSQL under `internal/adapters/postgres/migrations/`:

1. `202611010001_baseline_schema.sql` — Companies, accounts, journals, fiscal years, periods.
2. `202611090001_baseline_tables.sql` — Core journal entries, lines, and posted moves.
3. `202611100002_phase1_entry_classification.sql` — Financial categories and IFRS classifications.
4. `202611100003_phase2_idempotency_keys.sql` — Idempotency storage and constraints.
5. `202611100004_phase2_tamper_evident_audit.sql` — Cryptographic hash chaining for ledger audit.
6. `202611110005_phase3_storage_ports.sql` — Storage port support tables.
7. `202611120006_phase3_rls_policies.sql` — Row-Level Security tenant isolation policies.
8. `202611120007_phase4_audit_immutability.sql` — Database triggers enforcing append-only immutability.
9. `202611130008_phase4_period_close_reopen.sql` — Period closing and reopening state controls.
10. `202611130009_phase5_standards_and_reconciliation.sql` — Standards module state tracking.
11. `202611140010_phase6_standalone_readiness.sql` — Standalone readiness probes and metrics views.

Schema updates follow the zero-downtime Expand/Contract lifecycle documented in [docs/migration_policy.md](docs/migration_policy.md).

---

### 5. Known Limitations & Product Boundaries

1. **Authentication & Authorization Boundary:** In accordance with [ADR-0004](docs/adr/ADR-0004-auth-and-company-context.md), user authentication, credentials, and commercial business role authorization are the sole responsibility of the host system. The engine strictly requires and validates a trusted `ExecutionContext` for all operations.
2. **Standard Scope Boundaries:** IFRS and IAS modules represent bounded automation capabilities and do not constitute universal or certified accounting compliance. For detailed out-of-scope items, consult [P7-05 Release Readiness](docs/reports/release_readiness_p7-05.md).
3. **Storage Parity Differences:** Memory storage does not guarantee durability, foreign-key cascade semantics, or restart persistence; it is strictly intended for test environments.
4. **Standalone Service Status:** In line with [ADR-0006](docs/adr/ADR-0006-release-form-storage-toolchain.md), HTTP/gRPC standalone service daemons (`cmd/accounting-service`) are optional deployment adapters and deferred from initial primary release requirements.

---

### 6. Release Readiness & Go/No-Go Advisory

As evaluated in [P7-05 Release Readiness Report](docs/reports/release_readiness_p7-05.md):

- **Embedded Library Evaluation:** **GO**. The library is ready for integration, development, and testing inside host applications. All public contracts, architecture boundaries, and external consumer tests pass without errors.
- **Standalone Production Deployment:** **NO-GO**. Unattended standalone production deployment remains advisory-blocked until:
  1. A live physical database backup and PITR restore exercise (`pg_dump`/`pg_restore`) is completed (see P7-03).
  2. Formal multi-disciplinary production sign-offs (security, architecture, accounting) are completed.
