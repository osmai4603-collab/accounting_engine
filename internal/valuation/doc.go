// Package valuation provides exchange-rate access, currency conversion, and
// foreign-currency revaluation.
//
// Engine composes ConversionEngine and RevaluationEngine over a RateProviderSPI.
// Conversion applies an applicable rate to monetary values, while revaluation
// compares a foreign-currency balance's carrying value with its translated
// value at a reporting date and identifies the resulting movement. Rate
// resolution and result calculations are kept separate from storage through
// the provider interface.
//
// Callers provide the company, currencies, amounts, and effective date needed
// for a calculation, then use the returned values or pass accounting movements
// to posting. Rate providers own rate persistence and lookup policy. Missing,
// ambiguous, or invalid rate data is returned as an error; no implicit rate is
// substituted.
package valuation
