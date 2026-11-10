// Package ifrs15 models IFRS 15 revenue contracts and recognition schedules.
//
// Contract data represents the customer arrangement, performance obligations,
// transaction price, allocation inputs, and related recognition state.
// Package operations validate contract information, allocate the price to
// obligations, and calculate revenue recognized over time or at a point in
// time. Their outputs describe accounting results; they do not post journals
// or persist contract state by themselves.
//
// Callers create or load contract inputs, calculate and review the resulting
// allocation and recognition amounts, and then use the posting and storage
// layers when those results are to be recorded. Monetary and timing
// assumptions remain explicit in the inputs, and validation or calculation
// failures are returned to the caller.
package ifrs15
