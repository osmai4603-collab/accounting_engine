// Package subledgers manages operational balances that support the general
// ledger.
//
// The domain covers receivables and payables, open items and matching,
// settlement and write-off flows, fixed assets and depreciation schedules,
// treasury and bank items, control-account checks, reconciliation, and
// event-driven journal bridges. Service composes these ledgers over a shared
// Repository and, where a journal is required, a JournalCreator. A
// company-scoped constructor binds the ledgers to one company.
//
// Operational events are recorded or matched in the appropriate subledger,
// reconciled to control accounts, and bridged to journals when required.
// Repository operations own persistence and transactional guarantees; journal
// creation and posting remain the responsibility of the posting integration.
// Context-aware and company-scoped interfaces are used where available.
// Validation, matching, and storage errors are returned to callers rather
// than silently discarded.
package subledgers
