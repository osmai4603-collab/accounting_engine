// Package bank_parsers converts supported bank statement formats into common
// bank items for downstream reconciliation.
//
// Parsers normalize statement records while preserving the transaction
// information needed by the treasury and matching workflows. Format-specific
// implementations include CAMT.053 and MT940. The package only parses and
// maps input data: it does not connect to a bank, persist transactions, or
// reconcile them to ledger entries.
//
// Callers provide statement content to the appropriate parser, handle
// malformed or unsupported records through the returned errors, and pass the
// normalized items to the subledgers package for matching and reconciliation.
package bank_parsers
