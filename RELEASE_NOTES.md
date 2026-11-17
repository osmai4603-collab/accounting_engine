# Accounting Engine Release Notes

## Version v0.2.0 (2026-11-17)

**Release Type:** Minor Feature & Operational Hardening Release  
**Official Module Path:** `github.com/osmai4603-collab/accounting_engine`  
**Governing Documents:** [Compatibility Policy](docs/compatibility_policy.md), [SDK Developer Guide](docs/sdk_developer_guide.md), [Release Readiness Report v0.2.0](docs/release_readiness_v0.2.0.md), [Disaster Recovery Runbook](docs/operations/runbooks/disaster_recovery_runbook.md).

---

### 1. Overview and Architecture

`v0.2.0` is a major advancement that completes all architectural, accounting, and operational gaps identified after `v0.1.0`. The release delivers complete accounting core workflows (Atomic Year-End Closing, Dual-Method Cash Flows), public SPI subledgers (AR/AP, Fixed Assets, Treasury), automated standards posting with deterministic idempotency for IFRS/IAS modules, operational rate limiting for standalone deployments, and comprehensive fault recovery verification.

Most critically, **the conditional NO-GO advisory on standalone production deployment has been officially lifted** following a successful end-to-end disaster recovery drill on PostgreSQL with verified accounting integrity and zero data loss (RPO = 0).

---

### 2. Key Capabilities in v0.2.0

#### A. Operational Hardening & Disaster Recovery (`P12`)

- **Automated Live Disaster Recovery Drill (`P12-01`):** Verified full physical backup (`pg_dump -Fc`) and restore (`pg_restore`) against independent PostgreSQL databases. Achieved RTO ≈ 1.49s and RPO = 0 with 100% pass on all 6 core accounting integrity checks (double-entry balance, perpetual balance reconciliation, unbroken audit hash chain, strict RLS tenant isolation, idempotency key preservation, and monotonic sequence continuity).
- **Transport Rate Limiting for Standalone Service (`P12-02`):** Token Bucket algorithm protecting HTTP (`429 Too Many Requests`) and gRPC (`RESOURCE_EXHAUSTED`) endpoints with bounded memory usage, IP-based pre-auth shielding, and per-tenant authenticated quotas. The embedded SDK core remains 100% lightweight and free of network rate limiting constraints.
- **Performance Benchmarks & Baseline (`P12-03`):** Official benchmark suite covering draft creation, posting pipeline, concurrent posting, balance sheet generation, cash flow generation, and year-end closing across both Memory and PostgreSQL adapters.
- **Event Ingestion Fault Recovery (`P12-04`):** Verified effectively-once processing via durable idempotency, graceful transient error retries, deterministic failure logging, and crash resumption of pending `started` events.

#### B. Automated Standards Posting Pipeline (`P11`)

- **Automated Posting via Application Layer (`P11-05`):** Unified `StandardPostingCapability` in `pkg/spi/standards` orchestrating automated journal entry generation for:
  - **IFRS 15:** Performance obligation satisfaction, variable consideration adjustments, and contract modifications.
  - **IFRS 9:** Amortized cost classification, EIR interest accruals, and stage-based ECL provisions.
  - **IFRS 16:** Right-of-Use amortization, lease liabilities, short-term/low-value exemptions, and lessor accounting.
  - **IAS 12 / IFRIC 23:** Deferred tax computations, tax rate reconciliation disclosures, and uncertain tax treatments.
- **Chart of Accounts Validation:** Mandatory verification of target accounts via `MasterDataReader` prior to draft generation.
- **Deterministic Idempotency:** SHA-1 payload hashing guaranteeing replay safety across all standard transactions.

#### C. Public Subledgers SPI (`P10`)

- **Accounts Receivable & Payable (AR/AP) (`P10-01`):** Open-item tracking, aging analysis, and settlement matching exposed via public SPI with strict separation of commercial workflows from ledger moves.
- **Fixed Assets (`P10-02`):** Asset register, straight-line and accelerated depreciation schedules, and disposal accounting.
- **Treasury & Bank Reconciliation (`P10-03`):** Statement parsing, bank transaction matching, uncleared items tracking, and discrepancy resolution.
- **Foreign Exchange Accounting (`P10-04`):** Transaction currency handling, spot revaluation, and realized/unrealized FX gain/loss recognition under IAS 21.

#### D. Core Accounting Lifecycle (`P9`)

- **Atomic Year-End Closing (`P9-01`):** Single Unit of Work (Single UoW) closing nominal accounts, transferring net income to retained earnings, creating new fiscal year opening entries, and hard-closing the fiscal period.
- **Cash Flow Statement Dual-Method (`P9-02`):** Direct and Indirect cash flow calculation according to IAS 7 with presentation alignment under IFRS 18.
- **Adjusting Entries (`P9-03`) & Period Lifecycle (`P9-04`):** Explicit accrual/deferral adjusting entries and audited reopening controls.
- **Structured Disclosures (`P9-05`):** Multi-version disclosure notes linked to financial statement nodes.

#### E. Architectural Integrity (`P8`)

- **Single Unit of Work Atomic Posting (`P8-04`):** All posting operations execute in an all-or-nothing atomic transaction across storage ports.
- **Fail-Fast Bootstrap (`P8-05`):** Engine builder fails immediately if required capabilities lack their corresponding persistent storage stores.
- **Advisory Locks (`P8-03`):** Replaced in-memory mutexes with database-level session advisory locks for multi-instance concurrency safety.
- **StandardModule Ports SPI (`P8-01`):** Refactored standard modules to depend cleanly on `storage.Ports`.

---

### 3. Deprecations & Compatibility

- **`AccountTypeRevenue` Alias Deprecation (`P8-02`):** `types.AccountTypeRevenue` is formally marked **Deprecated** in favor of `types.AccountTypeIncome` in compliance with [ADR-0008](docs/adr/ADR-0008-account-type-revenue-alias.md). Backward compatibility is fully preserved as a type alias without requiring data migrations.
- **Root Package Facade Deprecation:** The root `accounting_engine.Engine` facade remains deprecated and will be removed in `v1.0.0`. Consumers must use `pkg/sdk.NewBuilder()`.

---

### 4. Database Migrations

PostgreSQL schema migrations have been extended from 11 to **32 sequential migrations** under `internal/adapters/postgres/migrations/`, adding support for perpetual account balances, analytical dimensions, reporting taxonomies, governance binding, public event idempotency, subledger reconciliation exceptions, and standards tracking.

All migrations follow the zero-downtime Expand/Contract pattern documented in [docs/migration_policy.md](docs/migration_policy.md).

---

### 5. Known Limitations & Boundaries

1. **Authentication & Authorization Boundary:** User authentication and commercial RBAC remain the responsibility of the host system ([ADR-0004](docs/adr/ADR-0004-auth-and-company-context.md)). The engine requires a valid, trusted `ExecutionContext`.
2. **Rate Limiting Boundary:** Transport rate limiting operates in-memory per service instance. Horizontally scaled clusters should deploy an external API gateway.
3. **Standards Scope Limits:**
   - IFRS 9 Hedge Accounting is explicitly deferred to future releases.
   - IAS 21 supports functional currency transactions and revaluations; foreign subsidiary financial statement translation is deferred.
   - Local tax calculations and statutory tax returns remain external host responsibilities.
4. **Memory Storage:** Memory adapter remains strictly non-durable and intended for automated unit testing and CI.

---

### 6. Release Readiness & Go/No-Go Decision

As officially evaluated in [Release Readiness Report v0.2.0](docs/release_readiness_v0.2.0.md):

- **Embedded SDK Library:** **GO (100% Production Ready)**.
- **Standalone Service Deployment:** **GO (100% Production Ready; previous NO-GO officially lifted)**.
- **Release Gate Evaluation:** All Release Gates **G1 through G12** evaluated and passed with 100% supporting automated evidence.

---

## Version v0.2.0 (2026-11-17)

**Release Type:** Minor Feature & Operational Hardening Release  
**Official Module Path:** `github.com/osmai4603-collab/accounting_engine`  
**Governing Documents:** [Compatibility Policy](docs/compatibility_policy.md), [SDK Developer Guide](docs/sdk_developer_guide.md), [Release Readiness Report v0.2.0](docs/release_readiness_v0.2.0.md), [Disaster Recovery Runbook](docs/operations/runbooks/disaster_recovery_runbook.md).

---

### 1. Overview and Architecture

`v0.2.0` is a major advancement that completes all architectural, accounting, and operational gaps identified after `v0.1.0`. The release delivers complete accounting core workflows (Atomic Year-End Closing, Dual-Method Cash Flows), public SPI subledgers (AR/AP, Fixed Assets, Treasury), automated standards posting with deterministic idempotency for IFRS/IAS modules, operational rate limiting for standalone deployments, and comprehensive fault recovery verification.

Most critically, **the conditional NO-GO advisory on standalone production deployment has been officially lifted** following a successful end-to-end disaster recovery drill on PostgreSQL with verified accounting integrity and zero data loss (RPO = 0).

---

### 2. Key Capabilities in v0.2.0

#### A. Operational Hardening & Disaster Recovery (`P12`)
- **Automated Live Disaster Recovery Drill (`P12-01`):** Verified full physical backup (`pg_dump -Fc`) and restore (`pg_restore`) against independent PostgreSQL databases. Achieved RTO ≈ 1.49s and RPO = 0 with 100% pass on all 6 core accounting integrity checks (double-entry balance, perpetual balance reconciliation, unbroken audit hash chain, strict RLS tenant isolation, idempotency key preservation, and monotonic sequence continuity).
- **Transport Rate Limiting for Standalone Service (`P12-02`):** Token Bucket algorithm protecting HTTP (`429 Too Many Requests`) and gRPC (`RESOURCE_EXHAUSTED`) endpoints with bounded memory usage, IP-based pre-auth shielding, and per-tenant authenticated quotas. The embedded SDK core remains 100% lightweight and free of network rate limiting constraints.
- **Performance Benchmarks & Baseline (`P12-03`):** Official benchmark suite covering draft creation, posting pipeline, concurrent posting, balance sheet generation, cash flow generation, and year-end closing across both Memory and PostgreSQL adapters.
- **Event Ingestion Fault Recovery (`P12-04`):** Verified effectively-once processing via durable idempotency, graceful transient error retries, deterministic failure logging, and crash resumption of pending `started` events.

#### B. Automated Standards Posting Pipeline (`P11`)
- **Automated Posting via Application Layer (`P11-05`):** Unified `StandardPostingCapability` in `pkg/spi/standards` orchestrating automated journal entry generation for:
  - **IFRS 15:** Performance obligation satisfaction, variable consideration adjustments, and contract modifications.
  - **IFRS 9:** Amortized cost classification, EIR interest accruals, and stage-based ECL provisions.
  - **IFRS 16:** Right-of-Use amortization, lease liabilities, short-term/low-value exemptions, and lessor accounting.
  - **IAS 12 / IFRIC 23:** Deferred tax computations, tax rate reconciliation disclosures, and uncertain tax treatments.
- **Chart of Accounts Validation:** Mandatory verification of target accounts via `MasterDataReader` prior to draft generation.
- **Deterministic Idempotency:** SHA-1 payload hashing guaranteeing replay safety across all standard transactions.

#### C. Public Subledgers SPI (`P10`)
- **Accounts Receivable & Payable (AR/AP) (`P10-01`):** Open-item tracking, aging analysis, and settlement matching exposed via public SPI with strict separation of commercial workflows from ledger moves.
- **Fixed Assets (`P10-02`):** Asset register, straight-line and accelerated depreciation schedules, and disposal accounting.
- **Treasury & Bank Reconciliation (`P10-03`):** Statement parsing, bank transaction matching, uncleared items tracking, and discrepancy resolution.
- **Foreign Exchange Accounting (`P10-04`):** Transaction currency handling, spot revaluation, and realized/unrealized FX gain/loss recognition under IAS 21.

#### D. Core Accounting Lifecycle (`P9`)
- **Atomic Year-End Closing (`P9-01`):** Single Unit of Work (Single UoW) closing nominal accounts, transferring net income to retained earnings, creating new fiscal year opening entries, and hard-closing the fiscal period.
- **Cash Flow Statement Dual-Method (`P9-02`):** Direct and Indirect cash flow calculation according to IAS 7 with presentation alignment under IFRS 18.
- **Adjusting Entries (`P9-03`) & Period Lifecycle (`P9-04`):** Explicit accrual/deferral adjusting entries and audited reopening controls.
- **Structured Disclosures (`P9-05`):** Multi-version disclosure notes linked to financial statement nodes.

#### E. Architectural Integrity (`P8`)
- **Single Unit of Work Atomic Posting (`P8-04`):** All posting operations execute in an all-or-nothing atomic transaction across storage ports.
- **Fail-Fast Bootstrap (`P8-05`):** Engine builder fails immediately if required capabilities lack their corresponding persistent storage stores.
- **Advisory Locks (`P8-03`):** Replaced in-memory mutexes with database-level session advisory locks for multi-instance concurrency safety.
- **StandardModule Ports SPI (`P8-01`):** Refactored standard modules to depend cleanly on `storage.Ports`.

---

### 3. Deprecations & Compatibility

- **`AccountTypeRevenue` Alias Deprecation (`P8-02`):** `types.AccountTypeRevenue` is formally marked **Deprecated** in favor of `types.AccountTypeIncome` in compliance with [ADR-0008](docs/adr/ADR-0008-account-type-revenue-alias.md). Backward compatibility is fully preserved as a type alias without requiring data migrations.
- **Root Package Facade Deprecation:** The root `accounting_engine.Engine` facade remains deprecated and will be removed in `v1.0.0`. Consumers must use `pkg/sdk.NewBuilder()`.

---

### 4. Database Migrations

PostgreSQL schema migrations have been extended from 11 to **32 sequential migrations** under `internal/adapters/postgres/migrations/`, adding support for perpetual account balances, analytical dimensions, reporting taxonomies, governance binding, public event idempotency, subledger reconciliation exceptions, and standards tracking.

All migrations follow the zero-downtime Expand/Contract pattern documented in [docs/migration_policy.md](docs/migration_policy.md).

---

### 5. Known Limitations & Boundaries

1. **Authentication & Authorization Boundary:** User authentication and commercial RBAC remain the responsibility of the host system ([ADR-0004](docs/adr/ADR-0004-auth-and-company-context.md)). The engine requires a valid, trusted `ExecutionContext`.
2. **Rate Limiting Boundary:** Transport rate limiting operates in-memory per service instance. Horizontally scaled clusters should deploy an external API gateway.
3. **Standards Scope Limits:**
   - IFRS 9 Hedge Accounting is explicitly deferred to future releases.
   - IAS 21 supports functional currency transactions and revaluations; foreign subsidiary financial statement translation is deferred.
   - Local tax calculations and statutory tax returns remain external host responsibilities.
4. **Memory Storage:** Memory adapter remains strictly non-durable and intended for automated unit testing and CI.

---

### 6. Release Readiness & Go/No-Go Decision

As officially evaluated in [Release Readiness Report v0.2.0](docs/release_readiness_v0.2.0.md):

- **Embedded SDK Library:** **GO (100% Production Ready)**.
- **Standalone Service Deployment:** **GO (100% Production Ready; previous NO-GO officially lifted)**.
- **Release Gate Evaluation:** All Release Gates **G1 through G12** evaluated and passed with 100% supporting automated evidence.

---

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
