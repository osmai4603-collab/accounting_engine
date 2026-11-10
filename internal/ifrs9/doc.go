// Package ifrs9 calculates expected credit loss (ECL) provisions.
//
// It classifies exposures into impairment stages using configured backstops
// and qualitative credit-risk flags, then calculates expected losses using
// portfolio data and the supplied probability-of-default, loss-given-default,
// and exposure assumptions. Provision-matrix inputs and allowance adjustments
// are represented explicitly so callers can inspect the resulting provision
// before recording it.
//
// Engine-based calculations load the inputs through a context-aware provider;
// direct calculation functions support deterministic in-memory inputs. The
// package does not persist allowances or post journals. Configuration and
// portfolio validation errors, including out-of-range rates, are returned to
// the caller.
package ifrs9
