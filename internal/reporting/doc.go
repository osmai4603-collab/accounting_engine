// Package reporting builds financial statements and operational accounting
// reports from repository data.
//
// Its engines generate trial balances, balance sheets, income statements,
// cash-flow statements, equity statements, general ledgers, ratios, and
// period-closing results. BundleEngine combines statement results with
// supporting fixed-asset roll-forward and aged-receivable reports; consolidated
// reporting uses the consolidation package and can apply an authorization
// checker. Repository abstractions provide ledger, account, period, currency,
// and subledger data, including company-scoped context-aware access paths.
//
// The usual lifecycle is to supply a repository, request a report for an
// explicit period and currency, and inspect or export the returned value.
// Currency translation and classification are performed as part of the
// relevant report calculations. These are read/reporting operations: they do
// not create accounting postings. Missing source data, access failures, and
// unbalanced or unreconciled conditions are surfaced through results or
// errors rather than concealed.
//
// RequirementsForReport and ValidateReportRequirements expose the ledger,
// account-taxonomy, and conditional currency-rate dependencies before
// activation. A missing repository or empty/unusable account taxonomy is an
// error, not a successful empty report.
//
// Engine can be connected to the policy unit to resolve an omitted statement
// currency to the company's effective presentation currency. Explicit
// translation currencies are checked against the policy's allowed currencies.
package reporting
