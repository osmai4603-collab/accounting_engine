// Package ifrs15 models IFRS 15 revenue contracts and recognition schedules.
//
// Contract data represents the customer arrangement, performance obligations,
// transaction price, allocation inputs, and related recognition state.
// Contracts allocate transaction price by relative standalone selling price
// and schedule point-in-time or over-time performance.
//
// Unit coordinates advance billing and performance-obligation recognition,
// maintains in-memory contract balances, and returns balanced general-ledger
// journal drafts for deferred revenue, contract assets, and earned revenue.
// Invoices and cash advances retain customer partner identifiers for
// receivable-subledger reconciliation. RevenueWorkflow is the bounded path
// that requires external service evidence, a real journal mapping, and an
// application-backed poster before confirming recognition in-memory. Callers
// remain responsible for durable storage, authorization, and posting recovery.
package ifrs15
