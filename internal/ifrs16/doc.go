// Package ifrs16 calculates bounded lessee lease balances and mapped journal
// drafts.
//
// NewLeaseContract validates commencement terms and sequential dated payments,
// determines a common currency, calculates the present value of the lease
// liability, and derives the initial right-of-use asset after direct costs,
// prepayments, and incentives. Present value and amortization use the supplied
// annual effective discount rate over each actual elapsed-day interval on an
// Actual/365 basis. Amounts use the engine's four-place financial scale;
// depreciation is straight-line by schedule period, with the last period
// absorbing any rounding residual.
//
// The supported modification helper remeasures host-classified non-separate
// modifications using host-supplied remaining payments, carrying amounts, and
// a revised rate. No exemption, lessor, options, variable-payment, index/rate
// reassessment, or legal classification workflow is implemented.
//
// EntryBuilder requires a host posting capability and a resolved configured
// journal mapping before producing drafts. Draft identities are deterministic
// by lease reference and operation, but durable retry safety requires the host
// application to persist/enforce idempotency transactionally. This package
// does not post or persist entries. See docs/ard/ARD-P5-06.md for the full
// boundary and accounting review limitations.
package ifrs16
