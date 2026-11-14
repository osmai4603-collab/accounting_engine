# ARD-P5-02: Capability Preflight and Feature Dependency Validation

## Status

Implemented.

## Context and Problem

Previously, system services (such as Reporting and Governance) could be built with missing or `nil` stores, which resulted in runtime nil-pointer dereferences or silent failure. Additionally, module dependency preflight validation did not accumulate missing requirements across all modules at once.

## Decision

1. **Predefined Capability Identifiers**:
   - Defined standard capabilities in `pkg/spi/standards`: `CapLedgerPosting`, `CapMasterData`, `CapReporting`, `CapGovernance`, `CapAuditTrail`, `CapApproval`, `CapCurrencyTranslation`, `CapScheduledJobs`, and standard module capabilities (`revenue_recognition`, `deferred_tax`, `lease_accounting`, `financial_instruments`).

2. **Preflight Validation for Services**:
   - `EngineBuilder` preflight checks Reporting dependencies (requires `BalanceReader`, `MasterDataReader`, `EntryReader` or `ReportingStore`). If missing, `CapReporting` is marked inactive and recorded in `EngineDiagnostics.DisabledReasons`.
   - `EngineBuilder` preflight checks Governance dependencies (requires `GovernanceStore` or `AuditWriter`). If missing, `CapGovernance` is marked inactive and recorded in `EngineDiagnostics.DisabledReasons`.
   - Disabled services return `types.ErrCapabilityNotImplemented` cleanly instead of causing runtime panics or returning `nil` interfaces as active capabilities.

3. **Accumulated Dependency Validation**:
   - Preflight validation collects all missing module requirements and capability issues across all modules into a combined error before initialization.

## Verification

- `TestP5_02_Preflight_ReportingAndGovernanceDisabledReasons` proves that missing stores report disabled reasons in `EngineDiagnostics` and return `ErrCapabilityNotImplemented`.
- `TestP5_02_Preflight_ActiveCapabilities` proves active registration of `CapGovernance` and `CapAuditTrail` when configured.
