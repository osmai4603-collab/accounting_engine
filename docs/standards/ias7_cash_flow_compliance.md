# IAS 7 cash-flow implementation profile

This note records the scope of the reporting engine's cash-flow statement. It is
an implementation boundary, not a claim of full IAS 7 or IFRS 18 compliance.

| Requirement | Implementation | Boundary |
|---|---|---|
| Operating, investing, and financing categories | Common statement sections and signed amounts | Classification correctness depends on the configured chart-of-accounts metadata for the indirect method. |
| Direct and indirect presentation | `CashFlowRequest.Method` selects either calculation | Direct presentation requires an explicit classification for each cash journal line with movement. The engine never infers transaction purpose from counterpart account balances. |
| Indirect presentation | IAS 7 profile starts from net profit; IFRS 18 profile starts from operating profit | Reconciliation adjustments use account movements and classifications available in the ledger. Accrual schedules, impairment details, and cash-tax schedules are not inferred where the ledger lacks them. |
| Reporting profile | `CashFlowRequest.Profile` selects the indirect-method starting point: net profit for IAS 7 or operating profit for IFRS 18 | Profile selection is explicit; the engine does not automatically switch profiles based on the reporting period or IFRS 18 effective date. Profile-specific interest/dividend policies are not automated. |
| Cash and cash equivalents | Accounts marked as cash equivalents are used by default; `CashFlowPolicy` can provide the entity's exact account set | The entity is responsible for a consistent policy, including whether qualifying demand overdrafts are included. |
| Beginning-to-ending reconciliation | Beginning cash is measured before the period; ending cash is measured at period end | A translation residual is separately presented when cash-equivalent account lines have foreign-currency exposure. |
| Non-cash investing and financing transactions | Caller-supplied non-cash schedule is returned as a distinct disclosure list | The engine does not presume that a balance-sheet movement is a non-cash transaction without transaction-level evidence. |
| Interest, dividends, and income taxes | Direct-method classifications can assign each cash movement to an activity | The caller must apply and document its IAS 7 policy consistently. The indirect method follows available account/IFRS classifications; automated profile-specific policy selection or persistence is not provided. |
| Cash equivalents and restricted cash | Cash account selection is explicit/configurable | Restricted-cash reconciliation and cash-equivalent maturity assessment are outside this implementation. |
| Comparative, discontinued-operation, and acquisition disclosures | Not supplied by the cash-flow engine | Use separate reporting/disclosure workflows. |

`pkg/api/v1/reporting.CashFlowRequest` describes the public request/response
shape. The SDK's shared `ReportingService.GetCashFlow` delegates to the internal
engine when its configured reporting store also implements the company-scoped
ledger-reporting repository. A store without that capability returns
`ErrCapabilityNotImplemented`; the SDK does not substitute a partial or
success-shaped report. Direct-method callers must provide a classification for
each cash journal line with movement. The API does not infer transaction
purpose, persist classifications, or automatically choose entity accounting
policies.
