// Package ifrs16 calculates lease accounting balances and journal entries.
//
// NewLeaseContract validates commencement terms and sequential monthly
// payments, determines a common currency, calculates the present value of the
// lease liability, and derives the initial right-of-use asset after direct
// costs, prepayments, and incentives. AmortizationSchedule then produces
// period-by-period liability interest and principal movements alongside
// right-of-use asset depreciation. The schedule is calculated without
// mutating its contract.
//
// Entry-generation helpers turn validated lease events or schedule amounts
// into posting journal entries. The caller supplies the appropriate accounts
// and remains responsible for authorization, posting, and persistence. Invalid
// contract terms, payment sequences, currencies, or carrying amounts are
// returned as errors.
package ifrs16
