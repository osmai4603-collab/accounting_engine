// Package ifrs9 provides bounded financial-asset classification, subsequent
// measurement, and ECL APIs, in addition to trade-receivable provision matrices.
//
// HostClassification validates final Business Model/SPPI references supplied
// by the host but does not make those judgments. CalculateEffectiveInterestRate
// derives EIR from dated contractual cash flows; MeasureSubsequentPeriod applies
// the approved category, effective interest, stage-3 net-basis interest, and
// category-specific fair-value destinations.
//
// CalculateSimplifiedECL applies lifetime matrix rates to receivables.
// CalculateGeneralECL and EvaluateVersionedECL apply scenario-weighted PD and
// LGD to EAD, which can be computed from ledger balances and undrawn commitments.
// The versioned API records provenance and category-specific allowance changes.
// Journal drafts can be posted through the public application service using
// PostJournalEntryThroughApplication.
//
// The host remains responsible for source data, classification, staging
// evidence, and risk assumptions. No API in this package establishes full
// IFRS 9 compliance or replaces independent professional review.
package ifrs9
