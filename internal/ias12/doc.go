// Package ias12 implements bounded income-tax calculations inspired by
// selected IAS 12 requirements; it does not implement IAS 12 in full.
//
// MeasureWithProfile measures supplied carrying amounts against supplied tax
// bases, selects an enacted profile rate by expected reversal date, and limits
// deductible differences to documented probable-profit support.
// MeasureCurrentTax calculates a simple estimate from a supplied nonnegative
// taxable-profit base and the profile rate effective at period end. Local tax
// law, tax-return computation, credits, loss utilization, uncertain tax
// positions, offsetting, presentation, and disclosure are outside this scope.
// Reconcile prepares a tax reconciliation from caller-supplied values.
//
// Configured entry helpers require host-provided mapping and application
// configuration references, then create journal drafts only. This package
// does not verify those references, authorize, post, or persist entries.
// Invalid periods, balances, currencies, missing provenance, or unavailable
// rates are returned as errors rather than silently adjusted.
package ias12
