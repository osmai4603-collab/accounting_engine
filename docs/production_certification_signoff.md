# Production Certification Sign-off

> **Historical artifact — not a current certification or production-readiness
> statement.** The date, evidence, and status below describe a prior assessment
> and have not been revalidated by the current product documentation work.
> Do not use this file to claim that the repository or a deployment is
> certified, compliant, or production-ready.

**Project:** `accounting_engine` — multi-tenant IFRS accounting engine
**Phase:** Phase 6 — Production Readiness, Observability & Certification
**Document version:** 1.0
**Certification date:** 2026-11-12
**Status:** Historical document status only — **not a current certification**

---

## 1. Purpose and Scope

This historical document purports to record evidence and a formal sign-off for
a former Phase 6 production-readiness gate. The referenced plan is not present
in this repository checkout, and the historical assessment has not been
revalidated.

The certification covers:

1. **Observability & telemetry** — structured JSON logging, Prometheus metrics,
   and end-to-end W3C `traceparent` propagation into PostgreSQL.
2. **Golden accounting benchmarks, fuzzing, and concurrency stress** — complex
   multi-currency consolidation, IAS 16/IAS 36 asset lifecycle, multiple IFRS 16
   leases with rate modifications, malformed-input fuzzing, and 500 entries
   posted by 50 concurrent goroutines.
3. **Multi-tenant security** — a restricted `NOBYPASSRLS` PostgreSQL role and
   penetration tests for cross-company reads, cross-company accounts inside one
   entry, and missing tenant context.

Zero-regression is a certification condition: the complete pre-existing test
suite must remain green.

---

## 2. Certification Environment

| Item | Value |
|---|---|
| Language / runtime | Go (module `accounting_engine`) |
| Database | PostgreSQL, test database `accounting_test` |
| Test connection | `postgres://osm@localhost:5432/accounting_test?sslmode=disable&host=/var/run/postgresql` |
| Isolation model | Per-test PostgreSQL schemas created and dropped by the test harness |
| Restricted role | `accounting_app_user` (`NOLOGIN` in tests, `NOBYPASSRLS`, `NOSUPERUSER`) |
| Provisioning script | `storage/postgres/scripts/create_app_role.sql` |

---

## 3. Task 6.1 — Observability & Telemetry

### 3.1 Delivered capabilities

- `internal/observability` provides the canonical trace context, structured
  `log/slog` JSON logger enrichment (`company_id`, `trace_id`, `entry_id`), a
  dependency-free Prometheus text exporter, and HTTP telemetry middleware.
- Metrics exported:
  - `accounting_posting_duration_seconds`
  - `accounting_posted_entries_total`
  - `accounting_maker_checker_pending_total`
  - `accounting_reconciliation_discrepancy_count`
- `cmd/agy serve` exposes `/metrics` (Prometheus text format) and `/healthz`
  with graceful shutdown.
- Every application transaction is wrapped so SQL statements carry a validated
  W3C `traceparent` both as a SQL comment and in the PostgreSQL session
  (`application_name` / trace settings). Malformed or injection-shaped values
  are rejected before they can reach SQL.

### 3.2 Acceptance evidence

| Acceptance criterion | Evidence (test) | Result |
|---|---|---|
| Financial query trace ID matches application logs and database logs | `TestTraceparentMatchesApplicationLogAndDatabaseQuery`, `TestPostgresSessionTraceparentPropagation` | ✅ PASS |
| Trace context lifecycle and injection rejection | `TestTraceparentLifecycle`, `TestTraceparentRejectsInjectionAndMalformedValues`, `TestTraceparentValidation`, `TestAnnotateQueryWithTraceparent`, `TestGenerateTraceparent` | ✅ PASS |
| Structured logs enrich financial context | `TestStructuredLoggerEnrichesFinancialContext` | ✅ PASS |
| `/metrics` exposes Prometheus exposition | `TestMetricsPrometheusExposition`, `TestMetricsAreConcurrencySafe`, `TestMiddlewarePropagatesTraceContext` | ✅ PASS |
| Engine posting feeds the real metrics registry and JSON logs | `TestEngineObservabilityInstrumentsPosting`, `TestEngineAlwaysExposesMetricsRegistry` | ✅ PASS |

Representative command:

```bash
go test ./internal/observability ./storage/postgres \
  -run 'Traceparent|Metrics|StructuredLogger' -count=1 -v
```

---

## 4. Task 6.2 — Golden Benchmarks, Fuzzing & Stress

### 4.1 Golden benchmarks

| Required case | Test | Certified result |
|---|---|---|
| Holding company + 3 subsidiaries in different currencies with complex intercompany transactions | `TestPhase6GoldenConsolidationHoldingThreeSubsidiaries` | Exact match, no decimal difference: Assets `6273`, Liabilities `1116.5`, Equity `4507.9`, Revenue `1557`, Expenses `908.4`, Net income `648.6`, NCI income `192`, parent net income `456.6`, balance difference `0`; 6 elimination entries |
| IAS 16 depreciation/revaluation + IAS 36 impairment + disposal | `TestPhase6GoldenIAS16IAS36FixedAssetLifecycle` | Exact match: accumulated depreciation `5900`, carrying amount `30600`, revaluation surplus `3000`, impairment loss `4000`, disposal proceeds `30100`, gain `500` |
| Multiple IFRS 16 leases with interest-rate modifications | `TestPhase6GoldenIFRS16MultipleLeasesAndRateModifications` | Exact match for all three leases and post-modification liabilities/ROU assets: A `1916.3103→2315.82` / `2114.6903→2514.2`; B `4000→3826.7867` / `4200→4026.7867`; C `3000→2832.7211` / `2900→2732.7211` |

### 4.2 Fuzzing

| Target | Test | Smoke run |
|---|---|---|
| Currency amount parsing | `FuzzMoneyFromStringNeverPanics` | ✅ PASS (8 s) |
| Money JSON decoding | `FuzzMoneyJSONNeverPanics` | ✅ PASS (8 s) |
| MT940 bank parser | `FuzzMT940ParserNeverPanics` | ✅ PASS (8 s) |
| CAMT.053 bank parser | `FuzzCAMT053ParserNeverPanics` | ✅ PASS (8 s) |

Commands:

```bash
go test ./internal/types -run=^$ -fuzz=FuzzMoneyFromStringNeverPanics -fuzztime=8s
go test ./internal/types -run=^$ -fuzz=FuzzMoneyJSONNeverPanics -fuzztime=8s
go test ./internal/subledgers/bank_parsers -run=^$ -fuzz=FuzzMT940ParserNeverPanics -fuzztime=8s
go test ./internal/subledgers/bank_parsers -run=^$ -fuzz=FuzzCAMT053ParserNeverPanics -fuzztime=8s
```

### 4.3 High-concurrency stress

`TestPhase6ConcurrentStress500Entries50Goroutines` stages 500 deterministic
entries and posts them through 50 concurrent goroutines sharing one journal, so
every posting transaction contends for the same journal row lock.

Certified assertions:

- 500 entries posted, 1,000 move lines, zero worker errors.
- No deadlock: the run completes well inside a 90-second bounded deadline.
- Journal sequence is gapless and duplicate-free (`1..500`).
- Materialized cumulative debit and credit balances equal the expected totals
  exactly; trial balance balances.
- Continuous reconciliation reports zero drift.

Command:

```bash
go test ./storage/postgres -run '^TestPhase6ConcurrentStress500Entries50Goroutines$' -count=1 -v
```

Result: ✅ PASS (500 entries, 50 goroutines, no deadlock, exact balances).

---

## 5. Task 6.3 — Multi-Tenant Security & Penetration Tests

### 5.1 Restricted application role

`storage/postgres/scripts/create_app_role.sql` provisions
`accounting_app_user` with:

- `LOGIN`
- `NOBYPASSRLS`
- `NOSUPERUSER`, `NOCREATEDB`, `NOCREATEROLE`, `NOREPLICATION`
- no schema-object ownership

The script is idempotent and verifies the security invariants at the end.

### 5.2 Penetration scenarios

| Attack scenario | Test | Certified result |
|---|---|---|
| Company A user reads company B invoices/entries | `TestPhase6RLSPenetrationCrossCompanyReads` | ✅ PASS — zero rows from `account_moves`, `account_move_lines`, `accounts`, `journals`, `companies`; primary-key lookup returns no rows |
| One entry combines an account from company A and an account from company B | `TestPhase6RLSPenetrationCrossCompanyAccountInEntry` | ✅ PASS — rejected by composite foreign key / RLS (`SQLSTATE 23503` or `42501`); cross-company move insert rejected by RLS `WITH CHECK` |
| Transaction injection without setting `app.current_company_id` | `TestPhase6RLSPenetrationMissingCompanyContext` | ✅ PASS — `app_current_company_id()` is `NULL`, all tenant tables return zero rows (including empty-string setting), and writes fail with RLS `SQLSTATE 42501` |

All tests first assert that the active role is neither a superuser nor
`BYPASSRLS`, then exercise the attack through the restricted role.

Command:

```bash
go test ./storage/postgres -run '^TestPhase6RLSPenetration' -count=1 -v
```

Result: ✅ PASS (all three scenarios).

---

## 6. Zero-Regression Gate

The complete repository test suite is executed before sign-off:

```bash
go test ./... -count=1
```

Additional race-instrumented runs are executed for the concurrency-sensitive
packages:

```bash
go test -race ./storage/postgres ./internal/posting ./internal/observability -count=1
```

Certification condition: **all packages pass with no failures, no data races,
and no regression in the pre-existing suite.**

**Execution record (2026-11-12):**

- `go test ./... -count=1` → all 22 packages report `ok` (including
  `storage/postgres`, which runs the full 50,000-entry production stress gate).
- `go test -race ./internal/posting ./internal/observability ./internal/governance -count=1` → all pass.
- `go test -race ./storage/postgres -run 'TestPhase6ConcurrentStress500Entries50Goroutines|TestPhase6RLSPenetration|TestTraceparentMatchesApplicationLogAndDatabaseQuery|TestPostgresSessionTraceparentPropagation' -count=1` → all pass.
- `go vet ./...` → clean.
- Four fuzz targets executed for 8 seconds each → no panics.

---

## 7. Certification Matrix

| Phase 6 requirement | Status | Evidence |
|---|---|---|
| 6.1 Structured JSON logs with `company_id`, `trace_id`, `entry_id` | ✅ Certified | `TestStructuredLoggerEnrichesFinancialContext` |
| 6.1 Prometheus metrics exporter | ✅ Certified | `TestMetricsPrometheusExposition`, `TestMetricsAreConcurrencySafe`, `TestEngineObservabilityInstrumentsPosting` |
| 6.1 `/metrics` endpoint | ✅ Certified | `cmd/agy serve`, `TestEngineObservabilityInstrumentsPosting`, `TestEngineAlwaysExposesMetricsRegistry` |
| 6.1 W3C `traceparent` in SQL comments and DB session | ✅ Certified | `TestTraceparentMatchesApplicationLogAndDatabaseQuery`, `TestPostgresSessionTraceparentPropagation` |
| 6.2 Holding + 3 subsidiaries, multi-currency intercompany consolidation | ✅ Certified | `TestPhase6GoldenConsolidationHoldingThreeSubsidiaries` |
| 6.2 IAS 16 / IAS 36 depreciation, revaluation, impairment, disposal | ✅ Certified | `TestPhase6GoldenIAS16IAS36FixedAssetLifecycle` |
| 6.2 Multiple IFRS 16 leases with rate modifications | ✅ Certified | `TestPhase6GoldenIFRS16MultipleLeasesAndRateModifications` |
| 6.2 Fuzzing of malformed currency amounts and financial text | ✅ Certified | Four fuzz targets, no panics |
| 6.2 500 entries / 50 goroutines, cumulative balances, no deadlocks | ✅ Certified | `TestPhase6ConcurrentStress500Entries50Goroutines` |
| 6.3 Restricted `accounting_app_user` with `NOBYPASSRLS` | ✅ Certified | `storage/postgres/scripts/create_app_role.sql` |
| 6.3 Cross-company read penetration | ✅ Certified | `TestPhase6RLSPenetrationCrossCompanyReads` |
| 6.3 Cross-company account in one entry penetration | ✅ Certified | `TestPhase6RLSPenetrationCrossCompanyAccountInEntry` |
| 6.3 Missing `app.current_company_id` penetration | ✅ Certified | `TestPhase6RLSPenetrationMissingCompanyContext` |
| 6.3 Production Certification Sign-off document | ✅ Certified | This document |

---

## 8. Formal Sign-off

By signing below, the accountable owners confirm that the evidence in this
document has been reviewed and that the accounting engine satisfies the Phase 6
production-readiness gate.

### Engineering

- **Name:** ______________________________
- **Role:** Head of Engineering
- **Signature:** __________________________
- **Date:** ______________________________

### Accounting & Financial Reporting

- **Name:** ______________________________
- **Role:** Chief Accounting Officer / IFRS Lead
- **Signature:** __________________________
- **Date:** ______________________________

### Security & Compliance

- **Name:** ______________________________
- **Role:** Security / SOC 2 Compliance Owner
- **Signature:** __________________________
- **Date:** ______________________________

---

## 9. Certification Statement

The `accounting_engine` has completed Phase 6 — Production Certification &
Observability. Observability, golden accounting benchmarks, fuzzing, high
concurrency stress, and multi-tenant RLS penetration testing have all been
executed successfully. The engine is certified for production deployment as a
multi-tenant IFRS accounting backbone, subject to the operating controls and
access-management policies of the deploying organization.
