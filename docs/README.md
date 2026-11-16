# Accounting Engine documentation

This index points to documentation that exists in this repository. It distinguishes
current integration contracts from design history, accounting references, and
operational procedures. Documentation is not evidence of production readiness,
regulatory approval, or compliance by itself.

## Start here: product and integration

| Document | Use it for |
| --- | --- |
| [Product boundary](./product_boundary.md) | What belongs to the engine, host application, and industry adapters |
| [SDK developer guide](./sdk_developer_guide.md) | Module path, SDK/SPI choices, bootstrap, external consumer journey, adapter contracts, and limitations |
| [Public API inventory](./public_api_inventory.md) | Supported import paths, public API/SPI packages, and compatibility boundaries |
| [Public contracts design](./public_contracts_design.md) | Contract design background; read the current SDK guide and package interfaces for live behavior |
| [External-consumer tests](../test/external_consumer/) | Executable examples for posting, industry integration, and a storage adapter implemented outside the engine |
| [API v1 snapshot](./api/v1.snapshot) | Generated public API compatibility baseline |
| [Error taxonomy](./error_taxonomy.md) | Public application errors, categories, and error handling guidance |

The Go module import path is `github.com/osmai4603-collab/accounting_engine`.
The recommended embedding entry point is `pkg/sdk`; use only public packages
under `pkg/api/v1` and `pkg/spi` when implementing integrations. Do not import
`internal/**` or make consumers depend on engine SQL or table names.

## Accounting scope and architecture

| Document | Use it for |
| --- | --- |
| [Accounting architecture](./financial_accounting_system_architecture.md) | High-level engine and adapter architecture |
| [Subledgers architecture](./architecture/subledgers_architecture.md) | Sub-ledgers (AR, AP, FA, Treasury) contracts and GL integration |
| [Entry validation invariants](./accounting/entry_validation_invariants.md) | Journal-entry validation rules |
| [Reversal specification](./accounting/reversal_specification.md) | Reversal behavior and constraints |
| [Closing and reopening policy](./accounting/closing_and_reopening_policy.md) | Fiscal-period close/reopen policy |
| [Financial reporting scope](./accounting/financial_reporting_scope.md) | Reporting capabilities and scope |
| [Golden accounting scenarios](./golden_accounting_scenarios.md) | Approved accounting ground truth and benchmark scenarios |
| [Standards automation](./ifrs_automation.md) | Current standards-module design and automation context |
| [Standards types and frameworks](./accounting_standards_types_and_frameworks.md) | Background reference on accounting frameworks |
| [Accounting standards specs](./standards/README.md) | Specifications for IFRS, IFRS 18, AAOIFI, and US GAAP |
| [Engineering methodology](./methodology/README.md) | Core software engine principles, metrics, and methodology |

Standards and industry integrations are bounded capabilities; neither the
presence of a package nor a passing test should be read as a claim of full
compliance with an accounting standard or sector-specific requirements.

## Operations and release references

These are operational references, not deployment guarantees. Follow the
applicable runbook and validate it against the target deployment:

| Document | Use it for |
| --- | --- |
| [Compatibility policy](./compatibility_policy.md) | SemVer rules, API/SPI stability, deprecation lifecycle, and storage compatibility |
| [Release notes v0.1.0](../RELEASE_NOTES.md) | Release notes for version v0.1.0 (Developer Preview / Embedded SDK) |
| [Release archive v0.1.0](./reports/release_archive_v0.1.0.md) | Verification logs, quality gates, and SBOM details for v0.1.0 |
| [P7-06 decision record](./ard/ARD-P7-06.md) | Task P7-06 completion evidence, SemVer selection, and packaging decisions |
| [Current P7-05 release readiness report](./reports/release_readiness_p7-05.md) | Current weighted evidence score, mandatory release gates, known limitations, and No-Go decision |
| [P7-05 decision record](./ard/ARD-P7-05.md) | Task acceptance evidence and outstanding release blockers |
| [CI notes](./ci.md) | Repository validation workflow |
| [Migration policy](./migration_policy.md) | Migration compatibility and change policy |
| [Backup and recovery policy](./operations/backup_and_recovery_policy.md) | Backup and recovery concepts |
| [Database restore runbook](./runbooks/runbook_database_restore.md) | Restore procedure |
| [Database unavailable runbook](./runbooks/runbook_db_unavailable.md) | Database availability response |
| [Migration failure runbook](./runbooks/runbook_migration_failure.md) | Migration failure response |
| [Deadlock spike runbook](./runbooks/runbook_deadlock_spike.md) | Deadlock investigation |
| [RLS incident runbook](./runbooks/runbook_rls_incident.md) | Tenant-isolation incident response |

The standalone service and its health/readiness endpoints are deployment-mode
specific. The embedded SDK guide does not prescribe a daemon command, endpoint
set, or production configuration.

## Historical or non-normative material

The following documents contain older assessments or proposals and must not be
used as current readiness, certification, or compliance evidence:

- [Production readiness specification](./archive/production_readiness_specification_and_verification.md)
- [Production certification sign-off](./archive/production_certification_signoff.md)
- [Gap analysis and standards verification report](./archive/gap_analysis_and_standards_verification_report.md)
- [Golden accounting benchmark](./archive/golden_accounting_benchmark.md)

The implementation plan is a task-tracking artifact, not a consumer contract.
