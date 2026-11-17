# Accounting Engine Release Notes

## Version v0.2.0 (2026-11-17)

**Release Type:** Minor Feature & Operational Hardening Release
**Official Module Path:** `github.com/osmai4603-collab/accounting_engine`
**Governing Documents:** [Compatibility Policy](docs/compatibility_policy.md), [SDK Developer Guide](docs/sdk_developer_guide.md), [Release Readiness Report v0.2.0](docs/release_readiness_v0.2.0.md), [Disaster Recovery Runbook](docs/operations/runbooks/disaster_recovery_runbook.md)

---

### 1. Overview

`v0.2.0` completes the remaining architectural, accounting, and operational gaps identified after `v0.1.0`. The release delivers:

- full atomic year-end closing and dual-method cash flow support;
- public SPI subledgers for AR/AP, Fixed Assets, and Treasury;
- automated standards posting for IFRS 15/9/16 and IAS 12 with deterministic idempotency;
- standalone service rate limiting and operational hardening;
- live disaster recovery validation with the previous NO-GO condition formally cleared.

The prior standalone-production advisory was lifted after a successful end-to-end PostgreSQL backup/restore drill, verified accounting integrity checks, and zero data loss (RPO = 0).

---

### 2. Highlights

#### A. Operational Hardening & Disaster Recovery (`P12`)

- Live disaster recovery drill (`P12-01`) validated `pg_dump -Fc` and `pg_restore` with RTO ≈ 1.49s and RPO = 0.
- Standalone service rate limiting (`P12-02`) protects HTTP (`429 Too Many Requests`) and gRPC (`RESOURCE_EXHAUSTED`) endpoints.
- Performance baselines (`P12-03`) were captured for core accounting workflows across Memory and PostgreSQL adapters.
- Event ingestion fault recovery (`P12-04`) was verified with idempotent replay and crash recovery.

#### B. Automated Standards Posting (`P11`)

- Standard posting via the application layer is available for IFRS 15, IFRS 9, IFRS 16 and IAS 12.
- Account validation using `MasterDataReader` prevents invalid journal drafts.
- Deterministic idempotency protects replay-safe execution.

#### C. Public Subledgers SPI (`P10`)

- AR/AP, Fixed Assets, and Treasury packages are exposed through public SPI contracts.
- Commercial workflows remain separated from ledger accounting rules.

#### D. Core Accounting Lifecycle (`P9`)

- Atomic year-end closing and reopening controls are available as a single-unit-of-work workflow.
- Cash-flow statement generation supports both direct and indirect methods aligned to IAS 7 and IFRS 18.

#### E. Architectural Integrity (`P8`)

- Atomic posting, fail-fast bootstrap, advisory locking, and public standards ports complete the `P8` line.

---

### 3. Deprecations & Compatibility

- `types.AccountTypeRevenue` is deprecated in favor of `types.AccountTypeIncome` with a compatible alias retained.
- The legacy root facade remains deprecated and must be replaced by `pkg/sdk.NewBuilder()` in consumer code.

---

### 4. Database & Operational Standards

PostgreSQL migrations continue the zero-downtime Expand/Contract model documented in [docs/migration_policy.md](docs/migration_policy.md). Operational policies and runbooks are maintained under [docs/operations](docs/operations) and [docs/runbooks](docs/runbooks).

---

### 5. Known Limits & Boundaries

1. Authentication, user directory, and commercial RBAC remain host responsibilities.
2. In-memory transport rate limiting is per-process and should be backed by an external gateway in horizontally scaled deployments.
3. Standards remain bounded automation layers rather than universal certification; detailed scope boundaries are documented in the standards and compliance references.
4. The memory adapter remains non-durable and is intended for testing and CI usage.

---

### 6. Release Readiness & Go/No-Go Decision

As evaluated in [docs/release_readiness_v0.2.0.md](docs/release_readiness_v0.2.0.md):

- **Embedded SDK Library:** GO (100% production ready)
- **Standalone Service Deployment:** GO (100% production ready; previous NO-GO condition lifted)
- **Release Gate Evaluation:** All G1–G12 gates passed with supporting automated evidence

This release is therefore the supported production baseline for the current `v0.2.0` documentation set.
