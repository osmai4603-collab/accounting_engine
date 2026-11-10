// Package ias12 implements selected IAS 12 income-tax calculations.
//
// Measure derives deferred-tax assets and liabilities from taxable or
// deductible temporary differences using the rates and measurement period
// supplied by the caller. Reconcile prepares a tax reconciliation from the
// current tax, accounting profit, applicable tax rate, and permanent
// differences. The input and result types preserve the relevant currency,
// balance kind, period, and rate metadata.
//
// GenerateDeferredTaxEntry converts a validated movement in a deferred-tax
// position into a balanced posting.JournalEntry using the requested accounts.
// The lifecycle is calculation first, review of the returned amounts, then
// optional journal creation and posting by the posting package. This package
// performs no persistence; invalid periods, balances, currencies, or
// unenacted rates are returned as errors rather than silently adjusted.
package ias12
