// Package ifrs16 calculates lease accounting balances and journal entries.
//
// NewLeaseContract validates commencement terms and sequential dated
// payments, determines a common currency, calculates the present value of the
// lease liability, and derives the initial right-of-use asset after direct
// costs, prepayments, and incentives. Present value and amortization use the
// annual effective discount rate over each actual elapsed-day interval on an
// Actual/365 basis. AmortizationSchedule produces liability interest and
// principal movements alongside right-of-use asset depreciation without
// mutating its contract.
//
// Guaranteed residual value amounts included in the lease liability must be
// included in the contractual payment cash flows. Modification remeasurement
// likewise expects the remaining cash flows to reflect any changed residual
// value guarantee; the engine does not infer a separate guarantee amount.
//
// Entry-generation helpers turn validated lease events or schedule amounts
// into posting journal entries. The caller supplies the appropriate accounts
// and remains responsible for authorization, posting, and persistence. Invalid
// contract terms, payment sequences, currencies, or carrying amounts are
// returned as errors.
package ifrs16
