// Package ifrs9 provides a simplified provision-matrix unit for trade
// receivables and a general multi-stage ECL calculation for financial assets.
//
// CalculateSimplifiedECL applies lifetime matrix rates to receivables.
// CalculateGeneralECL applies 12-month PD to Stage 1 and lifetime PD to
// Stages 2 and 3, combines scenario-weighted PD with LGD and EAD, and discounts
// expected losses using the effective interest rate. Model implementations
// provide risk estimates and remain responsible for forward-looking scenario
// support.
//
// Unit loads receivables and allowance balances through explicit sources and
// produces a balanced adjustment draft. It never substitutes a current
// allowance for a requested historical balance; it does not persist or post.
package ifrs9
