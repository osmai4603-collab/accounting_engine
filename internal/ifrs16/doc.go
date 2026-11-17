// Package ifrs16 calculates bounded lessee and lessor lease balances and mapped
// journal drafts.
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
// a revised rate. Lease classification, exemption elections, and contract
// identification remain host decisions.
//
// P11-03 adds host-classified lessor accounting and the lessee exemptions.
// For lessor operating leases the retained asset is depreciated on a
// straight-line basis (initial direct costs join the depreciated base) and
// lease income is recognized straight-line over the lease term with a signed
// deferred-income/accrued-rent contra. For lessor finance leases the asset is
// derecognized in favor of the net investment in the lease — the present value
// of receipts plus the unguaranteed residual value plus initial direct costs —
// and finance income is allocated at the implicit rate until the closing
// balance equals the residual value. Short-term (at most twelve months, no
// purchase option) and low-value (host-configurable threshold) exemptions
// charge payments to profit or loss straight-line with an accrued/prepaid
// contra instead of recognizing right-of-use assets.
//
// Disclosure builders produce IFRS 16 structured disclosure payloads for the
// P9-05 framework: the lease expense and income note, and the future lease
// payments maturity analysis derived from the lessee schedule. They are
// deterministic value builders from host-supplied inputs; module-level
// DisclosureProvider retrieval requires durable lease state and follows with
// the application-posting workflow (P11-05).
//
// EntryBuilder, LessorEntryBuilder, and ExemptionEntryBuilder require a host
// posting capability and a resolved configured journal mapping before
// producing drafts. Draft identities are deterministic by lease reference and
// operation, but durable retry safety requires the host application to
// persist/enforce idempotency transactionally. No builder posts or persists
// entries. See docs/ard/ARD-P5-06.md and docs/ard/ARD-P11-03.md for the full
// boundary and accounting review limitations.
package ifrs16
