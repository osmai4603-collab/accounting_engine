# ARD-P5-09: Optional Reporting, Reconciliation, and Consolidation Capabilities

## Status

منفذة (Implemented & Verified). Reporting, Reconciliation, and Consolidation capabilities are optional, declaratively discoverable via SDK Standards Diagnostics, and provide full source and comparison contracts.

## Decision

- Reporting declares per-report ledger-read and account-taxonomy dependencies,
  and requires a rate provider when source and presentation currencies differ.
  Missing repositories, empty/unusable account taxonomies, and absent rates
  fail explicitly instead of producing successful empty output.
- Reconciliation capability activation requires two distinct sources with a
  common currency and reporting window, explicit match keys, a nonnegative
  tolerance, and an exception-treatment policy. A concrete two-source read
  and comparison contract (`ComparisonSource`, `CompareSources`) performs
  deterministic cross-source record matching, tolerance evaluation, and exception tracking.
- Consolidation validates complete trial balances, mappings, functional and
  reporting currencies, exchange rates, ownership links, and explicit
  accounting/control/translation/elimination/intercompany policy identifiers.
  Every subsidiary relationship requires a stated control basis and evidence
  reference. Elimination and intercompany scopes must explicitly agree with
  their configured rules, including an explicit "none" when they do not apply.
- Shared capability identifiers `CapReconciliation` and `CapConsolidation` are registered
  in `pkg/spi/standards`. The SDK Builder discovers and tracks active capabilities and
  publishes explicit disabled reasons via `engine.Standards().Diagnostics()`.
- Core remains completely usable without these internal capabilities; their constructors
  and operations return dependency errors when required sources or declarations
  are absent, while core posting and ledger operations run unaffected.

## Verification

1. `internal/reconciliation/capability_test.go:TestReconciliationSourceComparison` verifies the concrete two-source matching engine across date windows, tolerance thresholds, and exception treatments.
2. `pkg/sdk/phase5_p02_p03_test.go:TestP5_09_OptionalCapabilitiesDiscoveryAndDiagnostics` verifies that Core runs alone without optional capabilities, and that SDK diagnostics accurately publishes disabled reasons for Reporting, Reconciliation, and Consolidation.
3. `internal/consolidation/engine_test.go` verifies multi-entity validation, ownership control evidence, and policy consistency.
