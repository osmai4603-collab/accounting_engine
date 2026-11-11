// Package ias12 implements selected IAS 12 income-tax calculations.
//
// Measure derives deferred-tax assets and liabilities from taxable or
// deductible temporary differences using the rates and measurement period
// supplied by the caller. Reconcile prepares a tax reconciliation from the
// current tax, accounting profit, applicable tax rate, and permanent
// differences. The input and result types preserve the relevant currency,
// balance kind, period, and rate metadata.
//
// Unit combines balance-sheet measurement, effective tax reconciliation, and
// journal generation for period close. Deferred-tax movements can be split
// between profit or loss and OCI; current-tax accruals are generated
// separately. Journal creation is separate from authorization, posting, and
// persistence. This package performs no persistence; invalid periods,
// balances, currencies, or unenacted rates are returned as errors rather than
// silently adjusted.
package ias12
