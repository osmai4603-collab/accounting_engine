# ARD-P9-02: Cash-flow statement reporting

## Status

Implemented and verified through the SDK reporting service and internal engine.

## Decision

Expose a stable public cash-flow request and report model and wire it through
`sdk.ReportingService.GetCashFlow`. The SDK delegates to the existing reporting
engine using a company-scoped ledger-reporting repository. A store without that
capability returns `ErrCapabilityNotImplemented`; no partial report is
fabricated.

## Calculation contract

- The caller selects direct or indirect presentation and an IAS 7 or IFRS 18
  profile. The profile chooses net profit or operating profit as the indirect
  method's starting point.
- Direct presentation requires an explicit classification for each cash
  journal line with movement. The engine does not infer a receipt or payment
  purpose from account balances or counterpart account names.
- Operating, investing, and financing activity sections report signed amounts.
  Beginning and ending cash, net change, and cash-flow reconciliation are
  included. The foreign-exchange effect is presented separately.
- Caller-supplied non-cash investing and financing items are returned as a
  distinct disclosure schedule.
- Cash-equivalent accounts are selected from account metadata unless the
  request supplies the entity's explicit account set.

## Boundaries

This is a reporting calculation, not a persisted entity-policy or
classification workflow. Hosts remain responsible for consistent
interest/dividend/tax classifications and any entity-specific cash-equivalent
policy. The indirect method uses available ledger/account classifications; it
does not invent missing accrual, impairment, or working-capital schedules.
Restricted-cash reconciliations, comparative statements, and discontinued
operations are outside this capability. IFRS 18 profile selection is explicit
and is not automatically based on the reporting date.

## Verification

Tests cover direct cash-line classification, indirect IAS 7 versus IFRS 18
starting points, cash reconciliation, FX effects, non-cash items, explicit
cash-equivalent policy, invalid direct requests, and retrieval through
`ReportingService.GetCashFlow`.
