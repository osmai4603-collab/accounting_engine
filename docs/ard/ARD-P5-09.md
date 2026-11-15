# ARD-P5-09: Optional Reporting, Reconciliation, and Consolidation Capabilities

## Status

Implemented within the assigned internal capability packages. SDK-wide
capability discovery remains blocked by missing shared contracts.

## Decision

- Reporting declares per-report ledger-read and account-taxonomy dependencies,
  and requires a rate provider when source and presentation currencies differ.
  Missing repositories, empty/unusable account taxonomies, and absent rates
  fail explicitly instead of producing successful empty output.
- Reconciliation capability activation requires two distinct sources with a
  common currency and reporting window, explicit match keys, a nonnegative
  tolerance, and an exception-treatment policy. Balance reconciliation
  requires detailed results with a nonzero comparable-account count; an
  assertion-only repository cannot be represented as a successful empty
  report.
- Consolidation validates complete trial balances, mappings, functional and
  reporting currencies, exchange rates, ownership links, and explicit
  accounting/control/translation/elimination/intercompany policy identifiers.
  Every subsidiary relationship requires a stated control basis and evidence
  reference. Elimination and intercompany scopes must explicitly agree with
  their configured rules, including an explicit "none" when they do not apply.
- Core remains usable without these internal capabilities; their constructors
  and operations return dependency errors when required sources or declarations
  are absent.

## Blockers

1. The public SDK registry currently advertises Reporting and Governance only;
   it has no non-standard capability contracts for Reconciliation or
   Consolidation. Adding those shared capability identifiers or changing the
   registry is outside this task's allowed scope.
2. The public `ReportingStore` returns already-built report values and does
   not expose its ledger, account taxonomy, or translation-rate dependencies.
   The SDK therefore cannot independently preflight those report-specific
   inputs without changing shared contracts.
3. The internal Reconciliation `Repository` persists allocation records but
   provides no two-source read/comparison contract. P5-09 validates the
   declared requirements and detailed balance-reconciliation results; it
   cannot execute general cross-source matching until the shared source
   contracts are designed.
4. Consolidation checks policy identifiers and evidence references for
   presence, but cannot independently verify control evidence or enforce
   jurisdiction-specific policy content. Those validations require dedicated
   evidence and policy contracts outside this scope.

## Verification

Targeted package tests cover disabled/core-only dependencies, valid
requirements, invalid source combinations, missing taxonomy/rates, incomplete
consolidation policies, and missing control evidence.
