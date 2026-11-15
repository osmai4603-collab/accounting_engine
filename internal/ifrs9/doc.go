// Package ifrs9 provides bounded ECL measurement APIs for trade receivables
// and caller-supplied general-approach financial-asset exposures.
//
// CalculateSimplifiedECL applies lifetime matrix rates to receivables.
// CalculateGeneralECL applies 12-month PD to Stage 1 and lifetime PD to
// Stages 2 and 3, combines scenario-weighted PD with LGD and EAD, and discounts
// expected losses using the effective interest rate. Model implementations
// provide risk estimates and remain responsible for forward-looking scenario
// support.
// EvaluateVersionedECL calculates the same bounded general-approach measure
// using explicit, versioned assumptions and source references, and returns a
// fingerprint of all calculation inputs. Its host supplies stage evidence and
// source snapshot identity; the function does not persist inputs or post.
//
// Unit loads receivables and allowance balances through explicit sources and
// produces a balanced adjustment draft. It never substitutes a current
// allowance for a requested historical balance; it does not persist or post.
// No API in this package establishes IFRS 9 compliance or validates the
// professional judgment behind host-supplied classifications and estimates.
package ifrs9
