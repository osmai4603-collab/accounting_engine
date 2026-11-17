# Accounting Engine documentation

This index points to normative documentation, architectural blueprints, integration contracts,
accounting references, and operational procedures in this repository.

> [!NOTE]
> **Current Version:** `v0.2.0` (Production Ready — Embedded SDK & Hardened Standalone Service).  
> All P0–P12 developmental and operational gates (G1–G12) have been verified with 100% compliance.

---

## Start here: product and integration

| Document | Use it for |
| --- | --- |
| [Product boundary](./product_boundary.md) | What belongs to the engine, host application, and industry adapters |
| [SDK developer guide](./sdk_developer_guide.md) | Module path, SDK/SPI choices, bootstrap, external consumer journey, adapter contracts, and limitations |
| [Public API inventory](./public_api_inventory.md) | Supported import paths, public API/SPI packages (`pkg/api/v1`, `pkg/spi/*`), and compatibility boundaries |
| [Public contracts design](./public_contracts_design.md) | Contract design background, subledgers SPI, and package interfaces |
| [API v1 snapshot](./api/v1.snapshot) | Generated public API compatibility baseline |
| [Error taxonomy](./error_taxonomy.md) | Public application errors, categories, HTTP/gRPC codes (including Rate Limiting), and handling guidance |
| [Compatibility policy](./compatibility_policy.md) | SemVer rules, API/SPI stability, deprecation lifecycle, and storage compatibility |

The Go module import path is `github.com/osmai4603-collab/accounting_engine`.
The recommended embedding entry point is `pkg/sdk`; use only public packages
under `pkg/api/v1` and `pkg/spi` when implementing integrations. Do not import
`internal/**` or make consumers depend on engine SQL or internal table schemas.

---

## Architecture and system design

| Document | Use it for |
| --- | --- |
| [Architecture diagrams catalog](./architecture/diagrams/README.md) | 30 comprehensive architectural diagrams (C4 structural, behavioral sequences, data ERDs, and operational) |
| [Subledgers architecture](./architecture/subledgers_architecture.md) | Sub-ledgers (AR, AP, Fixed Assets, Treasury) SPI contracts and General Ledger integration |
| [Unit of work and transactions](./architecture/unit_of_work_and_transactions.md) | Atomic posting boundaries, isolation levels, and retry semantics |
| [Bootstrap capability matrix](./architecture/bootstrap_capability_matrix.md) | Required dependencies and fail-fast rules for `WithRequiredCapabilities` |
| [Current dependency map](./architecture/current_dependency_map.md) | Layering rules and package dependency graph |
| [Financial accounting system architecture](./financial_accounting_system_architecture.md) | High-level engine boundary, monetary measurement, and ledger segregation |
| [Engineering methodology](./methodology/README.md) | Core software engine principles, metrics, and quality standards |

---

## Accounting scope and standards

| Document | Use it for |
| --- | --- |
| [Entry validation invariants](./accounting/entry_validation_invariants.md) | Journal-entry mathematical and validation rules |
| [Reversal specification](./accounting/reversal_specification.md) | Reversal mechanics, audit references, and constraints |
| [Closing and reopening policy](./accounting/closing_and_reopening_policy.md) | Atomic fiscal-period year-end close and reopening workflow |
| [Financial reporting scope](./accounting/financial_reporting_scope.md) | Balance Sheet, Income Statement, and Cash Flow (direct/indirect) scope |
| [Golden accounting scenarios](./golden_accounting_scenarios.md) | Approved accounting ground truth and benchmark test scenarios |
| [Accounting standards specifications](./standards/README.md) | Detailed specs for IFRS, IFRS 18, AAOIFI, and US GAAP |
| [IFRS 9 compliance matrix](./accounting/ifrs9_compliance_matrix.md) | Financial instruments classification and impairment rules |
| [IFRS 15 compliance matrix](./accounting/ifrs15_compliance_matrix.md) | Revenue from contracts with customers and recognition schedules |
| [IFRS 16 compliance matrix](./accounting/ifrs16_compliance_matrix.md) | Leases, right-of-use assets, and liability amortization |
| [IAS 21 compliance matrix & scope](./accounting/ias21_compliance_matrix.md) | Foreign currency translation, functional currency, and FX differences |
| [Standards references](./standards/references/) | Encyclopedic references for global financial reporting standards |

---

## Operations, testing and release references

| Document | Use it for |
| --- | --- |
| [Release readiness report v0.2.0](./release_readiness_v0.2.0.md) | Current production release readiness report, G1–G12 gate verifications, and GO decision |
| [Performance baseline v0.2.0](./performance/baseline_v0.2.0.md) | Official latency, throughput, and memory benchmarks across Memory and PostgreSQL |
| [Contract parity report](./reports/contract_parity_report.md) | Semantic parity verification between Memory, PostgreSQL, and SPI storage adapters |
| [Continuous integration (CI) gates](./ci.md) | CI workflow, static analysis, race detectors, and automated quality gates |
| [Migration policy](./migration_policy.md) | PostgreSQL zero-downtime expand/contract schema migration rules |
| [Backup and recovery policy](./operations/backup_and_recovery_policy.md) | PostgreSQL backup strategies, retention, and RTO/RPO targets |
| [Disaster recovery runbook](./runbooks/disaster_recovery_runbook.md) | End-to-end restore procedure and automated 6-step accounting integrity checks |
| [Database restore runbook](./runbooks/runbook_database_restore.md) | Database restore steps and pre-traffic validation |
| [Database unavailable runbook](./runbooks/runbook_db_unavailable.md) | Database connectivity loss response |
| [Migration failure runbook](./runbooks/runbook_migration_failure.md) | Migration failure rollback procedure |
| [Deadlock spike runbook](./runbooks/runbook_deadlock_spike.md) | Deadlock and concurrency contention investigation |
| [RLS incident runbook](./runbooks/runbook_rls_incident.md) | Row-level security and multi-tenancy breach containment |

---

## Execution plans and decision records

| Document | Use it for |
| --- | --- |
| [Execution plan overview (P8–P12)](./execution_plan_overview.md) | Supplemental execution plan that delivered v0.2.0 hardening and completed all 24 tasks |
| [Core implementation plan (P0–P7)](./accounting_engine_impl_plan.md) | Foundation productization plan that delivered v0.1.0 |
| [Architecture decision records (ADRs)](./adr/) | Formal architecture decisions (ADR-0001 through ADR-0008) |
| [Architecture record decisions (ARDs)](./ard/) | Historical audit trail of task completions (67 task completion records) |

---

## Historical or non-normative material

The following documents contain older assessments, superseded proposals, or obsolete prototypes and must not be used as current readiness, certification, or compliance evidence:

- [Release readiness report P7-05 (Superseded NO-GO)](./archive/reports/release_readiness_p7-05.md)
- [Production readiness specification](./archive/production_readiness_specification_and_verification.md)
- [Production certification sign-off](./archive/production_certification_signoff.md)
- [Gap analysis and standards verification report](./archive/gap_analysis_and_standards_verification_report.md)
- [Golden accounting benchmark draft](./archive/golden_accounting_benchmark.md)
- [IFRS automation CLI draft](./archive/ifrs_automation.md)
- [Root facade migration guide](./archive/root-facade.md)
- [Archive directory index](./archive/README.md)
